package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"converge/internal/config"
)

// testService builds a session service with a known secret and TTLs. The
// session path is stateless, so a nil pool is fine.
func testService() *Service {
	return &Service{
		cfg: config.Config{
			SessionSecret:   "test-secret",
			AccessTokenTTL:  time.Hour,
			RefreshTokenTTL: 720 * time.Hour,
			WebOrigin:       "http://localhost:3000",
		},
	}
}

// mint builds a token of the given kind, correctly signed with the given
// secret, for the given account and expiry (unix millis). Tests use this
// to construct expired or tampered material deterministically.
func mint(kind, account string, ate int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(kind + "|" + account + "|sid-1||" + strconv.FormatInt(ate, 10)))
	hs := hex.EncodeToString(mac.Sum(nil))
	payload, _ := json.Marshal(tokenPayload{R: 1, Ate: ate, Up: account, Sid: "sid-1", Hs: hs})
	return base64.StdEncoding.EncodeToString(payload)
}

func decodePayload(t *testing.T, token string) tokenPayload {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token is not base64: %v", err)
	}
	var p tokenPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("token body is not JSON: %v", err)
	}
	return p
}

func TestSignPayloadDeterministic(t *testing.T) {
	s := testService()
	a := s.signPayload("access", "acct-1", "sid-1", "", 123)
	b := s.signPayload("access", "acct-1", "sid-1", "", 123)
	if a != b {
		t.Fatalf("signPayload is not deterministic: %q vs %q", a, b)
	}
	if s.signPayload("refresh", "acct-1", "sid-1", "", 123) == a {
		t.Fatal("kind is not part of the signed payload")
	}
	if s.signPayload("access", "acct-2", "sid-1", "", 123) == a {
		t.Fatal("account is not part of the signed payload")
	}
	if s.signPayload("access", "acct-1", "sid-2", "", 123) == a {
		t.Fatal("session is not part of the signed payload")
	}
	if s.signPayload("access", "acct-1", "sid-1", "n", 123) == a {
		t.Fatal("nonce is not part of the signed payload")
	}
	if s.signPayload("access", "acct-1", "sid-1", "", 124) == a {
		t.Fatal("ate is not part of the signed payload")
	}
	other := &Service{cfg: config.Config{SessionSecret: "other-secret"}}
	if other.signPayload("access", "acct-1", "sid-1", "", 123) == a {
		t.Fatal("signature does not depend on the session secret")
	}
}

func TestIssueSession(t *testing.T) {
	s := testService()
	m := s.issueSession("acct-1", "sid-1")
	now := time.Now().UnixMilli()

	access := decodePayload(t, m.AccessToken)
	if access.R != 1 || access.Up != "acct-1" || access.Sid != "sid-1" {
		t.Fatalf("access payload = %+v", access)
	}
	if access.Ate < now || access.Ate > now+time.Hour.Milliseconds() {
		t.Fatalf("access ate %d outside [now, now+TTL]", access.Ate)
	}
	if id, ok := s.ValidateAccess(m.AccessToken); !ok || id != "acct-1" {
		t.Fatalf("issued access token does not validate: id=%q ok=%v", id, ok)
	}

	refresh := decodePayload(t, m.RefreshToken)
	if refresh.R != 1 || refresh.Up != "acct-1" || refresh.Sid != "sid-1" || refresh.Jti == "" {
		t.Fatalf("refresh payload = %+v", refresh)
	}
	if id, ok := s.ValidateRefresh(m.RefreshToken); !ok || id != "acct-1" {
		t.Fatalf("issued refresh token does not validate: id=%q ok=%v", id, ok)
	}
	// The sessions row stores the refresh token's hash, so two rotations
	// in the same millisecond must still differ.
	if again := s.issueSession("acct-1", "sid-1"); again.RefreshToken == m.RefreshToken {
		t.Fatal("two issued refresh tokens are identical")
	}
	// The two kinds must not be interchangeable: the HMAC binds the kind.
	if id, ok := s.ValidateAccess(m.RefreshToken); ok {
		t.Fatalf("refresh token accepted as access token (id=%q) — kind confusion", id)
	}
	if id, ok := s.ValidateRefresh(m.AccessToken); ok {
		t.Fatalf("access token accepted as refresh token (id=%q) — kind confusion", id)
	}

	front := decodePayload(t, m.FrontToken)
	if front.Uid != "acct-1" || front.Up != "" {
		t.Fatalf("front payload = %+v, want uid only", front)
	}
	if front.Hs != "" {
		t.Fatal("front token carries an hs field; the client ignores it, keep it absent")
	}

	if want := s.signPayload("csrf", "acct-1", "sid-1", "", 0)[:32]; m.AntiCsrf != want {
		t.Fatalf("anti-csrf = %q, want first 32 of %q", m.AntiCsrf, want)
	}
	if !s.ValidateAntiCsrf("acct-1", "sid-1", m.AntiCsrf) {
		t.Fatal("issued anti-csrf value fails validation for its own session")
	}
	if s.ValidateAntiCsrf("acct-2", "sid-1", m.AntiCsrf) {
		t.Fatal("anti-csrf value validates for a foreign account")
	}
	if s.ValidateAntiCsrf("acct-1", "sid-2", m.AntiCsrf) {
		t.Fatal("anti-csrf value validates for another session of the account")
	}
}

