// token_scope.go — what a narrowed API token may call (docs/spec 07).
// spec cs:agents:tokens
//
// A token with scopes or team grants is held to tokenRoutePolicy before
// any handler runs. The table names every /api/v1 route; a route missing
// from it is refused, so a new endpoint is closed to narrowed tokens
// until someone decides otherwise (TestTokenRoutePolicyCoversEveryRoute
// fails until the table names it). Web sessions and pre-scope tokens
// carry full authority and skip the table.
//
// Team grants are enforced where team access is decided (teamWorkspace),
// so every issue and comment path inherits them. Routes whose data spans
// the workspace (the sync feed, search, team metadata) cannot be
// filtered per team and are refused to team-limited tokens outright.
package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"converge/internal/auth"
)

// scopeIdentity marks a route any live token may call.
const scopeIdentity = "identity"

// routePolicy is one route's rule for narrowed tokens. scope "" means no
// narrowed token may call the route; wide means the route serves
// workspace-wide data and is refused to team-limited tokens.
type routePolicy struct {
	scope string
	wide  bool
}

var tokenRoutePolicy = map[string]routePolicy{
	"GET /users": {scope: scopeIdentity},

	"GET /sync_actions/bootstrap": {scope: auth.ScopeSyncRead, wide: true},
	"GET /sync_actions/delta":     {scope: auth.ScopeSyncRead, wide: true},
	"GET /sync_actions/stream":    {scope: auth.ScopeSyncRead, wide: true},

	"POST /issues":                {scope: auth.ScopeIssuesWrite},
	"POST /issues/{id}":           {scope: auth.ScopeIssuesWrite},
	"POST /issues/{id}/move":      {scope: auth.ScopeIssuesWrite},
	"POST /issues/{id}/handoff":   {scope: auth.ScopeIssuesWrite},
	"POST /issues/{id}/subscribe": {scope: auth.ScopeIssuesWrite},
	"GET /issues/{id}/relations":  {scope: auth.ScopeIssuesRead},
	"DELETE /issue_relation/{id}": {scope: auth.ScopeIssuesWrite, wide: true},

	"GET /issues/{id}/runs/{runId}/events": {scope: auth.ScopeIssuesRead},

	"POST /issue_comments":             {scope: auth.ScopeCommentsWrite},
	"POST /issue_comments/{id}":        {scope: auth.ScopeCommentsWrite},
	"DELETE /issue_comments/{id}":      {scope: auth.ScopeCommentsWrite},
	"GET /issue_comments/{id}":         {scope: auth.ScopeCommentsRead},
	"GET /issue_comments/{id}/replies": {scope: auth.ScopeCommentsRead},

	"GET /teams":              {scope: auth.ScopeIssuesRead, wide: true},
	"GET /teams/{id}":         {scope: auth.ScopeIssuesRead, wide: true},
	"GET /teams/name/{slug}":  {scope: auth.ScopeIssuesRead, wide: true},
	"GET /{teamId}/workflows": {scope: auth.ScopeIssuesRead, wide: true},
	"GET /labels":             {scope: auth.ScopeIssuesRead, wide: true},
	"GET /projects":           {scope: auth.ScopeIssuesRead, wide: true},
	"GET /search":             {scope: auth.ScopeIssuesRead, wide: true},

	"GET /agent/queue":                  {scope: auth.ScopeWork},
	"POST /issues/{id}/claim":           {scope: auth.ScopeWork},
	"POST /issues/{id}/claim/heartbeat": {scope: auth.ScopeWork},
	"POST /issues/{id}/claim/release":   {scope: auth.ScopeWork},
	"POST /issues/{id}/claim/report":    {scope: auth.ScopeWork},

	// Outside the token surface: hard delete (spec 07 keeps it out of
	// issues:write), profile and workspace administration, taxonomy and
	// view writes, the member-facing planes, and agent administration.
	"DELETE /issues/{id}":                                   {},
	"PUT /users":                                            {},
	"POST /workspaces/onboarding":                           {},
	"POST /teams":                                           {},
	"POST /teams/{id}":                                      {},
	"DELETE /teams/{id}":                                    {},
	"POST /teams/{id}/add-member":                           {},
	"POST /teams/{id}/remove-member":                        {},
	"POST /teams/{id}/preferences":                          {},
	"POST /{teamId}/workflows":                              {},
	"POST /{teamId}/workflows/{workflowId}":                 {},
	"POST /labels":                                          {},
	"POST /labels/{id}":                                     {},
	"DELETE /labels/{id}":                                   {},
	"POST /projects":                                        {},
	"POST /projects/{id}":                                   {},
	"POST /projects/{id}/delete":                            {},
	"POST /views":                                           {},
	"POST /views/{id}":                                      {},
	"DELETE /views/{id}":                                    {},
	"GET /views/{id}":                                       {},
	"POST /workspaces":                                      {},
	"POST /workspaces/preferences":                          {},
	"POST /workspaces/invite_users":                         {},
	"POST /workspaces/invite_action":                        {},
	"POST /workspaces/suspend":                              {},
	"GET /workspaces/{id}/swarm":                            {},
	"GET /notifications":                                    {},
	"POST /notifications/{id}/read":                         {},
	"POST /notifications/read_all":                          {},
	"GET /workspaces/{id}/metrics":                          {},
	"POST /workspaces/{id}/swarm/settings":                  {},
	"POST /workspaces/{id}/agents":                          {},
	"GET /workspaces/{id}/agents":                           {},
	"POST /workspaces/{id}/agents/{accountId}":              {},
	"POST /workspaces/{id}/agents/{accountId}/token":        {},
	"POST /workspaces/{id}/agents/{accountId}/token/revoke": {},
	"DELETE /workspaces/{id}/agents/{accountId}":            {},
}

