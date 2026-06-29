package outwebhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// enrichedPayload captures the three enrichment fields added to outboundPayload.
type enrichedPayload struct {
	IssueURL     string `json:"issue_url"`
	AssigneeType string `json:"assignee_type"`
	AssigneeName string `json:"assignee_name"`
}

const (
	memberID = "55555555-5555-5555-5555-555555555555"
	userID   = "66666666-6666-6666-6666-666666666666"
	agentID  = "77777777-7777-7777-7777-777777777777"
	squadID  = "88888888-8888-8888-8888-888888888888"
)

// dispatchOnce runs one event through a publicURL-configured dispatcher against
// a single workspace-level subscription and returns the delivered payload's
// enrichment fields.
func dispatchOnce(t *testing.T, store *fakeStore, publicURL string, ev IssueStatusChanged) enrichedPayload {
	t.Helper()
	c := &collector{wg: &sync.WaitGroup{}}
	srv := httptest.NewServer(http.HandlerFunc(c.handler))
	defer srv.Close()

	store.subs = []db.WebhookSubscription{sub(t, subID1, "", []string{EventIssueStatusChanged}, srv.URL)}
	d := newWithClient(store, publicURL, &http.Client{Timeout: deliveryTimeout})
	d.retryBackoff = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = d.Close(ctx)
	})

	ev.WorkspaceID = wsID
	c.wg.Add(1)
	d.DispatchIssueStatusChanged(ev)
	waitTimeout(t, c.wg, 5*time.Second)

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(c.bodies))
	}
	var got enrichedPayload
	if err := json.Unmarshal(c.bodies[0], &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return got
}

func storeWithWorkspaceSlug(slug string) *fakeStore {
	return &fakeStore{workspaces: map[string]db.Workspace{
		wsID: {Slug: slug},
	}}
}

func TestEnrich_IssueURL_BuiltFromSlugAndIdentifier(t *testing.T) {
	store := storeWithWorkspaceSlug("acme")
	got := dispatchOnce(t, store, "https://app.multica.ai", IssueStatusChanged{
		Identifier: "MUL-123",
		Issue:      map[string]any{"identifier": "MUL-123", "status": "done"},
	})
	if want := "https://app.multica.ai/acme/issues/MUL-123"; got.IssueURL != want {
		t.Errorf("issue_url = %q, want %q", got.IssueURL, want)
	}
}

func TestEnrich_IssueURL_OmittedWithoutPublicURL(t *testing.T) {
	store := storeWithWorkspaceSlug("acme")
	got := dispatchOnce(t, store, "", IssueStatusChanged{
		Identifier: "MUL-123",
		Issue:      map[string]any{"identifier": "MUL-123"},
	})
	if got.IssueURL != "" {
		t.Errorf("issue_url = %q, want empty (no public URL configured)", got.IssueURL)
	}
}

func TestEnrich_IssueURL_OmittedWhenSlugUnresolved(t *testing.T) {
	// Workspace lookup misses (empty store) → no slug → no URL.
	store := &fakeStore{}
	got := dispatchOnce(t, store, "https://app.multica.ai", IssueStatusChanged{
		Identifier: "MUL-123",
		Issue:      map[string]any{"identifier": "MUL-123"},
	})
	if got.IssueURL != "" {
		t.Errorf("issue_url = %q, want empty (slug unresolved)", got.IssueURL)
	}
}

func TestEnrich_IssueURL_EscapesSegments(t *testing.T) {
	store := storeWithWorkspaceSlug("acme corp") // space must be escaped
	got := dispatchOnce(t, store, "https://app.multica.ai", IssueStatusChanged{
		Identifier: "MUL 7",
		Issue:      map[string]any{"identifier": "MUL 7"},
	})
	if want := "https://app.multica.ai/acme%20corp/issues/MUL%207"; got.IssueURL != want {
		t.Errorf("issue_url = %q, want %q", got.IssueURL, want)
	}
}

