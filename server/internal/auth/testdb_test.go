package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"converge/internal/config"
)

// testServiceWithDB builds a Service against a real database. Tests that
// use it are gated on CONVERGE_TEST_DATABASE_URL (see apitoken_test.go).
func testServiceWithDB(t *testing.T, dbURL string) (*Service, *pgxpool.Pool, func(), error) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open pool: %w", err)
	}
	ctx := context.Background()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, nil, fmt.Errorf("ping: %w", err)
	}
	s := NewService(pool, config.Config{
		SessionSecret:   "test-secret",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 720 * time.Hour,
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return s, pool, func() { pool.Close() }, nil
}

// randomUUID renders an RFC-4122-shaped id from crypto/rand, so the tests
// do not take a uuid dependency.
func randomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // rand.Read on /dev/urandom does not fail in practice
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// createTestAccount inserts a throwaway account + api token and registers
// their removal, so the dogfood database stays clean.
func createTestAccount(t *testing.T, pool *pgxpool.Pool, status string) (string, string) {
	t.Helper()
	ctx := context.Background()
	accountID := randomUUID()
	email := hex.EncodeToString([]byte(randomUUID()))[:16] + "@test.local"
	plaintext, hash, err := IssueAPIToken()
	if err != nil {
		t.Fatalf("IssueAPIToken: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, name, status, kind)
		values ($1, $2, 'test', $3, 'agent')`, accountID, email, status); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into api_tokens (account_id, token_hash, token_prefix, expires_at)
		values ($1, $2, $3, now() + interval '10 years')`,
		accountID, hash, APITokenPrefix); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `delete from accounts where id = $1`, accountID); err != nil {
			t.Logf("cleanup account %s: %v", accountID, err)
		}
	})
	return accountID, plaintext
}
