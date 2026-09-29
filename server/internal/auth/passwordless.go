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
//
// Every route is rate limited per client IP; issuing a code (create,
// resend) is additionally limited per email address, so neither a
// single host nor a distributed sender can mail-bomb an inbox.
//
// Tokens are HMAC-signed base64-JSON payloads (see session.go); the
// client parses them with atob() and treats them opaquely, so the
// unmodified client signs in against this server.
//
// Codes are random high-entropy values whose SHA-256 hashes are stored.
// Refresh tokens are checked against their sessions row and rotated on
// every use (sessions.go); access tokens are stateless.
//
// State-changing requests that authenticate with session cookies must
// come from the web app's origin (RequireSameOrigin).
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
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"converge/internal/config"
	"converge/internal/metrics"
	"converge/internal/netx"
	"converge/internal/notify"
)

// Mailer delivers transactional mail (notify.Service in production).
type Mailer interface {
	Send(ctx context.Context, msg notify.Message) error
}

// mailTimeout bounds one background sign-in mail delivery.
const mailTimeout = 30 * time.Second

// Auth route limits. Per IP: a burst of 30, then one request every two
// seconds — far above a person signing in or a client refreshing hourly,
// far below a credential or email sprayer. Per email: 5 codes, then one
// every 3 minutes.
const (
	authIPRate     = 0.5
	authIPBurst    = 30
	authEmailRate  = 1.0 / 180
	authEmailBurst = 5
)

// authEvents counts the security-relevant session events. A rise in
// refresh_reuse means refresh tokens are being stolen and replayed.
var authEvents = metrics.Default.NewCounterVec("converge_auth_events_total",
	"Sign-in and session events: signin, refresh, refresh_race, refresh_reuse, signout, "+
		"rate_limited_ip, rate_limited_email, cross_origin_refused.", "event")

// Service owns session and magic-link code lifecycle.
type Service struct {
	pool    *pgxpool.Pool
	cfg     config.Config
	log     *slog.Logger
	mailer  Mailer
	ipLimit *netx.Limiter
	// emailLimit bounds code issuance per address.
	emailLimit *netx.Limiter
	revoked    *revocations
	// live remembers sessions this process just confirmed are not
	// revoked, so the shared check is not a query on every request.
	live    *revocations
	origins map[string]bool
}

// NewService builds the auth service. A nil mailer disables sign-in
// mail (tests); production passes the notify service.
func NewService(pool *pgxpool.Pool, cfg config.Config, log *slog.Logger, mailer Mailer) *Service {
	return &Service{
		pool:       pool,
		cfg:        cfg,
		log:        log,
		mailer:     mailer,
		ipLimit:    netx.NewLimiter(authIPRate, authIPBurst),
		emailLimit: netx.NewLimiter(authEmailRate, authEmailBurst),
		revoked:    newRevocations(),
		live:       newRevocations(),
		origins:    trustedOrigins(cfg.WebOrigin, cfg.PublicURL),
	}
}

// limitByIP rejects auth requests from a client IP over its budget.
func (s *Service) limitByIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := netx.ClientIP(r); !s.ipLimit.Allow(ip) {
			authEvents.With("rate_limited_ip").Inc()
			s.log.Warn("auth rate limit", "scope", "ip", "ip", ip, "path", r.URL.Path)
			writeRateLimited(w, int(1/authIPRate))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allowEmail consumes one code-issuance token for email, writing the 429
// when the address is over its budget.
func (s *Service) allowEmail(w http.ResponseWriter, email string) bool {
	if s.emailLimit.Allow(email) {
		return true
	}
	authEvents.With("rate_limited_email").Inc()
	s.log.Warn("auth rate limit", "scope", "email", "email", email)
	writeRateLimited(w, int(1/authEmailRate))
	return false
}

func writeRateLimited(w http.ResponseWriter, retryAfterSeconds int) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests; try again later")
}

// sendSignInLink mails a sign-in link off the request path: SMTP latency
// must neither slow the response nor let response timing distinguish
// addresses. Failures are logged without the link.
func (s *Service) sendSignInLink(ctx context.Context, email, link string) {
	if s.mailer == nil {
		return
	}
	msg := notify.SignInMessage(email, link, s.cfg.CodeTTL)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mailTimeout)
	go func() {
		defer cancel()
		if err := s.mailer.Send(ctx, msg); err != nil {
			s.log.Error("sign-in mail failed", "email", email, "error", err)
		}
	}()
}