func TestValidateAccessRejections(t *testing.T) {
	s := testService()
	now := time.Now().UnixMilli()
	cases := []struct {
		name  string
		token string
	}{
		{"tampered signature", mint("access", "acct-1", now+1000, "test-secret")[:len(mint("access", "acct-1", now+1000, "test-secret"))-4] + "zzzz"},
		{"expired", mint("access", "acct-1", now-1000, "test-secret")},
		{"refresh kind presented as access", mint("refresh", "acct-1", now+1000, "test-secret")},
		{"not base64", "!!!not-base64!!!"},
		{"not json", base64.StdEncoding.EncodeToString([]byte("hello"))},
		{"missing account", func() string {
			p, _ := json.Marshal(tokenPayload{R: 1, Ate: now + 1000, Hs: "x"})
			return base64.StdEncoding.EncodeToString(p)
		}()},
		{"zero ate", func() string {
			p, _ := json.Marshal(tokenPayload{R: 1, Up: "acct-1", Hs: "x"})
			return base64.StdEncoding.EncodeToString(p)
		}()},
		{"pre-session token without sid", func() string {
			ate := now + 1000
			mac := hmac.New(sha256.New, []byte("test-secret"))
			mac.Write([]byte("access|acct-1|" + strconv.FormatInt(ate, 10)))
			p, _ := json.Marshal(tokenPayload{R: 1, Ate: ate, Up: "acct-1", Hs: hex.EncodeToString(mac.Sum(nil))})
			return base64.StdEncoding.EncodeToString(p)
		}()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if id, ok := s.ValidateAccess(c.token); ok {
				t.Fatalf("rejected case accepted: id=%q", id)
			}
		})
	}
}

func TestValidateAccessWhitespaceTolerant(t *testing.T) {
	s := testService()
	m := s.issueSession("acct-1", "sid-1")
	if id, ok := s.ValidateAccess("  " + m.AccessToken + " \n"); !ok || id != "acct-1" {
		t.Fatalf("surrounding whitespace broke validation: id=%q ok=%v", id, ok)
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	envelope := encodeEnvelope("inner-token", "acct-1")
	inner, err := innerFromEnvelope(envelope)
	if err != nil || inner != "inner-token" {
		t.Fatalf("round trip = %q, %v; want inner-token, nil", inner, err)
	}
	if _, err := innerFromEnvelope("not-base64!!!"); err == nil {
		t.Fatal("non-base64 envelope accepted")
	}
	badJSON := base64.StdEncoding.EncodeToString([]byte("nope"))
	if _, err := innerFromEnvelope(badJSON); err == nil {
		t.Fatal("non-JSON envelope accepted")
	}
}

func TestBearerToken(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"Bearer abc", "abc"},
		{"bearer abc", "abc"}, // the client is case-agnostic via EqualFold
		{"BEARER abc", "abc"},
		{"Bearer  spaced  ", "spaced"},
		{"Basic abc", ""},
		{"Bearer", ""},  // no space: len > 7 fails
		{"Bearer ", ""}, // exactly 7 chars: len > 7 fails
		{"", ""},
		{"Abearer abc", ""},
	}
	for _, c := range cases {
		t.Run(c.header, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.header != "" {
				r.Header.Set("Authorization", c.header)
			}
			if got := bearerToken(r); got != c.want {
				t.Fatalf("bearerToken(%q) = %q, want %q", c.header, got, c.want)
			}
		})
	}
}

