package handler

import (
	"encoding/json"
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
	// Empty defaults to the single supported event.
	b, err := validateWebhookEvents(nil)
	if err != nil {
		t.Fatalf("validateWebhookEvents(nil) error: %v", err)
	}
	var got []string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0] != outwebhook.EventIssueStatusChanged {
		t.Errorf("default events = %v, want [%s]", got, outwebhook.EventIssueStatusChanged)
	}

	// Known event passes.
	if _, err := validateWebhookEvents([]string{outwebhook.EventIssueStatusChanged}); err != nil {
		t.Errorf("known event rejected: %v", err)
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
