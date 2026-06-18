package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestOutboundWebhookDeliveryCleanupJob_Spec validates the static job config so
// a typo in cadence/timeouts is caught without a database.
func TestOutboundWebhookDeliveryCleanupJob_Spec(t *testing.T) {
	spec := OutboundWebhookDeliveryCleanupJob(nil)
	if spec.Name != JobNameOutboundWebhookCleanup {
		t.Errorf("Name = %q, want %q", spec.Name, JobNameOutboundWebhookCleanup)
	}
	if spec.Handler == nil {
		t.Error("Handler is nil")
	}
	if spec.Cadence <= 0 || spec.RunTimeout <= 0 {
		t.Errorf("non-positive cadence/timeout: cadence=%v runTimeout=%v", spec.Cadence, spec.RunTimeout)
	}
}

// TestOutboundWebhookCleanupHandler_PurgesOld inserts a delivery older than the
// retention window and a recent one, runs the handler, and asserts only the old
// row is purged.
func TestOutboundWebhookCleanupHandler_PurgesOld(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	wsID, subID := outboundWebhookCleanupFixture(t, pool)
	now := time.Now()

	oldID := insertOutboundDelivery(t, pool, wsID, subID, now.Add(-outboundWebhookDeliveryRetention-time.Hour))
	recentID := insertOutboundDelivery(t, pool, wsID, subID, now.Add(-time.Hour))

	if _, err := makeOutboundWebhookCleanupHandler(pool)(ctx, HandlerInput{PlanTime: now}); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if n := scalarInt(t, pool, `SELECT count(*) FROM outbound_webhook_delivery WHERE id=$1`, oldID); n != 0 {
		t.Errorf("aged delivery should have been purged, count=%d", n)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM outbound_webhook_delivery WHERE id=$1`, recentID); n != 1 {
		t.Errorf("recent delivery should survive, count=%d", n)
	}
}

// outboundWebhookCleanupFixture creates a workspace + webhook subscription the
// delivery rows can reference, with cleanup.
func outboundWebhookCleanupFixture(t *testing.T, pool *pgxpool.Pool) (wsID, subID pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ('OWD Cleanup WS','owd-cleanup-'||substr(md5(random()::text),1,8)) RETURNING id`).Scan(&wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id=$1`, wsID) })
	if err := pool.QueryRow(ctx,
		`INSERT INTO webhook_subscription (workspace_id, url, secret) VALUES ($1,'https://example.com/hook','whsec_test') RETURNING id`,
		wsID).Scan(&subID); err != nil {
		t.Fatalf("create webhook subscription: %v", err)
	}
	return wsID, subID
}

func insertOutboundDelivery(t *testing.T, pool *pgxpool.Pool, wsID, subID pgtype.UUID, createdAt time.Time) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO outbound_webhook_delivery (workspace_id, subscription_id, event, status, created_at)
		 VALUES ($1,$2,'issue.status_changed','delivered',$3) RETURNING id`,
		wsID, subID, createdAt).Scan(&id); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}
	return id
}
