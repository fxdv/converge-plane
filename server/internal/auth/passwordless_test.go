package auth

import (
	"testing"

	"converge/internal/config"
)

// TestMagicLinkFormat pins the verify-URL shape the v17 client parses:
// preAuthSessionId in the query string, the link code in the hash
// fragment. magicLink is pure, so it needs no database.
func TestMagicLinkFormat(t *testing.T) {
	s := &Service{cfg: config.Config{WebOrigin: "http://localhost:3000"}}
	got := s.magicLink("pre-auth-123", "ABCDEF")
	want := "http://localhost:3000/auth/verify?preAuthSessionId=pre-auth-123#ABCDEF"
	if got != want {
		t.Fatalf("magicLink = %q, want %q", got, want)
	}
}
