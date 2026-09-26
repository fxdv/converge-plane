package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"converge/internal/config"
	"converge/internal/netx"
)

// mountedRouter serves s behind the client-IP middleware, as httpx does.
func mountedRouter(s *Service) http.Handler {
	r := chi.NewRouter()
	r.Use(netx.NewResolver(nil).Middleware)
	s.Mount(r)
	return r
}

func sessionProbe(h http.Handler, remote, xff string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	req.RemoteAddr = remote
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthRoutesRateLimitedPerClientIP(t *testing.T) {
	s := NewService(nil, config.Config{SessionSecret: "x"}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	h := mountedRouter(s)
	for i := 0; i < authIPBurst; i++ {
		if rec := sessionProbe(h, "127.0.0.1:1", "198.51.100.7"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d within budget = %d", i+1, rec.Code)
		}
	}
	rec := sessionProbe(h, "127.0.0.1:1", "198.51.100.7")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("over budget = %d (Retry-After %q), want 429 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
	// A spoofed leftmost X-Forwarded-For entry must not buy a fresh budget.
	if rec := sessionProbe(h, "127.0.0.1:1", "203.0.113.1, 198.51.100.7"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed XFF escaped the limit: %d", rec.Code)
	}
	if rec := sessionProbe(h, "127.0.0.1:1", "198.51.100.8"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a different client shares the budget: %d", rec.Code)
	}
}

func createCode(s *Service, email string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"email": email})
	rec := httptest.NewRecorder()
	s.handleCreateCode(rec, httptest.NewRequest(http.MethodPost, "/api/auth/signinup/code", bytes.NewReader(body)))
	return rec
}

func TestCreateCodeRateLimitedPerEmail(t *testing.T) {
	s, _, email := dbService(t)
	for i := 0; i < authEmailBurst; i++ {
		if rec := createCode(s, email); rec.Code != http.StatusOK {
			t.Fatalf("code %d within budget = %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if rec := createCode(s, email); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("code past the per-email budget = %d, want 429", rec.Code)
	}
}

func TestThrottledResendKeepsTheCurrentLink(t *testing.T) {
	s, _, email := dbService(t)
	code, preAuth, _, err := s.issueCode(context.Background(), email)
	if err != nil {
		t.Fatalf("issueCode: %v", err)
	}
	for i := 0; i < authEmailBurst; i++ {
		s.emailLimit.Allow(email)
	}
	body, _ := json.Marshal(map[string]string{"preAuthSessionId": preAuth})
	rec := httptest.NewRecorder()
	s.handleResendCode(rec, httptest.NewRequest(http.MethodPost, "/api/auth/signinup/code/resend", bytes.NewReader(body)))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("resend past the per-email budget = %d, want 429", rec.Code)
	}
	if got := consume(t, s, code, preAuth); got != "OK" {
		t.Fatalf("link after a throttled resend = %q, want OK (the code must not rotate)", got)
	}
}
