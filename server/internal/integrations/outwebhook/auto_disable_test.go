package outwebhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Failure-tracking tests (#38). Cover the dispatcher's interaction with the
// auto-disable bookkeeping queries — the success path resets, the failure
// path increments, and crossing the configured threshold flips enabled=false.

// withEnv temporarily sets an env var and restores the previous value on
// cleanup. Used to drive autoDisableThreshold() in tests.
func withEnv(t *testing.T, key, value string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("setenv %s=%s: %v", key, value, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestDispatcher_SuccessResetsFailureCounter(t *testing.T) {
	// A 2xx delivery should call ResetWebhookSubscriptionFailures so a recovered
	// subscription has its counter cleared. The query itself guards against
	// no-op updates (WHERE consecutive_failures > 0); the dispatcher always
	// calls it on success — the cheap check happens at the SQL layer.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := &fakeStore{subs: []db.WebhookSubscription{
		sub(t, subID1, "", []string{EventIssueStatusChanged}, srv.URL),
	}}
	d := newTestDispatcher(t, store, &http.Client{Timeout: deliveryTimeout})

	d.DispatchIssueStatusChanged(IssueStatusChanged{WorkspaceID: wsID, Issue: map[string]any{"id": "i"}})
	waitForRecords(t, store, 1, 2*time.Second)
	// Failure-tracking writes are best-effort and async; give them a moment.
	deadline := time.Now().Add(time.Second)
	for store.resetCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if store.resetCount() != 1 {
		t.Errorf("expected one ResetWebhookSubscriptionFailures call, got %d", store.resetCount())
	}
	if len(store.incrementParams()) != 0 {
		t.Errorf("expected zero increment calls on success, got %d", len(store.incrementParams()))
	}
}

func TestDispatcher_FailureIncrementsCounter(t *testing.T) {
	// A 4xx is terminal and should call the increment query exactly once with
	// the configured threshold so the SQL can decide whether to flip enabled.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	withEnv(t, envAutoDisableThreshold, "5")
	store := &fakeStore{subs: []db.WebhookSubscription{
		sub(t, subID1, "", []string{EventIssueStatusChanged}, srv.URL),
	}}
	d := newTestDispatcher(t, store, &http.Client{Timeout: deliveryTimeout})

	d.DispatchIssueStatusChanged(IssueStatusChanged{WorkspaceID: wsID, Issue: map[string]any{"id": "i"}})
	waitForRecords(t, store, 1, 2*time.Second)
	deadline := time.Now().Add(time.Second)
	for len(store.incrementParams()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	params := store.incrementParams()
	if len(params) != 1 {
		t.Fatalf("expected one increment, got %d", len(params))
	}
	if params[0].Threshold != 5 {
		t.Errorf("threshold = %d, want 5 (env-driven)", params[0].Threshold)
	}
	if store.resetCount() != 0 {
		t.Errorf("expected zero resets on failure, got %d", store.resetCount())
	}
}

func TestDispatcher_ExhaustedRetriesIncrementsOnce(t *testing.T) {
	// Three 5xx attempts → retries exhausted → one terminal "failed" record
	// → one increment. Verifies bookkeeping counts deliveries, not attempts:
	// a flaky receiver that fails three times in a row should not pretend
	// three separate subscriptions broke.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := &fakeStore{subs: []db.WebhookSubscription{
		sub(t, subID1, "", []string{EventIssueStatusChanged}, srv.URL),
	}}
	d := newTestDispatcher(t, store, &http.Client{Timeout: deliveryTimeout})

	d.DispatchIssueStatusChanged(IssueStatusChanged{WorkspaceID: wsID, Issue: map[string]any{"id": "i"}})
	waitForRecords(t, store, 1, 3*time.Second)
	deadline := time.Now().Add(time.Second)
	for len(store.incrementParams()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(store.incrementParams()); got != 1 {
		t.Errorf("expected one increment per terminal delivery, got %d", got)
	}
}

func TestDispatcher_ThresholdTripsAutoDisable(t *testing.T) {
	// Drive enough failed deliveries to cross the threshold and assert the
	// fake's enabled flag flips to false with the auto_disabled reason.
	// Verifies end-to-end: env → autoDisableThreshold → query argument →
	// fake's threshold gate (which mirrors the SQL gate).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	withEnv(t, envAutoDisableThreshold, "3")
	s := sub(t, subID1, "", []string{EventIssueStatusChanged}, srv.URL)
	store := &fakeStore{subs: []db.WebhookSubscription{s}}
	d := newTestDispatcher(t, store, &http.Client{Timeout: deliveryTimeout})

	for i := 0; i < 3; i++ {
		d.DispatchIssueStatusChanged(IssueStatusChanged{WorkspaceID: wsID, Issue: map[string]any{"id": "i"}})
	}
	waitForRecords(t, store, 3, 3*time.Second)
	deadline := time.Now().Add(time.Second)
	for len(store.incrementParams()) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	store.mu.Lock()
	key := uuidStr(s.ID)
	enabled := store.enabledByID[key]
	reason := store.disabledReason[key]
	failures := store.failuresByID[key]
	store.mu.Unlock()
	if enabled {
		t.Errorf("subscription should be auto-disabled after threshold; failures=%d", failures)
	}
	if reason != "auto_disabled_failure_threshold" {
		t.Errorf("disabled_reason = %q, want auto_disabled_failure_threshold", reason)
	}
}

func TestDispatcher_ThresholdZeroNeverDisables(t *testing.T) {
	// Threshold=0 disables the auto-disable feature. Counter still
	// increments (operator can see "this is failing"), but enabled stays
	// true regardless of how many failures pile up.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	withEnv(t, envAutoDisableThreshold, "0")
	s := sub(t, subID1, "", []string{EventIssueStatusChanged}, srv.URL)
	store := &fakeStore{subs: []db.WebhookSubscription{s}}
	d := newTestDispatcher(t, store, &http.Client{Timeout: deliveryTimeout})

	for i := 0; i < 5; i++ {
		d.DispatchIssueStatusChanged(IssueStatusChanged{WorkspaceID: wsID, Issue: map[string]any{"id": "i"}})
	}
	waitForRecords(t, store, 5, 3*time.Second)
	deadline := time.Now().Add(time.Second)
	for len(store.incrementParams()) < 5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	store.mu.Lock()
	enabled := store.enabledByID[uuidStr(s.ID)]
	store.mu.Unlock()
	if !enabled {
		t.Errorf("threshold=0 must leave subscription enabled regardless of failures")
	}
}

// Regression for the disable-then-in-flight-success race (Jerry-Xin review):
// if a stale success lands after auto-disable trips, ResetWebhookSubscriptionFailures
// must NOT zero the counter — that would leave the row in the contradictory
// state enabled=false + reason=auto_disabled + counter=0. Preserving the
// post-trip counter keeps the audit honest about why the subscription was
// disabled.
func TestDispatcher_ResetSkippedWhenAlreadyAutoDisabled(t *testing.T) {
	store := &fakeStore{}
	store.failuresByID = map[string]int32{}
	store.enabledByID = map[string]bool{}
	store.disabledReason = map[string]string{}
	s := sub(t, subID1, "", []string{EventIssueStatusChanged}, "http://example.invalid")
	key := uuidStr(s.ID)
	// Simulate post-auto-disable state: enabled=false, counter=20, reason set.
	store.failuresByID[key] = 20
	store.enabledByID[key] = false
	store.disabledReason[key] = "auto_disabled_failure_threshold"

	d := newTestDispatcher(t, store, &http.Client{Timeout: deliveryTimeout})

	// Drive markDelivered directly — simulates the in-flight delivery that
	// finished a few ms after the disable trip.
	d.markDelivered(s.ID)

	store.mu.Lock()
	gotFailures := store.failuresByID[key]
	gotEnabled := store.enabledByID[key]
	gotReason := store.disabledReason[key]
	store.mu.Unlock()

	if gotFailures != 20 {
		t.Errorf("counter must stay at 20 after stale success; got %d", gotFailures)
	}
	if gotEnabled {
		t.Errorf("enabled must remain false after stale success; subscription must not self-revive")
	}
	if gotReason != "auto_disabled_failure_threshold" {
		t.Errorf("disabled_reason must remain set; got %q", gotReason)
	}
}

// TestPush should mark the synthetic payload as TEST-0 / "Multica webhook test
// push" so the receiver can distinguish test traffic, and it should flow
// through the normal deliver()/record() path (delivery_history row, signing,
// failure-counter bookkeeping).
func TestDispatcher_TestPushDeliversSyntheticPayload(t *testing.T) {
	c := &collector{}
	srv := httptest.NewServer(http.HandlerFunc(c.handler))
	defer srv.Close()

	s := sub(t, subID1, "", []string{EventIssueStatusChanged}, srv.URL)
	store := &fakeStore{}
	d := newTestDispatcher(t, store, &http.Client{Timeout: deliveryTimeout})

	if ok := d.TestPush(s); !ok {
		t.Fatalf("TestPush returned false on a fresh dispatcher")
	}
	recs := waitForRecords(t, store, 1, 2*time.Second)
	if string(recs[0].Event) != EventIssueStatusChanged {
		t.Errorf("test push event = %q, want %q", recs[0].Event, EventIssueStatusChanged)
	}
	// The synthetic body must carry the TEST-0 marker; operators reading
	// delivery history rely on this to tell test pushes from real events.
	body := string(recs[0].RequestBody)
	if !strings.Contains(body, `"identifier":"TEST-0"`) || !strings.Contains(body, `"webhook-test"`) {
		t.Errorf("test push body missing test markers: %s", body)
	}
}

func TestDispatcher_TestPushAfterCloseReturnsFalse(t *testing.T) {
	// Once shutdown begins, TestPush must reject — mirrors Redeliver's
	// guard against sending on closed channels. Use the public Close()
	// lifecycle so worker goroutines actually drain rather than leaking
	// until process exit (Jerry-Xin review).
	store := &fakeStore{}
	d := newWithClient(store, &http.Client{Timeout: deliveryTimeout})
	d.retryBackoff = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}

	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.Close(closeCtx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if ok := d.TestPush(sub(t, subID1, "", []string{EventIssueStatusChanged}, "http://x")); ok {
		t.Errorf("TestPush should refuse after Close")
	}
}
