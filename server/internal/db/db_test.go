package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestNewBadDSN pins the fail-fast contract: an unparseable DSN aborts
// startup with the parse stage named, before any socket is touched.
func TestNewBadDSN(t *testing.T) {
	_, err := New(context.Background(), "definitely-not-a-dsn", 1, 5)
	if err == nil {
		t.Fatal("bad DSN accepted")
	}
	if !strings.Contains(err.Error(), "parse database url") {
		t.Fatalf("error = %q, want the parse stage named", err)
	}
}

// TestNewUnreachable pins the second fail-fast stage: the pool builds
// lazily, so an unreachable database is caught by the startup ping — with
// the connection refused within the bound, never a hung boot.
func TestNewUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Port 9 (tcpmux) is closed on this host: the connect fails fast.
	_, err := New(ctx, "postgresql://user:pass@127.0.0.1:9/converge?sslmode=disable&connect_timeout=2", 0, 2)
	if err == nil {
		t.Fatal("unreachable database accepted")
	}
	if !strings.Contains(err.Error(), "ping database") {
		t.Fatalf("error = %q, want the ping stage named", err)
	}
}

// TestHealthyAndClose runs against a real database when
// CONVERGE_TEST_DATABASE_URL is provided; CI without one skips.
func TestHealthyAndClose(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	dbURL := envOr("CONVERGE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("CONVERGE_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	d, err := New(ctx, dbURL, 1, 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()
	if err := d.Healthy(ctx); err != nil {
		t.Fatalf("Healthy: %v", err)
	}
	d.Close()
	// A closed pool must report unhealthy, and repeat safely.
	d.Close()
	if err := d.Healthy(ctx); err == nil {
		t.Fatal("Healthy on a closed pool succeeded")
	}
}

func envOr(key string) string {
	return os.Getenv(key)
}