// Mount registers the Supertokens-compatible routes on r.
func (s *Service) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(s.limitByIP)
		r.Post("/api/auth/signinup/code", s.handleCreateCode)
		r.Post("/api/auth/signinup/code/resend", s.handleResendCode)
		r.Post("/api/auth/signinup/code/consume", s.handleConsumeCode)
		r.Post("/api/auth/session/refresh", s.handleRefresh)
		r.Post("/api/auth/signout", s.handleSignout)
		r.Get("/api/auth/session", s.handleSession)
	})
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

// emailPattern accepts a bare address (no display name, no angle
// brackets) with a dotted domain. Whitespace and control characters are
// refused outright: the address is written into mail headers and logs.
func emailPattern(email string) bool {
	if len(email) < 5 || len(email) > 255 {
		return false
	}
	for _, r := range email {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || addr.Name != "" {
		return false
	}
	at := strings.LastIndex(email, "@")
	return at > 0 && strings.Contains(email[at+1:], ".")
}

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
	if !s.allowEmail(w, email) {
		return
	}

	// M6: agent identities authenticate with API tokens only; a magic
	// link for an agent email would hand a human the agent's account.
	if kind, err := s.accountKindByEmail(r.Context(), email); err != nil {
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "database error")
		return
	} else if kind == AccountKindAgent {
		writeError(w, http.StatusForbidden, "AGENT_ACCOUNT", "agent accounts cannot sign in")
		return
	}

	code, preAuthSessionID, deviceID, err := s.issueCode(r.Context(), email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "could not create code")
		return
	}

	s.log.Info("magic link issued", "email", email, "pre_auth_session_id", preAuthSessionID)
	s.sendSignInLink(r.Context(), email, s.magicLink(preAuthSessionID, code))
	s.writeCreateCodeResponse(w, preAuthSessionID, deviceID, code)
}

// issueCode persists a fresh single-use sign-in code for email and
// returns the code with the ids the magic link needs.
func (s *Service) issueCode(ctx context.Context, email string) (code, preAuthSessionID, deviceID string, err error) {
	code, err = newCode()
	if err != nil {
		return "", "", "", err
	}
	preAuthSessionID = newID()
	deviceID = newID()
	if _, err = s.pool.Exec(ctx, `
		insert into auth_codes (email, token_hash, expires_at, pre_auth_session_id, device_id)
		values ($1, $2, $3, $4, $5)`,
		email, hashValue(code), time.Now().Add(s.cfg.CodeTTL), preAuthSessionID, deviceID); err != nil {
		return "", "", "", err
	}
	return code, preAuthSessionID, deviceID, nil
}

// magicLink builds the verify URL the email provider would send. The v17
// client reads the link code from the URL hash fragment and the
// preAuthSessionId from the query string.
func (s *Service) magicLink(preAuthSessionID, code string) string {
	return fmt.Sprintf("%s/auth/verify?preAuthSessionId=%s#%s", s.cfg.WebOrigin, preAuthSessionID, code)
}

