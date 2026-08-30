// Session token model compatible with the Supertokens v16+ web client
// (supertokens-website 17, the SDK generation the forked Tegon client
// ships).
//
// The client keeps session state entirely in cookies and only ever calls
// POST /session/refresh and POST /signout; it parses token payloads with
// atob() + JSON.parse, so every value must be standard base64. Tokens are
// therefore opaque base64-encoded JSON documents:
//
//	accessToken  = base64({"r":1,"ate":<expiryMs>,"up":"<accountID>","hs":"<hmac>"})
//	refreshToken = base64({"r":1,"ate":<expiryMs>,"up":"<accountID>","hs":"<hmac>"})
//	frontToken   = base64({"r":1,"ate":<expiryMs>,"uid":"<accountID>"})
//
// The cookie that carries each token is an envelope with the same
// base64-JSON encoding:
//
//	st-access-token  = base64({"r":1,"t":"<accessToken>","u":"<accountID>"})
//	st-refresh-token = base64({"r":1,"t":"<refreshToken>","u":"<accountID>"})
//
// "hs" is a hex HMAC-SHA256 over "<kind>|<accountID>|<ate>" keyed with the
// server session secret. The client treats "hs" as an opaque extra field
// and passes tokens through untouched, which keeps payloads verifiable
// server-side while remaining wire-compatible with the unmodified client.
//
// Because the client decodes with atob, standard base64 (with padding) is
// mandatory; URL-safe base64 would throw and silently kill the session.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Cookie and header names of the Supertokens v16 session protocol.
// Cookie values are envelope/base64-JSON; the st-* headers mirror the
// cookie values for header-based transfer modes and last-access tracking.
const (
	CookieAccessToken      = "st-access-token"
	CookieRefreshToken     = "st-refresh-token"
	CookieFrontToken       = "sFrontToken"
	CookieAntiCsrf         = "sAntiCsrf"
	CookieLastAccessUpdate = "st-last-access-token-update"

	HeaderAccessToken  = "st-access-token"
	HeaderRefreshToken = "st-refresh-token"
	HeaderFrontToken   = "st-front-token"
	HeaderAntiCsrf     = "anti-csrf"
	HeaderUserID       = "s-user-id"
)

// tokenPayload is the base64-decoded body of an access/refresh/front token.
// Up is the account id in access/refresh tokens; Uid is used by the front
// token. Hs is the signature (client-ignored).
type tokenPayload struct {
	R   int    `json:"r"`
	Ate int64  `json:"ate"`
	Up  string `json:"up"`
	Uid string `json:"uid"`
	Hs  string `json:"hs,omitempty"`
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

// signPayload derives a stable HMAC binding a kind+account+ate triple to
// the session secret.
func (s *Service) signPayload(kind, accountID string, ate int64) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.SessionSecret))
	fmt.Fprintf(mac, "%s|%s|%d", kind, accountID, ate)
	return hex.EncodeToString(mac.Sum(nil))
}

// issueSession mints a full token set for the account.
func (s *Service) issueSession(accountID string) SessionMaterial {
	now := time.Now()
	accessExpiry := now.Add(s.cfg.AccessTokenTTL)
	refreshExpiry := now.Add(s.cfg.RefreshTokenTTL)

	m := SessionMaterial{
		AccountID:     accountID,
		AccessExpiry:  accessExpiry,
		RefreshExpiry: refreshExpiry,
	}
	m.AccessToken = b64(tokenPayload{
		R: 1, Ate: accessExpiry.UnixMilli(), Up: accountID,
		Hs: s.signPayload("access", accountID, accessExpiry.UnixMilli()),
	})
	m.RefreshToken = b64(tokenPayload{
		R: 1, Ate: refreshExpiry.UnixMilli(), Up: accountID,
		Hs: s.signPayload("refresh", accountID, refreshExpiry.UnixMilli()),
	})
	m.FrontToken = b64(tokenPayload{
		R: 1, Ate: accessExpiry.UnixMilli(), Uid: accountID,
	})
	// Bound to the account only (not the token) so it stays valid across
	// refreshes and can be re-verified from any token of the account.
	m.AntiCsrf = s.signPayload("csrf", accountID, 0)[:32]
	return m
}

// validateToken decodes an inner token, checks expiry and signature, and
// returns the bound account id.
func (s *Service) validateToken(kind, token string) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return "", false
	}
	var p tokenPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.Up == "" || p.Ate == 0 {
		return "", false
	}
	if p.Ate < time.Now().UnixMilli() {
		return "", false
	}
	want := s.signPayload(kind, p.Up, p.Ate)
	if !hmac.Equal([]byte(want), []byte(p.Hs)) {
		return "", false
	}
	return p.Up, true
}

// ValidateAccess verifies an access token (Authorization: Bearer value or
// the "t" of the st-access-token envelope).
func (s *Service) ValidateAccess(token string) (string, bool) {
	return s.validateToken("access", token)
}

// ValidateRefresh verifies a refresh token.
func (s *Service) ValidateRefresh(token string) (string, bool) {
	return s.validateToken("refresh", token)
}

// ValidateAntiCsrf checks the client-returned anti-csrf value against the
// account it was issued for.
func (s *Service) ValidateAntiCsrf(accountID, value string) bool {
	return hmac.Equal([]byte(s.signPayload("csrf", accountID, 0)[:32]), []byte(value))
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
	now := time.Now()
	accessAge := int(m.AccessExpiry.Sub(now).Seconds())
	refreshAge := int(m.RefreshExpiry.Sub(now).Seconds())
	set := func(name, value string, maxAge int, httpOnly bool) {
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
	set(CookieAccessToken, encodeEnvelope(m.AccessToken, m.AccountID), accessAge, true)
	set(CookieRefreshToken, encodeEnvelope(m.RefreshToken, m.AccountID), refreshAge, true)
	set(CookieFrontToken, m.FrontToken, accessAge, false)
	set(CookieAntiCsrf, m.AntiCsrf, accessAge, false)
	set(CookieLastAccessUpdate, strconv.FormatInt(now.UnixMilli(), 10), accessAge, false)

	w.Header().Set(HeaderAccessToken, encodeEnvelope(m.AccessToken, m.AccountID))
	w.Header().Set(HeaderRefreshToken, encodeEnvelope(m.RefreshToken, m.AccountID))
	w.Header().Set(HeaderFrontToken, m.FrontToken)
	w.Header().Set(HeaderAntiCsrf, m.AntiCsrf)
	w.Header().Set(HeaderUserID, m.AccountID)
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

// accountFromRequest extracts a candidate account id from the session
// material the request carries (Bearer token or st-access-token cookie
// envelope).
func (s *Service) accountFromRequest(r *http.Request) string {
	if t := bearerToken(r); t != "" {
		if id, ok := s.ValidateAccess(t); ok {
			return id
		}
	}
	if c, err := r.Cookie(CookieAccessToken); err == nil {
		if inner, err := innerFromEnvelope(c.Value); err == nil {
			if id, ok := s.ValidateAccess(inner); ok {
				return id
			}
		}
	}
	return ""
}
