package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/outwebhook"
)

func TestValidateWebhookURL(t *testing.T) {
	valid := []string{
		"https://example.com/hook",
		"http://hooks.acme.dev:9000/in",
		"https://hooks.acme.dev/multica?token=x",
		"https://203.0.113.10/hook", // public IP literal is allowed
	}
	for _, u := range valid {
		if err := validateWebhookURL(u); err != nil {
			t.Errorf("validateWebhookURL(%q) = %v, want nil", u, err)
		}
	}

	invalid := []string{
		"",
		"not-a-url",
		"ftp://example.com/x",
		"ws://example.com",
		"https://", // no host
		// SSRF guard: server-internal targets must be rejected.
		"http://localhost:9000/in",
		"https://127.0.0.1/hook",
		"http://169.254.169.254/latest/meta-data/", // cloud metadata
		"http://10.0.0.5/internal",
		"http://192.168.1.1/admin",
		"http://[::1]/hook", // IPv6 loopback
		"http://0.0.0.0/hook",
	}
	for _, u := range invalid {
		if err := validateWebhookURL(u); err == nil {
			t.Errorf("validateWebhookURL(%q) = nil, want error", u)
		}
	}
}

func TestValidateWebhookEvents(t *testing.T) {
	// Empty is rejected (the default is applied by the create handler, not here).
	if _, err := validateWebhookEvents(nil); err == nil {
		t.Error("empty events should be rejected")
	}
	if _, err := validateWebhookEvents([]string{}); err == nil {
		t.Error("explicit empty events should be rejected")
	}

	// Known event passes and round-trips.
	b, err := validateWebhookEvents([]string{outwebhook.EventIssueStatusChanged})
	if err != nil {
		t.Fatalf("known event rejected: %v", err)
	}
	var got []string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0] != outwebhook.EventIssueStatusChanged {
		t.Errorf("events = %v, want [%s]", got, outwebhook.EventIssueStatusChanged)
	}

	// Unknown event is rejected.
	if _, err := validateWebhookEvents([]string{"issue.created"}); err == nil {
		t.Error("unknown event should be rejected")
	}
}

func TestGenerateWebhookSecret(t *testing.T) {
	s1, err := generateWebhookSecret()
	if err != nil {
		t.Fatalf("generateWebhookSecret error: %v", err)
	}
	if len(s1) <= len(webhookSecretPrefix) {
		t.Errorf("secret too short: %q", s1)
	}
	if s1[:len(webhookSecretPrefix)] != webhookSecretPrefix {
		t.Errorf("secret missing prefix: %q", s1)
	}
	s2, _ := generateWebhookSecret()
	if s1 == s2 {
		t.Error("two generated secrets should differ")
	}
}

func TestSecretHint(t *testing.T) {
	if got := secretHint("whsec_abcd1234"); got != "1234" {
		t.Errorf("secretHint = %q, want 1234", got)
	}
	if got := secretHint("ab"); got != "" {
		t.Errorf("secretHint(short) = %q, want empty", got)
	}
}

// ── DB-backed CRUD / RBAC behavior (skipped when no test DB) ────────────────

func setWebhookTestMemberRole(t *testing.T, role string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE member SET role=$1 WHERE workspace_id=$2 AND user_id=$3`,
		role, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("set member role %q: %v", role, err)
	}
}

func cleanupWebhookSubscriptions(t *testing.T) {
	t.Helper()
	testPool.Exec(context.Background(),
		`DELETE FROM webhook_subscription WHERE workspace_id=$1`, testWorkspaceID)
}

func createWebhookForTest(t *testing.T, body map[string]any) WebhookSubscriptionResponse {
	t.Helper()
	req := newRequest("POST", "/api/webhook-subscriptions", body)
	w := httptest.NewRecorder()
	testHandler.CreateWebhookSubscription(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created WebhookSubscriptionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return created
}

func TestCreateWebhookSubscription_RequiresAdmin(t *testing.T) {
	setWebhookTestMemberRole(t, "member")
	t.Cleanup(func() { setWebhookTestMemberRole(t, "owner") })
	t.Cleanup(func() { cleanupWebhookSubscriptions(t) })

	req := newRequest("POST", "/api/webhook-subscriptions", map[string]any{"url": "https://example.com/hook"})
	w := httptest.NewRecorder()
	testHandler.CreateWebhookSubscription(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("member role should be forbidden, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWebhookSubscription_SecretShownOnce(t *testing.T) {
	t.Cleanup(func() { cleanupWebhookSubscriptions(t) })

	created := createWebhookForTest(t, map[string]any{"url": "https://example.com/hook"})
	if !strings.HasPrefix(created.SecretOnce, webhookSecretPrefix) {
		t.Errorf("create must return the full secret once, got %q", created.SecretOnce)
	}
	if created.SecretHint == "" {
		t.Errorf("create must return secret_hint")
	}

	lreq := newRequest("GET", "/api/webhook-subscriptions", nil)
	lw := httptest.NewRecorder()
	testHandler.ListWebhookSubscriptions(lw, lreq)
	if lw.Code != http.StatusOK {
		t.Fatalf("list: %d %s", lw.Code, lw.Body.String())
	}
	var listResp struct {
		Subscriptions []WebhookSubscriptionResponse `json:"subscriptions"`
	}
	if err := json.Unmarshal(lw.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var found bool
	for _, s := range listResp.Subscriptions {
		if s.ID != created.ID {
			continue
		}
		found = true
		if s.SecretOnce != "" {
			t.Errorf("list must not echo the secret, got %q", s.SecretOnce)
		}
		if s.SecretHint == "" {
			t.Errorf("list should expose secret_hint")
		}
	}
	if !found {
		t.Errorf("created subscription %s missing from list", created.ID)
	}
}

func TestCreateWebhookSubscription_ProjectMustBeInWorkspace(t *testing.T) {
	t.Cleanup(func() { cleanupWebhookSubscriptions(t) })

	foreignProject := "11111111-1111-1111-1111-111111111111"
	req := newRequest("POST", "/api/webhook-subscriptions", map[string]any{
		"url":        "https://example.com/hook",
		"project_id": foreignProject,
	})
	w := httptest.NewRecorder()
	testHandler.CreateWebhookSubscription(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign project should be 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateWebhookSubscription_RejectsEmptyEvents(t *testing.T) {
	t.Cleanup(func() { cleanupWebhookSubscriptions(t) })

	created := createWebhookForTest(t, map[string]any{"url": "https://example.com/hook"})

	ureq := newRequest("PATCH", "/api/webhook-subscriptions/"+created.ID, map[string]any{"events": []string{}})
	ureq = withURLParam(ureq, "id", created.ID)
	uw := httptest.NewRecorder()
	testHandler.UpdateWebhookSubscription(uw, ureq)
	if uw.Code != http.StatusBadRequest {
		t.Fatalf("explicit empty events should be 400, got %d: %s", uw.Code, uw.Body.String())
	}
}
