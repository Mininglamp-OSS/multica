// Package outwebhook delivers outbound webhooks to external HTTP endpoints when
// subscribed Multica events occur. It is the reverse direction of the inbound
// autopilot webhook ingress (server/internal/handler/autopilot_webhook.go):
// there, external systems POST to Multica; here, Multica POSTs to external URLs.
//
// v1 emits a single event type, issue.status_changed, to webhook_subscription
// rows. Delivery is async fire-and-forget: each subscription is delivered in its
// own goroutine with a couple of immediate retries on network error / 5xx, and
// outcomes are logged (no delivery-history persistence in v1).
package outwebhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/webhooksign"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// EventIssueStatusChanged is the only event type emitted in v1. Subscriptions
// opt into it via their `events` JSONB array.
const EventIssueStatusChanged = "issue.status_changed"

// SupportedEventTypes is the single source of truth for the event types this
// dispatcher can actually deliver. The handler allow-list (which events a
// subscription may declare) is derived from it. Adding an entry here requires a
// matching bus subscription in cmd/server/webhook_listeners.go — otherwise
// subscriptions for the new type would be accepted by the API but never fire.
var SupportedEventTypes = []string{EventIssueStatusChanged}

const (
	deliveryTimeout = 30 * time.Second
	maxAttempts     = 3 // 1 initial + 2 retries
	// listTimeout bounds the subscription lookup. It runs on a detached
	// goroutine (not the request path), so a slow query never blocks an issue
	// update — the timeout just stops a stuck query from leaking a goroutine.
	listTimeout = 10 * time.Second
	// maxConcurrentDeliveries caps in-flight outbound POSTs across the whole
	// process. Without a cap, a burst of status changes in a workspace with
	// many webhooks would spawn an unbounded number of goroutines, each holding
	// the payload and sleeping through retry backoff. Excess deliveries queue
	// on the semaphore instead.
	maxConcurrentDeliveries = 16
)

// retryBackoff is the wait before attempt N+1 (index 0 = wait before the first
// retry). Short and fixed — fire-and-forget delivery shouldn't hold a goroutine
// for long.
var retryBackoff = []time.Duration{1 * time.Second, 4 * time.Second}

// Store is the subset of *db.Queries the dispatcher needs. An interface keeps
// the selection/filtering/signing logic testable without a database.
type Store interface {
	ListEnabledWebhookSubscriptionsForDispatch(ctx context.Context, workspaceID pgtype.UUID) ([]db.WebhookSubscription, error)
}

// Dispatcher fans an event out to matching subscriptions.
type Dispatcher struct {
	store  Store
	client *http.Client
	// sem bounds concurrent deliveries (see maxConcurrentDeliveries).
	sem chan struct{}
}

// New builds a Dispatcher. The HTTP client carries a fixed per-request timeout;
// retries are handled per attempt, not by the client.
func New(store Store) *Dispatcher {
	return &Dispatcher{
		store:  store,
		client: &http.Client{Timeout: deliveryTimeout},
		sem:    make(chan struct{}, maxConcurrentDeliveries),
	}
}

// IssueStatusChanged describes a single issue status transition. The listener
// builds it from the issue:updated event payload. Issue is the JSON-serializable
// issue representation (handler.IssueResponse) embedded verbatim in the outbound
// body — the dispatcher does not depend on the handler package.
type IssueStatusChanged struct {
	WorkspaceID    string
	ProjectID      string // "" when the issue has no project
	ActorType      string
	ActorID        string
	PreviousStatus string
	Issue          any
}

// actorPayload mirrors the {type,id} shape used elsewhere for polymorphic actors.
type actorPayload struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// outboundPayload is the versioned JSON body POSTed to subscribers.
type outboundPayload struct {
	Event          string       `json:"event"`
	WorkspaceID    string       `json:"workspace_id"`
	Actor          actorPayload `json:"actor"`
	Issue          any          `json:"issue"`
	PreviousStatus string       `json:"previous_status"`
	DeliveredAt    string       `json:"delivered_at"`
}

// DispatchIssueStatusChanged hands the event off to a background goroutine and
// returns immediately. The event bus invokes listeners synchronously on the
// issue-update HTTP request path, so NO work here may touch that path — not the
// subscription DB query, not JSON marshaling, not delivery. Everything happens
// in dispatch().
func (d *Dispatcher) DispatchIssueStatusChanged(ev IssueStatusChanged) {
	go d.dispatch(ev)
}

