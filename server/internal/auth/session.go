// Session token model compatible with the Supertokens v16+ web client
// (supertokens-website 17, the SDK generation the forked Tegon client
// ships).
//
// The client keeps session state entirely in cookies and only ever calls
// POST /session/refresh and POST /signout; it parses token payloads with
// atob() + JSON.parse, so every value must be standard base64. Tokens are
// therefore opaque base64-encoded JSON documents:
//
//	accessToken  = base64({"r":1,"ate":<expiryMs>,"up":"<accountID>","sid":"<sessionID>","hs":"<hmac>"})
//	refreshToken = base64({"r":1,"ate":<expiryMs>,"up":"<accountID>","sid":"<sessionID>","jti":"<nonce>","hs":"<hmac>"})
//	frontToken   = base64({"r":1,"ate":<expiryMs>,"uid":"<accountID>"})
//
// The cookie that carries each token is an envelope with the same
// base64-JSON encoding:
//
//	st-access-token  = base64({"r":1,"t":"<accessToken>","u":"<accountID>"})
//	st-refresh-token = base64({"r":1,"t":"<refreshToken>","u":"<accountID>"})
//
// "hs" is a hex HMAC-SHA256 over "<kind>|<accountID>|<sid>|<jti>|<ate>"
// keyed with the server session secret. The client treats "hs", "sid" and
// "jti" as opaque extra fields and passes tokens through untouched, which
// keeps payloads verifiable server-side while remaining wire-compatible
// with the unmodified client.
//
// Access tokens are verified statelessly. Refresh tokens are additionally
// checked against their sessions row and rotated on use (sessions.go).
//
// Because the client decodes with atob, standard base64 (with padding) is
// mandatory; URL-safe base64 would throw and silently kill the session.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cookie and header names of the Supertokens v16 session protocol.
// Cookie values are envelope/base64-JSON. Raw access/refresh tokens are
// never sent as response headers: the client uses cookies only, and a
// header copy would hand the HttpOnly tokens to any script on the page.
const (
	CookieAccessToken      = "st-access-token"
	CookieRefreshToken     = "st-refresh-token"
	CookieFrontToken       = "sFrontToken"
	CookieAntiCsrf         = "sAntiCsrf"
	CookieLastAccessUpdate = "st-last-access-token-update"

	HeaderFrontToken = "st-front-token"
	HeaderAntiCsrf   = "anti-csrf"
	HeaderUserID     = "s-user-id"
)

// tokenPayload is the base64-decoded body of an access/refresh/front token.
// Up is the account id in access/refresh tokens; Uid is used by the front
// token. Sid names the sessions row; Jti makes every refresh token
// distinct so its hash identifies it. Hs is the signature (client-ignored).
type tokenPayload struct {
	R   int    `json:"r"`
	Ate int64  `json:"ate"`
	Up  string `json:"up"`
	Uid string `json:"uid"`
	Sid string `json:"sid,omitempty"`
	Jti string `json:"jti,omitempty"`
	Hs  string `json:"hs,omitempty"`
}

// tokenClaims is what a verified access or refresh token asserts.
type tokenClaims struct {
	Account string
	Session string
}

// tokenEnvelope is the base64-decoded body of the st-access-token and
// st-refresh-token cookies.
type tokenEnvelope struct {
	R int    `json:"r"`
	T string `json:"t"`
	U string `json:"u"`
}

// SessionMaterial is a full issued session: every token the client needs.
type SessionMaterial struct {
	AccountID     string
	SessionID     string
	AccessToken   string
	RefreshToken  string
	FrontToken    string
	AntiCsrf      string
	AccessExpiry  time.Time
	RefreshExpiry time.Time
}

func b64(v any) string {
	b, _ := json.Marshal(v)
	return base64.StdEncoding.EncodeToString(b)
}

// signPayload derives a stable HMAC binding a token's kind, account,
// session, nonce and expiry to the session secret.
func (s *Service) signPayload(kind, accountID, sessionID, jti string, ate int64) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.SessionSecret))
	fmt.Fprintf(mac, "%s|%s|%s|%s|%d", kind, accountID, sessionID, jti, ate)
	return hex.EncodeToString(mac.Sum(nil))
}

// antiCsrfFor is the double-submit value of one session. It is stable
// across refreshes, so every tab of the session holds the same value.
func (s *Service) antiCsrfFor(accountID, sessionID string) string {
	return s.signPayload("csrf", accountID, sessionID, "", 0)[:32]
}

