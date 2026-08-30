// Package auth implements the authentication boundary: server-side
// sessions plus the email magic-link flow.
//
// The web client speaks the Supertokens v16+ unified wire protocol
// (supertokens-website 17 / supertokens-web-js 0.8 — the SDK generation
// the forked Tegon client ships). This package implements the same-origin
// subset the client uses:
//
//	POST /api/auth/signinup/code          (issue magic link)
//	POST /api/auth/signinup/code/resend
//	POST /api/auth/signinup/code/consume  (magic link or typed code)
//	POST /api/auth/session/refresh
//	POST /api/auth/signout
//	GET  /api/auth/session                (debug/compat, unused by v17)
//	GET  /api/auth/signup/email/exists
//
// Tokens are HMAC-signed base64-JSON payloads (see session.go); the
// client parses them with atob() and treats them opaquely, so the
// unmodified client signs in against this server.
//
// Code and refresh tokens are random high-entropy values whose SHA-256
// hashes are stored; access tokens are stateless.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"circle/internal/config"
)

// Service owns session and magic-link code lifecycle.
type Service struct {
	pool *pgxpool.Pool
	cfg  config.Config
	log  *slog.Logger
}

func NewService(pool *pgxpool.Pool, cfg config.Config, log *slog.Logger) *Service {
	return &Service{pool: pool, cfg: cfg, log: log}
}

// Mount registers the Supertokens-compatible routes on r.
func (s *Service) Mount(r chi.Router) {
	r.Post("/api/auth/signinup/code", s.handleCreateCode)
	r.Post("/api/auth/signinup/code/resend", s.handleResendCode)
	r.Post("/api/auth/signinup/code/consume", s.handleConsumeCode)
	r.Post("/api/auth/session/refresh", s.handleRefresh)
	r.Post("/api/auth/signout", s.handleSignout)
	r.Get("/api/auth/session", s.handleSession)
	r.Get("/api/auth/signup/email/exists", s.handleEmailExists)
}

// ---- magic link ----------------------------------------------------------

func newCode() (string, error) {
	b := make([]byte, 15)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}
	// URL-safe, unpadded base64: the code travels inside a URL hash
	// fragment and is matched case-insensitively at consume time.
	return strings.ToUpper(base64.RawURLEncoding.EncodeToString(b)[:20]), nil
}

func newID() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString(b[:10])
	}
	return hex.EncodeToString(b)
}

func hashValue(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

type codeRequest struct {
	Email       string `json:"email"`
	ContactInfo struct {
		Email string `json:"email"`
	} `json:"contactInfo"` // legacy (<=v15) shape, accepted for compatibility
}

func (req codeRequest) email() string {
	email := req.Email
	if email == "" {
		email = req.ContactInfo.Email
	}
	return strings.ToLower(strings.TrimSpace(email))
}

var emailPattern = func() func(string) bool {
	return func(email string) bool {
		if email == "" || len(email) < 5 || len(email) > 255 {
			return false
		}
		at := strings.Index(email, "@")
		return at > 0 && at < len(email)-1 && !strings.Contains(email[at+1:], "@") && strings.Contains(email[at+1:], ".")
	}
}()

// handleCreateCode implements POST /api/auth/signinup/code.
//
// Supertokens sign-in/up semantics: an unknown email is not an error at
// this step; the account materializes when the code is consumed.
func (s *Service) handleCreateCode(w http.ResponseWriter, r *http.Request) {
	var req codeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "malformed request body")
		return
	}
	email := req.email()
	if !emailPattern(email) {
		writeError(w, http.StatusBadRequest, "INVALID_EMAIL", "a valid email is required")
		return
	}

	code, err := newCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "could not create code")
		return
	}
	preAuthSessionID := newID()
	deviceID := newID()
	if _, err := s.pool.Exec(r.Context(), `
		insert into auth_codes (email, token_hash, expires_at, pre_auth_session_id, device_id)
		values ($1, $2, $3, $4, $5)`,
		email, hashValue(code), time.Now().Add(s.cfg.CodeTTL), preAuthSessionID, deviceID); err != nil {
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "could not create code")
		return
	}

	s.log.Info("magic link issued", "email", email, "pre_auth_session_id", preAuthSessionID, "link", s.magicLink(preAuthSessionID, code))
	s.writeCreateCodeResponse(w, preAuthSessionID, deviceID, code)
}

