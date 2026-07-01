package octo_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/integrations/octo"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// These tests exercise the Octo services against the generalized channel_*
// tables on a real PostgreSQL instance. They follow the repo convention (see
// internal/handler/handler_test.go): read DATABASE_URL, and skip — never fail —
// when no database is reachable, so the suite is a no-op locally without a DB
// but runs for real in CI.

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	if pool, err := pgxpool.New(ctx, dbURL); err == nil {
		if perr := pool.Ping(ctx); perr == nil {
			testPool = pool
		} else {
			fmt.Printf("octo DB tests will skip: database not reachable: %v\n", perr)
			pool.Close()
		}
	} else {
		fmt.Printf("octo DB tests will skip: cannot connect: %v\n", err)
	}
	code := m.Run()
	if testPool != nil {
		testPool.Close()
	}
	os.Exit(code)
}

// requireDB skips a test when no database is configured, so mock-only tests in
// the package still run locally without a database.
func requireDB(t *testing.T) {
	t.Helper()
	if testPool == nil {
		t.Skip("no database available (set DATABASE_URL)")
	}
}

func randToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// fixture creates a throwaway workspace + user + member + agent and returns
// their IDs, registering cleanup that cascades everything away.
func fixture(t *testing.T) (workspaceID, userID, agentID pgtype.UUID) {
	t.Helper()
	ctx := context.Background()

	slug := "octo-test-" + randToken()[:8]
	email := "octo-test-" + randToken()[:8] + "@example.com"

	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id`,
		"Octo Test WS", slug).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (email, name) VALUES ($1, $2) RETURNING id`,
		email, "Octo Tester").Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		workspaceID, userID); err != nil {
		t.Fatalf("create member: %v", err)
	}
	var runtimeID pgtype.UUID
	if err := testPool.QueryRow(ctx,
		`INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider)
		 VALUES ($1, 'Octo Runtime', 'local', 'octo_test') RETURNING id`,
		workspaceID).Scan(&runtimeID); err != nil {
		t.Fatalf("create agent_runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id)
		 VALUES ($1, $2, 'local', $3) RETURNING id`,
		workspaceID, "Octo Agent", runtimeID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	wsID, uID := workspaceID, userID
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel_installation WHERE workspace_id = $1`, wsID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, uID)
	})
	return workspaceID, userID, agentID
}

// mustInstallSvc builds an InstallationService over the test pool, failing the
// test if construction errors.
func mustInstallSvc(t *testing.T, q *db.Queries) *octo.InstallationService {
	t.Helper()
	svc, err := octo.NewInstallationService(q, newBox(t))
	if err != nil {
		t.Fatalf("NewInstallationService: %v", err)
	}
	return svc
}

// newInstallation creates an Octo channel_installation via the production
// service (so the config blob is encoded exactly as production writes it) and
// returns the row.
func newInstallation(t *testing.T, svc *octo.InstallationService, wsID, userID, agentID pgtype.UUID) db.ChannelInstallation {
	t.Helper()
	inst, err := svc.Upsert(context.Background(), octo.InstallationParams{
		WorkspaceID:     wsID,
		AgentID:         agentID,
		BotToken:        "bf_" + randToken(),
		RobotID:         "robot_" + randToken(),
		BotName:         "Octo-Z",
		OwnerUID:        "owner_uid_x",
		APIURL:          "https://im.example/api",
		WSURL:           "wss://im.example/ws",
		InstallerUserID: userID,
	})
	if err != nil {
		t.Fatalf("Upsert installation: %v", err)
	}
	return inst
}
