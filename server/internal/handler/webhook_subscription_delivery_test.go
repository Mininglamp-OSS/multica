package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeWebhookRedeliverer captures Redeliver calls for the redeliver handler test.
type fakeWebhookRedeliverer struct {
	called   bool
	ret      bool
	lastFrom pgtype.UUID
}

func (f *fakeWebhookRedeliverer) Redeliver(_ db.WebhookSubscription, _ string, _ []byte, fromID pgtype.UUID) bool {
	f.called = true
	f.lastFrom = fromID
	return f.ret
}

// insertOutboundDelivery seeds a delivery row for subID and returns its id.
// withBody controls whether request_body is populated (redeliver needs it).
func insertOutboundDelivery(t *testing.T, subID, status string, withBody bool) string {
	t.Helper()
	var body []byte
	if withBody {
		body = []byte(`{"event":"issue.status_changed"}`)
	}
	var id string
	err := testPool.QueryRow(context.Background(),
		`INSERT INTO outbound_webhook_delivery
		   (workspace_id, subscription_id, event, status, attempt_count, response_status, request_body, response_body)
		 VALUES ($1, $2, 'issue.status_changed', $3, 1, 200, $4, 'ok')
		 RETURNING id`,
		testWorkspaceID, subID, status, body).Scan(&id)
	if err != nil {
		t.Fatalf("insert delivery: %v", err)
	}
	return id
}

func TestListWebhookSubscriptionDeliveries_RequiresAdmin(t *testing.T) {
	setWebhookTestMemberRole(t, "member")
	t.Cleanup(func() { setWebhookTestMemberRole(t, "owner") })

	req := newRequest("GET", "/api/webhook-subscriptions/x/deliveries", nil)
	req = withURLParam(req, "id", "44444444-4444-4444-4444-444444444444")
	w := httptest.NewRecorder()
	testHandler.ListWebhookSubscriptionDeliveries(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("member role should be forbidden, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListAndGetWebhookSubscriptionDeliveries(t *testing.T) {
	t.Cleanup(func() { cleanupWebhookSubscriptions(t) })

	sub := createWebhookForTest(t, map[string]any{"url": "https://example.com/hook"})
	insertOutboundDelivery(t, sub.ID, "failed", false)
	withBodyID := insertOutboundDelivery(t, sub.ID, "delivered", true)

	// List — slim, total counts both, bodies omitted.
	lreq := withURLParam(newRequest("GET", "/api/webhook-subscriptions/"+sub.ID+"/deliveries", nil), "id", sub.ID)
	lw := httptest.NewRecorder()
	testHandler.ListWebhookSubscriptionDeliveries(lw, lreq)
	if lw.Code != http.StatusOK {
		t.Fatalf("list: %d %s", lw.Code, lw.Body.String())
	}
	var listResp struct {
		Deliveries []OutboundWebhookDeliveryResponse `json:"deliveries"`
		Total      int64                             `json:"total"`
	}
	if err := json.Unmarshal(lw.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listResp.Total != 2 || len(listResp.Deliveries) != 2 {
		t.Fatalf("total=%d len=%d, want 2/2", listResp.Total, len(listResp.Deliveries))
	}
	for _, d := range listResp.Deliveries {
		if d.RequestBody != nil || d.ResponseBody != nil {
			t.Errorf("list must omit bodies, got request=%v response=%v", d.RequestBody, d.ResponseBody)
		}
	}

	// Detail — bodies present.
	greq := withURLParams(newRequest("GET", "/x", nil), "id", sub.ID, "deliveryId", withBodyID)
	gw := httptest.NewRecorder()
	testHandler.GetWebhookSubscriptionDelivery(gw, greq)
	if gw.Code != http.StatusOK {
		t.Fatalf("get: %d %s", gw.Code, gw.Body.String())
	}
	var got OutboundWebhookDeliveryResponse
	if err := json.Unmarshal(gw.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.RequestBody == nil || *got.RequestBody == "" {
		t.Errorf("detail must include request_body")
	}
}

func TestGetWebhookSubscriptionDelivery_CrossSubscription404(t *testing.T) {
	t.Cleanup(func() { cleanupWebhookSubscriptions(t) })

	subA := createWebhookForTest(t, map[string]any{"url": "https://example.com/a"})
	subB := createWebhookForTest(t, map[string]any{"url": "https://example.com/b"})
	deliveryB := insertOutboundDelivery(t, subB.ID, "delivered", true)

	// Ask for subB's delivery under subA's id — must 404, not leak.
	req := withURLParams(newRequest("GET", "/x", nil), "id", subA.ID, "deliveryId", deliveryB)
	w := httptest.NewRecorder()
	testHandler.GetWebhookSubscriptionDelivery(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-subscription delivery should be 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRedeliverWebhookSubscriptionDelivery(t *testing.T) {
	t.Cleanup(func() { cleanupWebhookSubscriptions(t) })

	sub := createWebhookForTest(t, map[string]any{"url": "https://example.com/hook"})
	noBodyID := insertOutboundDelivery(t, sub.ID, "failed", false)
	withBodyID := insertOutboundDelivery(t, sub.ID, "failed", true)

	// No dispatcher wired → 503.
	r1 := withURLParams(newRequest("POST", "/x", nil), "id", sub.ID, "deliveryId", withBodyID)
	w1 := httptest.NewRecorder()
	testHandler.RedeliverWebhookSubscriptionDelivery(w1, r1)
	if w1.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil dispatcher should be 503, got %d: %s", w1.Code, w1.Body.String())
	}

	fake := &fakeWebhookRedeliverer{ret: true}
	testHandler.WebhookDispatcher = fake
	t.Cleanup(func() { testHandler.WebhookDispatcher = nil })

	// No stored body → 400 (before enqueue).
	r2 := withURLParams(newRequest("POST", "/x", nil), "id", sub.ID, "deliveryId", noBodyID)
	w2 := httptest.NewRecorder()
	testHandler.RedeliverWebhookSubscriptionDelivery(w2, r2)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("missing payload should be 400, got %d: %s", w2.Code, w2.Body.String())
	}
	if fake.called {
		t.Errorf("dispatcher must not be called when payload is missing")
	}

	// With stored body → 202 and lineage passed through.
	r3 := withURLParams(newRequest("POST", "/x", nil), "id", sub.ID, "deliveryId", withBodyID)
	w3 := httptest.NewRecorder()
	testHandler.RedeliverWebhookSubscriptionDelivery(w3, r3)
	if w3.Code != http.StatusAccepted {
		t.Fatalf("redeliver should be 202, got %d: %s", w3.Code, w3.Body.String())
	}
	if !fake.called {
		t.Fatalf("dispatcher Redeliver was not called")
	}
	if uuidToString(fake.lastFrom) != withBodyID {
		t.Errorf("redelivered_from = %q, want %q", uuidToString(fake.lastFrom), withBodyID)
	}
}