func TestEnrich_Assignee_Member(t *testing.T) {
	store := storeWithWorkspaceSlug("acme")
	store.members = map[string]db.Member{memberID: {UserID: mustUUID(t, userID)}}
	store.users = map[string]db.User{userID: {Name: "张三"}}

	got := dispatchOnce(t, store, "https://app.multica.ai", IssueStatusChanged{
		Identifier:   "MUL-1",
		AssigneeType: "member",
		AssigneeID:   memberID,
		Issue:        map[string]any{"identifier": "MUL-1"},
	})
	if got.AssigneeType != "member" || got.AssigneeName != "张三" {
		t.Errorf("assignee = %q/%q, want member/张三", got.AssigneeType, got.AssigneeName)
	}
}

func TestEnrich_Assignee_Agent(t *testing.T) {
	store := storeWithWorkspaceSlug("acme")
	store.agents = map[string]db.Agent{agentID: {Name: "Codex Bot"}}

	got := dispatchOnce(t, store, "https://app.multica.ai", IssueStatusChanged{
		Identifier:   "MUL-1",
		AssigneeType: "agent",
		AssigneeID:   agentID,
		Issue:        map[string]any{"identifier": "MUL-1"},
	})
	if got.AssigneeType != "agent" || got.AssigneeName != "Codex Bot" {
		t.Errorf("assignee = %q/%q, want agent/Codex Bot", got.AssigneeType, got.AssigneeName)
	}
}

func TestEnrich_Assignee_Squad(t *testing.T) {
	store := storeWithWorkspaceSlug("acme")
	store.squads = map[string]db.Squad{squadID: {Name: "Platform Squad"}}

	got := dispatchOnce(t, store, "https://app.multica.ai", IssueStatusChanged{
		Identifier:   "MUL-1",
		AssigneeType: "squad",
		AssigneeID:   squadID,
		Issue:        map[string]any{"identifier": "MUL-1"},
	})
	if got.AssigneeType != "squad" || got.AssigneeName != "Platform Squad" {
		t.Errorf("assignee = %q/%q, want squad/Platform Squad", got.AssigneeType, got.AssigneeName)
	}
}

func TestEnrich_Assignee_OmittedWhenUnresolved(t *testing.T) {
	// assignee_type present but the member row is missing → name can't resolve,
	// so BOTH assignee_type and assignee_name are dropped (no bare type).
	store := storeWithWorkspaceSlug("acme")
	got := dispatchOnce(t, store, "https://app.multica.ai", IssueStatusChanged{
		Identifier:   "MUL-1",
		AssigneeType: "member",
		AssigneeID:   memberID, // not in store.members
		Issue:        map[string]any{"identifier": "MUL-1"},
	})
	if got.AssigneeType != "" || got.AssigneeName != "" {
		t.Errorf("assignee = %q/%q, want both empty (unresolved)", got.AssigneeType, got.AssigneeName)
	}
}

func TestEnrich_Assignee_OmittedWhenUnassigned(t *testing.T) {
	store := storeWithWorkspaceSlug("acme")
	got := dispatchOnce(t, store, "https://app.multica.ai", IssueStatusChanged{
		Identifier: "MUL-1",
		Issue:      map[string]any{"identifier": "MUL-1"},
	})
	if got.AssigneeType != "" || got.AssigneeName != "" {
		t.Errorf("assignee = %q/%q, want both empty (unassigned)", got.AssigneeType, got.AssigneeName)
	}
}

// TestResolveAssigneeName_BadUUID guards the parse path directly: a non-UUID
// assignee id returns "" rather than erroring.
func TestResolveAssigneeName_BadUUID(t *testing.T) {
	d := &Dispatcher{store: &fakeStore{}}
	if name := d.resolveAssigneeName(context.Background(), "member", "not-a-uuid"); name != "" {
		t.Errorf("resolveAssigneeName(bad uuid) = %q, want empty", name)
	}
}
