// Package auth implements the authentication boundary: server-side
// sessions plus the email magic-link flow.
//
// The web client speaks the Supertokens passwordless + session wire
// protocol (it is the forked Tegon client, unchanged in v1). This package
// implements the same-origin subset that client uses:
//
//	POST /api/auth/recipe/passwordless/create-code
//	POST /api/auth/recipe/passwordless/consume-code
//	GET  /api/auth/session
//	POST /api/auth/signout
//
// Tokens are high-entropy random values; only their SHA-256 hashes are
// stored. Cookies are HttpOnly + SameSite=Lax; the client calls this
// server same-origin (the web app proxies /api/* to it), so no
// cross-origin anti-CSRF handling is required.
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
	r.Post("/api/auth/recipe/passwordless/create-code", s.handleCreateCode)
	r.Post("/api/auth/recipe/passwordless/consume-code", s.handleConsumeCode)
	r.Get("/api/auth/session", s.handleSession)
	r.Post("/api/auth/signout", s.handleSignout)
}

// ---- sessions -----------------------------------------------------------

func newToken() (string, error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// createSession inserts a session row and sets the cookie.
func (s *Service) createSession(ctx context.Context, w http.ResponseWriter, r *http.Request, accountID string) error {
	token, err := newToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(s.cfg.SessionTTL)
	if _, err := s.pool.Exec(ctx, `
		insert into sessions (account_id, token_hash, expires_at, user_agent, ip)
		values ($1, $2, $3, $4, $5)`,
		accountID, hashToken(token), expires, r.UserAgent(), clientIP(r)); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	s.setCookie(w, token, int(s.cfg.SessionTTL.Seconds()))
	return nil
}

func (s *Service) setCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   s.cfg.SecureCookies,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   s.cfg.SecureCookies,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// SessionAccountID resolves the session cookie to an account id, or "" if
// absent, invalid, expired, or revoked.
func (s *Service) SessionAccountID(ctx context.Context, r *http.Request) string {
	cookie, err := r.Cookie(s.cfg.SessionCookieName)
	if err != nil || cookie.Value == "" {
		return ""
	}
	var accountID string
	err = s.pool.QueryRow(ctx, `
		select account_id from sessions
		where token_hash = $1 and revoked_at is null and expires_at > now()`,
		hashToken(cookie.Value)).Scan(&accountID)
	if err != nil {
		return ""
	}
	return accountID
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

type sessionBody struct {
	UserID          string         `json:"userId"`
	SessionID       string         `json:"sessionId"`
	SessionHandle   string         `json:"sessionHandle"`
	CreatedAt       int64          `json:"createdAt"`
	Expires         int64          `json:"expires"`
	SessionDataInDB map[string]any `json:"sessionDataInDatabase"`
}

type sessionResponse struct {
	Status               string       `json:"status"`
	Session              *sessionBody `json:"session"`
	AntiCSRFToken        string       `json:"antiCsrfToken"`
	User                 userResponse `json:"user"`
	CreatedNewRecipeUser bool         `json:"createdNewRecipeUser"`
}

// ---- handlers -----------------------------------------------------------

func (s *Service) handleCreateCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ContactInfo struct {
			Email string `json:"email"`
		} `json:"contactInfo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "malformed request body")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.ContactInfo.Email))
	if !strings.Contains(email, "@") || len(email) < 5 || len(email) > 255 {
		writeError(w, http.StatusBadRequest, "INVALID_EMAIL", "a valid email is required")
		return
	}

	code, err := newCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "could not create code")
		return
	}
	// Supertokens sign-in/up semantics: an unknown email is not an error
	// at this step; the account materializes when the code is consumed.
	if _, err := s.pool.Exec(r.Context(), `
		insert into auth_codes (email, token_hash, expires_at)
		values ($1, $2, $3)`, email, hashToken(code), time.Now().Add(s.cfg.CodeTTL)); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "could not create code")
		return
	}

	link := fmt.Sprintf("%s/auth/verify?code=%s&preAuthFactorId=email", s.cfg.WebOrigin, code)
	s.log.Info("magic link issued", "email", email, "link", link)
	writeJSON(w, http.StatusOK, map[string]string{"status": "OK"})
}

func (s *Service) handleConsumeCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Code == "" {
		body.Code = r.URL.Query().Get("code")
	}
	code := strings.ToUpper(strings.TrimSpace(body.Code))
	if code == "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "code is required")
		return
	}
	ctx := r.Context()

	// 1. A live, unexpired, unconsumed code must match.
	var email string
	err := s.pool.QueryRow(ctx, `
		select email from auth_codes
		where token_hash = $1 and consumed_at is null and expires_at > now()
		limit 1`, hashToken(code)).Scan(&email)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "database error")
			return
		}
		writeError(w, http.StatusBadRequest, "INVALID_OR_EXPIRED_CODE", "Login failed. Please try again.")
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
			writeError(w, http.StatusInternalServerError, "INTERNAL", "database error")
			return
		}
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", "database error")
		return
	}

	// 3. Consume the code and start the session.
	if _, err := s.pool.Exec(ctx,
		`update auth_codes set consumed_at = now() where token_hash = $1`,
		hashToken(code)); err != nil {
		s.log.Error("mark code consumed", "error", err)
	}
	if err := s.createSession(ctx, w, r, accountID); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "could not start session")
		return
	}

	s.log.Info("user authenticated", "account_id", accountID, "email", email, "new_user", created)
	writeJSON(w, http.StatusOK, sessionResponse{
		Status:               "OK",
		Session:              s.sessionBody(accountID, email, time.Now()),
		AntiCSRFToken:        "",
		User:                 userResponse{ID: accountID, Emails: []string{email}, LoginMethods: []loginMethod{{ID: hashToken(code)[:16], EmailID: email, LinkScore: 100}}},
		CreatedNewRecipeUser: created,
	})
}

func (s *Service) handleSession(w http.ResponseWriter, r *http.Request) {
	accountID := s.SessionAccountID(r.Context(), r)
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
	cookie, _ := r.Cookie(s.cfg.SessionCookieName)
	writeJSON(w, http.StatusOK, sessionResponse{
		Status: "OK",
		Session: &sessionBody{
			UserID:          accountID,
			SessionID:       strings.ToUpper(strings.TrimRight(cookie.Value, "=")[:32]),
			SessionHandle:   email,
			CreatedAt:       time.Now().UnixMilli(),
			Expires:         time.Now().Add(s.cfg.SessionTTL).UnixMilli(),
			SessionDataInDB: map[string]any{},
		},
		AntiCSRFToken: "",
		User:          userResponse{ID: accountID, Emails: []string{email}, LoginMethods: []loginMethod{{EmailID: email, LinkScore: 100}}},
	})
}

func (s *Service) handleSignout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(s.cfg.SessionCookieName); err == nil && cookie.Value != "" {
		if _, err := s.pool.Exec(r.Context(),
			`update sessions set revoked_at = now() where token_hash = $1`,
			hashToken(cookie.Value)); err != nil {
			s.log.Error("revoke session", "error", err)
		}
	}
	s.clearCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "OK"})
}

// sessionBody builds the session object for a signed-in account.
func (s *Service) sessionBody(accountID, email string, at time.Time) *sessionBody {
	return &sessionBody{
		UserID:          accountID,
		SessionID:       "",
		SessionHandle:   email,
		CreatedAt:       at.UnixMilli(),
		Expires:         at.Add(s.cfg.SessionTTL).UnixMilli(),
		SessionDataInDB: map[string]any{},
	}
}

// ---- helpers ------------------------------------------------------------

func newCode() (string, error) {
	b := make([]byte, 15)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}
	return strings.ToUpper(base64.StdEncoding.EncodeToString(b)[:20]), nil
}

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
