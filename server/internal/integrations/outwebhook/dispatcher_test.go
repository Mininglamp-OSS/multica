package outwebhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/webhooksign"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeStore returns a fixed subscription list, ignoring the workspace filter
// (the dispatcher does the workspace-level vs project-level filtering itself).
type fakeStore struct {
	subs []db.WebhookSubscription
	err  error
}

func (f *fakeStore) ListEnabledWebhookSubscriptionsForDispatch(_ context.Context, _ pgtype.UUID) ([]db.WebhookSubscription, error) {
	return f.subs, f.err
}

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	u, err := util.ParseUUID(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return u
}

const (
	wsID   = "11111111-1111-1111-1111-111111111111"
	projA  = "22222222-2222-2222-2222-222222222222"
	projB  = "33333333-3333-3333-3333-333333333333"
	subID1 = "44444444-4444-4444-4444-444444444444"
)

func sub(t *testing.T, id, project string, events []string, url string) db.WebhookSubscription {
	t.Helper()
	ev, _ := json.Marshal(events)
	s := db.WebhookSubscription{
		ID:          mustUUID(t, id),
		WorkspaceID: mustUUID(t, wsID),
		Url:         url,
		Secret:      "whsec_test",
		Events:      ev,
		Enabled:     true,
	}
	if project != "" {
		s.ProjectID = mustUUID(t, project)
	}
	return s
}

func TestSubscriptionMatches(t *testing.T) {
	wsLevel := sub(t, subID1, "", []string{EventIssueStatusChanged}, "http://x")
	projLevel := sub(t, subID1, projA, []string{EventIssueStatusChanged}, "http://x")

	if !subscriptionMatches(wsLevel, projA) {
		t.Error("workspace-level subscription should match any project")
	}
	if !subscriptionMatches(wsLevel, "") {
		t.Error("workspace-level subscription should match issues with no project")
	}
	if !subscriptionMatches(projLevel, projA) {
		t.Error("project-level subscription should match its own project")
	}
	if subscriptionMatches(projLevel, projB) {
		t.Error("project-level subscription must NOT match a different project")
	}
	if subscriptionMatches(projLevel, "") {
		t.Error("project-level subscription must NOT match issues with no project")
	}
}

func TestSubscribedToEvent(t *testing.T) {
	s := sub(t, subID1, "", []string{"issue.status_changed"}, "http://x")
	if !subscribedToEvent(s, EventIssueStatusChanged) {
		t.Error("expected subscription to match its declared event")
	}
	if subscribedToEvent(s, "issue.created") {
		t.Error("expected subscription not to match an undeclared event")
	}

	// Malformed events column is treated as "no events", never a panic.
	bad := s
	bad.Events = []byte("{not json")
	if subscribedToEvent(bad, EventIssueStatusChanged) {
		t.Error("malformed events column should match nothing")
	}
}

// collector is a test webhook receiver that records request bodies + headers.
type collector struct {
	mu       sync.Mutex
	bodies   [][]byte
	sigs     []string
	events   []string
	wg       *sync.WaitGroup
	failNext atomic.Int32 // respond 500 this many times before succeeding
}

