// Package api assembles the application HTTP API: the Supertokens
// compatible auth routes plus the Converge /api/v1 endpoints consumed by
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

	"converge/internal/auth"
	"converge/internal/broadcast"
	"converge/internal/config"
	"converge/internal/notify"
	"log/slog"
)

// API bundles the application dependencies.
type API struct {
	pool *pgxpool.Pool
	cfg  config.Config
	log  *slog.Logger
	auth *auth.Service
	// notify delivers transactional mail (workspace invitations).
	notify *notify.Service
	// bcast fans out committed change records to realtime subscribers.
	bcast *broadcast.Broadcaster
	// limiter bounds per-account request rates (swarm flood guard).
	limiter *accountRateLimiter
}

func New(pool *pgxpool.Pool, cfg config.Config, log *slog.Logger, authSvc *auth.Service, notifySvc *notify.Service) *API {
	return &API{
		pool:    pool,
		cfg:     cfg,
		log:     log,
		auth:    authSvc,
		notify:  notifySvc,
		bcast:   broadcast.New(),
		limiter: newAccountRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst),
	}
}

// Principal is the authenticated caller, derived exclusively from the
// session material (web session or agent API token) — never from
// request parameters. Kind distinguishes human from agent accounts.
type Principal struct {
	AccountID string
	Email     string
	Fullname  string
	Kind      string
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
		// The guard runs after the principal is resolved: unauthenticated
		// probes pay the middleware and nothing else.
		r.Use(a.sessionMiddleware, a.rateLimitGuard)
		r.Get("/users", a.handleGetUser)
		r.Put("/users", a.handleUpdateUser)
		r.Post("/workspaces/onboarding", a.handleOnboarding)
		r.Get("/sync_actions/bootstrap", a.handleSync)
		r.Get("/sync_actions/delta", a.handleSync)
		// M2: issue mutations + realtime.
		r.Post("/issues", a.handleCreateIssue)
		r.Post("/issues/{id}", a.handleUpdateIssue)
		r.Delete("/issues/{id}", a.handleDeleteIssue)
		r.Post("/issues/{id}/move", a.handleMoveIssue)
		// D1: the agent handoff protocol (docs/spec/12).
		r.Post("/issues/{id}/handoff", a.handleHandoff)
		r.Post("/issues/{id}/subscribe", a.handleSubscribeIssue)
		r.Post("/issue_comments", a.handleCreateComment)
		r.Post("/issue_comments/{id}", a.handleUpdateComment)
		r.Delete("/issue_comments/{id}", a.handleDeleteComment)
		r.Get("/issue_comments/{id}", a.handleGetComment)
		r.Get("/issue_comments/{id}/replies", a.handleGetCommentReplies)
		r.Get("/sync_actions/stream", a.handleStream)
		// M5: teams (the teamId wildcard below never collides with these
		// static-first routes; chi prefers the static segment, and the
		// workflow handlers reject non-UUID first segments with 404).
		r.Post("/teams", a.handleCreateTeam)
		r.Post("/teams/{id}", a.handleUpdateTeam)
		r.Delete("/teams/{id}", a.handleDeleteTeam)
		r.Get("/teams", a.handleListTeams)
		r.Get("/teams/{id}", a.handleGetTeam)
		r.Get("/teams/name/{slug}", a.handleGetTeamByName)
		r.Post("/teams/{id}/add-member", a.handleAddTeamMember)
		r.Post("/teams/{id}/remove-member", a.handleRemoveTeamMember)
		r.Post("/teams/{id}/preferences", a.handleUpdateTeamPreferences)
		// M5: workflows ride the client's quirky /{teamId}/workflows path.
		r.Post("/{teamId}/workflows", a.handleCreateWorkflow)
		r.Post("/{teamId}/workflows/{workflowId}", a.handleUpdateWorkflow)
		r.Get("/{teamId}/workflows", a.handleListWorkflows)
		// M5: labels, views, workspace administration, search.
		r.Post("/labels", a.handleCreateLabel)
		r.Post("/labels/{id}", a.handleUpdateLabel)
		r.Delete("/labels/{id}", a.handleDeleteLabel)
		r.Get("/labels", a.handleListLabels)
		r.Post("/views", a.handleCreateView)
		r.Post("/views/{id}", a.handleUpdateView)
		r.Delete("/views/{id}", a.handleDeleteView)
		r.Get("/views/{id}", a.handleGetView)
		r.Post("/workspaces", a.handleUpdateWorkspace)
		r.Post("/workspaces/preferences", a.handleUpdateWorkspacePreferences)
		r.Post("/workspaces/invite_users", a.handleInviteUsers)
		r.Post("/workspaces/invite_action", a.handleInviteAction)
		r.Post("/workspaces/suspend", a.handleSuspendMember)
		// M6: agent actors (swarm-capable machine members).
		// D2: the swarm panel's fleet roster (read-only, any member).
		r.Get("/workspaces/{id}/swarm", a.handleSwarmStatus)
		r.Post("/workspaces/{id}/agents", a.handleCreateAgent)
		r.Get("/workspaces/{id}/agents", a.handleListAgents)
		r.Post("/workspaces/{id}/agents/{accountId}/token", a.handleRotateAgentToken)
		r.Post("/workspaces/{id}/agents/{accountId}/token/revoke", a.handleRevokeAgentToken)
		r.Delete("/workspaces/{id}/agents/{accountId}", a.handleDeleteAgent)
		r.Get("/search", a.handleSearch)
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
			"select email, name, kind from accounts where id = $1 and status = 'active'",
			accountID).Scan(&p.Email, &p.Fullname, &p.Kind); err != nil {
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
