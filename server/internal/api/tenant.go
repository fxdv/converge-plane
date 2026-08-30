// Package api assembles the application HTTP API: the Supertokens
// compatible auth routes plus the Circle /api/v1 endpoints consumed by
// the web client.
package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"circle/internal/auth"
	"circle/internal/config"
	"log/slog"
)

// API bundles the application dependencies.
type API struct {
	pool *pgxpool.Pool
	cfg  config.Config
	log  *slog.Logger
	auth *auth.Service
}

func New(pool *pgxpool.Pool, cfg config.Config, log *slog.Logger, authSvc *auth.Service) *API {
	return &API{pool: pool, cfg: cfg, log: log, auth: authSvc}
}

// Principal is the authenticated human caller, derived exclusively from
// the server-side session — never from request parameters.
type Principal struct {
	AccountID string
	Email     string
	Fullname  string
}

type ctxKey string

const principalKey ctxKey = "principal"

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFromContext returns the authenticated principal or nil.
func PrincipalFromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey).(*Principal)
	return p
}

// Mount registers all application routes on r.
func (a *API) Mount(r chi.Router) {
	a.auth.Mount(r)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(a.sessionMiddleware)
		r.Get("/users", a.handleGetUser)
		r.Post("/workspaces/onboarding", a.handleOnboarding)
		r.Get("/sync_actions/bootstrap", a.handleSync)
		r.Get("/sync_actions/delta", a.handleSync)
	})
}

// sessionMiddleware resolves the session to an authenticated principal or
// rejects the request with 401.
func (a *API) sessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accountID := a.auth.ResolveAccountID(r.Context(), r)
		if accountID == "" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthenticated"}`))
			return
		}
		var p Principal
		p.AccountID = accountID
		if err := a.pool.QueryRow(r.Context(),
			"select email, name from accounts where id = $1 and status = 'active'",
			accountID).Scan(&p.Email, &p.Fullname); err != nil {
			a.log.Error("principal lookup failed", "error", err, "account_id", accountID)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthenticated"}`))
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), &p)))
	})
}

// workspaceRole returns the principal's active role in a workspace.
// ok is false when the principal has no active membership.
func (a *API) workspaceRole(ctx context.Context, principal *Principal, workspaceID string) (role string, ok bool) {
	var r string
	err := a.pool.QueryRow(ctx, `
		select role from workspace_members
		where workspace_id = $1 and account_id = $2 and status = 'active'`,
		workspaceID, principal.AccountID).Scan(&r)
	if err != nil {
		return "", false
	}
	return strings.ToUpper(r), true
}

// writeJSON serializes v as the HTTP JSON response.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// internalError logs the error and returns a generic 500.
func (a *API) internalError(w http.ResponseWriter, err error) {
	a.log.Error("api internal error", "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": "internal error",
	})
}

// jsonDecode decodes a request body into v.
func jsonDecode(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
