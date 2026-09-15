package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

const testWorkspaceID = "7858469c-7a2d-4107-b171-a2dacb7a936a"

func TestAgentEmail(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"Bravo-1", "ag-7858469c-bravo_1@converge.local"},
		{"alpha", "ag-7858469c-alpha@converge.local"},
		{"ALPHA", "ag-7858469c-alpha@converge.local"},
		{"Bravo One", "ag-7858469c-bravo_one@converge.local"},
		{"a b/c.d", "ag-7858469c-a_b_c_d@converge.local"},
		{"", "ag-7858469c-agent@converge.local"},
		{"!!!", "ag-7858469c-___@converge.local"},
		{strings.Repeat("x", 49), "ag-7858469c-" + strings.Repeat("x", 48) + "@converge.local"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AgentEmail(testWorkspaceID, c.name); got != c.want {
				t.Fatalf("AgentEmail = %q, want %q", got, c.want)
			}
		})
	}
}

func TestIssueAPIToken(t *testing.T) {
	plaintext, hash, err := IssueAPIToken()
	if err != nil {
		t.Fatalf("IssueAPIToken: %v", err)
	}
	if !strings.HasPrefix(plaintext, APITokenPrefix) {
		t.Fatalf("token %q missing prefix %q", plaintext, APITokenPrefix)
	}
	body := strings.TrimPrefix(plaintext, APITokenPrefix)
	if len(body) != 43 { // 32 bytes, unpadded URL-safe base64
		t.Fatalf("token body length = %d, want 43 (256 bits)", len(body))
	}
	sum := sha256.Sum256([]byte(plaintext))
	if hash != hex.EncodeToString(sum[:]) {
		t.Fatal("hash is not sha256(plaintext)")
	}
	other, _, err := IssueAPIToken()
	if err != nil || other == plaintext {
		t.Fatalf("two issued tokens are identical (err=%v)", err)
	}
}

// TestValidateAPIToken exercises the SQL against a real Postgres. It is
// gated on CONVERGE_TEST_DATABASE_URL so CI without a database stays green;
// the local dogfood sets it. The SQL is the contract: revoked, expired and
// suspended-account tokens must all be rejected.
func TestValidateAPIToken(t *testing.T) {
	dbURL := os.Getenv("CONVERGE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("CONVERGE_TEST_DATABASE_URL not set")
	}
	s, pool, done, err := testServiceWithDB(t, dbURL)
	if err != nil {
		t.Fatalf("db setup: %v", err)
	}
	defer done()

	accountID, token := createTestAccount(t, pool, "active")
	ctx := context.Background()

	if got, ok := s.ValidateAPIToken(ctx, token); !ok || got != accountID {
		t.Fatalf("valid token: got=%q ok=%v, want %q true", got, ok, accountID)
	}

	// Revocation kills the token.
	if _, err := pool.Exec(ctx, `update api_tokens set revoked_at = now() where token_hash = $1`, sha256hex(token)); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got, ok := s.ValidateAPIToken(ctx, token); ok {
		t.Fatalf("revoked token accepted: %q", got)
	}

	// A fresh token for a suspended account is dead too.
	suspendedID, suspendedToken := createTestAccount(t, pool, "suspended")
	if _, ok := s.ValidateAPIToken(ctx, suspendedToken); ok {
		t.Fatalf("token of suspended account %q accepted", suspendedID)
	}

	// Expired token is dead.
	accountID2, expiredToken := createTestAccount(t, pool, "active")
	if _, err := pool.Exec(ctx, `update api_tokens set expires_at = now() - interval '1 second' where account_id = $1`, accountID2); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if _, ok := s.ValidateAPIToken(ctx, expiredToken); ok {
		t.Fatal("expired token accepted")
	}
}

// TestValidateAPITokenLastUseThrottle pins the once-per-hour stamp: the
// second call within an hour must not write, so a swarm poll loop costs one
// indexed read.
func TestValidateAPITokenLastUseThrottle(t *testing.T) {
	dbURL := os.Getenv("CONVERGE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("CONVERGE_TEST_DATABASE_URL not set")
	}
	s, pool, done, err := testServiceWithDB(t, dbURL)
	if err != nil {
		t.Fatalf("db setup: %v", err)
	}
	defer done()

	accountID, token := createTestAccount(t, pool, "active")
	ctx := context.Background()

	if _, ok := s.ValidateAPIToken(ctx, token); !ok {
		t.Fatal("first validation failed")
	}
	var afterFirst int
	if err := pool.QueryRow(ctx, `select count(*) from api_tokens where token_hash = $1 and last_used_at is not null`, sha256hex(token)).Scan(&afterFirst); err != nil {
		t.Fatalf("count: %v", err)
	}
	if afterFirst != 1 {
		t.Fatalf("last-use stamp after first validation = %d, want 1", afterFirst)
	}
	// Second call within the hour: still exactly one stamped row, and the
	// timestamp must be unchanged (no rewrite).
	var ts1, ts2 string
	if err := pool.QueryRow(ctx, `select last_used_at::text from api_tokens where token_hash = $1`, sha256hex(token)).Scan(&ts1); err != nil {
		t.Fatalf("read stamp: %v", err)
	}
	if _, ok := s.ValidateAPIToken(ctx, token); !ok {
		t.Fatal("second validation failed")
	}
	if err := pool.QueryRow(ctx, `select last_used_at::text from api_tokens where token_hash = $1`, sha256hex(token)).Scan(&ts2); err != nil {
		t.Fatalf("read stamp: %v", err)
	}
	if ts1 != ts2 {
		t.Fatalf("last-use rewritten within the throttle window: %q -> %q", ts1, ts2)
	}
	_ = accountID
}

func sha256hex(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}