// CodeForEmail issues a fresh sign-in code for email and returns the
// magic link. Invitation mail carries this link: consuming it signs the
// invitee in (the account already exists — the invite materialized it)
// and the client then routes to the pending invitation to accept or
// decline.
func (s *Service) CodeForEmail(ctx context.Context, email string) (string, error) {
	code, preAuthSessionID, _, err := s.issueCode(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return "", fmt.Errorf("issue code for %s: %w", email, err)
	}
	return s.magicLink(preAuthSessionID, code), nil
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
// a fresh code for the same pre-auth session (same email). A consumed
// session cannot be re-armed: the flow restarts from create-code.
func (s *Service) handleResendCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID         string `json:"deviceId"`
		PreAuthSessionID string `json:"preAuthSessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "malformed request body")
		return
	}
	preAuthSessionID := strings.TrimSpace(body.PreAuthSessionID)
	if preAuthSessionID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "preAuthSessionId is required")
		return
	}
	expiredSession := func() {
		writeError(w, http.StatusBadRequest, "EXPIRED_PRE_AUTH_SESSION", "the sign-in session is no longer active; start again")
	}
	// The address is charged before the code rotates, so a throttled
	// resend leaves the previous link working.
	var email string
	if err := s.pool.QueryRow(r.Context(),
		`select email from auth_codes where pre_auth_session_id = $1 and consumed_at is null`,
		preAuthSessionID).Scan(&email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			expiredSession()
			return
		}
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "could not resend code")
		return
	}
	if !s.allowEmail(w, email) {
		return
	}
	code, err := newCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "could not resend code")
		return
	}
	if err := s.pool.QueryRow(r.Context(), `
		update auth_codes
		set token_hash = $1, expires_at = now() + $2::interval
		where pre_auth_session_id = $3 and consumed_at is null
		returning email`,
		hashValue(code), intervalSQL(s.cfg.CodeTTL), preAuthSessionID).
		Scan(&email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			expiredSession()
			return
		}
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "could not resend code")
		return
	}
	link := s.magicLink(preAuthSessionID, code)
	s.log.Info("magic link reissued", "email", email, "pre_auth_session_id", preAuthSessionID)
	s.sendSignInLink(r.Context(), email, link)
	resp := map[string]string{"status": "OK"}
	if s.cfg.DevMode {
		resp["devMagicLink"] = link
	}
	writeJSON(w, http.StatusOK, resp)
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
	// Never from the query string: a code in a URL lands in proxy access
	// logs, and a query needs no body a cross-site form must shape.
	if code == "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "code is required")
		return
	}
	ctx := r.Context()

	accountID, email, created, status, err := s.consumeCode(ctx, code, strings.TrimSpace(body.PreAuthSessionID))
	if err != nil {
		s.log.Error("consume sign-in code", "error", err)
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "database error")
		return
	}
	if status != "OK" {
		writeJSON(w, http.StatusOK, map[string]string{"status": status})
		return
	}

	material, err := s.startSession(ctx, accountID, netx.ClientIP(r), r.UserAgent())
	if err != nil {
		s.log.Error("start session", "account_id", accountID, "error", err)
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "database error")
		return
	}
	s.SetSessionMaterial(w, material)

	authEvents.With("signin").Inc()
	s.log.Info("user authenticated", "account_id", accountID, "session_id", material.SessionID, "email", email, "new_user", created)
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

// consumeCode claims a live code and resolves (or creates) its account in
// one transaction. The claim is a single conditional UPDATE, so of two
// concurrent consumers exactly one wins; a consumed or expired code never
// matches again. On success every other outstanding code for the address
// is burned too, so an older link that leaked cannot be used later.
//
// status is "OK" or the Supertokens status the verify page keys off;
// err is reserved for infrastructure failures.
func (s *Service) consumeCode(ctx context.Context, code, preAuthSessionID string) (accountID, email string, created bool, status string, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", "", false, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	match := `token_hash = $1`
	args := []any{hashValue(code)}
	if preAuthSessionID != "" {
		match += ` and pre_auth_session_id = $2`
		args = append(args, preAuthSessionID)
	}

	err = tx.QueryRow(ctx, `
		update auth_codes set consumed_at = now()
		where `+match+` and consumed_at is null and expires_at > now()
		returning email`, args...).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		var expired bool
		err = tx.QueryRow(ctx, `
			select consumed_at is null and expires_at <= now()
			from auth_codes where `+match+` limit 1`, args...).Scan(&expired)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return "", "", false, "INVALID_LINK_CODE", nil
		case err != nil:
			return "", "", false, "", err
		case expired:
			return "", "", false, "EXPIRED_LINK_CODE", nil
		default:
			return "", "", false, "INVALID_LINK_CODE", nil
		}
	}
	if err != nil {
		return "", "", false, "", err
	}

	// Sign-in/up: create the account if it does not exist yet.
	name := strings.SplitN(email, "@", 2)[0]
	err = tx.QueryRow(ctx, `
		insert into accounts (email, name) values ($1, $2)
		on conflict (email) do nothing
		returning id`, email, name).Scan(&accountID)
	switch {
	case err == nil:
		created = true
	case errors.Is(err, pgx.ErrNoRows):
		var kind, acctStatus string
		if err := tx.QueryRow(ctx,
			"select id, kind, status from accounts where email = $1", email).Scan(&accountID, &kind, &acctStatus); err != nil {
			return "", "", false, "", err
		}
		// M6: an agent account must never take a browser session.
		if kind == AccountKindAgent {
			return "", "", false, "AGENT_ACCOUNT", nil
		}
		if acctStatus != "active" {
			return "", "", false, "SIGN_IN_UP_NOT_ALLOWED", nil
		}
	default:
		return "", "", false, "", err
	}

	if _, err := tx.Exec(ctx,
		`update auth_codes set consumed_at = now() where email = $1 and consumed_at is null`, email); err != nil {
		return "", "", false, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", false, "", err
	}
	return accountID, email, created, "OK", nil
}

// handleRefresh implements POST /api/auth/session/refresh. The refresh
// token comes from "Authorization: Bearer <token>" or the refresh cookie;
// only the cookie path needs the anti-csrf value, since a header is not
// ambient authority. Access tokens are not accepted here.
func (s *Service) handleRefresh(w http.ResponseWriter, r *http.Request) {
	unauthorized := func() {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"status": "UNAUTHORIZED"})
	}
	token, fromCookie := bearerToken(r), false
	if token == "" {
		if c, err := r.Cookie(CookieRefreshToken); err == nil {
			if inner, err := innerFromEnvelope(c.Value); err == nil {
				token, fromCookie = inner, true
			}
		}
	}
	claims, ok := s.parseToken("refresh", token)
	if !ok {
		unauthorized()
		return
	}
	if fromCookie && !s.ValidateAntiCsrf(claims.Account, claims.Session, r.Header.Get(HeaderAntiCsrf)) {
		unauthorized()
		return
	}

	material, rotated, err := s.refreshSession(r.Context(), claims, token, netx.ClientIP(r), r.UserAgent())
	switch {
	case errors.Is(err, errSessionReused):
		authEvents.With("refresh_reuse").Inc()
		s.log.Warn("refresh token reused; session revoked",
			"account_id", claims.Account, "session_id", claims.Session, "ip", netx.ClientIP(r))
		s.ClearSessionMaterial(w)
		unauthorized()
		return
	case errors.Is(err, errSessionInvalid):
		s.ClearSessionMaterial(w)
		unauthorized()
		return
	case err != nil:
		s.log.Error("refresh session", "session_id", claims.Session, "error", err)
		writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "database error")
		return
	}
	if rotated {
		authEvents.With("refresh").Inc()
		s.SetSessionMaterial(w, material)
	} else {
		authEvents.With("refresh_race").Inc()
		s.setAccessMaterial(w, material)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "OK",
		"session": s.sessionBody(material),
	})
}

// handleSignout revokes the request's session server-side and expires
// every session cookie. The refresh token is dead at once everywhere; its
// access tokens at once on this instance and within the access TTL after
// a restart.
func (s *Service) handleSignout(w http.ResponseWriter, r *http.Request) {
	if c, ok := s.sessionFromRequest(r); ok {
		if err := s.revokeSession(r.Context(), c, "signout"); err != nil {
			s.log.Error("revoke session", "session_id", c.Session, "error", err)
			writeError(w, http.StatusInternalServerError, "GENERAL_ERROR", "database error")
			return
		}
		authEvents.With("signout").Inc()
		s.log.Info("user signed out", "account_id", c.Account, "session_id", c.Session)
	}
	s.ClearSessionMaterial(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "OK"})
}

// handleSession reports the current session for debugging and for clients
// that still poll GET /session (the v17 SDK does not call it).
func (s *Service) handleSession(w http.ResponseWriter, r *http.Request) {
	accountID, _ := s.accountFromRequest(r)
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

// sessionBody renders the session object for responses.
func (s *Service) sessionBody(m SessionMaterial) map[string]any {
	return map[string]any{
		"userId":          m.AccountID,
		"created":         time.Now().UnixMilli(),
		"expires":         m.AccessExpiry.UnixMilli(),
		"sessionDataInDB": map[string]any{},
	}
}

// accountKindByEmail reports the kind of the account behind an email
// ("human" when no account exists yet) so the sign-in flow can reject
// agent identities before issuing or consuming a code.
func (s *Service) accountKindByEmail(ctx context.Context, email string) (string, error) {
	var kind string
	err := s.pool.QueryRow(ctx, "select kind from accounts where email = $1", email).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccountKindHuman, nil
	}
	if err != nil {
		return "", err
	}
	return kind, nil
}

// ResolveAccountID resolves the calling account from the request session
// material. It accepts, in priority order:
//
//  1. Authorization: Bearer <accessToken>
//  2. st-access-token cookie envelope
//
// It returns "" when the request carries no usable session. tok is the
// API token's grant when one authenticated the request, nil otherwise.
func (s *Service) ResolveAccountID(ctx context.Context, r *http.Request) (accountID string, tok *APIToken) {
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