func (c *collector) handler(w http.ResponseWriter, r *http.Request) {
	body := make([]byte, r.ContentLength)
	_, _ = r.Body.Read(body)
	c.mu.Lock()
	c.bodies = append(c.bodies, body)
	c.sigs = append(c.sigs, r.Header.Get("X-Multica-Signature-256"))
	c.events = append(c.events, r.Header.Get("X-Multica-Event"))
	c.mu.Unlock()
	if c.failNext.Load() > 0 {
		c.failNext.Add(-1)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	if c.wg != nil {
		c.wg.Done()
	}
}

func TestDispatchDeliversToMatchingSubscriptions(t *testing.T) {
	c := &collector{wg: &sync.WaitGroup{}}
	srv := httptest.NewServer(http.HandlerFunc(c.handler))
	defer srv.Close()

	// Two subs: a workspace-level one and a project-A one. An issue in project A
	// should hit both. A project-B sub should NOT fire.
	store := &fakeStore{subs: []db.WebhookSubscription{
		sub(t, subID1, "", []string{EventIssueStatusChanged}, srv.URL),
		sub(t, projA, projA, []string{EventIssueStatusChanged}, srv.URL),
		sub(t, projB, projB, []string{EventIssueStatusChanged}, srv.URL),
	}}
	d := newWithClient(store, &http.Client{Timeout: deliveryTimeout})

	c.wg.Add(2) // expect exactly 2 successful deliveries
	d.DispatchIssueStatusChanged(IssueStatusChanged{
		WorkspaceID:    wsID,
		ProjectID:      projA,
		ActorType:      "member",
		ActorID:        "actor-1",
		PreviousStatus: "in_progress",
		Issue:          map[string]any{"id": "issue-1", "status": "done"},
	})

	waitTimeout(t, c.wg, 5*time.Second)

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) != 2 {
		t.Fatalf("expected 2 deliveries, got %d", len(c.bodies))
	}

	// Verify signature + payload shape on the first delivery.
	var payload struct {
		Event          string `json:"event"`
		WorkspaceID    string `json:"workspace_id"`
		PreviousStatus string `json:"previous_status"`
		Actor          struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"actor"`
	}
	if err := json.Unmarshal(c.bodies[0], &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Event != EventIssueStatusChanged {
		t.Errorf("event = %q, want %q", payload.Event, EventIssueStatusChanged)
	}
	if payload.WorkspaceID != wsID {
		t.Errorf("workspace_id = %q, want %q", payload.WorkspaceID, wsID)
	}
	if payload.PreviousStatus != "in_progress" {
		t.Errorf("previous_status = %q, want in_progress", payload.PreviousStatus)
	}
	if payload.Actor.Type != "member" || payload.Actor.ID != "actor-1" {
		t.Errorf("actor = %+v, want member/actor-1", payload.Actor)
	}
	if c.events[0] != EventIssueStatusChanged {
		t.Errorf("X-Multica-Event = %q", c.events[0])
	}
	if !webhooksign.Verify("whsec_test", c.sigs[0], c.bodies[0]) {
		t.Errorf("signature did not verify: %q", c.sigs[0])
	}
}

func TestDispatchSkipsUnsubscribedEvent(t *testing.T) {
	c := &collector{}
	srv := httptest.NewServer(http.HandlerFunc(c.handler))
	defer srv.Close()

	store := &fakeStore{subs: []db.WebhookSubscription{
		sub(t, subID1, "", []string{"issue.created"}, srv.URL),
	}}
	d := newWithClient(store, &http.Client{Timeout: deliveryTimeout})
	d.DispatchIssueStatusChanged(IssueStatusChanged{
		WorkspaceID: wsID,
		Issue:       map[string]any{"id": "issue-1"},
	})

	// No goroutine should have been spawned; give any stray one a moment.
	time.Sleep(200 * time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) != 0 {
		t.Fatalf("expected 0 deliveries for unsubscribed event, got %d", len(c.bodies))
	}
}

// TestProductionClientBlocksInternalDelivery proves the default constructor
// wires the SSRF-restricted client: a delivery to a loopback endpoint (what
// httptest binds) must be refused at dial time, so the catcher records nothing.
func TestProductionClientBlocksInternalDelivery(t *testing.T) {
	c := &collector{}
	srv := httptest.NewServer(http.HandlerFunc(c.handler))
	defer srv.Close()

	store := &fakeStore{subs: []db.WebhookSubscription{
		sub(t, subID1, "", []string{EventIssueStatusChanged}, srv.URL),
	}}
	d := New(store) // real restricted client
	d.DispatchIssueStatusChanged(IssueStatusChanged{
		WorkspaceID: wsID,
		Issue:       map[string]any{"id": "issue-1"},
	})

	// Allow the (doomed) attempts to run; they fail fast at dial.
	time.Sleep(300 * time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) != 0 {
		t.Fatalf("SSRF guard should have blocked loopback delivery, got %d deliveries", len(c.bodies))
	}
}

func TestDeliverRetriesOn5xx(t *testing.T) {
	c := &collector{wg: &sync.WaitGroup{}}
	c.failNext.Store(2) // fail twice, succeed on the 3rd attempt
	srv := httptest.NewServer(http.HandlerFunc(c.handler))
	defer srv.Close()

	// Shorten backoff so the test is fast.
	orig := retryBackoff
	retryBackoff = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
	defer func() { retryBackoff = orig }()

	store := &fakeStore{subs: []db.WebhookSubscription{
		sub(t, subID1, "", []string{EventIssueStatusChanged}, srv.URL),
	}}
	d := newWithClient(store, &http.Client{Timeout: deliveryTimeout})

	c.wg.Add(1) // one eventual success
	d.DispatchIssueStatusChanged(IssueStatusChanged{
		WorkspaceID: wsID,
		Issue:       map[string]any{"id": "issue-1"},
	})

	waitTimeout(t, c.wg, 5*time.Second)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) != 3 {
		t.Fatalf("expected 3 attempts (2 failures + 1 success), got %d", len(c.bodies))
	}
}

func waitTimeout(t *testing.T, wg *sync.WaitGroup, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("timed out waiting for deliveries")
	}
}
