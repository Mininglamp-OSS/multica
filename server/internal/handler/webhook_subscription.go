package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/outwebhook"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ── Outbound webhook subscriptions ──────────────────────────────────────────
//
// CRUD for webhook_subscription (see migration 121): external HTTP endpoints
// Multica POSTs to when subscribed issue events fire. project_id IS NULL is a
// workspace-level webhook (GitHub "org" webhook); a set project_id is a
// project-level webhook (GitHub "repo" webhook). All endpoints are gated to
// workspace owner/admin — webhooks can exfiltrate issue data, so creating one
// is a privileged action.

// webhookSecretPrefix marks a Multica webhook signing secret so a leaked value
// is recognisable. "whsec_" follows the convention used by Stripe/GitHub for
// signing secrets. 32 random bytes => 43 chars of URL-safe base64.
const webhookSecretPrefix = "whsec_"

// supportedWebhookEvents is the v1 event allow-list. It is derived from the
// dispatcher's SupportedEventTypes — the single source of truth for the events
// Multica can actually deliver. Adding an event there requires a matching bus
// subscription in cmd/server/webhook_listeners.go, otherwise subscriptions for
// it would be accepted but never fire. Unknown event types in a create/update
// request are rejected so a typo doesn't silently subscribe to nothing.
var supportedWebhookEvents = func() map[string]bool {
	m := make(map[string]bool, len(outwebhook.SupportedEventTypes))
	for _, e := range outwebhook.SupportedEventTypes {
		m[e] = true
	}
	return m
}()

