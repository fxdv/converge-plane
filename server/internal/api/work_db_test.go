// work_db_test.go — the work API and scoped tokens against a real
// database: lease exclusivity under racing claims, the lease lifecycle
// (heartbeat, expiry, supersede, release, sweep), the runtime staying
// out of external agents' work, and a narrowed token end to end through
// the real router. Gated on CONVERGE_TEST_DATABASE_URL.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"converge/internal/auth"
	"converge/internal/broadcast"
	"converge/internal/config"
	"converge/internal/migrate"
)

type workFixture struct {
	t                         *testing.T
	pool                      *pgxpool.Pool
	a                         *API
	ws, owner, t1, t2         string
	todo, doing, review, done string
	todo2                     string
	ext1, ext2, rt1           string
	number                    int
	accounts                  []string
}

func newWorkFixture(t *testing.T) *workFixture {
	t.Helper()
	dbURL := os.Getenv("CONVERGE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("CONVERGE_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Run(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	f := &workFixture{t: t, pool: pool, a: &API{pool: pool, log: discardLogger(), bcast: broadcast.New()}}
	f.ws = testUUID()
	f.exec(`insert into workspaces (id, name, slug) values ($1, 'work api', $2)`, f.ws, "work-"+f.ws[:8])
	account := func(kind, driver string) string {
		id := testUUID()
		f.exec(`insert into accounts (id, email, name, kind, agent_driver) values ($1, $2, $3, $4, $5)`,
			id, id[:8]+"@work.test", kind+"-"+id[:4], kind, driver)
		role := "owner"
		if kind == "agent" {
			role = "agent"
		}
		f.exec(`insert into workspace_members (workspace_id, account_id, role, status, joined_at) values ($1, $2, $3, 'active', now())`,
			f.ws, id, role)
		f.accounts = append(f.accounts, id)
		return id
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := pool.Exec(ctx, `delete from workspaces where id = $1`, f.ws); err != nil {
			t.Logf("cleanup workspace: %v", err)
		}
		if _, err := pool.Exec(ctx, `delete from api_tokens where account_id = any($1::uuid[]) or created_by = any($1::uuid[])`, f.accounts); err != nil {
			t.Logf("cleanup tokens: %v", err)
		}
		if _, err := pool.Exec(ctx, `delete from accounts where id = any($1::uuid[])`, f.accounts); err != nil {
			t.Logf("cleanup accounts: %v", err)
		}
	})
	f.owner = account("human", "runtime")
	f.ext1 = account("agent", "external")
	f.ext2 = account("agent", "external")
	f.rt1 = account("agent", "runtime")
	team := func(ident string) string {
		id := testUUID()
		f.exec(`insert into teams (id, workspace_id, name, identifier) values ($1, $2, $3, $4)`, id, f.ws, ident, ident)
		return id
	}
	f.t1, f.t2 = team("ENG"), team("OPS")
	status := func(teamID, name, category string, pos int) string {
		id := testUUID()
		f.exec(`insert into workflow_statuses (id, team_id, name, category, position) values ($1, $2, $3, $4, $5)`,
			id, teamID, name, category, pos)
		return id
	}
	f.todo = status(f.t1, "Todo", "UNSTARTED", 1)
	f.doing = status(f.t1, "In Progress", "STARTED", 2)
	f.review = status(f.t1, "Human Review", "STARTED", 3)
	f.done = status(f.t1, "Done", "COMPLETED", 4)
	f.todo2 = status(f.t2, "Todo", "UNSTARTED", 1)
	for _, ag := range []string{f.ext1, f.ext2, f.rt1} {
		f.exec(`insert into team_members (team_id, account_id) values ($1, $2)`, f.t1, ag)
	}
	return f
}

func (f *workFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *workFixture) issue(teamID, statusID, assignee string) string {
	f.t.Helper()
	f.number++
	id := testUUID()
	var who any
	if assignee != "" {
		who = assignee
	}
	f.exec(`insert into issues (id, team_id, number, title, status_id, assignee_id, created_by) values ($1, $2, $3, 'work item', $4, $5, $6)`,
		id, teamID, f.number, statusID, who, f.owner)
	return id
}

func (f *workFixture) call(p *Principal, h func(*API, http.ResponseWriter, *http.Request), method, path, body string, params ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	rec := httptest.NewRecorder()
	h(f.a, rec, requestFor(f.t, p, method, "http://x"+path, body, params...))
	return rec
}

type claimResponse struct {
	Claim  claimView `json:"claim"`
	Packet struct {
		Issue    map[string]any  `json:"issue"`
		Team     packetTeam      `json:"team"`
		States   []packetState   `json:"states"`
		Comments []packetComment `json:"comments"`
	} `json:"packet"`
}

func (f *workFixture) claim(agent, issueID string, want int) claimResponse {
	f.t.Helper()
	rec := f.call(externalAgentPrincipal(agent), (*API).handleClaimIssue, "POST", "/api/v1/issues/"+issueID+"/claim", "", "id", issueID)
	checkStatus(f.t, rec, want)
	var out claimResponse
	if want == http.StatusOK {
		decodeBody(f.t, rec, &out)
	}
	return out
}

func (f *workFixture) transition(agent, issueID, claimID string, release bool) *httptest.ResponseRecorder {
	f.t.Helper()
	h, verb := (*API).handleClaimHeartbeat, "heartbeat"
	if release {
		h, verb = (*API).handleClaimRelease, "release"
	}
	return f.call(externalAgentPrincipal(agent), h, "POST", "/api/v1/issues/"+issueID+"/claim/"+verb,
		`{"claimId":"`+claimID+`"}`, "id", issueID)
}

func (f *workFixture) endReason(claimID string) string {
	f.t.Helper()
	var reason *string
	if err := f.pool.QueryRow(context.Background(), `select end_reason from issue_claims where id = $1`, claimID).Scan(&reason); err != nil {
		f.t.Fatalf("read claim: %v", err)
	}
	return strval(reason)
}

func (f *workFixture) claimedBy(issueID string) string {
	f.t.Helper()
	row, err := f.a.issueByID(context.Background(), issueID)
	if err != nil {
		f.t.Fatalf("read issue: %v", err)
	}
	return strval(row.ClaimedByID)
}

func ids(items []map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, it := range items {
		out[it["id"].(string)] = true
	}
	return out
}

func TestWorkAPILeaseLifecycle(t *testing.T) {
	f := newWorkFixture(t)
	pooled := f.issue(f.t1, f.todo, "")
	mine := f.issue(f.t1, f.todo, f.ext1)
	otherTeam := f.issue(f.t2, f.todo2, "")
	started := f.issue(f.t1, f.doing, "")

	t.Run("queue", func(t *testing.T) {
		rec := f.call(externalAgentPrincipal(f.ext1), (*API).handleAgentQueue, "GET", "/api/v1/agent/queue", "")
		checkStatus(t, rec, 200)
		var q struct {
			WorkspaceID string           `json:"workspaceId"`
			Assigned    []map[string]any `json:"assigned"`
			Available   []map[string]any `json:"available"`
		}
		decodeBody(t, rec, &q)
		if q.WorkspaceID != f.ws || !ids(q.Assigned)[mine] || len(q.Assigned) != 1 {
			t.Fatalf("assigned = %v, want exactly %s", ids(q.Assigned), mine)
		}
		avail := ids(q.Available)
		if !avail[pooled] || avail[otherTeam] || avail[started] || avail[mine] {
			t.Fatalf("available = %v: want the unstarted, unassigned issue of its own team only", avail)
		}
	})

	t.Run("doors", func(t *testing.T) {
		checkStatus(t, f.call(humanPrincipal(f.owner), (*API).handleClaimIssue, "POST", "/x", "", "id", pooled), 403)
		checkStatus(t, f.call(agentPrincipal(f.rt1), (*API).handleClaimIssue, "POST", "/x", "", "id", pooled), 403)
		f.claim(f.ext1, otherTeam, http.StatusConflict) // not its team's pool
		f.claim(f.ext1, started, http.StatusConflict)   // pool is unstarted work only
	})

	t.Run("pool claim assigns and publishes", func(t *testing.T) {
		before, _ := f.a.issueByID(context.Background(), pooled)
		c := f.claim(f.ext1, pooled, http.StatusOK)
		if c.Claim.Mode != "pool" || c.Claim.ID == "" || c.Claim.HeartbeatInterval != 100 {
			t.Fatalf("claim = %+v", c.Claim)
		}
		after, _ := f.a.issueByID(context.Background(), pooled)
		if strval(after.AssigneeID) != f.ext1 || strval(after.ClaimedByID) != f.ext1 || after.Version <= before.Version {
			t.Fatalf("issue after claim: assignee %v claimedBy %v version %d->%d",
				strval(after.AssigneeID), strval(after.ClaimedByID), before.Version, after.Version)
		}
		if c.Packet.Team.ID != f.t1 || len(c.Packet.States) != 4 || c.Packet.Issue["claimedById"] != f.ext1 {
			t.Fatalf("packet = team %+v states %d claimedById %v", c.Packet.Team, len(c.Packet.States), c.Packet.Issue["claimedById"])
		}
		var data string
		if err := f.pool.QueryRow(context.Background(), `
			select data::text from sync_outbox
			where workspace_id = $1 and model_name = 'Issue' and model_id = $2
			order by sequence_id desc limit 1`, f.ws, pooled).Scan(&data); err != nil || !strings.Contains(data, `"claimedById": "`+f.ext1+`"`) && !strings.Contains(data, `"claimedById":"`+f.ext1+`"`) {
			t.Fatalf("the claim must reach the sync feed: %s (%v)", data, err)
		}
		f.claim(f.ext2, pooled, http.StatusConflict) // assigned to ext1 now
	})

	t.Run("heartbeat, fencing, release", func(t *testing.T) {
		c := f.claim(f.ext1, mine, http.StatusOK)
		if c.Claim.Mode != "assigned" {
			t.Fatalf("mode = %q, want assigned", c.Claim.Mode)
		}
		rec := f.transition(f.ext1, mine, c.Claim.ID, false)
		checkStatus(t, rec, 200)
		checkStatus(t, f.transition(f.ext2, mine, c.Claim.ID, false), 404)
		checkStatus(t, f.transition(f.ext1, mine, testUUID(), false), 404)

		// A restarted process re-claims: the old lease is fenced.
		c2 := f.claim(f.ext1, mine, http.StatusOK)
		if f.endReason(c.Claim.ID) != claimEndSuperseded {
			t.Fatalf("old claim ended %q, want superseded", f.endReason(c.Claim.ID))
		}
		checkStatus(t, f.transition(f.ext1, mine, c.Claim.ID, false), 409)

		checkStatus(t, f.transition(f.ext1, mine, c2.Claim.ID, true), 200)
		if f.claimedBy(mine) != "" || f.endReason(c2.Claim.ID) != claimEndReleased {
			t.Fatalf("release left claimedBy %q reason %q", f.claimedBy(mine), f.endReason(c2.Claim.ID))
		}
		rec = f.transition(f.ext1, mine, c2.Claim.ID, true)
		checkStatus(t, rec, 200)
		var again map[string]any
		decodeBody(t, rec, &again)
		if again["released"] != false || again["endReason"] != claimEndReleased {
			t.Fatalf("second release = %v, want the idempotent answer", again)
		}
	})

	t.Run("expiry ends the lease at the next heartbeat", func(t *testing.T) {
		c := f.claim(f.ext1, mine, http.StatusOK)
		f.exec(`update issue_claims set expires_at = now() - interval '1 second' where id = $1`, c.Claim.ID)
		checkStatus(t, f.transition(f.ext1, mine, c.Claim.ID, false), 409)
		if f.endReason(c.Claim.ID) != claimEndExpired || f.claimedBy(mine) != "" {
			t.Fatalf("expired claim: reason %q claimedBy %q", f.endReason(c.Claim.ID), f.claimedBy(mine))
		}
	})

	t.Run("human takeover: reassignment fences, the sweep ends it", func(t *testing.T) {
		c := f.claim(f.ext1, mine, http.StatusOK)
		f.exec(`update issues set assignee_id = $2 where id = $1`, mine, f.ext2)
		// The old lease is still open until swept: ext2 is refused.
		rec := f.claim(f.ext2, mine, http.StatusConflict)
		_ = rec
		n, err := f.a.SweepClaims(context.Background())
		if err != nil || n < 1 {
			t.Fatalf("sweep = %d, %v", n, err)
		}
		if f.endReason(c.Claim.ID) != claimEndReassigned {
			t.Fatalf("reason = %q, want reassigned", f.endReason(c.Claim.ID))
		}
		c2 := f.claim(f.ext2, mine, http.StatusOK)
		f.exec(`update issues set status_id = $2 where id = $1`, mine, f.done)
		if _, err := f.a.SweepClaims(context.Background()); err != nil {
			t.Fatal(err)
		}
		if f.endReason(c2.Claim.ID) != claimEndClosed {
			t.Fatalf("reason = %q, want closed", f.endReason(c2.Claim.ID))
		}
		f.claim(f.ext2, mine, http.StatusConflict) // closed work is not claimable
	})

	t.Run("parked work is not claimable", func(t *testing.T) {
		parked := f.issue(f.t1, f.review, f.ext1)
		f.claim(f.ext1, parked, http.StatusConflict)
		paused := f.issue(f.t1, f.todo, f.ext1)
		f.exec(`update issues set agent_paused = true where id = $1`, paused)
		f.claim(f.ext1, paused, http.StatusConflict)
	})

	t.Run("handing the agent to the runtime revokes its claims", func(t *testing.T) {
		work := f.issue(f.t1, f.todo, f.ext2)
		c := f.claim(f.ext2, work, http.StatusOK)
		rec := f.call(humanPrincipal(f.owner), (*API).handleUpdateAgent, "POST", "/x", `{"driver":"runtime"}`,
			"id", f.ws, "accountId", f.ext2)
		checkStatus(t, rec, 200)
		if f.endReason(c.Claim.ID) != claimEndRevoked || f.claimedBy(work) != "" {
			t.Fatalf("driver switch left reason %q claimedBy %q", f.endReason(c.Claim.ID), f.claimedBy(work))
		}
		f.exec(`update accounts set agent_driver = 'external' where id = $1`, f.ext2)
	})

	t.Run("leaving the team hides its work and ends its claims", func(t *testing.T) {
		work := f.issue(f.t1, f.todo, f.ext2)
		c := f.claim(f.ext2, work, http.StatusOK)
		f.exec(`delete from team_members where team_id = $1 and account_id = $2`, f.t1, f.ext2)
		t.Cleanup(func() {
			f.exec(`insert into team_members (team_id, account_id) values ($1, $2) on conflict do nothing`, f.t1, f.ext2)
		})
		rec := f.call(externalAgentPrincipal(f.ext2), (*API).handleAgentQueue, "GET", "/api/v1/agent/queue", "")
		checkStatus(t, rec, 200)
		var q struct {
			Assigned  []map[string]any `json:"assigned"`
			Available []map[string]any `json:"available"`
		}
		decodeBody(t, rec, &q)
		if len(q.Assigned) != 0 || len(q.Available) != 0 {
			t.Fatalf("queue after leaving the team: assigned %v available %v", ids(q.Assigned), ids(q.Available))
		}
		if _, err := f.a.SweepClaims(context.Background()); err != nil {
			t.Fatal(err)
		}
		if f.endReason(c.Claim.ID) != claimEndRevoked || f.claimedBy(work) != "" {
			t.Fatalf("team removal left reason %q claimedBy %q", f.endReason(c.Claim.ID), f.claimedBy(work))
		}
	})
}

// Racing claims on one pool issue: exactly one wins.
func TestWorkAPIClaimRace(t *testing.T) {
	f := newWorkFixture(t)
	for round := range 5 {
		issueID := f.issue(f.t1, f.todo, "")
		var wg sync.WaitGroup
		codes := make([]int, 2)
		for i, agent := range []string{f.ext1, f.ext2} {
			wg.Go(func() {
				rec := httptest.NewRecorder()
				req := requestFor(t, externalAgentPrincipal(agent), "POST", "http://x/claim", "", "id", issueID)
				f.a.handleClaimIssue(rec, req)
				codes[i] = rec.Code
			})
		}
		wg.Wait()
		wins := 0
		for _, c := range codes {
			if c == http.StatusOK {
				wins++
			} else if c != http.StatusConflict {
				t.Fatalf("round %d: unexpected status %d", round, c)
			}
		}
		var open int
		if err := f.pool.QueryRow(context.Background(),
			`select count(*) from issue_claims where issue_id = $1 and ended_at is null`, issueID).Scan(&open); err != nil {
			t.Fatal(err)
		}
		if wins != 1 || open != 1 {
			t.Fatalf("round %d: %d winners, %d open claims (codes %v)", round, wins, open, codes)
		}
	}
}

// The runtime never drives an external agent: its work does not spawn a
// worker, and a workspace with only external agents is not scanned.
func TestRuntimeSkipsExternalAgents(t *testing.T) {
	f := newWorkFixture(t)
	f.issue(f.t1, f.todo, f.ext1)
	f.issue(f.t1, f.todo, f.rt1)
	rt := newAgentRuntime(f.a)
	fleet, err := rt.fleetSnapshot(context.Background(), f.ws)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, ag := range fleet {
		counts[ag.accountID] = ag.openCount
	}
	if counts[f.rt1] != 1 || counts[f.ext1] != 0 {
		t.Fatalf("fleet counts = %v: the runtime agent has work, the external one none", counts)
	}
	if !rt.agentActive(context.Background(), f.ws, f.rt1) || rt.agentActive(context.Background(), f.ws, f.ext1) {
		t.Fatal("agentActive must admit runtime agents only")
	}
	f.exec(`update workspace_members set status = 'suspended' where workspace_id = $1 and account_id = $2`, f.ws, f.rt1)
	wss, err := rt.workspacesWithAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range wss {
		if ws == f.ws {
			t.Fatal("a workspace with only external agents must not be scanned")
		}
	}
}

// A narrowed token, end to end: issued through the admin API, resolved by
// the real session middleware, held to its scopes and team grants.
func TestScopedTokenEndToEnd(t *testing.T) {
	f := newWorkFixture(t)
	authSvc := auth.NewService(f.pool, config.Config{SessionSecret: "test-secret", AccessTokenTTL: time.Hour}, slog.New(slog.DiscardHandler), nil)
	f.a.auth = authSvc
	f.a.limiter = newAccountRateLimiter(0, 0)
	r := chi.NewRouter()
	f.a.Mount(r)

	body := `{"name":"coder-` + f.ws[:6] + `","teamIds":["` + f.t1 + `"],"driver":"external",` +
		`"token":{"scopes":["work","issues:write","comments:write"],"teamIds":["` + f.t1 + `"],"ttlHours":24}}`
	rec := f.call(humanPrincipal(f.owner), (*API).handleCreateAgent, "POST", "/x", body, "id", f.ws)
	checkStatus(t, rec, 201)
	var created agentResponse
	decodeBody(t, rec, &created)
	f.accounts = append(f.accounts, created.ID)
	if created.Driver != "external" || len(created.TokenScopes) != 3 || len(created.TokenTeamIDs) != 1 ||
		time.Until(created.TokenExpiresAt) > 25*time.Hour {
		t.Fatalf("created = %+v", created)
	}

	mine := f.issue(f.t1, f.todo, created.ID)
	elsewhere := f.issue(f.t2, f.todo2, created.ID)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://x"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+created.Token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	cases := []struct {
		method, path, body string
		code               int
	}{
		{"GET", "/api/v1/users", "", 200},
		{"GET", "/api/v1/agent/queue", "", 200},
		{"POST", "/api/v1/issues/" + mine + "/claim", "", 200},
		{"POST", "/api/v1/issues/" + elsewhere + "/claim", "", 404},            // outside the team grant
		{"GET", "/api/v1/sync_actions/bootstrap?workspaceId=" + f.ws, "", 403}, // no sync:read
		{"DELETE", "/api/v1/issues/" + mine, "", 403},                          // hard delete is off the surface
		{"GET", "/api/v1/workspaces/" + f.ws + "/agents", "", 403},
		{"GET", "/api/v1/issue_comments/" + testUUID(), "", 403}, // no comments:read
	}
	for _, c := range cases {
		if got := do(c.method, c.path, c.body); got.Code != c.code {
			t.Errorf("%s %s = %d (%s), want %d", c.method, c.path, got.Code, got.Body.String(), c.code)
		}
	}

	// The queue under a team grant lists only granted teams' work.
	rec = do("GET", "/api/v1/agent/queue", "")
	var q struct {
		Assigned []map[string]any `json:"assigned"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &q)
	if a := ids(q.Assigned); a[elsewhere] || !a[mine] {
		t.Fatalf("queue under a team grant = %v", a)
	}

	// The admin list shows the grant, never the material.
	rec = f.call(humanPrincipal(f.owner), (*API).handleListAgents, "GET", "/x", "", "id", f.ws)
	checkStatus(t, rec, 200)
	if strings.Contains(rec.Body.String(), created.Token) || !strings.Contains(rec.Body.String(), `"scopes":["work","issues:write","comments:write"]`) {
		t.Fatalf("agent list = %s", rec.Body.String())
	}

	f.exec(`update api_tokens set revoked_at = now() where account_id = $1`, created.ID)
	if got := do("GET", "/api/v1/agent/queue", ""); got.Code != 401 {
		t.Fatalf("revoked token = %d, want 401", got.Code)
	}
}