// tokenNarrowed reports whether the caller's token is held to the route
// policy (it has scopes or team grants).
func tokenNarrowed(p *Principal) bool {
	return p != nil && (p.Token.Scoped() || p.Token.TeamLimited())
}

// tokenScopeGuard refuses a narrowed token any route its policy does not
// allow. routes is the router the guard is installed on: the matched
// pattern is looked up there, relative to its mount point.
func (a *API) tokenScopeGuard(routes chi.Routes) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := PrincipalFromContext(r.Context())
			if !tokenNarrowed(p) {
				next.ServeHTTP(w, r)
				return
			}
			path := r.URL.Path
			if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePath != "" {
				path = rctx.RoutePath
			}
			pattern := routes.Find(chi.NewRouteContext(), r.Method, path)
			pol, known := tokenRoutePolicy[r.Method+" "+pattern]
			switch {
			case pattern == "":
				// Unrouted: let the router answer 404/405 as for anyone.
				next.ServeHTTP(w, r)
				return
			case !known || pol.scope == "":
				writeError(w, http.StatusForbidden, "this token may not call this endpoint")
				return
			case pol.scope != scopeIdentity && !p.Token.HasScope(pol.scope):
				writeJSON(w, http.StatusForbidden, map[string]string{
					"error":         "this token's scopes do not allow this request",
					"requiredScope": pol.scope,
				})
				return
			case pol.wide && p.Token.TeamLimited():
				writeError(w, http.StatusForbidden, "this token is limited to specific teams; workspace-wide reads need a token without team grants")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// tokenTeamAllowed reports whether a team-limited token may reach the
// team: the grant must name it and the agent must still belong to it
// (a grant never outlives the membership it narrowed).
func (a *API) tokenTeamAllowed(ctx context.Context, p *Principal, teamID string) bool {
	if !p.Token.TeamLimited() {
		return true
	}
	if !p.Token.TeamGranted(teamID) {
		return false
	}
	var member bool
	err := a.pool.QueryRow(ctx,
		"select exists(select 1 from team_members where team_id = $1 and account_id = $2)",
		teamID, p.AccountID).Scan(&member)
	return err == nil && member
}

// tokenReachesIssue reports whether the caller may reference the issue
// (a parent or a related issue) under its team grants. Unrestricted
// callers keep the workspace boundary the handlers already check.
func (a *API) tokenReachesIssue(ctx context.Context, p *Principal, issueID string) bool {
	if !p.Token.TeamLimited() {
		return true
	}
	_, _, ok := a.issueAccess(ctx, p, issueID)
	return ok
}
