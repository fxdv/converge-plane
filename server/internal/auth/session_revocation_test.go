// session_revocation_test.go — the shared revocation check fails
// closed: a database that cannot answer refuses a session it has not
// confirmed recently, and honours one it confirmed within the grace.
package auth

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// unreachableService is a session service whose pool can never connect
// (nothing listens on port 1), so every revocation check errors.
func unreachableService(t *testing.T) *Service {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://x:x@127.0.0.1:1/x?connect_timeout=1")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	s := testService()
	s.pool = pool
	s.log = slog.New(slog.DiscardHandler)
	s.revoked = newRevocations()
	s.live = newRevocations()
	s.verified = newRevocations()
	return s
}

func TestRevocationCheckErrorWithoutVerificationRefuses(t *testing.T) {
	s := unreachableService(t)
	m := s.issueSession("acct-1", "sid-cold")
	if _, ok := s.ValidateAccess(m.AccessToken); ok {
		t.Fatal("a failed check on a never-confirmed session must refuse the token")
	}
	// The refusal is not cached as a revocation: the database's next
	// answer decides.
	if s.revoked.has("sid-cold") {
		t.Fatal("a failed check must not be remembered as a revocation")
	}
}

func TestRevocationCheckErrorWithinGraceAllows(t *testing.T) {
	s := unreachableService(t)
	m := s.issueSession("acct-1", "sid-warm")
	s.verified.add("sid-warm", time.Now().Add(revocationGrace))
	if _, ok := s.ValidateAccess(m.AccessToken); !ok {
		t.Fatal("a failed check inside the grace must allow the token")
	}

	// Past the grace the same failure refuses.
	s.live = newRevocations()
	s.verified.add("sid-warm", time.Now().Add(-time.Second))
	if _, ok := s.ValidateAccess(m.AccessToken); ok {
		t.Fatal("a failed check past the grace must refuse the token")
	}
}

func TestRevocationCheckDatabaseSaysRevoked(t *testing.T) {
	dbURL := os.Getenv("CONVERGE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("CONVERGE_TEST_DATABASE_URL not set")
	}
	s, pool, done, err := testServiceWithDB(t, dbURL)
	if err != nil {
		t.Fatalf("db setup: %v", err)
	}
	t.Cleanup(done)
	ctx := context.Background()
	accountID, _ := createTestAccount(t, pool, "active")
	m, err := s.startSession(ctx, accountID, "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if _, ok := s.ValidateAccess(m.AccessToken); !ok {
		t.Fatal("a live session's token was refused")
	}
	if !s.verified.has(m.SessionID) {
		t.Fatal("a confirmed check must record the confirmation")
	}
	if !s.SessionLive(ctx, m.SessionID) {
		t.Fatal("SessionLive refused a live session")
	}

	if _, err := pool.Exec(ctx, `update sessions set revoked_at = now() where id = $1`, m.SessionID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	s.live = newRevocations() // skip the 2s live window
	if _, ok := s.ValidateAccess(m.AccessToken); ok {
		t.Fatal("a session the database reports revoked was accepted")
	}
	if !s.revoked.has(m.SessionID) {
		t.Fatal("a database revocation must be cached")
	}
	if s.SessionLive(ctx, m.SessionID) {
		t.Fatal("SessionLive accepted a revoked session")
	}
}
