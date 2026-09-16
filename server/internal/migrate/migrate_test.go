package migrate

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestLoad pins the embedded manifest: every migration parses, versions
// are strictly ascending, and no file is empty. A malformed or
// duplicate-numbered file fails at build-startup, not in production.
func TestLoad(t *testing.T) {
	migs, err := load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(migs) == 0 {
		t.Fatal("no migrations embedded")
	}
	for i, m := range migs {
		if m.Version <= 0 {
			t.Errorf("migration %d has version %d", i, m.Version)
		}
		if m.Name == "" {
			t.Errorf("migration %04d has no name", m.Version)
		}
		if len(m.SQL) == 0 {
			t.Errorf("migration %04d_%s is empty", m.Version, m.Name)
		}
		if i > 0 {
			if migs[i-1].Version >= m.Version {
				t.Errorf("versions not strictly ascending at %04d / %04d", migs[i-1].Version, m.Version)
			}
		}
	}
	// The initial schema is the floor: the auth boundary rides on it.
	found := map[int]bool{}
	for _, m := range migs {
		found[m.Version] = true
	}
	if !found[1] {
		t.Fatal("migration 0001 missing from the embedded set")
	}
}

// poolOrSkip opens the test database when provided (local dogfood sets
// CONVERGE_TEST_DATABASE_URL), skipping the CI runs that have none.
func poolOrSkip(t *testing.T) *pgxpool.Pool {
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

// TestRunIdempotentOnApplied pins the startup contract against a real
// database: every migration already applied, Run is a no-op that succeeds
// (the docker-compose up path), and Latest reports the full set.
func TestRunIdempotentOnApplied(t *testing.T) {
	pool := poolOrSkip(t)
	ctx := context.Background()
	want := 0
	if migs, err := load(); err == nil {
		want = migs[len(migs)-1].Version
	}
	if err := Run(ctx, pool); err != nil {
		t.Fatalf("Run on an applied schema: %v", err)
	}
	got, err := Latest(ctx, pool)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got != want {
		t.Fatalf("Latest = %d, want %d (the embedded set)", got, want)
	}
}
