// Package api assembles the application HTTP API: the Supertokens
// compatible auth routes plus the Converge /api/v1 endpoints consumed by
// the web client.
// spec cs:api:rest
package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"converge/internal/auth"
	"converge/internal/broadcast"
	"converge/internal/config"
	"converge/internal/notify"
	"log/slog"
)

// db is the database surface the API drives. pgxpool.Pool satisfies it
// in production; the tests swap in an in-memory fake (the same seam as
// queryer: the code depends on the interface, the tests pin the
// behavior). Pool-specific methods (Stat for the metrics gauges) stay
// on the concrete type via a type assertion.
type db interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// API bundles the application dependencies.
type API struct {
	pool db
	cfg  config.Config
	log  *slog.Logger
	auth *auth.Service
	// notify delivers transactional mail (workspace invitations).
	notify *notify.Service
	// bcast fans out committed change records to realtime subscribers.
	bcast *broadcast.Broadcaster
	// limiter bounds per-account request rates (swarm flood guard).
	limiter *accountRateLimiter
	// runtime is the in-process agent runtime (D3): it acts for agents
	// that have work, through the same transactional paths as the API.
	runtime *AgentRuntime
	// sweeperDone stops the claim sweeper (work.go).
	sweeperDone chan struct{}
	// githubDone stops the pull request poller (github_poll.go), which
	// runs only when CONVERGE_GITHUB_REPOS lists repositories.
	githubDone chan struct{}
	// webhooks delivers signed outbound events. Nil skips enqueue, so
	// tests that drive handlers without StartRuntime do not need endpoints.
	webhooks    *webhookDispatcher
	webhookDone chan struct{}
	// startedAt is the process birth (the metrics plane's uptime source).
	startedAt time.Time
	// instanceID distinguishes this process's own fan-out notices from
	// another process's. Empty until StartRuntime.
	instanceID string
	fanoutDone chan struct{}
	leaderStop chan struct{}
	svcMu      sync.Mutex
	servicesOn bool
	stopOnce   sync.Once
	// root is the router Mount was given; MCP tool calls re-enter it.
	root http.Handler
}

func New(pool *pgxpool.Pool, cfg config.Config, log *slog.Logger, authSvc *auth.Service, notifySvc *notify.Service) *API {
	a := &API{
		pool:      pool,
		cfg:       cfg,
		log:       log,
		auth:      authSvc,
		notify:    notifySvc,
		bcast:     broadcast.New(),
		limiter:   newAccountRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst),
		startedAt: time.Now(),
	}
	a.runtime = newAgentRuntime(a)
	return a
}

// StartRuntime launches the agent runtime on the process context (D3)
// and the work API's claim sweeper. The runtime is a no-op when
// CONVERGE_RUNTIME is off; the sweeper always runs, since external
// agents do not depend on the runtime. main calls it before the HTTP
// listener and StopRuntime on the way out.
func (a *API) StartRuntime(ctx context.Context) {
	a.instanceID = newInstanceID()
	a.fanoutDone = make(chan struct{})
	a.leaderStop = make(chan struct{})
	go a.listenFanout(ctx)
	go a.leadRuntime(ctx)
}

// StopRuntime drains the runtime: the dispatcher stops and live workers
// finish their current action. Best-effort — the process context does
// the real forcing on shutdown.
func (a *API) StopRuntime() {
	a.stopOnce.Do(func() {
		if a.leaderStop != nil {
			close(a.leaderStop)
		}
		if a.fanoutDone != nil {
			close(a.fanoutDone)
		}
	})
	a.stopServices()
}