// issueSession mints a full token set for an existing sessions row.
func (s *Service) issueSession(accountID, sessionID string) SessionMaterial {
	now := time.Now()
	accessExpiry := now.Add(s.cfg.AccessTokenTTL)
	refreshExpiry := now.Add(s.cfg.RefreshTokenTTL)

	m := SessionMaterial{
		AccountID:     accountID,
		SessionID:     sessionID,
		AccessExpiry:  accessExpiry,
		RefreshExpiry: refreshExpiry,
	}
	m.AccessToken = b64(tokenPayload{
		R: 1, Ate: accessExpiry.UnixMilli(), Up: accountID, Sid: sessionID,
		Hs: s.signPayload("access", accountID, sessionID, "", accessExpiry.UnixMilli()),
	})
	jti := newID()
	m.RefreshToken = b64(tokenPayload{
		R: 1, Ate: refreshExpiry.UnixMilli(), Up: accountID, Sid: sessionID, Jti: jti,
		Hs: s.signPayload("refresh", accountID, sessionID, jti, refreshExpiry.UnixMilli()),
	})
	m.FrontToken = b64(tokenPayload{
		R: 1, Ate: accessExpiry.UnixMilli(), Uid: accountID,
	})
	m.AntiCsrf = s.antiCsrfFor(accountID, sessionID)
	return m
}

// parseToken decodes an inner token and checks its kind, expiry and
// signature. Tokens without a session id predate server-side sessions and
// are refused.
func (s *Service) parseToken(kind, token string) (tokenClaims, bool) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return tokenClaims{}, false
	}
	var p tokenPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.Up == "" || p.Sid == "" || p.Ate == 0 {
		return tokenClaims{}, false
	}
	if p.Ate < time.Now().UnixMilli() {
		return tokenClaims{}, false
	}
	want := s.signPayload(kind, p.Up, p.Sid, p.Jti, p.Ate)
	if !hmac.Equal([]byte(want), []byte(p.Hs)) {
		return tokenClaims{}, false
	}
	return tokenClaims{Account: p.Up, Session: p.Sid}, true
}

// accessClaims verifies an access token and that its session has not
// been revoked on this instance.
func (s *Service) accessClaims(token string) (tokenClaims, bool) {
	c, ok := s.parseToken("access", token)
	if !ok || s.revoked.has(c.Session) {
		return tokenClaims{}, false
	}
	if s.pool != nil && (s.live == nil || !s.live.has(c.Session)) && s.sessionRevoked(c.Session) {
		s.revoked.add(c.Session, time.Now().Add(s.cfg.AccessTokenTTL))
		return tokenClaims{}, false
	}
	if s.pool != nil && s.live != nil {
		s.live.add(c.Session, time.Now().Add(2*time.Second))
	}
	return c, true
}

// NoteRevoked marks a session revoked on this process. The listener calls
// it when another process notifies converge_session.
func (s *Service) NoteRevoked(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	until := time.Now().Add(s.cfg.AccessTokenTTL)
	if s.cfg.AccessTokenTTL <= 0 {
		until = time.Now().Add(time.Hour)
	}
	s.revoked.add(sessionID, until)
}

func (s *Service) sessionRevoked(id string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var revoked bool
	err := s.pool.QueryRow(ctx, `select revoked_at is not null from sessions where id = $1`, id).Scan(&revoked)
	if err != nil {
		return false
	}
	return revoked
}

// ValidateAccess verifies an access token (Authorization: Bearer value or
// the "t" of the st-access-token envelope).
func (s *Service) ValidateAccess(token string) (string, bool) {
	c, ok := s.accessClaims(token)
	return c.Account, ok
}

// ValidateRefresh verifies a refresh token's signature and expiry. Whether
// it is still the session's current token is refreshSession's decision.
func (s *Service) ValidateRefresh(token string) (string, bool) {
	c, ok := s.parseToken("refresh", token)
	return c.Account, ok
}

// ValidateAntiCsrf checks the client-returned anti-csrf value against the
// session it was issued for.
func (s *Service) ValidateAntiCsrf(accountID, sessionID, value string) bool {
	return hmac.Equal([]byte(s.antiCsrfFor(accountID, sessionID)), []byte(value))
}

// revocations remembers revoked session ids until every access token they
// issued has expired, so sign-out takes effect at once on this instance
// instead of at the access token's expiry. A restart forgets them; the
// window is then bounded by the access token TTL.
type revocations struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func newRevocations() *revocations {
	return &revocations{until: map[string]time.Time{}}
}

func (r *revocations) add(sessionID string, until time.Time) {
	if r == nil {
		return
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, t := range r.until {
		if now.After(t) {
			delete(r.until, id)
		}
	}
	r.until[sessionID] = until
}

func (r *revocations) has(sessionID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.until[sessionID]
	return ok && time.Now().Before(t)
}

// newUUID renders a random RFC 4122 version 4 id.
func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// encodeEnvelope wraps an inner token in the cookie envelope format.
func encodeEnvelope(inner, accountID string) string {
	return b64(tokenEnvelope{R: 1, T: inner, U: accountID})
}

// innerFromEnvelope extracts the inner token from a cookie envelope value.
func innerFromEnvelope(envelope string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(envelope))
	if err != nil {
		return "", fmt.Errorf("decode envelope: %w", err)
	}
	var e tokenEnvelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return "", fmt.Errorf("parse envelope: %w", err)
	}
	return e.T, nil
}