// WebhookSubscriptionResponse is the API shape. The signing secret is returned
// ONLY once, on create (SecretOnce); list/get never echo it. SecretHint carries
// the last 4 chars so operators can tell two secrets apart in the UI.
type WebhookSubscriptionResponse struct {
	ID          string   `json:"id"`
	WorkspaceID string   `json:"workspace_id"`
	ProjectID   *string  `json:"project_id"`
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	Enabled     bool     `json:"enabled"`
	SecretHint  string   `json:"secret_hint"`
	// SecretOnce is the full signing secret, populated only in the create
	// response. Empty on list/get.
	SecretOnce string `json:"secret,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func webhookSubscriptionToResponse(s db.WebhookSubscription) WebhookSubscriptionResponse {
	var events []string
	if err := json.Unmarshal(s.Events, &events); err != nil {
		events = []string{}
	}
	resp := WebhookSubscriptionResponse{
		ID:          uuidToString(s.ID),
		WorkspaceID: uuidToString(s.WorkspaceID),
		ProjectID:   uuidToPtr(s.ProjectID),
		URL:         s.Url,
		Events:      events,
		Enabled:     s.Enabled,
		SecretHint:  secretHint(s.Secret),
		CreatedAt:   timestampToString(s.CreatedAt),
		UpdatedAt:   timestampToString(s.UpdatedAt),
	}
	return resp
}

// secretHint returns the last 4 characters of a secret (or "" if too short).
func secretHint(secret string) string {
	if len(secret) < 4 {
		return ""
	}
	return secret[len(secret)-4:]
}

// generateWebhookSecret returns a cryptographically random signing secret,
// reusing the shared credential generator so token and secret entropy stay in
// lockstep.
func generateWebhookSecret() (string, error) {
	return generateCredential(webhookSecretPrefix)
}

// validateWebhookURL enforces an absolute http(s) URL and rejects endpoints
// that point at the server's own network — loopback, link-local (incl. the
// 169.254.169.254 cloud metadata endpoint), private, and unspecified ranges.
// This is a best-effort SSRF guard on IP-literal hosts plus "localhost"; it
// does not resolve DNS, so a hostname that resolves to an internal address can
// still slip through (DNS-rebinding is out of scope for v1). Webhooks are
// admin-created, but an admin shouldn't be able to turn the server into a probe
// of its own metadata service or internal subnet.
func validateWebhookURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return errors.New("url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("url must include a host")
	}
	if strings.EqualFold(host, "localhost") {
		return errors.New("url must not target localhost")
	}
	if ip := net.ParseIP(host); ip != nil && isInternalIP(ip) {
		return errors.New("url must not target an internal or loopback address")
	}
	return nil
}

// isInternalIP reports whether ip is in a range that should never be reachable
// from a user-configured outbound webhook.
func isInternalIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() ||
		ip.IsUnspecified()
}

// validateWebhookEvents checks every requested event against the allow-list and
// returns the JSONB bytes to persist. An empty request defaults to the single
// supported event.
func validateWebhookEvents(events []string) ([]byte, error) {
	if len(events) == 0 {
		events = []string{outwebhook.EventIssueStatusChanged}
	}
	for _, e := range events {
		if !supportedWebhookEvents[e] {
			return nil, fmt.Errorf("unsupported event: %q", e)
		}
	}
	b, err := json.Marshal(events)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// ── Requests ────────────────────────────────────────────────────────────────

type CreateWebhookSubscriptionRequest struct {
	URL string `json:"url"`
	// ProjectID, when set, scopes the webhook to one project (project-level /
	// "repo" webhook). Omit for a workspace-level / "org" webhook.
	ProjectID *string  `json:"project_id"`
	Events    []string `json:"events"`
}

type UpdateWebhookSubscriptionRequest struct {
	URL     *string   `json:"url"`
	Events  *[]string `json:"events"`
	Enabled *bool     `json:"enabled"`
}

// ── Handlers ────────────────────────────────────────────────────────────────

// ListWebhookSubscriptions returns subscriptions for the workspace. With a
// `project_id` query param it returns that project's webhooks; without it,
// workspace-level webhooks only.
func (h *Handler) ListWebhookSubscriptions(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	var rows []db.WebhookSubscription
	var err error
	if projectIDParam := strings.TrimSpace(r.URL.Query().Get("project_id")); projectIDParam != "" {
		projectUUID, ok := parseUUIDOrBadRequest(w, projectIDParam, "project_id")
		if !ok {
			return
		}
		rows, err = h.Queries.ListWebhookSubscriptionsByProject(r.Context(), db.ListWebhookSubscriptionsByProjectParams{
			WorkspaceID: wsUUID,
			ProjectID:   projectUUID,
		})
	} else {
		rows, err = h.Queries.ListWebhookSubscriptionsByWorkspace(r.Context(), wsUUID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list webhook subscriptions")
		return
	}

	out := make([]WebhookSubscriptionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, webhookSubscriptionToResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": out})
}

// CreateWebhookSubscription registers a new outbound webhook. The generated
// signing secret is returned once in the response body.
func (h *Handler) CreateWebhookSubscription(w http.ResponseWriter, r *http.Request) {
	var req CreateWebhookSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateWebhookURL(req.URL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	eventsJSON, err := validateWebhookEvents(req.Events)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	// Resolve and validate project scope (project-level webhook).
	var projectUUID pgtype.UUID
	if req.ProjectID != nil && *req.ProjectID != "" {
		projectUUID, ok = parseUUIDOrBadRequest(w, *req.ProjectID, "project_id")
		if !ok {
			return
		}
		if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
			ID:          projectUUID,
			WorkspaceID: wsUUID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "project not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load project")
			return
		}
	}

	secret, err := generateWebhookSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate secret")
		return
	}

	row, err := h.Queries.CreateWebhookSubscription(r.Context(), db.CreateWebhookSubscriptionParams{
		WorkspaceID: wsUUID,
		ProjectID:   projectUUID,
		Url:         strings.TrimSpace(req.URL),
		Secret:      secret,
		Events:      eventsJSON,
		Enabled:     true,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create webhook subscription")
		return
	}

	resp := webhookSubscriptionToResponse(row)
	resp.SecretOnce = secret // shown once on create
	writeJSON(w, http.StatusCreated, resp)
}

// UpdateWebhookSubscription patches url / events / enabled on an existing
// subscription. Per the UUID-parsing convention, the {id} path param is
// validated and scoped by workspace_id in the query — never round-tripped raw.
func (h *Handler) UpdateWebhookSubscription(w http.ResponseWriter, r *http.Request) {
	sub, ok := h.loadWebhookSubscription(w, r)
	if !ok {
		return
	}

	var req UpdateWebhookSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	params := db.UpdateWebhookSubscriptionParams{
		ID:          sub.ID,
		WorkspaceID: sub.WorkspaceID,
	}
	if req.URL != nil {
		if err := validateWebhookURL(*req.URL); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.Url = pgtype.Text{String: strings.TrimSpace(*req.URL), Valid: true}
	}
	if req.Events != nil {
		eventsJSON, err := validateWebhookEvents(*req.Events)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.Events = eventsJSON
	}
	if req.Enabled != nil {
		params.Enabled = pgtype.Bool{Bool: *req.Enabled, Valid: true}
	}

	row, err := h.Queries.UpdateWebhookSubscription(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update webhook subscription")
		return
	}
	writeJSON(w, http.StatusOK, webhookSubscriptionToResponse(row))
}

// DeleteWebhookSubscription removes a subscription, scoped by workspace_id.
func (h *Handler) DeleteWebhookSubscription(w http.ResponseWriter, r *http.Request) {
	sub, ok := h.loadWebhookSubscription(w, r)
	if !ok {
		return
	}
	if err := h.Queries.DeleteWebhookSubscription(r.Context(), db.DeleteWebhookSubscriptionParams{
		ID:          sub.ID,
		WorkspaceID: sub.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete webhook subscription")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loadWebhookSubscription resolves the {id} path param to a subscription owned
// by the caller's workspace, gating on owner/admin. Returns the resolved row so
// callers use row.ID / row.WorkspaceID for writes (never the raw URL string).
func (h *Handler) loadWebhookSubscription(w http.ResponseWriter, r *http.Request) (db.WebhookSubscription, bool) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return db.WebhookSubscription{}, false
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.WebhookSubscription{}, false
	}
	idUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return db.WebhookSubscription{}, false
	}
	sub, err := h.Queries.GetWebhookSubscriptionInWorkspace(r.Context(), db.GetWebhookSubscriptionInWorkspaceParams{
		ID:          idUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "webhook subscription not found")
			return db.WebhookSubscription{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load webhook subscription")
		return db.WebhookSubscription{}, false
	}
	return sub, true
}