func TestAccountFromRequest(t *testing.T) {
	s := testService()
	m := s.issueSession("acct-1", "sid-1")

	t.Run("bearer session token", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer "+m.AccessToken)
		if got, tok := s.accountFromRequest(r); tok != nil || got != "acct-1" {
			t.Fatalf("account = %q, want acct-1", got)
		}
	})

	t.Run("cookie envelope", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: CookieAccessToken, Value: encodeEnvelope(m.AccessToken, "acct-1")})
		if got, tok := s.accountFromRequest(r); tok != nil || got != "acct-1" {
			t.Fatalf("account = %q, want acct-1", got)
		}
	})

	t.Run("bad bearer falls through to good cookie", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer "+mint("access", "acct-1", 1, "wrong-secret"))
		r.AddCookie(&http.Cookie{Name: CookieAccessToken, Value: encodeEnvelope(m.AccessToken, "acct-1")})
		if got, tok := s.accountFromRequest(r); tok != nil || got != "acct-1" {
			t.Fatalf("account = %q, want acct-1 via cookie", got)
		}
	})

	t.Run("bad bearer and bad cookie", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer garbage")
		r.AddCookie(&http.Cookie{Name: CookieAccessToken, Value: "junk"})
		if got, tok := s.accountFromRequest(r); tok != nil || got != "" {
			t.Fatalf("account = %q, want empty", got)
		}
	})
}

func TestSetSessionMaterial(t *testing.T) {
	s := testService()
	m := s.issueSession("acct-1", "sid-1")
	w := httptest.NewRecorder()
	s.SetSessionMaterial(w, m)

	cookies := map[string]*http.Cookie{}
	for _, c := range w.Result().Cookies() {
		cookies[c.Name] = c
	}
	for _, name := range []string{CookieAccessToken, CookieRefreshToken, CookieFrontToken, CookieAntiCsrf, CookieLastAccessUpdate} {
		c, ok := cookies[name]
		if !ok {
			t.Fatalf("cookie %s not set", name)
		}
		if c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
			t.Fatalf("%s: path=%q sameSite=%v", name, c.Path, c.SameSite)
		}
		if c.MaxAge <= 0 {
			t.Fatalf("%s: maxAge %d not positive", name, c.MaxAge)
		}
	}
	// HttpOnly split: access/refresh are JS-invisible; the rest are read by the client.
	for _, name := range []string{CookieAccessToken, CookieRefreshToken} {
		if !cookies[name].HttpOnly {
			t.Fatalf("%s must be HttpOnly", name)
		}
	}
	for _, name := range []string{CookieFrontToken, CookieAntiCsrf, CookieLastAccessUpdate} {
		if cookies[name].HttpOnly {
			t.Fatalf("%s must NOT be HttpOnly (the client reads it with document.cookie)", name)
		}
	}
	// The envelope cookie carries the inner token; decode and re-validate.
	inner, err := innerFromEnvelope(cookies[CookieAccessToken].Value)
	if err != nil {
		t.Fatalf("access cookie does not parse as an envelope: %v", err)
	}
	if id, ok := s.ValidateAccess(inner); !ok || id != "acct-1" {
		t.Fatalf("envelope inner token invalid: id=%q ok=%v", id, ok)
	}
	if got := w.Result().Header.Get(HeaderUserID); got != "acct-1" {
		t.Fatalf("s-user-id header = %q", got)
	}
	// The HttpOnly tokens must not leak into script-readable headers.
	for _, h := range []string{"st-access-token", "st-refresh-token"} {
		if v := w.Result().Header.Get(h); v != "" {
			t.Fatalf("response header %s = %q; raw tokens must stay in HttpOnly cookies", h, v)
		}
	}
	// The anti-csrf value must outlive the access token: the refresh that
	// follows an expired access token presents it.
	if got, want := cookies[CookieAntiCsrf].MaxAge, cookies[CookieRefreshToken].MaxAge; got != want {
		t.Fatalf("anti-csrf maxAge = %d, want the refresh lifetime %d", got, want)
	}
	// Secure is driven by config.
	if cookies[CookieAccessToken].Secure {
		t.Fatal("Secure set without CONVERGE_SECURE_COOKIES")
	}
}

func TestSetSessionMaterialSecureCookies(t *testing.T) {
	s := testService()
	s.cfg.SecureCookies = true
	m := s.issueSession("acct-1", "sid-1")
	w := httptest.NewRecorder()
	s.SetSessionMaterial(w, m)
	for _, c := range w.Result().Cookies() {
		if !c.Secure {
			t.Fatalf("cookie %s missing Secure with SecureCookies=true", c.Name)
		}
	}
}