// bearerToken extracts the token from an Authorization: Bearer header.
func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

// SetSessionMaterial writes all session cookies and the Supertokens v16
// response headers. Must be called before the response body is written.
//
// Cookie HttpOnly flags follow the client's access pattern: the front
// token, anti-csrf token and last-access marker are read by JavaScript
// (document.cookie) and must NOT be HttpOnly; the access/refresh tokens
// are HttpOnly.
func (s *Service) SetSessionMaterial(w http.ResponseWriter, m SessionMaterial) {
	s.setAccessMaterial(w, m)
	s.setCookie(w, CookieRefreshToken, encodeEnvelope(m.RefreshToken, m.AccountID),
		int(time.Until(m.RefreshExpiry).Seconds()), true)
}

// setAccessMaterial writes everything but the refresh cookie. A refresh
// that lost a race to another tab uses it alone: the winner's response
// already put the session's current refresh token in the cookie jar.
//
// The anti-csrf cookie lives as long as the refresh token, because the
// refresh that follows an expired access token must still present it.
func (s *Service) setAccessMaterial(w http.ResponseWriter, m SessionMaterial) {
	now := time.Now()
	accessAge := int(m.AccessExpiry.Sub(now).Seconds())
	s.setCookie(w, CookieAccessToken, encodeEnvelope(m.AccessToken, m.AccountID), accessAge, true)
	s.setCookie(w, CookieFrontToken, m.FrontToken, accessAge, false)
	s.setCookie(w, CookieAntiCsrf, m.AntiCsrf, int(m.RefreshExpiry.Sub(now).Seconds()), false)
	s.setCookie(w, CookieLastAccessUpdate, strconv.FormatInt(now.UnixMilli(), 10), accessAge, false)

	w.Header().Set(HeaderFrontToken, m.FrontToken)
	w.Header().Set(HeaderAntiCsrf, m.AntiCsrf)
	w.Header().Set(HeaderUserID, m.AccountID)
}

func (s *Service) setCookie(w http.ResponseWriter, name, value string, maxAge int, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   s.cfg.SecureCookies,
		HttpOnly: httpOnly,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionMaterial expires every session cookie (used on signout).
func (s *Service) ClearSessionMaterial(w http.ResponseWriter) {
	for _, c := range []struct {
		name     string
		httpOnly bool
	}{
		{CookieAccessToken, true},
		{CookieRefreshToken, true},
		{CookieFrontToken, false},
		{CookieAntiCsrf, false},
		{CookieLastAccessUpdate, false},
	} {
		http.SetCookie(w, &http.Cookie{
			Name:     c.name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			Secure:   s.cfg.SecureCookies,
			HttpOnly: c.httpOnly,
			SameSite: http.SameSiteLaxMode,
		})
	}
	w.Header().Set(HeaderUserID, "")
}

// sessionFromRequest finds the session a request belongs to, for
// sign-out: the access token when it is still valid, else the refresh
// cookie (an idle tab signs out after its access token expired).
func (s *Service) sessionFromRequest(r *http.Request) (tokenClaims, bool) {
	if t := bearerToken(r); t != "" && !strings.HasPrefix(t, APITokenPrefix) {
		if c, ok := s.accessClaims(t); ok {
			return c, true
		}
	}
	for _, cookie := range []struct{ name, kind string }{
		{CookieAccessToken, "access"},
		{CookieRefreshToken, "refresh"},
	} {
		c, err := r.Cookie(cookie.name)
		if err != nil {
			continue
		}
		inner, err := innerFromEnvelope(c.Value)
		if err != nil {
			continue
		}
		if claims, ok := s.parseToken(cookie.kind, inner); ok {
			return claims, true
		}
	}
	return tokenClaims{}, false
}

// accountFromRequest extracts a candidate account id from the session
// material the request carries (Bearer token or st-access-token cookie
// envelope).
//
// Bearer values with the API-token prefix (machine actors, M6) take a
// distinct path into ValidateAPIToken: one indexed lookup, no HMAC.
// Session tokens stay on the stateless path, so web traffic is
// unaffected. tok is non-nil exactly when an API token authenticated
// the request.
func (s *Service) accountFromRequest(r *http.Request) (accountID string, tok *APIToken) {
	if t := bearerToken(r); t != "" {
		if strings.HasPrefix(t, APITokenPrefix) {
			tok, ok := s.ValidateAPIToken(r.Context(), t)
			if ok {
				return tok.AccountID, tok
			}
			return "", nil
		}
		if id, ok := s.ValidateAccess(t); ok {
			return id, nil
		}
	}
	if c, err := r.Cookie(CookieAccessToken); err == nil {
		if inner, err := innerFromEnvelope(c.Value); err == nil {
			if id, ok := s.ValidateAccess(inner); ok {
				return id, nil
			}
		}
	}
	return "", nil
}
