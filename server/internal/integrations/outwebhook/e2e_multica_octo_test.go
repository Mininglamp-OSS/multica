// e2e_multica_octo_test.go — verifies the multica outbound webhook system
// delivers an issue.status_changed event to octo-server's new /multica
// adapter end-to-end against a live local stack.
//
// Build tag keeps this off normal `go test`. Run with:
//
//   E2E_WEBHOOK_URL='http://localhost:28180/v1/incoming-webhooks/<id>/<token>/multica' \
//     go test -tags e2e_multica -run TestE2E_MulticaOctoWebhook -v ./internal/integrations/outwebhook/
//
// The test:
//   1. Connects to the local Postgres (DATABASE_URL).
//   2. Seeds a workspace + project + webhook subscription pointing at the
//      passed octo /multica URL.
//   3. Wires the real *outwebhook.Dispatcher and calls TestPush(sub).
//   4. Polls outbound_webhook_delivery for the resulting row and asserts
//      response_status=200 — i.e. octo accepted the synthetic payload.
//
//go:build e2e_multica
// +build e2e_multica

package outwebhook

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestE2E_MulticaOctoWebhook(t *testing.T) {
	octoURL := os.Getenv("E2E_WEBHOOK_URL")
	if octoURL == "" {
		t.Skip("set E2E_WEBHOOK_URL to a live octo /multica endpoint to run")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pg connect: %v", err)
	}
	defer pool.Close()

	q := db.New(pool)

	// Seed a workspace + owner user + webhook subscription. Use unique names
	// keyed by current Unix-ns to avoid collisions across reruns.
	now := time.Now().UnixNano()
	var wsID, userID, subID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (email, name)
		VALUES ($1, 'E2E webhook test user')
		RETURNING id`, formatTestEmail(now)).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, issue_prefix)
		VALUES ($1, $2, 'TEST')
		RETURNING id`,
		"E2E Webhook Test", formatTestSlug(now)).Scan(&wsID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	// The dispatcher itself does not check membership — the subscription's
	// workspace_id and url are all it needs. We skip seeding workspace_member
	// to keep this test focused on the delivery contract (auth gating is
	// covered by the handler-side unit tests).
	t.Cleanup(func() {
		// Cascade deletes drop the subscription + delivery rows.
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})

	// Real production delivery uses the SSRF-restricted client which rejects
	// loopback — fine in production, not fine for a localhost E2E. The
	// dispatcher's newWithClient takes any client; here we feed it the
	// default (loopback-permissive) one to exercise the real
	// signing/marshal/retry path against our local octo. The publicURL is set
	// so the enriched issue_url is exercised end-to-end through octo's renderer.
	d := newWithClient(q, "https://app.multica.test", defaultPermissiveHTTPClient())
	t.Cleanup(func() {
		closeCtx, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelClose()
		_ = d.Close(closeCtx)
	})

	if err := pool.QueryRow(ctx, `
		INSERT INTO webhook_subscription (workspace_id, url, secret, events, enabled)
		VALUES ($1, $2, 'whsec_e2e_test_secret', '["issue.status_changed"]'::jsonb, true)
		RETURNING id`, wsID, octoURL).Scan(&subID); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	sub, err := q.GetWebhookSubscriptionInWorkspace(ctx, db.GetWebhookSubscriptionInWorkspaceParams{
		ID:          parseUUIDForTest(t, subID),
		WorkspaceID: parseUUIDForTest(t, wsID),
	})
	if err != nil {
		t.Fatalf("load subscription: %v", err)
	}

	if !d.TestPush(sub) {
		t.Fatal("TestPush returned false — queue full or shutdown")
	}

	// Poll for the resulting delivery row.
	deadline := time.Now().Add(15 * time.Second)
	var status string
	var respStatus *int32
	for time.Now().Before(deadline) {
		var rs int32
		var st string
		var valid bool
		err := pool.QueryRow(ctx, `
			SELECT status, response_status IS NOT NULL, COALESCE(response_status, 0)
			FROM outbound_webhook_delivery
			WHERE subscription_id = $1
			ORDER BY created_at DESC LIMIT 1`, subID).Scan(&st, &valid, &rs)
		if err == nil {
			status = st
			if valid {
				respStatus = &rs
			}
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if status == "" {
		t.Fatal("no delivery row appeared within deadline")
	}
	if status != "delivered" {
		t.Errorf("delivery status = %q, want delivered", status)
	}
	if respStatus == nil || *respStatus != 200 {
		t.Errorf("response_status = %v, want 200", respStatus)
	}
	t.Logf("E2E success: multica → octo /multica delivered status=%q http=%v", status, respStatus)
}
