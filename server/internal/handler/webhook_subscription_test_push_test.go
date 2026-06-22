package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Test push endpoint (#38).

func TestTestWebhookSubscription_OK(t *testing.T) {
	t.Cleanup(func() { cleanupWebhookSubscriptions(t) })

	sub := createWebhookForTest(t, map[string]any{"url": "https://example.com/hook"})

	fake := &fakeWebhookRedeliverer{testRet: true}
	testHandler.WebhookDispatcher = fake
	t.Cleanup(func() { testHandler.WebhookDispatcher = nil })

	req := withURLParam(newRequest("POST", "/x", nil), "id", sub.ID)
	w := httptest.NewRecorder()
	testHandler.TestWebhookSubscription(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	if !fake.testCalled {
		t.Fatalf("dispatcher.TestPush was not invoked")
	}
	if len(fake.testCallSubs) != 1 {
		t.Errorf("test push subs len = %d, want 1", len(fake.testCallSubs))
	}
	// The handler passes the loaded row through; the dispatcher uses its
	// WorkspaceID / Url / Secret. Sanity-check the URL round-tripped.
	if fake.testCallSubs[0].Url != "https://example.com/hook" {
		t.Errorf("test push url = %q, want https://example.com/hook", fake.testCallSubs[0].Url)
	}
}

func TestTestWebhookSubscription_DisabledRejected(t *testing.T) {
	// A disabled subscription must reject test pushes the same way it
	// rejects manual redeliveries — kill switch is authoritative for every
	// non-automatic egress path (only operator re-enable should resume).
	t.Cleanup(func() { cleanupWebhookSubscriptions(t) })

	sub := createWebhookForTest(t, map[string]any{"url": "https://example.com/hook"})
	if _, err := testPool.Exec(context.Background(),
		`UPDATE webhook_subscription SET enabled = false WHERE id = $1`, sub.ID); err != nil {
		t.Fatalf("disable subscription: %v", err)
	}

	fake := &fakeWebhookRedeliverer{testRet: true}
	testHandler.WebhookDispatcher = fake
	t.Cleanup(func() { testHandler.WebhookDispatcher = nil })

	req := withURLParam(newRequest("POST", "/x", nil), "id", sub.ID)
	w := httptest.NewRecorder()
	testHandler.TestWebhookSubscription(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if fake.testCalled {
		t.Errorf("dispatcher must not be called for a disabled subscription")
	}
}

func TestTestWebhookSubscription_DispatcherUnavailable(t *testing.T) {
	// nil dispatcher (the test harness never wired one) is a 503, not a 500
	// — same surface as redeliver.
	t.Cleanup(func() { cleanupWebhookSubscriptions(t) })

	sub := createWebhookForTest(t, map[string]any{"url": "https://example.com/hook"})

	testHandler.WebhookDispatcher = nil

	req := withURLParam(newRequest("POST", "/x", nil), "id", sub.ID)
	w := httptest.NewRecorder()
	testHandler.TestWebhookSubscription(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestTestWebhookSubscription_QueueFull(t *testing.T) {
	// TestPush returning false (queue full) → 503 so the operator can retry.
	t.Cleanup(func() { cleanupWebhookSubscriptions(t) })

	sub := createWebhookForTest(t, map[string]any{"url": "https://example.com/hook"})

	fake := &fakeWebhookRedeliverer{testRet: false}
	testHandler.WebhookDispatcher = fake
	t.Cleanup(func() { testHandler.WebhookDispatcher = nil })

	req := withURLParam(newRequest("POST", "/x", nil), "id", sub.ID)
	w := httptest.NewRecorder()
	testHandler.TestWebhookSubscription(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if !fake.testCalled {
		t.Errorf("dispatcher should have been called before being declared unavailable")
	}
}
