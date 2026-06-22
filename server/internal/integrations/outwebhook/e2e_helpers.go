//go:build e2e_multica
// +build e2e_multica

package outwebhook

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

// E2E helpers (build-tag gated). The standard build never sees these — they
// only compile when -tags e2e_multica is passed, the same tag the live test
// is guarded by. Keeping them in their own file means the production build
// graph is unchanged.

// defaultPermissiveHTTPClient returns an http.Client without the SSRF
// restrictions netguard adds. Required for the E2E test because octo is
// served from loopback in this stack.
func defaultPermissiveHTTPClient() *http.Client {
	return &http.Client{Timeout: deliveryTimeout}
}

// formatTestEmail / formatTestSlug produce deterministic-yet-unique values
// keyed by the test's start time, so reruns don't collide on the unique
// indexes (email, slug).
func formatTestEmail(now int64) string {
	return fmt.Sprintf("e2e-webhook-%d@example.invalid", now)
}

func formatTestSlug(now int64) string {
	return fmt.Sprintf("e2e-webhook-%d", now)
}

func parseUUIDForTest(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	u, err := util.ParseUUID(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return u
}

// ensureDispatcherIsHealthy guards against the dispatcher silently going
// away mid-test (Close, etc) so a hung TestPush surfaces as a real error.
func ensureDispatcherIsHealthy(t *testing.T, d *Dispatcher) {
	t.Helper()
	select {
	case <-d.stopDispatch:
		t.Fatal("dispatcher already stopped before test push")
	case <-time.After(10 * time.Millisecond):
	}
}