// magicLink builds the verify URL the email provider would send. The v17
// client reads the link code from the URL hash fragment and the
// preAuthSessionId from the query string.
func (s *Service) magicLink(preAuthSessionID, code string) string {
	return fmt.Sprintf("%s/auth/verify?preAuthSessionId=%s#%s", s.cfg.WebOrigin, preAuthSessionID, code)
}

// writeCreateCodeResponse emits the v16 create-code body. The magic link
// itself is normally sent by the email provider, not returned; in dev
// mode (no email provider) it is included as devMagicLink so the local
// client can offer it to the user directly.
func (s *Service) writeCreateCodeResponse(w http.ResponseWriter, preAuthSessionID, deviceID, code string) {
	resp := map[string]any{
		"status":           "OK",
		"deviceId":         deviceID,
		"preAuthSessionId": preAuthSessionID,
		"flowType":         "PASSWORDLESS",
	}
	if s.cfg.DevMode {
		resp["devMagicLink"] = s.magicLink(preAuthSessionID, code)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleResendCode implements POST /api/auth/signinup/code/resend:
// a fresh code for the same pre-auth session (same email).
func (s *Service) handleResendCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID         string `json:"deviceId"`
		PreAuthSessionID string `json:"preAuthSessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "malformed request body")
		return
	}
	if strings.TrimSpace(body.PreAuthSessionID) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "preAuthSessionId is required")
		return
	}
	code, err := newCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "could not resend code")
		return
	}
	var affected int
	if err := s.pool.QueryRow(r.Context(), `
		update auth_codes
		set token_hash = $1, expires_at = now() + $2::interval
		where pre_auth_session_id = $3 or device_id = $4
		returning 1`,
		hashValue(code), intervalSQL(s.cfg.CodeTTL), body.PreAuthSessionID, body.DeviceID).
		Scan(&affected); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "EXPIRED_PRE_AUTH_SESSION", "the sign-in session is no longer active; start again")
			return
		}
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "could not resend code")
		return
	}
	if affected == 0 {
		writeError(w, http.StatusBadRequest, "EXPIRED_PRE_AUTH_SESSION", "the sign-in session is no longer active; start again")
		return
	}
	var email string
	if err := s.pool.QueryRow(r.Context(),
		"select email from auth_codes where pre_auth_session_id = $1 or device_id = $2 limit 1",
		body.PreAuthSessionID, body.DeviceID).Scan(&email); err != nil {
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "could not resend code")
		return
	}
	s.log.Info("magic link reissued", "email", email, "pre_auth_session_id", body.PreAuthSessionID, "link", s.magicLink(body.PreAuthSessionID, code))
	writeJSON(w, http.StatusOK, map[string]string{"status": "OK"})
}

// intervalSQL renders a Go duration as a Postgres interval literal (text
// parameter), avoiding a new driver type.
func intervalSQL(d time.Duration) string {
	return fmt.Sprintf("%d seconds", int(d.Seconds()))
}

// handleConsumeCode implements POST /api/auth/signinup/code/consume.
//
// The v17 client sends {linkCode, preAuthSessionId} for magic links
// (linkCode is the URL hash fragment) or {userInputCode, deviceId,
// preAuthSessionId} for typed codes. The legacy {code} body is accepted
// so plain-HTTP tooling keeps working.
//
// Non-fatal outcomes (expired/invalid code) are returned with HTTP 200
// and a descriptive status, matching the Supertokens core v16 behaviour;
// the verify page keys off the status field.
func (s *Service) handleConsumeCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LinkCode         string `json:"linkCode"`
		UserInputCode    string `json:"userInputCode"`
		PreAuthSessionID string `json:"preAuthSessionId"`
		DeviceID         string `json:"deviceId"`
		Code             string `json:"code"` // legacy shape
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	code := strings.ToUpper(strings.TrimSpace(body.LinkCode))
	if code == "" {
		code = strings.ToUpper(strings.TrimSpace(body.UserInputCode))
	}
	if code == "" {
		code = strings.ToUpper(strings.TrimSpace(body.Code))
	}
	if code == "" {
		code = strings.ToUpper(r.URL.Query().Get("code"))
	}
	if code == "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "code is required")
		return
	}
	ctx := r.Context()

	// 1. A live, unexpired, unconsumed code must match.
	q := `
		select email, expires_at from auth_codes
		where token_hash = $1`
	args := []any{hashValue(code)}
	if strings.TrimSpace(body.PreAuthSessionID) != "" {
		q += ` and pre_auth_session_id = $2`
		args = append(args, strings.TrimSpace(body.PreAuthSessionID))
	}
	q += ` limit 1`
	var email string
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx, q, args...).Scan(&email, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "INVALID_LINK_CODE"})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "database error")
		return
	}
	if expiresAt.Before(time.Now()) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "EXPIRED_LINK_CODE"})
		return
	}

	// 2. Sign-in/up: create the account if it does not exist yet.
	name := strings.SplitN(email, "@", 2)[0]
	var accountID string
	created := false
	err = s.pool.QueryRow(ctx, `
		insert into accounts (email, name) values ($1, $2)
		on conflict (email) do nothing
		returning id`, email, name).Scan(&accountID)
	switch {
	case err == nil:
		created = true
	case errors.Is(err, pgx.ErrNoRows):
		err = s.pool.QueryRow(ctx, "select id from accounts where email = $1", email).Scan(&accountID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "database error")
			return
		}
	default:
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "database error")
		return
	}

	// 3. Consume the code and issue the session.
	if _, err := s.pool.Exec(ctx,
		`update auth_codes set consumed_at = now() where token_hash = $1`,
		hashValue(code)); err != nil {
		s.log.Error("mark code consumed", "error", err)
	}
	s.auditSession(ctx, accountID, clientIP(r), r.UserAgent())

	material := s.issueSession(accountID)
	s.SetSessionMaterial(w, material)

	s.log.Info("user authenticated", "account_id", accountID, "email", email, "new_user", created)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "OK",
		"session": s.sessionBody(material),
		"user": userResponse{
			ID:           accountID,
			Emails:       []string{email},
			LoginMethods: []loginMethod{{ID: hashValue(code)[:16], EmailID: email, LinkScore: 100}},
		},
		"createdNewRecipeUser": created,
	})
}

// handleRefresh implements POST /api/auth/session/refresh. The v17 client
// presents the refresh token as "Authorization: Bearer <token>" (falling
// back to accepting an access token keeps manual debugging pleasant).
func (s *Service) handleRefresh(w http.ResponseWriter, r *http.Request) {
	accountID := ""
	token := bearerToken(r)
	if token == "" {
		if c, err := r.Cookie(CookieRefreshToken); err == nil {
			if inner, err := innerFromEnvelope(c.Value); err == nil {
				token = inner
			}
		}
	}
	if token != "" {
		if id, ok := s.ValidateRefresh(token); ok {
			accountID = id
		} else if id, ok := s.ValidateAccess(token); ok {
			accountID = id
		}
	}
	if accountID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"status": "UNAUTHORIZED"})
		return
	}
	if csrf := r.Header.Get(HeaderAntiCsrf); csrf != "" && !s.ValidateAntiCsrf(accountID, csrf) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"status": "UNAUTHORIZED"})
		return
	}
	// The account must still be active (suspended accounts lose sessions).
	var email string
	if err := s.pool.QueryRow(r.Context(),
		"select email from accounts where id = $1 and status = 'active'",
		accountID).Scan(&email); err != nil {
		s.ClearSessionMaterial(w)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"status": "UNAUTHORIZED"})
		return
	}
	material := s.issueSession(accountID)
	s.auditSession(r.Context(), accountID, clientIP(r), r.UserAgent())
	s.SetSessionMaterial(w, material)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "OK",
		"session": s.sessionBody(material),
	})
}

// handleSignout expires all session cookies. Tokens are HMAC-signed and
// stateless, so revocation is client-side; the account status check in
// the API middleware still blocks suspended accounts on the next request.
func (s *Service) handleSignout(w http.ResponseWriter, r *http.Request) {
	if id := s.accountFromRequest(r); id != "" {
		s.log.Info("user signed out", "account_id", id)
	}
	s.ClearSessionMaterial(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "OK"})
}

// handleSession reports the current session for debugging and for clients
// that still poll GET /session (the v17 SDK does not call it).
func (s *Service) handleSession(w http.ResponseWriter, r *http.Request) {
	accountID := s.accountFromRequest(r)
	if accountID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"status": "UNAUTHORIZED"})
		return
	}
	var email, name string
	if err := s.pool.QueryRow(r.Context(),
		"select email, name from accounts where id = $1", accountID).Scan(&email, &name); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"status": "UNAUTHORIZED"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "OK",
		"session": map[string]any{
			"userId":  accountID,
			"handle":  email,
			"created": time.Now().UnixMilli(),
			"expires": time.Now().Add(s.cfg.SessionTTL).UnixMilli(),
		},
		"user": userResponse{
			ID:           accountID,
			Emails:       []string{email},
			LoginMethods: []loginMethod{{EmailID: email, LinkScore: 100}},
		},
	})
}

// handleEmailExists implements GET /api/auth/signup/email/exists
// (Supertokens EMAIL_EXISTS pre-check used by some auth UIs).
func (s *Service) handleEmailExists(w http.ResponseWriter, r *http.Request) {
	email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
	if email == "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "email is required")
		return
	}
	var exists bool
	if err := s.pool.QueryRow(r.Context(),
		"select exists(select 1 from accounts where email = $1)", email).Scan(&exists); err != nil {
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "OK", "exists": exists})
}

// auditSession records a session lifecycle event for operator visibility.
// Failures are logged, never fatal to the auth response.
func (s *Service) auditSession(ctx context.Context, accountID, ip, userAgent string) {
	if _, err := s.pool.Exec(ctx, `
		insert into sessions (account_id, token_hash, expires_at, user_agent, ip)
		values ($1, $2, now() + $3::interval, $4, $5)`,
		accountID, "hmac-session", intervalSQL(s.cfg.SessionTTL), userAgent, ip); err != nil {
		s.log.Warn("session audit insert failed", "error", err)
	}
}

// sessionBody renders the session object for responses.
func (s *Service) sessionBody(m SessionMaterial) map[string]any {
	return map[string]any{
		"userId":          m.AccountID,
		"created":         time.Now().UnixMilli(),
		"expires":         m.AccessExpiry.UnixMilli(),
		"sessionDataInDB": map[string]any{},
	}
}

// ResolveAccountID resolves the calling account from the request session
// material. It accepts, in priority order:
//
//  1. Authorization: Bearer <accessToken>
//  2. st-access-token cookie envelope
//
// It returns "" when the request carries no usable session.
func (s *Service) ResolveAccountID(ctx context.Context, r *http.Request) string {
	return s.accountFromRequest(r)
}

// ---- wire types ----------------------------------------------------------

type loginMethod struct {
	ID        string `json:"id"`
	EmailID   string `json:"emailId"`
	LinkScore int    `json:"linkScore"`
}

type userResponse struct {
	ID           string        `json:"id"`
	Emails       []string      `json:"emails"`
	LoginMethods []loginMethod `json:"loginMethods"`
}

// ---- helpers -------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, codeStr, message string) {
	writeJSON(w, code, map[string]any{
		"status":  "ERROR",
		"error":   codeStr,
		"message": message,
	})
}

// clientIP resolves the client address, trusting X-Forwarded-For only for
// local proxy connections.
func clientIP(r *http.Request) string {
	connIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		connIP = host
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" && isLocal(connIP) {
		if first := strings.TrimSpace(strings.Split(fwd, ",")[0]); first != "" {
			return first
		}
	}
	return connIP
}

func isLocal(ip string) bool {
	return ip == "127.0.0.1" || ip == "::1" ||
		strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "192.168.") || strings.HasPrefix(ip, "172.16.")
}

var _ = url.QueryEscape