// wakeIssueOwner enqueues the issue's assignee for runtime work when the
// assignee is an active agent. The mutation paths that CREATE agent work
// (handoff applied, reassignment, resume-from-pause) call it after
// commit. The wakeup is a fast path, not a correctness mechanism: the
// dispatcher's tick re-derives the same work from the database (the
// issue table is the queue), so a missed or dropped wake costs at most
// one tick.
func (a *API) wakeIssueOwner(ctx context.Context, workspaceID, issueID string) {
	var assignee string
	err := a.pool.QueryRow(ctx, `
		select i.assignee_id::text
		from issues i
		join accounts a2 on a2.id = i.assignee_id and a2.kind = $2 and a2.status = 'active'
		                and a2.agent_driver = '`+agentDriverRuntime+`'
		where i.id = $1`, issueID, auth.AccountKindAgent).Scan(&assignee)
	if err == nil {
		a.runtime.Wake(workspaceID, assignee)
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
	// Driver is who works an agent account: agentDriverRuntime or
	// agentDriverExternal. Meaningless for humans.
	Driver string
	// Token is the API token's grant; nil for web sessions.
	Token *auth.APIToken
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

// Mount registers all application routes on r. Every one of them refuses
// writes a browser reports as coming from a foreign origin.
func (a *API) Mount(r chi.Router) {
	a.root = r
	r.Group(func(r chi.Router) {
		r.Use(a.auth.RequireSameOrigin)
		a.routes(r)
	})
}

func (a *API) routes(r chi.Router) {
	a.auth.Mount(r)
	// Phase 2: GitHub's webhook deliveries, signed with the shared secret
	// instead of carrying a session.
	if a.cfg.GitHubWebhookSecret != "" {
		r.Post("/api/github/webhook", a.handleGitHubWebhook)
	}

	r.Route("/api/v1", func(r chi.Router) {
		// The guard runs after the principal is resolved: unauthenticated
		// probes pay the middleware and nothing else. Narrowed tokens are
		// held to their route policy before any handler runs.
		r.Use(a.sessionMiddleware, a.tokenScopeGuard(r), a.rateLimitGuard)
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
		// v1.1: first-class issue relations (docs/spec 08): the edge
		// itself (create rides the issue update) and the reader's list.
		r.Delete("/issue_relation/{id}", a.handleDeleteIssueRelation)
		r.Get("/issues/{id}/relations", a.handleListIssueRelations)
		// Phase 2: pull requests a member links or unlinks by hand.
		r.Post("/issues/{id}/pull_requests", a.handleLinkPullRequest)
		r.Delete("/issues/{id}/pull_requests/{linkId}", a.handleUnlinkPullRequest)
		r.Post("/issues/{id}/done-approval", a.handleApproveDone)
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

		// v1.1: projects (workspace-scoped meaning labels; the board's
		// project rail).
		r.Get("/projects", a.handleListProjects)
		r.Post("/projects", a.handleCreateProject)
		r.Post("/projects/{id}", a.handleUpdateProject)
		r.Post("/projects/{id}/delete", a.handleDeleteProject)
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
		r.Get("/workspaces/{id}/trace", a.handleTraceExport)
		r.Get("/workspaces/{id}/webhooks", a.handleListWebhooks)
		r.Post("/workspaces/{id}/webhooks", a.handleCreateWebhook)
		r.Get("/workspaces/{id}/webhooks/deliveries", a.handleListWebhookDeliveries)
		r.Post("/workspaces/{id}/webhooks/deliveries/{eventId}/retry", a.handleRetryWebhookDelivery)
		r.Delete("/workspaces/{id}/webhooks/{endpointId}", a.handleDeleteWebhook)
		r.Post("/workspaces/{id}/webhooks/{endpointId}/rotate", a.handleRotateWebhookSecret)
		// The in-app inbox (docs/spec 12): the recipient's own rows, the
		// one per-recipient read in the sync world; read state is the
		// user's badge.
		r.Get("/notifications", a.handleListNotifications)
		r.Post("/notifications/{id}/read", a.handleMarkNotificationRead)
		r.Post("/notifications/read_all", a.handleMarkNotificationsRead)
		// The metrics plane (docs/spec ch. 6, §Metrics): product,
		// codebase, swarm, proxy — read-only, any active member.
		r.Get("/workspaces/{id}/metrics", a.handleMetrics)
		// D4: the swarm plane's fleet settings (owner/admin; agents 422).
		r.Post("/workspaces/{id}/swarm/settings", a.handleUpdateSwarmSettings)
		r.Post("/workspaces/{id}/agents", a.handleCreateAgent)
		r.Get("/workspaces/{id}/agents", a.handleListAgents)
		r.Post("/workspaces/{id}/agents/{accountId}", a.handleUpdateAgent)
		r.Post("/workspaces/{id}/agents/{accountId}/token", a.handleIssueAgentToken)
		r.Post("/workspaces/{id}/agents/{accountId}/token/rotate", a.handleRotateAgentToken)
		r.Post("/workspaces/{id}/agents/{accountId}/token/revoke", a.handleRevokeAgentToken)
		r.Delete("/workspaces/{id}/agents/{accountId}", a.handleDeleteAgent)
		r.Get("/search", a.handleSearch)
		// Phase 2: the work API for external agents (claim leases).
		r.Get("/agent/queue", a.handleAgentQueue)
		r.Post("/issues/{id}/claim", a.handleClaimIssue)
		r.Post("/issues/{id}/claim/heartbeat", a.handleClaimHeartbeat)
		r.Post("/issues/{id}/claim/release", a.handleClaimRelease)
		r.Post("/issues/{id}/claim/report", a.handleClaimReport)
		r.Get("/issues/{id}/runs/{runId}/events", a.handleRunEvents)
		// Phase 2: the MCP endpoint (tools over the routes above).
		r.Post("/mcp", a.handleMCP)
	})
}

// sessionMiddleware resolves the session to an authenticated principal or
// rejects the request with 401.
func (a *API) sessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accountID, tok := a.auth.ResolveAccountID(r.Context(), r)
		if accountID == "" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthenticated"}`))
			return
		}
		var p Principal
		p.AccountID = accountID
		p.Token = tok
		if err := a.pool.QueryRow(r.Context(),
			"select email, name, kind, agent_driver from accounts where id = $1 and status = 'active'",
			accountID).Scan(&p.Email, &p.Fullname, &p.Kind, &p.Driver); err != nil {
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
