package seed

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"converge/internal/migrate"
)

const scratchDB = "converge_seed_test"

// adminPool opens the provided test database; the scratch database tests
// run migrations + seed in a throwaway database and drop it after.
func adminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("CONVERGE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("CONVERGE_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestRunSkipsWhenPresent pins the docker-compose contract against the
// real (already seeded) dogfood database: a re-run is a no-op success
// that writes nothing, so one-shot seeders can exit 0 forever.
func TestRunSkipsWhenPresent(t *testing.T) {
	pool := adminPool(t)
	ctx := context.Background()
	count := func() int {
		var n int
		if err := pool.QueryRow(ctx, `select count(*) from workspaces where slug = $1`, demoSlug).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	before := count()
	email, err := Run(ctx, pool, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("Run on a seeded database: %v", err)
	}
	if email != demoEmail {
		t.Fatalf("returned email %q, want %q", email, demoEmail)
	}
	if after := count(); after != before {
		t.Fatalf("workspace count changed %d -> %d; the skip path must write nothing", before, after)
	}
}

// TestRunSeedsFreshDatabase seeds a throwaway database from scratch and
// re-runs to prove idempotency end to end.
func TestRunSeedsFreshDatabase(t *testing.T) {
	admin := adminPool(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `drop database if exists `+scratchDB); err != nil {
		t.Skipf("cannot drop scratch database (permission?): %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `drop database if exists `+scratchDB) })
	if _, err := admin.Exec(ctx, `create database `+scratchDB); err != nil {
		t.Skipf("cannot create scratch database: %v", err)
	}
	cfg, err := pgx.ParseConfig(os.Getenv("CONVERGE_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse test dsn: %v", err)
	}
	// Same credentials and host, different database.
	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=disable",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, scratchDB))
	if err != nil {
		t.Fatalf("open scratch pool: %v", err)
	}
	defer pool.Close()
	if err := migrate.Run(ctx, pool); err != nil {
		t.Fatalf("migrate scratch: %v", err)
	}

	email, err := Run(ctx, pool, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("Run on a fresh database: %v", err)
	}
	if email != demoEmail {
		t.Fatalf("email = %q, want %q", email, demoEmail)
	}

	q := func(sql string, args ...any) int {
		var n int
		if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("query %q: %v", sql, err)
		}
		return n
	}
	if n := q(`select count(*) from workspaces where slug = $1`, demoSlug); n != 1 {
		t.Fatalf("demo workspaces = %d, want 1", n)
	}
	if n := q(`select count(*) from accounts where email = $1`, demoEmail); n != 1 {
		t.Fatalf("demo account = %d, want 1", n)
	}
	if n := q(`select count(*) from accounts where kind = 'agent'`); n != 2 {
		t.Fatalf("agent accounts = %d, want 2", n)
	}
	if n := q(`select count(*) from api_tokens`); n != 2 {
		t.Fatalf("api tokens = %d, want 2 (one per agent)", n)
	}
	if n := q(`select count(*) from teams`); n < 1 {
		t.Fatalf("teams = %d, want >= 1", n)
	}
	teams := func() int { return q(`select count(*) from teams`) }
	issues := func() int { return q(`select count(*) from issues`) }
	beforeTeams, beforeIssues := teams(), issues()

	// The second run must be a no-op: idempotency is the compose contract.
	if _, err := Run(ctx, pool, slog.New(slog.NewTextHandler(os.Stderr, nil))); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if n := teams(); n != beforeTeams {
		t.Fatalf("teams changed %d -> %d on re-run", beforeTeams, n)
	}
	if n := issues(); n != beforeIssues {
		t.Fatalf("issues changed %d -> %d on re-run", beforeIssues, n)
	}
}