func TestClearSessionMaterial(t *testing.T) {
	s := testService()
	w := httptest.NewRecorder()
	s.ClearSessionMaterial(w)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge >= 0 {
			t.Fatalf("cookie %s not expired (maxAge=%d)", c.Name, c.MaxAge)
		}
		if c.Value != "" {
			t.Fatalf("cookie %s has a value after clear", c.Name)
		}
	}
	if got := w.Result().Header.Get(HeaderUserID); got != "" {
		t.Fatalf("s-user-id = %q after clear, want empty", got)
	}
}

func TestValidateAntiCsrfBoundToSession(t *testing.T) {
	s := testService()
	value := s.antiCsrfFor("acct-1", "sid-1")
	if !s.ValidateAntiCsrf("acct-1", "sid-1", value) {
		t.Fatal("own-session validation failed")
	}
	if s.ValidateAntiCsrf("acct-2", "sid-1", value) {
		t.Fatal("foreign-account validation passed")
	}
	if s.ValidateAntiCsrf("acct-1", "", "") {
		t.Fatal("missing anti-csrf value accepted")
	}
	// Truncation boundary: 31 chars must fail even for the right session.
	if s.ValidateAntiCsrf("acct-1", "sid-1", value[:31]) {
		t.Fatal("truncated anti-csrf value accepted")
	}
	// Tampered last byte must fail (hmac.Equal is constant-time, but the
	// value must not validate).
	tampered := value[:len(value)-1]
	if tampered[len(tampered)-1] == 'a' {
		tampered = tampered[:len(tampered)-1] + "b"
	} else {
		tampered = tampered[:len(tampered)-1] + "a"
	}
	if s.ValidateAntiCsrf("acct-1", "sid-1", tampered) {
		t.Fatal("tampered anti-csrf value accepted")
	}
}

func TestRevokedSessionRejectsAccessTokens(t *testing.T) {
	s := testService()
	s.revoked = newRevocations()
	gone := s.issueSession("acct-1", "sid-gone")
	kept := s.issueSession("acct-1", "sid-kept")

	s.revoked.add("sid-gone", time.Now().Add(time.Hour))
	if _, ok := s.ValidateAccess(gone.AccessToken); ok {
		t.Fatal("access token of a revoked session accepted")
	}
	if _, ok := s.ValidateAccess(kept.AccessToken); !ok {
		t.Fatal("revoking one session rejected another")
	}

	// An entry past its horizon no longer matters and is swept on add.
	s.revoked.add("sid-old", time.Now().Add(-time.Second))
	s.revoked.add("sid-new", time.Now().Add(time.Hour))
	if s.revoked.has("sid-old") {
		t.Fatal("expired revocation still applies")
	}
	if _, ok := s.revoked.until["sid-old"]; ok {
		t.Fatal("expired revocation not swept")
	}
}

func TestSessionFromRequest(t *testing.T) {
	s := testService()
	m := s.issueSession("acct-1", "sid-1")

	t.Run("access cookie", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.AddCookie(&http.Cookie{Name: CookieAccessToken, Value: encodeEnvelope(m.AccessToken, "acct-1")})
		if c, ok := s.sessionFromRequest(r); !ok || c.Session != "sid-1" {
			t.Fatalf("claims = %+v, %v", c, ok)
		}
	})
	t.Run("refresh cookie once the access token is gone", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.AddCookie(&http.Cookie{Name: CookieRefreshToken, Value: encodeEnvelope(m.RefreshToken, "acct-1")})
		if c, ok := s.sessionFromRequest(r); !ok || c.Session != "sid-1" || c.Account != "acct-1" {
			t.Fatalf("claims = %+v, %v", c, ok)
		}
	})
	t.Run("refresh token in the access cookie is refused", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.AddCookie(&http.Cookie{Name: CookieAccessToken, Value: encodeEnvelope(m.RefreshToken, "acct-1")})
		if _, ok := s.sessionFromRequest(r); ok {
			t.Fatal("kind confusion across cookies")
		}
	})
}

// lastAccessValue pins the last-access marker format the client echoes back.
func TestSetSessionMaterialLastAccess(t *testing.T) {
	s := testService()
	m := s.issueSession("acct-1", "sid-1")
	w := httptest.NewRecorder()
	s.SetSessionMaterial(w, m)
	for _, c := range w.Result().Cookies() {
		if c.Name != CookieLastAccessUpdate {
			continue
		}
		if _, err := strconv.ParseInt(c.Value, 10, 64); err != nil {
			t.Fatalf("last-access value %q is not an int64 millis stamp: %v", c.Value, err)
		}
	}
}
