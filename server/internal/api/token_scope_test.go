package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"converge/internal/auth"
	"converge/internal/config"
)

// Every /api/v1 route must have a deliberate policy entry, and every
// entry must name a real route: a new endpoint cannot open to narrowed
// tokens by omission, and a renamed one cannot leave a stale grant.
func TestTokenRoutePolicyCoversEveryRoute(t *testing.T) {
	a := &API{pool: &fakePool{}, log: discardLogger(), cfg: config.Config{}, limiter: newAccountRateLimiter(0, 0)}
	r := chi.NewRouter()
	a.Mount(r)
	mounted := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if rest, ok := strings.CutPrefix(route, "/api/v1/"); ok {
			mounted[method+" /"+rest] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mounted) == 0 {
		t.Fatal("walked no /api/v1 routes")
	}
	for key := range mounted {
		if _, ok := tokenRoutePolicy[key]; !ok {
			t.Errorf("route %q has no token policy entry", key)
		}
	}
	for key := range tokenRoutePolicy {
		if !mounted[key] {
			t.Errorf("token policy names %q, which is not mounted", key)
		}
	}
}

// guardRouter mounts a handful of real policy routes behind the guard
// with a fixed principal, the way the /api/v1 router does.
func guardRouter(p *Principal) http.Handler {
	a := &API{log: discardLogger()}
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(WithPrincipal(req.Context(), p)))
			})
		}, a.tokenScopeGuard(r))
		r.Get("/users", ok)
		r.Post("/issues/{id}", ok)
		r.Delete("/issues/{id}", ok)
		r.Get("/sync_actions/bootstrap", ok)
		r.Get("/agent/queue", ok)
		r.Post("/issues/{id}/claim", ok)
		r.Get("/unlisted", ok)
	})
	return r
}

func TestTokenScopeGuard(t *testing.T) {
	agent := func(tok *auth.APIToken) *Principal {
		p := externalAgentPrincipal("ag1")
		p.Token = tok
		return p
	}
	session := humanPrincipal("u1")
	legacy := agent(&auth.APIToken{ID: "k0", AccountID: "ag1"})
	worker := agent(&auth.APIToken{ID: "k1", AccountID: "ag1", Scopes: []string{auth.ScopeWork, auth.ScopeIssuesWrite}})
	reader := agent(&auth.APIToken{ID: "k2", AccountID: "ag1", Scopes: []string{auth.ScopeSyncRead}})
	teamOnly := agent(&auth.APIToken{ID: "k3", AccountID: "ag1", TeamIDs: []string{"t1"}})

	cases := []struct {
		name      string
		p         *Principal
		method    string
		path      string
		code      int
		wantScope string
	}{
		{"session reaches everything", session, "DELETE", "/api/v1/issues/x", 204, ""},
		{"session reaches unlisted", session, "GET", "/api/v1/unlisted", 204, ""},
		{"pre-scope token keeps full authority", legacy, "DELETE", "/api/v1/issues/x", 204, ""},
		{"scoped: identity", worker, "GET", "/api/v1/users", 204, ""},
		{"scoped: work", worker, "GET", "/api/v1/agent/queue", 204, ""},
		{"scoped: claim", worker, "POST", "/api/v1/issues/x/claim", 204, ""},
		{"scoped: write", worker, "POST", "/api/v1/issues/x", 204, ""},
		{"scoped: missing scope", worker, "GET", "/api/v1/sync_actions/bootstrap", 403, auth.ScopeSyncRead},
		{"scoped: hard delete is outside the surface", worker, "DELETE", "/api/v1/issues/x", 403, ""},
		{"scoped: unlisted route is refused", worker, "GET", "/api/v1/unlisted", 403, ""},
		{"scoped: unrouted path still 404s", worker, "GET", "/api/v1/nope", 404, ""},
		{"scoped: wrong method still 405s", worker, "PATCH", "/api/v1/issues/x", 405, ""},
		{"sync reader: feed", reader, "GET", "/api/v1/sync_actions/bootstrap", 204, ""},
		{"sync reader: no writes", reader, "POST", "/api/v1/issues/x", 403, auth.ScopeIssuesWrite},
		{"team-limited: team-scoped write", teamOnly, "POST", "/api/v1/issues/x", 204, ""},
		{"team-limited: workspace-wide read refused", teamOnly, "GET", "/api/v1/sync_actions/bootstrap", 403, ""},
		{"team-limited: outside the surface", teamOnly, "DELETE", "/api/v1/issues/x", 403, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			guardRouter(c.p).ServeHTTP(rec, httptest.NewRequest(c.method, "http://x"+c.path, nil))
			if rec.Code != c.code {
				t.Fatalf("%s %s = %d, want %d (%s)", c.method, c.path, rec.Code, c.code, rec.Body.String())
			}
			if c.wantScope != "" {
				var body map[string]string
				_ = json.Unmarshal(rec.Body.Bytes(), &body)
				if body["requiredScope"] != c.wantScope {
					t.Fatalf("requiredScope = %q, want %q", body["requiredScope"], c.wantScope)
				}
			}
		})
	}
}

