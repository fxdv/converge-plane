// route_table_test.go — the v1 route table: the map the API must match.
//
// Mount (tenant.go) registers the routes; this file is their registry and
// contract. chi does not expose the routes it has registered, so the
// table is the machine-verifiable map, verified in two phases:
//
//  1. The router walk drives the real chi router (Mount, as in
//     production) with NO auth material and expects the session guard's
//     401 on every route. A route added to Mount but missing here trips
//     the count pin; a route in the table missing from Mount 404s the
//     walk. The walk runs a nil auth service on purpose: a tokenless
//     probe (no bearer, no session cookie) never dereferences the
//     service — ResolveAccountID reads request material only and returns
//     "" — so the 401 path is exactly the production path. If a future
//     middleware starts dereferencing the service for tokenless
//     requests, this test fails on purpose.
//
//  2. The seam phase drives each handler directly over the in-memory
//     pool fake (the seam_helpers family) and pins the route's primary
//     contract: the door (400/404/422 with the exact client-visible
//     message), or the response shape, or the SQL the write must issue.
//     The table is a static package-level var; each case binds its fake
//     to the subtest's t (bind) before the call.
//
// The 19 handlers with deeper seam suites (wave 2) appear here with
// their primary contract only — the regression net. The 32 that had no
// handler-level coverage get their first here.
//
// Fail-loud by design: a handler is a pure function of (principal,
// request, pool), so the scripted rules are its entire world. Every
// Query/QueryRow it issues must match a rule (a miss fails the test,
// naming the SQL); every Exec is recorded for assertion. A query added
// to a handler surfaces here as a test error, not a silent regression.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"converge/internal/config"
)

// The table's canonical identities: canonical 8-4-4-4-12 UUIDs so the
// isUUID gates pass.
const (
	rtWS       = "11111111-1111-1111-1111-111111111111"
	rtTeam     = "22222222-2222-2222-2222-222222222222"
	rtTeam2    = "22222222-2222-2222-2222-222222222223"
	rtIssue    = "33333333-3333-3333-3333-333333333333"
	rtIssue2   = "33333333-3333-3333-3333-333333333334"
	rtComment  = "44444444-4444-4444-4444-444444444444"
	rtRelation = "55555555-5555-5555-5555-555555555555"
	rtLabel    = "66666666-6666-6666-6666-666666666666"
	rtProject  = "77777777-7777-7777-7777-777777777777"
	rtView     = "88888888-8888-8888-8888-888888888888"
	rtState    = "aaaaaaaa-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	rtToken    = "aaaaaaaa-bbbb-bbbb-bbbb-bbbbbbbbbbb1"
	rtAgent    = "99999999-9999-9999-9999-999999999999"
	rtUser     = "00000000-0000-0000-0000-000000000000"
	rtAdmin    = "00000000-0000-0000-0000-000000000001"
	rtMemberID = "00000000-0000-0000-0000-000000000002"
)

// routeHandler binds a handler method to the API instance at call time
// (a method expression: the table stays receiver-free data).
type routeHandler func(*API, http.ResponseWriter, *http.Request)

// routeCase is one row of the map: the registered route, the concrete
// request both phases send, and the scripted world of its handler.
type routeCase struct {
	route     string // the Mount pattern, verbatim (the sync contract)
	method    string
	path      string // concrete path + query (both phases)
	handler   routeHandler
	principal *Principal // nil = the human principal (rtUser)
	body      string
	urlParams []string // chi params for the seam phase
	pool      *fakePool
	code      int
	err       string // exact client-visible error ("" = assert the shape)
	check     func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool)
}

// ---- shared rule builders (the API's recurring reads) -------------------

// rtRole scripts workspaceRole (the single most-asked question in the
// API: the principal's active role in a workspace).
func rtRole(role string) fakeRule {
	return fakeRule{frag: "select role from workspace_members", rowVals: []any{role}}
}

// rtTeamWS scripts teamWorkspace (the team's tenant).
func rtTeamWS(ws string) fakeRule {
	return fakeRule{frag: "join workspaces w on w.id = t.workspace_id", rowVals: []any{ws}}
}

// rtTeamRow scripts teamByID / teamByIDTx (the eight team columns).
func rtTeamRow(id, name, ident, status, ws, prefs string) fakeRule {
	now := time.Now()
	return fakeRule{frag: "from teams where id = $1",
		rowVals: []any{id, name, ident, status, ws, now, now, []byte(prefs)}}
}

// rtMemberRow scripts memberByAccount / memberByAccountTx (the eight
// membership columns, in memberColumns order).
func rtMemberRow(id, role, status, account, ws string, teams ...string) fakeRule {
	now := time.Now()
	if teams == nil {
		teams = []string{}
	}
	return fakeRule{frag: "from workspace_members wm where wm.workspace_id = $1 and wm.account_id = $2",
		rowVals: []any{id, role, status, account, ws, now, now, teams}}
}

// rtIssueRow scripts issueByID / issueByIDTx (the nineteen sync columns,
// in issueColumns order).
func rtIssueRow(id, team, statusID string, number int, paused bool, assignee, projectIDs any) fakeRule {
	now := time.Now()
	return fakeRule{frag: "i.id, i.team_id, i.number", rowVals: []any{
		id, team, number, 2, 3, "Test issue", "{}", "active",
		now, now, rtUser, assignee, nil, statusID, paused,
		projectIDs, []string{}, []string{}, json.RawMessage("[]"),
	}}
}

// rtCommentRow scripts commentAccess / commentByID (the seven comment
// columns, in the handler's select order).
func rtCommentRow(id, body, author, issue string) fakeRule {
	now := time.Now()
	return fakeRule{frag: "from comments cm where cm.id = $1",
		rowVals: []any{id, body, author, issue, nil, now, now}}
}

// rtLabelRow scripts labelByID / labelByIDTx (the seven label columns).
func rtLabelRow(id, name, color, ws string) fakeRule {
	now := time.Now()
	return fakeRule{frag: "from labels where id = $1",
		rowVals: []any{id, name, color, nil, ws, now, now}}
}

// rtProjectRow scripts projectByIDTx (the eleven project columns).
func rtProjectRow(id, name, color, ws string) fakeRule {
	now := time.Now()
	return fakeRule{frag: "from projects where id = $1",
		rowVals: []any{id, name, color, ws, nil, nil, nil, []string{}, nil, now, now}}
}

// rtViewRow scripts viewByID / viewByIDTx (the thirteen view columns).
func rtViewRow(id, name, ws, createdBy string, bookmarked bool) fakeRule {
	now := time.Now()
	return fakeRule{frag: "from saved_views where id = $1",
		rowVals: []any{id, name, "active", nil, []byte("{}"), bookmarked, 1,
			"workspace", createdBy, ws, nil, now, now}}
}

// rtSeq scripts claimSequenceTx (the outbox sequence the mutation claims).
func rtSeq(n int64) fakeRule {
	return fakeRule{frag: "insert into sync_sequences", rowVals: []any{n}}
}

// rtHistoryRules scripts writeHistoryTx: the insert (returning id) and
// the created_at read-back (created_at doubles as updated_at).
func rtHistoryRules() []fakeRule {
	return []fakeRule{
		{frag: "insert into issue_history", rowVals: []any{"h1"}},
		{frag: "select created_at from issue_history", rowVals: []any{time.Now()}},
	}
}

// rtTeamListRow is one team for the list handlers/collectors.
func rtTeamListRow(id, name, ident, ws string) []any {
	now := time.Now()
	return []any{id, name, ident, "active", ws, now, now, []byte("{}")}
}

