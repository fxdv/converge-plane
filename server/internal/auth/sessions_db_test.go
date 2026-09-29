package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// signedIn is one browser's cookie jar after a sign-in.
type signedIn struct {
	cookies map[string]*http.Cookie
	sid     string
}

func (b signedIn) inner(t *testing.T, name string) string {
	t.Helper()
	inner, err := innerFromEnvelope(b.cookies[name].Value)
	if err != nil {
		t.Fatalf("%s envelope: %v", name, err)
	}
	return inner
}

func cookiesOf(rec *httptest.ResponseRecorder) map[string]*http.Cookie {
	out := map[string]*http.Cookie{}
	for _, c := range rec.Result().Cookies() {
		out[c.Name] = c
	}
	return out
}

func signIn(t *testing.T, s *Service, email string) signedIn {
	t.Helper()
	code, preAuth, _, err := s.issueCode(context.Background(), email)
	if err != nil {
		t.Fatalf("issueCode: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"linkCode": code, "preAuthSessionId": preAuth})
	rec := httptest.NewRecorder()
	s.handleConsumeCode(rec, httptest.NewRequest(http.MethodPost, "/api/auth/signinup/code/consume", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("consume = %d %s", rec.Code, rec.Body.String())
	}
	b := signedIn{cookies: cookiesOf(rec)}
	c, ok := s.parseToken("refresh", b.inner(t, CookieRefreshToken))
	if !ok {
		t.Fatal("sign-in issued an invalid refresh token")
	}
	b.sid = c.Session
	return b
}

func refreshWith(s *Service, refreshCookie *http.Cookie, antiCsrf string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/session/refresh", nil)
	req.AddCookie(refreshCookie)
	if antiCsrf != "" {
		req.Header.Set(HeaderAntiCsrf, antiCsrf)
	}
	rec := httptest.NewRecorder()
	s.handleRefresh(rec, req)
	return rec
}

func sessionRow(t *testing.T, pool *pgxpool.Pool, sid string) (tokenHash string, revoked bool, reason string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		select token_hash, revoked_at is not null, coalesce(revoked_reason, '')
		from sessions where id = $1`, sid).Scan(&tokenHash, &revoked, &reason); err != nil {
		t.Fatalf("session row %s: %v", sid, err)
	}
	return tokenHash, revoked, reason
}

func TestSignInCreatesSessionRow(t *testing.T) {
	s, pool, email := dbService(t)
	b := signIn(t, s, email)
	hash, revoked, _ := sessionRow(t, pool, b.sid)
	if revoked || hash != hashValue(b.inner(t, CookieRefreshToken)) {
		t.Fatalf("row hash/revoked = %q/%v; want the refresh token's hash, live", hash, revoked)
	}
	if _, ok := s.ValidateAccess(b.inner(t, CookieAccessToken)); !ok {
		t.Fatal("sign-in access token does not validate")
	}
}

func TestRefreshRotatesAndDetectsReuse(t *testing.T) {
	s, pool, email := dbService(t)
	b := signIn(t, s, email)
	anti := b.cookies[CookieAntiCsrf].Value
	first := b.cookies[CookieRefreshToken]

	rec := refreshWith(s, first, anti)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh = %d %s", rec.Code, rec.Body.String())
	}
	next := cookiesOf(rec)[CookieRefreshToken]
	if next == nil || next.Value == first.Value {
		t.Fatal("refresh did not rotate the refresh token")
	}
	if hash, _, _ := sessionRow(t, pool, b.sid); hash != hashValue(signedIn{cookies: cookiesOf(rec)}.inner(t, CookieRefreshToken)) {
		t.Fatal("the row does not hold the rotated token's hash")
	}

	// A second tab that refreshed with the same cookie a moment later is
	// a race, not theft: it gets fresh access material and leaves the
	// jar's refresh cookie (the winner's) alone.
	rec = refreshWith(s, first, anti)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh inside the grace window = %d %s", rec.Code, rec.Body.String())
	}
	if got := cookiesOf(rec); got[CookieRefreshToken] != nil || got[CookieAccessToken] == nil {
		t.Fatalf("grace refresh cookies = %v; want access material only", got)
	}

	// Past the grace window the superseded token is a stolen copy.
	if _, err := pool.Exec(context.Background(),
		`update sessions set rotated_at = now() - interval '1 minute' where id = $1`, b.sid); err != nil {
		t.Fatal(err)
	}
	access := b.inner(t, CookieAccessToken)
	rec = refreshWith(s, first, anti)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed refresh = %d, want 401", rec.Code)
	}
	if _, revoked, reason := sessionRow(t, pool, b.sid); !revoked || reason != "reuse" {
		t.Fatalf("row revoked=%v reason=%q, want revoked for reuse", revoked, reason)
	}
	// Both holders are out: the current token and the live access token.
	if rec := refreshWith(s, next, anti); rec.Code != http.StatusUnauthorized {
		t.Fatalf("current token after reuse = %d, want 401", rec.Code)
	}
	if _, ok := s.ValidateAccess(access); ok {
		t.Fatal("access token of a revoked session still validates")
	}
	other := &Service{pool: pool, cfg: s.cfg, revoked: newRevocations(), live: newRevocations()}
	if _, ok := other.ValidateAccess(access); ok {
		t.Fatal("another process still accepted the revoked access token")
	}
}

func TestConcurrentRefreshesAllSucceed(t *testing.T) {
	s, pool, email := dbService(t)
	b := signIn(t, s, email)
	const tabs = 6
	codes := make(chan int, tabs)
	var wg sync.WaitGroup
	for i := 0; i < tabs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- refreshWith(s, b.cookies[CookieRefreshToken], b.cookies[CookieAntiCsrf].Value).Code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusOK {
			t.Fatalf("a concurrent refresh got %d; tabs must not sign each other out", code)
		}
	}
	if _, revoked, _ := sessionRow(t, pool, b.sid); revoked {
		t.Fatal("concurrent refreshes revoked the session")
	}
}

func TestRefreshCredentialChecks(t *testing.T) {
	s, _, email := dbService(t)
	b := signIn(t, s, email)
	other := signIn(t, s, email)

	if rec := refreshWith(s, b.cookies[CookieRefreshToken], ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("cookie refresh without anti-csrf = %d, want 401", rec.Code)
	}
	if rec := refreshWith(s, b.cookies[CookieRefreshToken], other.cookies[CookieAntiCsrf].Value); rec.Code != http.StatusUnauthorized {
		t.Fatalf("cookie refresh with another session's anti-csrf = %d, want 401", rec.Code)
	}

	bearer := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/session/refresh", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		s.handleRefresh(rec, req)
		return rec.Code
	}
	if code := bearer(b.inner(t, CookieAccessToken)); code != http.StatusUnauthorized {
		t.Fatalf("access token at refresh = %d, want 401", code)
	}
	if code := bearer(b.inner(t, CookieRefreshToken)); code != http.StatusOK {
		t.Fatalf("header refresh = %d, want 200 (no ambient authority, no anti-csrf needed)", code)
	}
}

func TestRefreshRefusedForSuspendedAccount(t *testing.T) {
	s, pool, email := dbService(t)
	b := signIn(t, s, email)
	if _, err := pool.Exec(context.Background(),
		`update accounts set status = 'suspended' where email = $1`, email); err != nil {
		t.Fatal(err)
	}
	if rec := refreshWith(s, b.cookies[CookieRefreshToken], b.cookies[CookieAntiCsrf].Value); rec.Code != http.StatusUnauthorized {
		t.Fatalf("suspended refresh = %d, want 401", rec.Code)
	}
}

func TestSignoutRevokesSession(t *testing.T) {
	s, pool, email := dbService(t)
	b := signIn(t, s, email)
	kept := signIn(t, s, email)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/signout", nil)
	req.AddCookie(b.cookies[CookieAccessToken])
	rec := httptest.NewRecorder()
	s.handleSignout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signout = %d %s", rec.Code, rec.Body.String())
	}
	if _, revoked, reason := sessionRow(t, pool, b.sid); !revoked || reason != "signout" {
		t.Fatalf("row revoked=%v reason=%q after signout", revoked, reason)
	}
	if rec := refreshWith(s, b.cookies[CookieRefreshToken], b.cookies[CookieAntiCsrf].Value); rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after signout = %d, want 401", rec.Code)
	}
	if _, ok := s.ValidateAccess(b.inner(t, CookieAccessToken)); ok {
		t.Fatal("access token still validates after signout")
	}
	// Signing out one device leaves the account's other sessions alone.
	if rec := refreshWith(s, kept.cookies[CookieRefreshToken], kept.cookies[CookieAntiCsrf].Value); rec.Code != http.StatusOK {
		t.Fatalf("other session after signout = %d, want 200", rec.Code)
	}
}