// Team grants bind where team access is decided: a team outside the
// grant is invisible without a query, and a granted team still needs a
// live membership.
func TestTeamWorkspaceHonorsTokenGrants(t *testing.T) {
	limited := externalAgentPrincipal("ag1")
	limited.Token = &auth.APIToken{ID: "k", AccountID: "ag1", Scopes: []string{auth.ScopeWork}, TeamIDs: []string{"t1"}}

	run := func(t *testing.T, teamID string, member bool) bool {
		pool := (&fakePool{rules: []fakeRule{
			rtTeamWS(rtWS),
			rtRole("agent"),
			{frag: "from team_members where team_id", rowVals: []any{member}},
		}}).bind(t)
		a := apiForTests(t, pool)
		_, ok := a.teamWorkspace(context.Background(), limited, teamID)
		return ok
	}
	if !run(t, "t1", true) {
		t.Fatal("granted team with membership must be reachable")
	}
	if run(t, "t1", false) {
		t.Fatal("a grant must not outlive the team membership")
	}
	if run(t, "t2", true) {
		t.Fatal("a team outside the grant must be unreachable")
	}
	unrestricted := externalAgentPrincipal("ag1")
	pool := (&fakePool{rules: []fakeRule{rtTeamWS(rtWS), rtRole("agent")}}).bind(t)
	if _, ok := apiForTests(t, pool).teamWorkspace(context.Background(), unrestricted, "t2"); !ok {
		t.Fatal("an unrestricted caller keeps workspace-level team access")
	}
}

// The token spec narrows or it is refused: unknown scopes, empty lists,
// teams the agent does not belong to, sync:read with team grants, and an
// out-of-range lifetime.
func TestTokenSpecValidate(t *testing.T) {
	const t1 = "22222222-2222-2222-2222-222222222222"
	const t2 = "22222222-2222-2222-2222-222222222223"
	hours := func(h int) *int { return &h }
	cases := []struct {
		name    string
		spec    tokenSpec
		problem string
	}{
		{"scopes required", tokenSpec{}, "scopes must name at least one scope"},
		{"scoped", tokenSpec{Scopes: []string{"work", "work", "issues:write"}}, ""},
		{"team-limited", tokenSpec{Scopes: []string{"work"}, TeamIDs: []string{t1}}, ""},
		{"empty scopes", tokenSpec{Scopes: []string{}}, "scopes must name at least one scope"},
		{"unknown scope", tokenSpec{Scopes: []string{"admin"}}, "unknown scope: admin"},
		{"empty teams", tokenSpec{Scopes: []string{"work"}, TeamIDs: []string{}}, "teamIds must name at least one team (omit it for every team)"},
		{"foreign team", tokenSpec{Scopes: []string{"work"}, TeamIDs: []string{t2}}, "token teamIds must be teams the agent belongs to"},
		{"sync with teams", tokenSpec{Scopes: []string{"sync:read"}, TeamIDs: []string{t1}}, "sync:read covers the whole workspace and cannot be combined with teamIds"},
		{"ttl too short", tokenSpec{Scopes: []string{"work"}, TTLHours: hours(0)}, "ttlHours must be between 1 and 8760"},
		{"ttl too long", tokenSpec{Scopes: []string{"work"}, TTLHours: hours(8761)}, "ttlHours must be between 1 and 8760"},
		{"ttl ok", tokenSpec{Scopes: []string{"work"}, TTLHours: hours(24)}, ""},
		{"ttl at the cap", tokenSpec{Scopes: []string{"work"}, TTLHours: hours(8760)}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, problem := c.spec.validate([]string{t1})
			if problem != c.problem {
				t.Fatalf("problem = %q, want %q", problem, c.problem)
			}
			if c.name == "scoped" && len(out.Scopes) != 2 {
				t.Fatalf("scopes not deduplicated: %v", out.Scopes)
			}
			if c.name == "scopes required" && out.Scopes != nil {
				t.Fatalf("a refused spec must not invent scopes: %+v", out)
			}
		})
	}
}