// rtRelationRow is the one relation row the table shares (the nine
// issue_relations columns, in the handler's select order).
func rtRelationRow() []any {
	now := time.Now()
	return []any{rtRelation, rtWS, rtTeam, rtIssue, rtIssue2, "BLOCKS", rtUser, now, now}
}

// bind attaches the per-run testing.T to a static pool (and its
// transactions) when a case runs: the table is a package-level var, so
// the fakes build without a test context and receive the loud-failure
// hook (matchRule fails through it) at call time.
func (p *fakePool) bind(t *testing.T) *fakePool {
	t.Helper()
	p.t = t
	for _, tx := range p.txs {
		tx.t = t
	}
	return p
}

// ---- the table (Mount order) --------------------------------------------

var rtRouteCases = []routeCase{
	// GET /users — a brand-new account: the non-null wire contract (the
	// client calls .find/.length on both collections; null would crash
	// the sign-in -> onboarding journey).
	{
		route: "GET /api/v1/users", method: "GET", path: "/api/v1/users",
		handler: (*API).handleGetUser,
		pool: &fakePool{rules: []fakeRule{
			{frag: "wm.status in ('active', 'suspended')", rows: [][]any{}},
			{frag: "from invitations i", rows: [][]any{}},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var u struct {
				Workspaces []json.RawMessage `json:"workspaces"`
				Invites    []json.RawMessage `json:"invites"`
				Role       string            `json:"role"`
				Kind       string            `json:"kind"`
			}
			decodeBody(t, rec, &u)
			if u.Workspaces == nil || len(u.Workspaces) != 0 || u.Invites == nil || len(u.Invites) != 0 {
				t.Fatalf("fresh account = workspaces %v, invites %v, want empty arrays, never null", u.Workspaces, u.Invites)
			}
			if u.Role != "USER" || u.Kind != "human" {
				t.Fatalf("role = %q kind = %q, want USER/human", u.Role, u.Kind)
			}
		},
	},

	// PUT /users — the display name round-trips (email is the identity
	// key and is never accepted as input).
	{
		route: "PUT /api/v1/users", method: "PUT", path: "/api/v1/users",
		handler: (*API).handleUpdateUser,
		body:    `{"fullname":"Alex Smith"}`,
		pool: &fakePool{rules: []fakeRule{
			{frag: "wm.status in ('active', 'suspended')", rows: [][]any{}},
			{frag: "from invitations i", rows: [][]any{}},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var u struct {
				Email string `json:"email"`
			}
			decodeBody(t, rec, &u)
			if u.Email != rtUser+"@example.com" {
				t.Fatalf("email = %q, want the principal's identity echoed", u.Email)
			}
		},
	},

	// POST /workspaces/onboarding — the signup journey: workspace +
	// owner + team + default statuses/labels, atomically.
	{
		route: "POST /api/v1/workspaces/onboarding", method: "POST", path: "/api/v1/workspaces/onboarding",
		handler: (*API).handleOnboarding,
		body:    `{"fullname":"Alex","workspaceName":"Acme  Corp","teamName":"Engineering","teamIdentifier":"eng"}`,
		pool: &fakePool{
			rules: []fakeRule{
				{frag: "select exists(select 1 from workspaces where slug = $1)", rowVals: []any{false}},
			},
			txs: []*fakeTx{{rules: []fakeRule{
				{frag: "insert into workspaces (name, slug", rowVals: []any{rtWS, time.Now(), time.Now(), "acme-corp", "Acme Corp"}},
				{frag: "insert into teams (workspace_id, name, identifier", rowVals: []any{rtTeam}},
			}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var w struct {
				Slug string `json:"slug"`
				Name string `json:"name"`
			}
			decodeBody(t, rec, &w)
			if w.Slug != "acme-corp" || w.Name != "Acme Corp" {
				t.Fatalf("workspace = %q/%q, want the slugified onboarding result", w.Slug, w.Name)
			}
			tx := pool.txs[0]
			for _, frag := range []string{"insert into workflow_statuses", "insert into labels", "insert into issue_counters"} {
				if !hasSQL(tx, frag) {
					t.Fatalf("onboarding skipped the seed: no %q", frag)
				}
			}
		},
	},

	// GET /sync_actions/bootstrap — the full tenant replay (two models
	// requested, one row each).
	{
		route: "GET /api/v1/sync_actions/bootstrap", method: "GET",
		path:    "/api/v1/sync_actions/bootstrap?workspaceId=" + rtWS + "&modelNames=Workspace,Team",
		handler: (*API).handleSync,
		pool: &fakePool{rules: []fakeRule{
			rtRole("owner"),
			{frag: "from sync_sequences where workspace_id = $1", rowVals: []any{int64(0)}},
			{frag: "from workspaces where id = $1", rowVals: []any{rtWS, "acme", "Acme Corp", time.Now(), time.Now()}},
			{frag: "from teams where workspace_id = $1", rows: [][]any{rtTeamListRow(rtTeam, "Engineering", "ENG", rtWS)}},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var resp struct {
				SyncActions    []json.RawMessage `json:"syncActions"`
				LastSequenceID string            `json:"lastSequenceId"`
			}
			decodeBody(t, rec, &resp)
			if len(resp.SyncActions) != 2 {
				t.Fatalf("bootstrap delivered %d records, want 2 (one per model row)", len(resp.SyncActions))
			}
			if resp.LastSequenceID != "0" {
				t.Fatalf("lastSequenceId = %q, want the server watermark 0", resp.LastSequenceID)
			}
		},
	},

	// GET /sync_actions/delta — the outbox tail past the client's
	// watermark; wire actions compressed (UPDATE -> U), the server's
	// watermark echoed (never the client's own cursor).
	{
		route: "GET /api/v1/sync_actions/delta", method: "GET",
		path:    "/api/v1/sync_actions/delta?workspaceId=" + rtWS + "&lastSequenceId=5",
		handler: (*API).handleSync,
		pool: &fakePool{rules: []fakeRule{
			rtRole("owner"),
			{frag: "from sync_sequences where workspace_id = $1", rowVals: []any{int64(10)}},
			{frag: "sequence_id > $2", rows: [][]any{
				{int64(11), "Issue", rtIssue, "UPDATE", json.RawMessage(`{"id":"` + rtIssue + `"}`)},
			}},
			{frag: "select min(sequence_id) from sync_outbox", rowVals: []any{int64(1)}},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var resp struct {
				SyncActions []struct {
					Action string `json:"action"`
				} `json:"syncActions"`
				LastSequenceID string `json:"lastSequenceId"`
			}
			decodeBody(t, rec, &resp)
			if len(resp.SyncActions) != 1 || resp.SyncActions[0].Action != "U" {
				t.Fatalf("delta = %+v, want one U record (the wire contract)", resp.SyncActions)
			}
			if resp.LastSequenceID != "10" {
				t.Fatalf("lastSequenceId = %q, want the server watermark, never the client's cursor", resp.LastSequenceID)
			}
		},
	},

	// POST /issues — the door: an issue cannot be born without a team.
	{
		route: "POST /api/v1/issues", method: "POST", path: "/api/v1/issues",
		handler: (*API).handleCreateIssue,
		body:    `{"title":"Task"}`, pool: &fakePool{},
		code: 400, err: "teamId is required",
	},

	// POST /issues/{id} — the swarm guard at the seam: an agent never
	// mutates a paused card (D1 escalation).
	{
		route: "POST /api/v1/issues/{id}", method: "POST",
		path:      "/api/v1/issues/" + rtIssue,
		urlParams: []string{"id", rtIssue},
		handler:   (*API).handleUpdateIssue,
		principal: agentPrincipal(rtAgent), body: `{}`,
		pool: &fakePool{rules: []fakeRule{
			rtIssueRow(rtIssue, rtTeam, rtState, 7, true, rtAgent, []string{}),
			rtTeamWS(rtWS), rtRole("owner"),
		}},
		code: 422, err: "issue is paused for human review; agents cannot act on it",
	},

	// DELETE /issues/{id} — the soft delete: the row stays (history
	// attribution), the feed drops it.
	{
		route: "DELETE /api/v1/issues/{id}", method: "DELETE",
		path:      "/api/v1/issues/" + rtIssue,
		urlParams: []string{"id", rtIssue},
		handler:   (*API).handleDeleteIssue,
		pool: &fakePool{
			rules: []fakeRule{
				rtIssueRow(rtIssue, rtTeam, rtState, 7, false, nil, []string{}),
				rtTeamWS(rtWS), rtRole("owner"),
				{frag: "join accounts a2", rowErr: pgx.ErrNoRows}, // the wake: no agent assignee
			},
			txs: []*fakeTx{{rules: append(rtHistoryRules(), rtSeq(8))}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				ID string `json:"id"`
			}
			decodeBody(t, rec, &v)
			if v.ID != rtIssue {
				t.Fatalf("id = %q, want %q", v.ID, rtIssue)
			}
			if !hasSQL(pool.txs[0], "update issues set status = 'deleted'") {
				t.Fatal("the delete did not archive the issue row")
			}
		},
	},

	// POST /issues/{id}/move — the team move renumbers in the
	// destination team.
	{
		route: "POST /api/v1/issues/{id}/move", method: "POST",
		path:      "/api/v1/issues/" + rtIssue + "/move",
		urlParams: []string{"id", rtIssue},
		handler:   (*API).handleMoveIssue,
		body:      `{"teamId":"` + rtTeam2 + `"}`,
		pool: &fakePool{
			rules: []fakeRule{
				rtIssueRow(rtIssue, rtTeam, rtState, 7, false, nil, []string{}),
				rtTeamWS(rtWS), rtRole("owner"),
				{frag: "join accounts a2", rowErr: pgx.ErrNoRows}, // the wake: no agent assignee
			},
			txs: []*fakeTx{{rules: []fakeRule{
				{frag: "coalesce(max(number), 0) + 1", rowVals: []any{5}},
				rtSeq(9),
				rtIssueRow(rtIssue, rtTeam2, rtState, 5, false, nil, []string{}),
			}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				TeamID string `json:"teamId"`
				Number int    `json:"number"`
			}
			decodeBody(t, rec, &v)
			if v.TeamID != rtTeam2 || v.Number != 5 {
				t.Fatalf("moved issue = team %q #%d, want %q renumbered to 5", v.TeamID, v.Number, rtTeam2)
			}
		},
	},

	// DELETE /issue_relation/{id} — the soft edge delete refreshes both
	// endpoints (their denormalized arrays changed).
	{
		route: "DELETE /api/v1/issue_relation/{id}", method: "DELETE",
		path:      "/api/v1/issue_relation/" + rtRelation,
		urlParams: []string{"id", rtRelation},
		handler:   (*API).handleDeleteIssueRelation,
		pool: &fakePool{
			rules: []fakeRule{
				{frag: "from issue_relations where id = $1", rowVals: rtRelationRow()},
				rtRole("owner"),
			},
			txs: []*fakeTx{{rules: append(rtHistoryRules(),
				fakeRule{frag: "set deleted_at = now(), updated_at = now()", rowVals: rtRelationRow()},
				rtSeq(10),
				rtIssueRow(rtIssue, rtTeam, rtState, 7, false, nil, []string{}),
				rtIssueRow(rtIssue2, rtTeam, rtState, 7, false, nil, []string{}),
			)}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Type           string `json:"type"`
				RelatedIssueID string `json:"relatedIssueId"`
			}
			decodeBody(t, rec, &v)
			if v.Type != "BLOCKS" || v.RelatedIssueID != rtIssue2 {
				t.Fatalf("relation = %q -> %q, want the pre-delete shape", v.Type, v.RelatedIssueID)
			}
		},
	},

	// GET /issues/{id}/relations — the reader-side list (the reader
	// perspective rewrite lives in the query).
	{
		route: "GET /api/v1/issues/{id}/relations", method: "GET",
		path:      "/api/v1/issues/" + rtIssue + "/relations",
		urlParams: []string{"id", rtIssue},
		handler:   (*API).handleListIssueRelations,
		pool: &fakePool{rules: []fakeRule{
			rtIssueRow(rtIssue, rtTeam, rtState, 7, false, nil, []string{}),
			rtTeamWS(rtWS), rtRole("owner"),
			{frag: "jsonb_agg(jsonb_build_object", rowVals: []any{[]byte(`[]`)}},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			if strings.TrimSpace(rec.Body.String()) != `[]` {
				t.Fatalf("relations = %s, want the empty array (the client iterates it)", rec.Body.String())
			}
		},
	},

	// POST /issues/{id}/handoff — the quiet guards: a loop trip pauses
	// the issue (the escalation the human sees) and rejects the
	// handoff.
	{
		route: "POST /api/v1/issues/{id}/handoff", method: "POST",
		path:      "/api/v1/issues/" + rtIssue + "/handoff",
		urlParams: []string{"id", rtIssue},
		handler:   (*API).handleHandoff,
		body:      `{"toAccountId":"` + rtAgent + `","summary":"stuck"}`,
		pool: &fakePool{
			rules: []fakeRule{
				rtIssueRow(rtIssue, rtTeam, rtState, 7, false, rtUser, []string{}),
				rtTeamWS(rtWS), rtRole("owner"),
				{frag: "wm.status = 'active' and a.kind = $3", rowVals: []any{true}}, // agentMember
			},
			txs: []*fakeTx{{rules: append(rtHistoryRules(),
				fakeRule{frag: "to_account_id = $2 and created_at > now()", rowVals: []any{3}}, // the loop count
				fakeRule{frag: "h.issue_id = $1 and a.kind = $2", rowVals: []any{0}},           // the op budget
				fakeRule{frag: "and action = 'paused'", rowVals: []any{0, time.Time{}, 0}},     // reviewFacts: a fresh issue
				fakeRule{frag: "lower(name) = $2", rowErr: pgx.ErrNoRows},                      // no Human Review state
				rtSeq(11),
				rtIssueRow(rtIssue, rtTeam, rtState, 7, true, rtUser, []string{}),
				fakeRule{frag: "values ($1, $2, $3::jsonb, null)", rowVals: []any{"c1"}}, // the human-handoff comment
				rtCommentRow("c1", "{}", rtUser, rtIssue),
			)}},
		},
		code: 422, err: "quiet guard tripped; issue paused for human review: handoff loop detected (the same agent received this issue 3 times in 24h)",
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			if !hasSQL(pool.txs[0], "agent_paused = true") {
				t.Fatal("the guard tripped but the issue was not paused")
			}
		},
	},

	// POST /issues/{id}/subscribe — the acknowledgement (v1 has no
	// subscription table): the current object comes back.
	{
		route: "POST /api/v1/issues/{id}/subscribe", method: "POST",
		path:      "/api/v1/issues/" + rtIssue + "/subscribe",
		urlParams: []string{"id", rtIssue},
		handler:   (*API).handleSubscribeIssue,
		pool: &fakePool{rules: []fakeRule{
			rtIssueRow(rtIssue, rtTeam, rtState, 7, false, nil, []string{}),
			rtTeamWS(rtWS), rtRole("owner"),
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Title string `json:"title"`
			}
			decodeBody(t, rec, &v)
			if v.Title != "Test issue" {
				t.Fatalf("subscribe returned %q, want the current object", v.Title)
			}
		},
	},

	// POST /issue_comments — the door: a comment is its body.
	{
		route: "POST /api/v1/issue_comments", method: "POST",
		path:    "/api/v1/issue_comments?issueId=" + rtIssue,
		handler: (*API).handleCreateComment,
		body:    `{"body":""}`, pool: &fakePool{},
		code: 400, err: "body is required",
	},

	// POST /issue_comments/{id} — the door.
	{
		route: "POST /api/v1/issue_comments/{id}", method: "POST",
		path:      "/api/v1/issue_comments/" + rtComment,
		urlParams: []string{"id", rtComment},
		handler:   (*API).handleUpdateComment,
		body:      `{"body":""}`, pool: &fakePool{},
		code: 400, err: "body is required",
	},

	// DELETE /issue_comments/{id} — the soft delete.
	{
		route: "DELETE /api/v1/issue_comments/{id}", method: "DELETE",
		path:      "/api/v1/issue_comments/" + rtComment,
		urlParams: []string{"id", rtComment},
		handler:   (*API).handleDeleteComment,
		pool: &fakePool{
			rules: []fakeRule{
				rtCommentRow(rtComment, "{}", rtUser, rtIssue),
				rtIssueRow(rtIssue, rtTeam, rtState, 7, false, nil, []string{}),
				rtTeamWS(rtWS), rtRole("owner"),
			},
			txs: []*fakeTx{{rules: []fakeRule{rtSeq(12)}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				ID string `json:"id"`
			}
			decodeBody(t, rec, &v)
			if v.ID != rtComment {
				t.Fatalf("id = %q, want %q", v.ID, rtComment)
			}
			if !hasSQL(pool.txs[0], "update comments set status = 'deleted'") {
				t.Fatal("the delete did not archive the comment row")
			}
		},
	},

	// GET /issue_comments/{id} — the single read.
	{
		route: "GET /api/v1/issue_comments/{id}", method: "GET",
		path:      "/api/v1/issue_comments/" + rtComment,
		urlParams: []string{"id", rtComment},
		handler:   (*API).handleGetComment,
		pool: &fakePool{rules: []fakeRule{
			rtCommentRow(rtComment, "{}", rtUser, rtIssue),
			rtIssueRow(rtIssue, rtTeam, rtState, 7, false, nil, []string{}),
			rtTeamWS(rtWS), rtRole("owner"),
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				UserID  string `json:"userId"`
				IssueID string `json:"issueId"`
			}
			decodeBody(t, rec, &v)
			if v.UserID != rtUser || v.IssueID != rtIssue {
				t.Fatalf("comment = %q on %q, want %q on %q", v.UserID, v.IssueID, rtUser, rtIssue)
			}
		},
	},

	// GET /issue_comments/{id}/replies — the empty thread.
	{
		route: "GET /api/v1/issue_comments/{id}/replies", method: "GET",
		path:      "/api/v1/issue_comments/" + rtComment + "/replies",
		urlParams: []string{"id", rtComment},
		handler:   (*API).handleGetCommentReplies,
		pool: &fakePool{rules: []fakeRule{
			rtCommentRow(rtComment, "{}", rtUser, rtIssue),
			rtIssueRow(rtIssue, rtTeam, rtState, 7, false, nil, []string{}),
			rtTeamWS(rtWS), rtRole("owner"),
			{frag: "where cm.parent_id = $1", rows: [][]any{}},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			if strings.TrimSpace(rec.Body.String()) != `[]` {
				t.Fatalf("replies = %s, want the empty array", rec.Body.String())
			}
		},
	},

	// GET /sync_actions/stream — the door (the happy path is a blocking
	// stream: the seam pins the guard, the wire contract pins the rest).
	{
		route: "GET /api/v1/sync_actions/stream", method: "GET",
		path:    "/api/v1/sync_actions/stream?workspaceId=",
		handler: (*API).handleStream,
		pool:    &fakePool{},
		code:    400, err: "workspaceId is required",
	},

	// POST /teams — the door.
	{
		route: "POST /api/v1/teams", method: "POST", path: "/api/v1/teams",
		handler: (*API).handleCreateTeam,
		body:    `{"identifier":"eng"}`, pool: &fakePool{},
		code: 400, err: "name and identifier are required",
	},

	// POST /teams/{id} — an unknown team is a 404 (no existence leak).
	{
		route: "POST /api/v1/teams/{id}", method: "POST",
		path:      "/api/v1/teams/" + rtTeam,
		urlParams: []string{"id", rtTeam},
		handler:   (*API).handleUpdateTeam,
		body:      `{"name":"Eng"}`,
		pool: &fakePool{rules: []fakeRule{
			{frag: "from teams where id = $1", rowErr: pgx.ErrNoRows},
		}},
		code: 404, err: "not found",
	},

	// DELETE /teams/{id} — the archive: the team leaves, its active
	// issues are archived with it (no dangling cards in the client).
	{
		route: "DELETE /api/v1/teams/{id}", method: "DELETE",
		path:      "/api/v1/teams/" + rtTeam,
		urlParams: []string{"id", rtTeam},
		handler:   (*API).handleDeleteTeam,
		pool: &fakePool{
			rules: []fakeRule{rtTeamRow(rtTeam, "Engineering", "ENG", "active", rtWS, "{}"), rtRole("owner")},
			txs: []*fakeTx{{rules: []fakeRule{
				rtSeq(13),
				{frag: "from issues where team_id = $1", rows: [][]any{}},
			}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				ID string `json:"id"`
			}
			decodeBody(t, rec, &v)
			if v.ID != rtTeam {
				t.Fatalf("id = %q, want %q", v.ID, rtTeam)
			}
			if !hasSQL(pool.txs[0], "update teams set status = 'archived'") {
				t.Fatal("the delete did not archive the team")
			}
		},
	},

	// GET /teams — the workspace's active teams (empty board).
	{
		route: "GET /api/v1/teams", method: "GET",
		path:    "/api/v1/teams?workspaceId=" + rtWS,
		handler: (*API).handleListTeams,
		pool: &fakePool{rules: []fakeRule{
			rtRole("member"),
			{frag: "from teams where workspace_id = $1", rows: [][]any{}},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			if strings.TrimSpace(rec.Body.String()) != `[]` {
				t.Fatalf("teams = %s, want the empty array", rec.Body.String())
			}
		},
	},

	// GET /teams/{id} — the single read (preferences always an object,
	// currentCycle always present).
	{
		route: "GET /api/v1/teams/{id}", method: "GET",
		path:      "/api/v1/teams/" + rtTeam,
		urlParams: []string{"id", rtTeam},
		handler:   (*API).handleGetTeam,
		pool: &fakePool{rules: []fakeRule{
			rtTeamRow(rtTeam, "Engineering", "ENG", "active", rtWS, "{}"),
			rtRole("member"),
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Identifier   string         `json:"identifier"`
				CurrentCycle any            `json:"currentCycle"`
				Preferences  map[string]any `json:"preferences"`
			}
			decodeBody(t, rec, &v)
			if v.Identifier != "ENG" || v.CurrentCycle != nil {
				t.Fatalf("team = %q, cycle %v, want ENG/nil", v.Identifier, v.CurrentCycle)
			}
			if v.Preferences == nil {
				t.Fatal("preferences = null, want an object (the client sub-model is strict)")
			}
		},
	},

	// GET /teams/name/{slug} — the identifier lookup (the client's
	// board resolution): a miss is a plain 404.
	{
		route: "GET /api/v1/teams/name/{slug}", method: "GET",
		path:      "/api/v1/teams/name/ENG?workspaceId=" + rtWS,
		urlParams: []string{"slug", "ENG"},
		handler:   (*API).handleGetTeamByName,
		pool: &fakePool{rules: []fakeRule{
			rtRole("member"),
			{frag: "identifier = $2 and status = 'active'", rowErr: pgx.ErrNoRows},
		}},
		code: 404, err: "not found",
	},

	// POST /teams/{id}/add-member — the door: the target must be an id.
	{
		route: "POST /api/v1/teams/{id}/add-member", method: "POST",
		path:      "/api/v1/teams/" + rtTeam + "/add-member",
		urlParams: []string{"id", rtTeam},
		handler:   (*API).handleAddTeamMember,
		body:      `{"userId":"nope"}`, pool: &fakePool{},
		code: 400, err: "userId is required",
	},

	// POST /teams/{id}/remove-member — the door.
	{
		route: "POST /api/v1/teams/{id}/remove-member", method: "POST",
		path:      "/api/v1/teams/" + rtTeam + "/remove-member",
		urlParams: []string{"id", rtTeam},
		handler:   (*API).handleRemoveTeamMember,
		body:      `{"userId":"nope"}`, pool: &fakePool{},
		code: 400, err: "userId is required",
	},

	// POST /teams/{id}/preferences — the supplied object replaces the
	// stored preferences wholesale (the client always sends the full
	// object).
	{
		route: "POST /api/v1/teams/{id}/preferences", method: "POST",
		path:      "/api/v1/teams/" + rtTeam + "/preferences",
		urlParams: []string{"id", rtTeam},
		handler:   (*API).handleUpdateTeamPreferences,
		body:      `{"cyclesEnabled":true}`,
		pool: &fakePool{
			rules: []fakeRule{rtTeamRow(rtTeam, "Engineering", "ENG", "active", rtWS, "{}"), rtRole("owner")},
			txs: []*fakeTx{{rules: []fakeRule{
				rtSeq(14),
				rtTeamRow(rtTeam, "Engineering", "ENG", "active", rtWS, `{"cyclesEnabled":true}`),
			}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Preferences struct {
					CyclesEnabled bool `json:"cyclesEnabled"`
				} `json:"preferences"`
			}
			decodeBody(t, rec, &v)
			if !v.Preferences.CyclesEnabled {
				t.Fatalf("preferences = %+v, want cyclesEnabled true (the round-trip)", v.Preferences)
			}
		},
	},

	// POST /{teamId}/workflows — a new status (the client's quirky
	// path; the category normalizes to the client's uppercase enum).
	{
		route: "POST /api/v1/{teamId}/workflows", method: "POST",
		path:      "/api/v1/" + rtTeam + "/workflows",
		urlParams: []string{"teamId", rtTeam},
		handler:   (*API).handleCreateWorkflow,
		body:      `{"name":"Blocked","position":3,"color":"#8884d8","category":"started"}`,
		pool: &fakePool{
			rules: []fakeRule{rtTeamRow(rtTeam, "Engineering", "ENG", "active", rtWS, "{}"), rtRole("owner")},
			txs: []*fakeTx{{rules: []fakeRule{
				{frag: "insert into workflow_statuses (team_id, name", rowVals: []any{rtState}},
				rtSeq(15),
				{frag: "from workflow_statuses where id = $1", rowVals: []any{rtState, "Blocked", nil, 3, "#8884d8", "STARTED", rtTeam, time.Now(), time.Now()}},
			}}},
		},
		code: 201,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Name   string `json:"name"`
				TeamID string `json:"teamId"`
			}
			decodeBody(t, rec, &v)
			if v.Name != "Blocked" || v.TeamID != rtTeam {
				t.Fatalf("created status = %q/%q, want Blocked in the team", v.Name, v.TeamID)
			}
		},
	},

	// POST /{teamId}/workflows/{workflowId} — the partial patch.
	{
		route: "POST /api/v1/{teamId}/workflows/{workflowId}", method: "POST",
		path:      "/api/v1/" + rtTeam + "/workflows/" + rtState,
		urlParams: []string{"teamId", rtTeam, "workflowId", rtState},
		handler:   (*API).handleUpdateWorkflow,
		body:      `{"name":"Blocked"}`,
		pool: &fakePool{
			rules: []fakeRule{
				rtTeamRow(rtTeam, "Engineering", "ENG", "active", rtWS, "{}"), rtRole("owner"),
				{frag: "from workflow_statuses where id = $1 and team_id = $2", rowVals: []any{rtState, "Open", nil, 3, "#8884d8", "STARTED", rtTeam, time.Now(), time.Now()}},
			},
			txs: []*fakeTx{{rules: []fakeRule{rtSeq(16)}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Name string `json:"name"`
			}
			decodeBody(t, rec, &v)
			if v.Name != "Blocked" {
				t.Fatalf("name = %q, want the patched value", v.Name)
			}
		},
	},

	// GET /{teamId}/workflows — the board's columns (empty workflow).
	{
		route: "GET /api/v1/{teamId}/workflows", method: "GET",
		path:      "/api/v1/" + rtTeam + "/workflows",
		urlParams: []string{"teamId", rtTeam},
		handler:   (*API).handleListWorkflows,
		pool: &fakePool{rules: []fakeRule{
			rtTeamRow(rtTeam, "Engineering", "ENG", "active", rtWS, "{}"),
			rtRole("member"),
			{frag: "select role from team_members", rowVals: []any{"manager"}},
			{frag: "from workflow_statuses where team_id = $1 and status = 'active'", rows: [][]any{}},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			if strings.TrimSpace(rec.Body.String()) != `[]` {
				t.Fatalf("workflows = %s, want the empty array", rec.Body.String())
			}
		},
	},

	// POST /labels — a workspace-shared label (admin only; teamId and
	// groupId are null — labels are not team-scoped in v1).
	{
		route: "POST /api/v1/labels", method: "POST", path: "/api/v1/labels",
		handler: (*API).handleCreateLabel,
		body:    `{"name":"Urgent","color":"#cf222e","workspaceId":"` + rtWS + `"}`,
		pool: &fakePool{
			rules: []fakeRule{rtRole("owner")},
			txs: []*fakeTx{{rules: []fakeRule{
				{frag: "insert into labels (workspace_id", rowVals: []any{rtLabel}},
				rtSeq(17),
				rtLabelRow(rtLabel, "Urgent", "#cf222e", rtWS),
			}}},
		},
		code: 201,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Name    string `json:"name"`
				TeamID  any    `json:"teamId"`
				GroupID any    `json:"groupId"`
			}
			decodeBody(t, rec, &v)
			if v.Name != "Urgent" || v.TeamID != nil || v.GroupID != nil {
				t.Fatalf("label = %q (team %v, group %v), want Urgent with null team/group", v.Name, v.TeamID, v.GroupID)
			}
		},
	},

	// POST /labels/{id} — the patch.
	{
		route: "POST /api/v1/labels/{id}", method: "POST",
		path:      "/api/v1/labels/" + rtLabel,
		urlParams: []string{"id", rtLabel},
		handler:   (*API).handleUpdateLabel,
		body:      `{"color":"#000000"}`,
		pool: &fakePool{
			rules: []fakeRule{rtLabelRow(rtLabel, "Urgent", "#cf222e", rtWS), rtRole("owner")},
			txs:   []*fakeTx{{rules: []fakeRule{rtSeq(18)}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Color string `json:"color"`
			}
			decodeBody(t, rec, &v)
			if v.Color != "#000000" {
				t.Fatalf("color = %q, want the patched value", v.Color)
			}
		},
	},

	// DELETE /labels/{id} — the archive (issue label links survive).
	{
		route: "DELETE /api/v1/labels/{id}", method: "DELETE",
		path:      "/api/v1/labels/" + rtLabel,
		urlParams: []string{"id", rtLabel},
		handler:   (*API).handleDeleteLabel,
		pool: &fakePool{
			rules: []fakeRule{rtLabelRow(rtLabel, "Urgent", "#cf222e", rtWS), rtRole("owner")},
			txs:   []*fakeTx{{rules: []fakeRule{rtSeq(19)}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				ID string `json:"id"`
			}
			decodeBody(t, rec, &v)
			if v.ID != rtLabel {
				t.Fatalf("id = %q, want %q", v.ID, rtLabel)
			}
		},
	},

	// GET /labels — the workspace's active labels (empty).
	{
		route: "GET /api/v1/labels", method: "GET",
		path:    "/api/v1/labels?workspaceId=" + rtWS,
		handler: (*API).handleListLabels,
		pool: &fakePool{rules: []fakeRule{
			rtRole("member"),
			{frag: "from labels where workspace_id = $1", rows: [][]any{}},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			if strings.TrimSpace(rec.Body.String()) != `[]` {
				t.Fatalf("labels = %s, want the empty array", rec.Body.String())
			}
		},
	},

	// GET /projects — the board's project rail (empty).
	{
		route: "GET /api/v1/projects", method: "GET",
		path:    "/api/v1/projects?workspaceId=" + rtWS,
		handler: (*API).handleListProjects,
		pool: &fakePool{rules: []fakeRule{
			rtRole("member"),
			{frag: "cardinality(teams) = 0", rows: [][]any{}},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			if strings.TrimSpace(rec.Body.String()) != `[]` {
				t.Fatalf("projects = %s, want the empty array", rec.Body.String())
			}
		},
	},

	// POST /projects — a project (a grouping, never a state).
	{
		route: "POST /api/v1/projects", method: "POST", path: "/api/v1/projects",
		handler: (*API).handleCreateProject,
		body:    `{"name":"Ship Converge","workspaceId":"` + rtWS + `"}`,
		pool: &fakePool{
			rules: []fakeRule{rtRole("owner")},
			txs: []*fakeTx{{rules: []fakeRule{
				{frag: "insert into projects (workspace_id", rowVals: []any{rtProject, time.Now()}},
				rtProjectRow(rtProject, "Ship Converge", "#4484d5", rtWS),
				rtSeq(20),
			}}},
		},
		code: 201,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			}
			decodeBody(t, rec, &v)
			if v.Name != "Ship Converge" || v.Status != "ACTIVE" {
				t.Fatalf("project = %q/%q, want the created row (status ACTIVE)", v.Name, v.Status)
			}
		},
	},

	// POST /projects/{id} — the patch.
	{
		route: "POST /api/v1/projects/{id}", method: "POST",
		path:      "/api/v1/projects/" + rtProject + "?workspaceId=" + rtWS,
		urlParams: []string{"id", rtProject},
		handler:   (*API).handleUpdateProject,
		body:      `{"name":"Shipped"}`,
		pool: &fakePool{
			rules: []fakeRule{
				rtRole("owner"),
				{frag: "from projects where id = $1 and deleted_at is null", rowVals: rtProjectRow(rtProject, "Ship Converge", "#4484d5", rtWS).rowVals},
			},
			txs: []*fakeTx{{rules: []fakeRule{
				rtSeq(20),
				rtProjectRow(rtProject, "Shipped", "#4484d5", rtWS),
			}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Name string `json:"name"`
			}
			decodeBody(t, rec, &v)
			if v.Name != "Shipped" {
				t.Fatalf("name = %q, want the patched value", v.Name)
			}
		},
	},

	// POST /projects/{id}/delete — the soft delete clears membership
	// on every member issue in the same transaction (no cards stranded
	// on a dead project).
	{
		route: "POST /api/v1/projects/{id}/delete", method: "POST",
		path:      "/api/v1/projects/" + rtProject + "/delete?workspaceId=" + rtWS,
		urlParams: []string{"id", rtProject},
		handler:   (*API).handleDeleteProject,
		pool: &fakePool{
			rules: []fakeRule{
				rtRole("owner"),
				{frag: "from projects where id = $1 and deleted_at is null", rowVals: rtProjectRow(rtProject, "Ship Converge", "#4484d5", rtWS).rowVals},
			},
			txs: []*fakeTx{{rules: []fakeRule{
				{frag: "$2 = any(i.project_ids)", rows: [][]any{{rtIssue}}},
				rtSeq(21),
				rtIssueRow(rtIssue, rtTeam, rtState, 7, false, nil, []string{rtProject}),
			}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			if !hasSQL(pool.txs[0], "update issues set project_ids = '{}'") {
				t.Fatal("the delete did not clear the member issues' project membership")
			}
		},
	},

	// POST /views — a saved view (any member; the definition is opaque
	// client JSON: stored and returned verbatim, never reinterpreted).
	{
		route: "POST /api/v1/views", method: "POST", path: "/api/v1/views",
		handler: (*API).handleCreateView,
		body:    `{"name":"My view","workspaceId":"` + rtWS + `"}`,
		pool: &fakePool{
			rules: []fakeRule{rtRole("member")},
			txs: []*fakeTx{{rules: []fakeRule{
				{frag: "insert into saved_views (workspace_id", rowVals: []any{rtView}},
				rtSeq(22),
				rtViewRow(rtView, "My view", rtWS, rtUser, false),
			}}},
		},
		code: 201,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Filters map[string]any `json:"filters"`
				TeamID  any            `json:"teamId"`
			}
			decodeBody(t, rec, &v)
			if v.Filters == nil || v.TeamID != nil {
				t.Fatalf("view = filters %v, teamId %v, want an object and a null teamId", v.Filters, v.TeamID)
			}
		},
	},

	// POST /views/{id} — the patch (the creator always writes).
	{
		route: "POST /api/v1/views/{id}", method: "POST",
		path:      "/api/v1/views/" + rtView,
		urlParams: []string{"id", rtView},
		handler:   (*API).handleUpdateView,
		body:      `{"isBookmarked":true}`,
		pool: &fakePool{
			rules: []fakeRule{rtViewRow(rtView, "My view", rtWS, rtUser, false)},
			txs:   []*fakeTx{{rules: []fakeRule{rtSeq(23)}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				IsBookmarked bool `json:"isBookmarked"`
			}
			decodeBody(t, rec, &v)
			if !v.IsBookmarked {
				t.Fatal("isBookmarked = false, want the patched value")
			}
		},
	},

	// DELETE /views/{id} — the archive.
	{
		route: "DELETE /api/v1/views/{id}", method: "DELETE",
		path:      "/api/v1/views/" + rtView,
		urlParams: []string{"id", rtView},
		handler:   (*API).handleDeleteView,
		pool: &fakePool{
			rules: []fakeRule{rtViewRow(rtView, "My view", rtWS, rtUser, false)},
			txs:   []*fakeTx{{rules: []fakeRule{rtSeq(24)}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				ID string `json:"id"`
			}
			decodeBody(t, rec, &v)
			if v.ID != rtView {
				t.Fatalf("id = %q, want %q", v.ID, rtView)
			}
		},
	},

	// GET /views/{id} — the read (workspace members; private views are
	// creator-only, but v1 creates none).
	{
		route: "GET /api/v1/views/{id}", method: "GET",
		path:      "/api/v1/views/" + rtView,
		urlParams: []string{"id", rtView},
		handler:   (*API).handleGetView,
		pool: &fakePool{rules: []fakeRule{
			rtViewRow(rtView, "My view", rtWS, rtUser, false),
			rtRole("member"),
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Name string `json:"name"`
			}
			decodeBody(t, rec, &v)
			if v.Name != "My view" {
				t.Fatalf("name = %q, want My view", v.Name)
			}
		},
	},

	// POST /workspaces — the rename door (admin-only below it).
	{
		route: "POST /api/v1/workspaces", method: "POST", path: "/api/v1/workspaces",
		handler: (*API).handleUpdateWorkspace,
		body:    `{"workspaceId":"` + rtWS + `"}`, pool: &fakePool{},
		code: 422, err: "name must be 1-100 chars",
	},

	// POST /workspaces/preferences — the honest no-op acknowledgement
	// (workspace preferences are not a v1 concept).
	{
		route: "POST /api/v1/workspaces/preferences", method: "POST",
		path:    "/api/v1/workspaces/preferences",
		handler: (*API).handleUpdateWorkspacePreferences,
		body:    `{"workspaceId":"` + rtWS + `"}`,
		pool:    &fakePool{rules: []fakeRule{rtRole("member")}},
		code:    200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			if strings.TrimSpace(rec.Body.String()) != `{}` {
				t.Fatalf("preferences = %s, want the empty object", rec.Body.String())
			}
		},
	},

	// POST /workspaces/invite_users — the email gate (the batch is
	// rejected with the offending address, as-is).
	{
		route: "POST /api/v1/workspaces/invite_users", method: "POST",
		path:    "/api/v1/workspaces/invite_users",
		handler: (*API).handleInviteUsers,
		body:    `{"emailIds":"bad","workspaceId":"` + rtWS + `"}`,
		pool: &fakePool{rules: []fakeRule{
			rtRole("owner"),
			{frag: "select name from workspaces where id = $1", rowVals: []any{"Acme Corp"}},
		}},
		code: 422, err: "invalid email address: bad",
	},

	// POST /workspaces/invite_action — the door: the invitee must
	// decide (accept is explicit, never defaulted).
	{
		route: "POST /api/v1/workspaces/invite_action", method: "POST",
		path:    "/api/v1/workspaces/invite_action",
		handler: (*API).handleInviteAction,
		body:    `{"inviteId":"` + rtToken + `"}`, pool: &fakePool{},
		code: 400, err: "inviteId and accept are required",
	},

	// POST /workspaces/suspend — the kill switch's guard: the workspace
	// Owner cannot be suspended.
	{
		route: "POST /api/v1/workspaces/suspend", method: "POST",
		path:      "/api/v1/workspaces/suspend",
		principal: humanPrincipal(rtAdmin),
		handler:   (*API).handleSuspendMember,
		body:      `{"userId":"` + rtUser + `","workspaceId":"` + rtWS + `"}`,
		pool: &fakePool{rules: []fakeRule{
			rtRole("admin"),
			rtMemberRow(rtMemberID, "owner", "active", rtUser, rtWS, rtTeam),
		}},
		code: 422, err: "the workspace owner cannot be suspended",
	},

	// GET /workspaces/{id}/swarm — the fleet panel on a swarm-free
	// workspace: the arrays are empty, never null (the client iterates
	// them); the settings fall back to the deploy defaults without a
	// saved row.
	{
		route: "GET /api/v1/workspaces/{id}/swarm", method: "GET",
		path:      "/api/v1/workspaces/" + rtWS + "/swarm",
		urlParams: []string{"id", rtWS},
		handler:   (*API).handleSwarmStatus,
		pool: &fakePool{rules: []fakeRule{
			rtRole("member"),
			{frag: "where a.kind = $2", rows: [][]any{}},
			{frag: "order by i.updated_at desc", rows: [][]any{}},
			{frag: "from swarm_settings where workspace_id = $1", rowErr: pgx.ErrNoRows},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Agents       []json.RawMessage `json:"agents"`
				PausedIssues []json.RawMessage `json:"pausedIssues"`
				Settings     struct {
					Topology string `json:"topology"`
				} `json:"settings"`
			}
			decodeBody(t, rec, &v)
			if v.Agents == nil || len(v.Agents) != 0 || v.PausedIssues == nil || len(v.PausedIssues) != 0 {
				t.Fatalf("panel = %d agents, %d paused, want empty arrays, never null", len(v.Agents), len(v.PausedIssues))
			}
			if v.Settings.Topology != "" {
				t.Fatalf("topology = %q, want the deploy default (empty without a saved row)", v.Settings.Topology)
			}
		},
	},

	// GET /api/v1/workspaces/{id}/metrics — the metrics plane on an
	// idle workspace: every section answers zeros, the proxy degrades
	// to "off" (a dashboard shows its own dead state, never a 500).
	{
		route: "GET /api/v1/workspaces/{id}/metrics", method: "GET",
		path:      "/api/v1/workspaces/" + rtWS + "/metrics",
		urlParams: []string{"id", rtWS},
		handler:   (*API).handleMetrics,
		pool: &fakePool{rules: []fakeRule{
			rtRole("member"),
			{frag: "from workspace_members where workspace_id = $1", rowVals: []any{0, 0}},
			{frag: "select 'teams', count(*)", rows: [][]any{
				{"teams", 0}, {"workflows", 0}, {"labels", 0}, {"projects", 0},
				{"views", 0}, {"issues", 0}, {"comments", 0}, {"history", 0},
			}},
			{frag: "group by 1, 2 order by 3 desc", rows: [][]any{}},
			// The six bounded-window counts share two shapes (24h / 7d);
			// one rule per window covers all six calls.
			{frag: "created_at > now() - interval '24 hours'", rowVals: []any{0}},
			{frag: "interval '7 days'", rowVals: []any{0}},
			{frag: "select count(*) from sync_outbox where workspace_id = $1", rowVals: []any{0}},
			{frag: "from sync_sequences where workspace_id = $1", rowVals: []any{int64(0)}},
			// The agent-share count must come before the roster rule:
			// the roster's statement contains its fragment too, and the
			// first match wins.
			{frag: "count(*) filter (where a.kind = $2)", rowVals: []any{0, 0}},
			{frag: "where a.kind = $2", rows: [][]any{}},
			{frag: "not in ('COMPLETED', 'CANCELED')", rowVals: []any{0, 0}},
			{frag: "select count(*) filter (where h.action = 'paused')", rowVals: []any{0, 0}},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Product struct {
					Issues int `json:"issues"`
				}
				Codebase struct {
					FeedSequence string `json:"feedSequence"`
				}
				Swarm struct {
					Agents int `json:"agents"`
				}
				Proxy struct {
					Mode string `json:"mode"`
				}
			}
			decodeBody(t, rec, &v)
			if v.Product.Issues != 0 || v.Swarm.Agents != 0 || v.Codebase.FeedSequence != "0" || v.Proxy.Mode != "off" {
				t.Fatalf("metrics = product %d, swarm %d, feed %q, proxy %q, want all idle",
					v.Product.Issues, v.Swarm.Agents, v.Codebase.FeedSequence, v.Proxy.Mode)
			}
		},
	},

	// POST /workspaces/{id}/swarm/settings — the human's lever: agents
	// may never steer the fleet (the humans' control over them).
	{
		route: "POST /api/v1/workspaces/{id}/swarm/settings", method: "POST",
		path:      "/api/v1/workspaces/" + rtWS + "/swarm/settings",
		urlParams: []string{"id", rtWS},
		principal: agentPrincipal(rtAgent),
		handler:   (*API).handleUpdateSwarmSettings,
		body:      `{"topology":"flat"}`, pool: &fakePool{},
		code: 422, err: "agents cannot change fleet settings",
	},

	// POST /workspaces/{id}/agents — the door: the name is the identity
	// (1-64 printable characters).
	{
		route: "POST /api/v1/workspaces/{id}/agents", method: "POST",
		path:      "/api/v1/workspaces/" + rtWS + "/agents",
		urlParams: []string{"id", rtWS},
		handler:   (*API).handleCreateAgent,
		body:      `{"name":""}`, pool: &fakePool{},
		code: 422, err: "name must be 1-64 printable characters",
	},

	// GET /workspaces/{id}/agents — the roster (any member; the agents
	// are already visible through the sync, so the list adds no secret).
	{
		route: "GET /api/v1/workspaces/{id}/agents", method: "GET",
		path:      "/api/v1/workspaces/" + rtWS + "/agents",
		urlParams: []string{"id", rtWS},
		handler:   (*API).handleListAgents,
		pool: &fakePool{rules: []fakeRule{
			rtRole("member"),
			{frag: "where a.kind = 'agent'", rows: [][]any{}},
		}},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			if strings.TrimSpace(rec.Body.String()) != `[]` {
				t.Fatalf("agents = %s, want the empty array", rec.Body.String())
			}
		},
	},

	// POST /workspaces/{id}/agents/{accountId}/token — the rotation:
	// one additional live token (the old stays valid until revoked, so
	// a rotation never drops an agent mid-swarm).
	{
		route: "POST /api/v1/workspaces/{id}/agents/{accountId}/token", method: "POST",
		path:      "/api/v1/workspaces/" + rtWS + "/agents/" + rtAgent + "/token",
		urlParams: []string{"id", rtWS, "accountId", rtAgent},
		handler:   (*API).handleRotateAgentToken,
		pool: &fakePool{
			rules: []fakeRule{
				rtRole("owner"),
				rtMemberRow(rtMemberID, "agent", "active", rtAgent, rtWS, rtTeam),
				{frag: "select kind from accounts where id = $1", rowVals: []any{"agent"}},
			},
			txs: []*fakeTx{{rules: []fakeRule{
				{frag: "insert into api_tokens (account_id, name", rowVals: []any{"tok1"}},
			}}},
		},
		code: 201,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Token       string `json:"token"`
				TokenPrefix string `json:"tokenPrefix"`
			}
			decodeBody(t, rec, &v)
			if v.Token == "" || v.TokenPrefix == "" {
				t.Fatalf("rotation = token %q prefix %q, want the plaintext shown once", v.Token, v.TokenPrefix)
			}
		},
	},

	// POST /workspaces/{id}/agents/{accountId}/token/revoke — the soft
	// kill switch: the credentials die, the membership survives.
	{
		route: "POST /api/v1/workspaces/{id}/agents/{accountId}/token/revoke", method: "POST",
		path:      "/api/v1/workspaces/" + rtWS + "/agents/" + rtAgent + "/token/revoke",
		urlParams: []string{"id", rtWS, "accountId", rtAgent},
		handler:   (*API).handleRevokeAgentToken,
		pool: &fakePool{
			rules: []fakeRule{
				rtRole("owner"),
				rtMemberRow(rtMemberID, "agent", "active", rtAgent, rtWS, rtTeam),
				{frag: "select kind from accounts where id = $1", rowVals: []any{"agent"}},
			},
			txs: []*fakeTx{{}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Revoked int `json:"revoked"`
			}
			decodeBody(t, rec, &v)
			if v.Revoked != 0 {
				t.Fatalf("revoked = %d, want 0 (the fake's empty command tag)", v.Revoked)
			}
		},
	},

	// DELETE /workspaces/{id}/agents/{accountId} — the agent leaves:
	// memberships stripped, tokens revoked, the feed drops it (the
	// account row stays dormant — attribution survives).
	{
		route: "DELETE /api/v1/workspaces/{id}/agents/{accountId}", method: "DELETE",
		path:      "/api/v1/workspaces/" + rtWS + "/agents/" + rtAgent,
		urlParams: []string{"id", rtWS, "accountId", rtAgent},
		handler:   (*API).handleDeleteAgent,
		pool: &fakePool{
			rules: []fakeRule{
				rtRole("owner"),
				rtMemberRow(rtMemberID, "agent", "active", rtAgent, rtWS, rtTeam),
				{frag: "select kind from accounts where id = $1", rowVals: []any{"agent"}},
			},
			txs: []*fakeTx{{rules: []fakeRule{rtSeq(25)}}},
		},
		code: 200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			var v struct {
				Deleted bool `json:"deleted"`
			}
			decodeBody(t, rec, &v)
			if !v.Deleted {
				t.Fatal("delete = false, want true")
			}
			if !hasSQL(pool.txs[0], "update api_tokens set revoked_at") {
				t.Fatal("the delete did not revoke the agent's tokens")
			}
		},
	},

	// GET /search — the empty query is an empty suggestion list, never
	// an error (the client renders it as nothing).
	{
		route: "GET /api/v1/search", method: "GET",
		path:    "/api/v1/search?workspaceId=" + rtWS + "&query=",
		handler: (*API).handleSearch,
		pool:    &fakePool{rules: []fakeRule{rtRole("member")}},
		code:    200,
		check: func(t *testing.T, rec *httptest.ResponseRecorder, pool *fakePool) {
			if strings.TrimSpace(rec.Body.String()) != `[]` {
				t.Fatalf("search = %s, want the empty array", rec.Body.String())
			}
		},
	},
}

// ---- the tests ------------------------------------------------------------

// TestRouteTableCompleteness pins the route count: the table IS the map
// (chi does not expose its registrations for machine-verified
// completeness). A route added to Mount must be added here; the count
// catches the accidental deletion, and the router walk + the seam
// contract catch the accidental drift in both directions.
func TestRouteTableCompleteness(t *testing.T) {
	const want = 57 // the v1 surface: every route in Mount, one entry each
	if len(rtRouteCases) != want {
		t.Fatalf("the route table holds %d entries, want %d — Mount and the table drifted", len(rtRouteCases), want)
	}
	seen := make(map[string]bool, len(rtRouteCases))
	for _, c := range rtRouteCases {
		if c.handler == nil {
			t.Fatalf("route %q has no handler bound", c.route)
		}
		if seen[c.route] {
			t.Fatalf("route %q appears twice in the table", c.route)
		}
		seen[c.route] = true
	}
}

// TestRouteTableRouterGuard drives the real chi router (Mount, as in
// production) with no auth material and expects the session guard's 401
// on every route: registration and the auth wall, verified together.
func TestRouteTableRouterGuard(t *testing.T) {
	a := &API{
		pool:    &fakePool{},
		log:     discardLogger(),
		cfg:     config.Config{},
		limiter: newAccountRateLimiter(0, 0), // disabled: nothing to bound past the 401
	}
	r := chi.NewRouter()
	a.Mount(r)

	for _, c := range rtRouteCases {
		t.Run(c.route, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "http://x"+c.path, strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			wantError(t, rec, http.StatusUnauthorized, "unauthenticated")
		})
	}
}

// TestRouteTableSeamContracts drives each handler directly over the
// in-memory pool fake and pins the route's primary contract: the door's
// exact client-visible error, or the response shape, or the SQL the
// write must issue.
func TestRouteTableSeamContracts(t *testing.T) {
	for _, c := range rtRouteCases {
		c := c
		t.Run(c.route, func(t *testing.T) {
			pool := c.pool
			if pool == nil {
				pool = &fakePool{}
			}
			pool.bind(t)
			a := apiForTests(t, pool)
			principal := c.principal
			if principal == nil {
				principal = humanPrincipal(rtUser)
			}
			req := requestFor(t, principal, c.method, "http://x"+c.path, c.body, c.urlParams...)
			rec := httptest.NewRecorder()
			c.handler(a, rec, req)
			if c.err != "" {
				wantError(t, rec, c.code, c.err)
				return
			}
			checkStatus(t, rec, c.code)
			if c.check != nil {
				c.check(t, rec, pool)
			}
		})
	}
}