// dispatch (off the request path) selects matching subscriptions and spawns a
// bounded set of delivery goroutines.
func (d *Dispatcher) dispatch(ev IssueStatusChanged) {
	wsUUID, err := util.ParseUUID(ev.WorkspaceID)
	if err != nil {
		slog.Warn("outwebhook: invalid workspace id", "workspace_id", ev.WorkspaceID, "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	subs, err := d.store.ListEnabledWebhookSubscriptionsForDispatch(ctx, wsUUID)
	cancel()
	if err != nil {
		slog.Error("outwebhook: failed to list subscriptions", "workspace_id", ev.WorkspaceID, "error", err)
		return
	}

	matched := make([]db.WebhookSubscription, 0, len(subs))
	for _, s := range subs {
		if subscriptionMatches(s, ev.ProjectID) && subscribedToEvent(s, EventIssueStatusChanged) {
			matched = append(matched, s)
		}
	}
	if len(matched) == 0 {
		return
	}

	body, err := json.Marshal(outboundPayload{
		Event:          EventIssueStatusChanged,
		WorkspaceID:    ev.WorkspaceID,
		Actor:          actorPayload{Type: ev.ActorType, ID: ev.ActorID},
		Issue:          ev.Issue,
		PreviousStatus: ev.PreviousStatus,
		DeliveredAt:    time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		slog.Error("outwebhook: failed to marshal payload", "workspace_id", ev.WorkspaceID, "error", err)
		return
	}

	for _, s := range matched {
		// Acquire a slot before spawning so concurrent deliveries stay bounded;
		// this goroutine blocks here when the semaphore is full, providing
		// backpressure instead of unbounded goroutine growth.
		d.sem <- struct{}{}
		go func(sub db.WebhookSubscription) {
			defer func() { <-d.sem }()
			d.deliver(sub, EventIssueStatusChanged, body)
		}(s)
	}
}

// deliver POSTs body to one subscription, retrying on network error / 5xx.
func (d *Dispatcher) deliver(sub db.WebhookSubscription, event string, body []byte) {
	deliveryID := uuid.NewString()
	signature := webhooksign.Sign(sub.Secret, body)
	subID := util.UUIDToString(sub.ID)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(retryBackoff[attempt-1])
		}

		status, err := d.post(sub.Url, event, deliveryID, signature, body)
		if err == nil && status >= 200 && status < 300 {
			slog.Debug("outwebhook: delivered",
				"subscription_id", subID, "event", event, "status", status, "attempt", attempt+1)
			return
		}

		retryable := err != nil || status >= 500
		slog.Warn("outwebhook: delivery attempt failed",
			"subscription_id", subID, "event", event, "url", sub.Url,
			"status", status, "attempt", attempt+1, "retryable", retryable, "error", err)
		if !retryable {
			return // 4xx — endpoint rejected the payload; retrying won't help.
		}
	}
	slog.Error("outwebhook: delivery exhausted retries",
		"subscription_id", subID, "event", event, "url", sub.Url)
}

// post performs a single delivery attempt and returns the HTTP status code.
func (d *Dispatcher) post(url, event, deliveryID, signature string, body []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), deliveryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Multica-Webhook/1.0")
	req.Header.Set("X-Multica-Event", event)
	req.Header.Set("X-Multica-Delivery", deliveryID)
	req.Header.Set("X-Multica-Signature-256", signature)

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	// Drain the body so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}

// subscriptionMatches reports whether a subscription applies to an issue with
// the given project. Workspace-level subscriptions (no project_id) match every
// issue; project-level subscriptions match only their own project.
func subscriptionMatches(sub db.WebhookSubscription, issueProjectID string) bool {
	if !sub.ProjectID.Valid {
		return true // workspace-level
	}
	return util.UUIDToString(sub.ProjectID) == issueProjectID
}

// subscribedToEvent reports whether the subscription's events array contains
// event. A malformed events column is treated as "no events" (skip), never a
// panic.
func subscribedToEvent(sub db.WebhookSubscription, event string) bool {
	var events []string
	if err := json.Unmarshal(sub.Events, &events); err != nil {
		slog.Warn("outwebhook: subscription has malformed events column",
			"subscription_id", util.UUIDToString(sub.ID), "error", err)
		return false
	}
	for _, e := range events {
		if e == event {
			return true
		}
	}
	return false
}
