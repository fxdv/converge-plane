// issues_seam_test.go — the issue mutation contract over in-memory
// fakes (wave 2): the 422/400/404/409 families at the door, the write
// shape inside the transaction (the advisory lock, the number, the
// history rows, the outbox refresh), the D1 pause/resume rule, and the
// wire shapes the client's stores validate.
package api

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------
// Pure seams.

func TestToJSONB(t *testing.T) {
	cases := map[string]string{
		"":        `null`,
		`{"a":1}`: `{"a":1}`, // valid JSON (a rich-text doc) stores verbatim
		"plain":   `"plain"`, // anything else becomes a JSON string scalar
	}
	for in, want := range cases {
		if got := toJSONB(in); got != want {
			t.Errorf("toJSONB(%q) = %s, want %s", in, got, want)
		}
	}
	// Text with JSON-hostile bytes must still be valid JSON.
	for _, in := range []string{`quo"tes`, "back\\slash", "line\nbreak", "ctrl\x00char"} {
		if !json.Valid([]byte(toJSONB(in))) {
			t.Errorf("toJSONB(%q) = %s is not valid JSON", in, toJSONB(in))
		}
	}
}

func TestNullAndKeyHelpers(t *testing.T) {
	empty, full := "", "x"
	if nullForEmpty(nil) != nil || nullForEmpty(&empty) != nil || *nullForEmpty(&full) != "x" {
		t.Fatal("nullForEmpty: nil and \"\" bind as NULL, non-empty passes through")
	}
	if ptrOrNull("") != nil || *ptrOrNull("y") != "y" {
		t.Fatal("ptrOrNull: \"\" is nil, else the value")
	}
	if mustJSONArray(nil) != "null" || mustJSONArray([]string{"a", "b"}) != `["a","b"]` {
		t.Fatal("mustJSONArray must render the ids as a JSON array")
	}
	if projectKey([]string{"p1", "p2"}) != "p1" || projectKey(nil) != "" {
		t.Fatal("projectKey collapses the membership to its single v1 value")
	}
}

// TestDescriptionForClientBranches pins the fence branches the wire
// contract test leaves unmarked: an object (a ProseMirror document) and
// an array pass through verbatim, a bare number is not a document, and
// unparseable input degrades to empty.
func TestDescriptionForClientBranches(t *testing.T) {
	cases := map[string]string{
		`{}`:       `{}`,
		`[1,2]`:    `[1,2]`,
		`1`:        "", // a bare number is not a document
		"not json": "",
	}
	for in, want := range cases {
		if got := descriptionForClient(in); got != want {
			t.Errorf("descriptionForClient(%q) = %q, want %q", in, got, want)
		}
	}
}

// issueRowFixture is one canned issues-table row in issueColumns' order
// (19 values; the version that issueRowVals appends is ignored by the
// 19-target scan).
func issueRowFixture(number int, title, statusID string, priority any) []any {
	now := time.Now()
	return []any{
		"i1", "t1", number, priority, 0, title, "{}", "active",
		now, now, "u1", nil, nil, statusID, false,
		[]string{}, []string{}, []string{}, json.RawMessage("[]"),
	}
}

// TestIssueDataShape pins the exact map the client's Issue model
// validates: the arrays are never null, the legacy projectId derives
// from the membership, and the keys the model declares are all present.
func TestIssueDataShape(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	now := time.Now()
	row := issueRow{
		ID: "i1", TeamID: "t1", Number: 7, Title: "Test", DescRaw: `{}`,
		Status:    "active",
		CreatedAt: now, UpdatedAt: now, CreatedByID: str("u1"), StatusID: str("st2"),
		ProjectIds: []string{"p1"}, LabelIDs: []string{"l1"}, Children: []string{},
		RelationRaw: json.RawMessage("[]"),
	}
	got := a.issueData(row)
	stamp := now.Format(iso)
	want := map[string]any{
		"id": "i1", "createdAt": stamp, "updatedAt": stamp, "title": "Test",
		"number": 7, "description": "{}", "priority": (*int)(nil), "dueDate": nil,
		"sortOrder": 0, "estimate": 0, "teamId": "t1", "createdById": "u1",
		"assigneeId": nil, "labelIds": []string{"l1"}, "parentId": nil,
		"stateId": "st2", "subscriberIds": []string{}, "cycleId": nil,
		"projectId": "p1", "projectIds": []string{"p1"}, "projectMilestoneId": nil,
		"sourceMetadata": nil, "children": []string{}, "agentPaused": false,
		"relations": json.RawMessage("[]"),
	}
	for k, v := range want {
		gv, ok := got[k]
		if !ok {
			t.Fatalf("key %q missing from the wire shape", k)
		} else if !reflect.DeepEqual(gv, v) {
			// DeepEqual, never ==: the values include slices, and a
			// == on an interface holding one panics.
			t.Errorf("key %q = %v, want %v", k, gv, v)
		}
	}
	// nil project membership: the legacy field is null, the array empty.
	row2 := row
	row2.ProjectIds = nil
	got2 := a.issueData(row2)
	if got2["projectId"] != nil {
		t.Fatalf("projectId = %v, want null for a projectless issue", got2["projectId"])
	}
	if _, ok := got2["projectIds"].([]string); !ok {
		t.Fatalf("projectIds = %v, want an empty array, never null", got2["projectIds"])
	}
}

func str(v string) *string { return &v }

// TestHistoryDataMatrix pins the activity-row shape per field: the
// client renders from/to pairs, and every nullable pair must be present
// (null when unset) — a missing key fails mobx-state-tree validation.
func TestHistoryDataMatrix(t *testing.T) {
	now := time.Now()
	mk := func(action, field, from, to string) map[string]any {
		return historyData("h1", now, now, str("u1"), "i1", action, field,
			ptrOrNull(from), ptrOrNull(to), nil)
	}
	// Every nullable pair is present and null on the base row.
	base := mk("updated", "status", "s1", "s2")
	for _, k := range []string{"fromPriority", "toPriority", "fromStateId", "toStateId",
		"fromAssigneeId", "toAssigneeId", "fromParentId", "toParentId", "fromEstimate",
		"toEstimate", "addedLabelIds", "removedLabelIds", "relationChanges",
		"sourceMetadata", "summary"} {
		if _, ok := base[k]; !ok {
			t.Fatalf("base history row missing key %q", k)
		}
	}
	if base["fromStateId"] != "s1" || base["toStateId"] != "s2" {
		t.Fatalf("status transition = %v/%v, want s1/s2", base["fromStateId"], base["toStateId"])
	}
	// created stamps the starting status in toStateId only.
	created := mk("created", "status", "", "s1")
	if created["fromStateId"] != nil || created["toStateId"] != "s1" {
		t.Fatalf("created row = %v/%v, want nil/s1", created["fromStateId"], created["toStateId"])
	}
	// assignee and priority pair up their own keys.
	if a := mk("updated", "assignee", "u1", "u2"); a["fromAssigneeId"] != "u1" || a["toAssigneeId"] != "u2" {
		t.Fatalf("assignee row = %v/%v", a["fromAssigneeId"], a["toAssigneeId"])
	}
	if p := mk("updated", "priority", "0", "2"); p["fromPriority"] != 0 || p["toPriority"] != 2 {
		t.Fatalf("priority row = %v/%v, want 0/2", p["fromPriority"], p["toPriority"])
	}
	// labels ride JSON arrays in from/to.
	l := mk("updated", "labels", `["a"]`, `["a","b"]`)
	if rem, add := l["removedLabelIds"], l["addedLabelIds"]; len(rem.([]string)) != 1 || len(add.([]string)) != 2 {
		t.Fatalf("labels row = %v / %v, want the parsed arrays", rem, add)
	}
	// relation edges ride relationChanges.
	r := mk("updated", "relation", "i2", "BLOCKS")
	rc := r["relationChanges"].(map[string]any)
	if rc["relatedIssueId"] != "i2" || rc["type"] != "BLOCKS" || rc["isDeleted"] != false {
		t.Fatalf("relation row = %v", rc)
	}
	// A handoff summary only appears when supplied.
	h := historyData("h1", now, now, str("u1"), "i1", "updated", "status",
		nil, str("s2"), str("parked: waiting on infra"))
	if h["summary"] != "parked: waiting on infra" {
		t.Fatalf("summary = %v, want the handoff note", h["summary"])
	}
}

// ---------------------------------------------------------------------
// handleCreateIssue.

// createIssuePool wires the pool reads of the create path.
func createIssuePool(t *testing.T, wsForTeam string, roleErr error, statusOK any, wakeVals []any) *fakePool {
	t.Helper()
	return &fakePool{t: t, rules: []fakeRule{
		{frag: "join workspaces w on w.id = t.workspace_id", rowVals: []any{wsForTeam}},
		{frag: "select role from workspace_members", rowVals: []any{"owner"}, rowErr: roleErr},
		{frag: "exists(select 1 from workflow_statuses", rowVals: []any{statusOK}},
		{frag: "join accounts a2", rowVals: wakeVals},
	}}
}

// createIssueTx wires the transaction of the create path; number is the
// next team number the insert will claim.
func createIssueTx(t *testing.T, number int, reloadRow []any) *fakeTx {
	t.Helper()
	return &fakeTx{t: t, rules: []fakeRule{
		{frag: "coalesce(max(number), 0) + 1", rowVals: []any{number}},
		{frag: "insert into issues (", rowVals: []any{"i1", time.Now()}},
		{frag: "from issues i where i.id = $1", rowVals: reloadRow},
		{frag: "insert into issue_history", rowVals: []any{"h1"}},
		{frag: "created_at from issue_history", rowVals: []any{time.Now()}},
		{frag: "insert into sync_sequences", rowVals: []any{int64(11)}},
	}}
}

func TestCreateIssueContract(t *testing.T) {
	body := `{"title":"  Test issue  ","teamId":"11111111-1111-1111-1111-111111111111","stateId":"st2","priority":0}` // teamId must be a canonical UUID
	happy := issueRowFixture(3, "Test issue", "st2", int(0))

	t.Run("unauthenticated", func(t *testing.T) {
		a := apiForTests(t, &fakePool{t: t})
		rec := record(t, a, requestFor(t, nil, "POST", "http://x/api/v1/issues", body), a.handleCreateIssue)
		checkStatus(t, rec, 401)
	})
	t.Run("malformed body 400s", func(t *testing.T) {
		a := apiForTests(t, &fakePool{t: t})
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues", `{`), a.handleCreateIssue)
		wantError(t, rec, 400, "invalid request body")
	})
	t.Run("missing team 400s", func(t *testing.T) {
		a := apiForTests(t, &fakePool{t: t})
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues", `{"title":"x"}`), a.handleCreateIssue)
		wantError(t, rec, 400, "teamId is required")
	})
	t.Run("an oversized title 422s", func(t *testing.T) {
		long := `{"title":"` + strings.Repeat("x", 256) + `","teamId":"11111111-1111-1111-1111-111111111111"}`
		a := apiForTests(t, &fakePool{t: t})
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues", long), a.handleCreateIssue)
		wantError(t, rec, 422, "title is required (1-255 chars)")
	})
	t.Run("a team in a foreign workspace 404s", func(t *testing.T) {
		// The team resolves to ws2; the principal is not a member there:
		// 404 hides the team's existence.
		a := apiForTests(t, createIssuePool(t, "ws2", pgx.ErrNoRows, nil, nil))
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues", body), a.handleCreateIssue)
		wantError(t, rec, 404, "not found")
	})
	t.Run("a non-member 404s", func(t *testing.T) {
		a := apiForTests(t, createIssuePool(t, "ws1", pgx.ErrNoRows, nil, nil))
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues", body), a.handleCreateIssue)
		wantError(t, rec, 404, "not found")
	})
	t.Run("a status outside the workflow 422s", func(t *testing.T) {
		a := apiForTests(t, createIssuePool(t, "ws1", nil, false, nil))
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues", body), a.handleCreateIssue)
		wantError(t, rec, 422, "stateId must be one of the team workflow statuses")
	})
	t.Run("the happy path writes and wakes", func(t *testing.T) {
		pool := createIssuePool(t, "ws1", nil, true, []any{nil})
		pool.txs = []*fakeTx{createIssueTx(t, 3, happy)}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues", body), a.handleCreateIssue)
		checkStatus(t, rec, 201)

		tx := pool.txs[0]
		// The insert binds the trimmed title, the jsonb-fenced description,
		// the client's priority verbatim (0 = "no explicit priority"), and
		// the validated status.
		ins := txSQL(tx, "insert into issues (")
		if len(ins) != 1 {
			t.Fatalf("issue inserts = %d, want 1", len(ins))
		}
		args := ins[0].args
		if args[2] != "Test issue" {
			t.Fatalf("title bound = %q, want the trimmed value", args[2])
		}
		// The client's priority verbatim: 0 = "no explicit priority".
		// It binds as a *int (the column is nullable), never a scalar.
		if pr, _ := args[5].(*int); pr == nil || *pr != 0 {
			t.Fatalf("priority bound = %v, want 0 (the client's value verbatim)", args[5])
		}
		// The history records the creation into the starting status.
		hist := txSQL(tx, "insert into issue_history")
		if len(hist) != 1 || hist[0].args[4] != "created" || hist[0].args[5] != "status" {
			t.Fatalf("history = %+v, want the created/status row", hist)
		}
		// Two outbox rows (the Issue and its IssueHistory); find the
		// Issue one by its model. The fake claims the same sequence for
		// both; the real upsert increments.
		outs := txSQL(tx, "insert into sync_outbox")
		var issueOut *fakeExecCall
		for i := range outs {
			if outs[i].args[2] == "Issue" {
				issueOut = &outs[i]
			}
		}
		if issueOut == nil || issueOut.args[1] != int64(11) || issueOut.args[4] != "CREATE" {
			t.Fatalf("issue outbox = %+v, want seq 11 / CREATE", issueOut)
		}
		// The wake is the D3 fast path: an agent assignee is work.
		if !poolExecutedQuery(pool, "join accounts a2") {
			t.Fatal("the create path must probe the assignee for a runtime wake")
		}
		// The response speaks the client's vocabulary.
		var out2 struct {
			Title       string `json:"title"`
			Number      int    `json:"number"`
			StateID     string `json:"stateId"`
			Priority    *int   `json:"priority"`
			AgentPaused bool   `json:"agentPaused"`
		}
		decodeBody(t, rec, &out2)
		if out2.Title != "Test issue" || out2.Number != 3 || out2.StateID != "st2" || out2.Priority == nil || *out2.Priority != 0 {
			t.Fatalf("response = %+v, want the freshly created issue", out2)
		}
	})
}

// ---------------------------------------------------------------------
// handleUpdateIssue.

// updateIssuePool wires the pool reads of the update path for one known
// row; extra appends the per-field validation rules a subtest needs.
func updateIssuePool(t *testing.T, row []any, extra ...fakeRule) *fakePool {
	t.Helper()
	rules := []fakeRule{
		{frag: "from issues i where i.id = $1", rowVals: row},
		{frag: "join workspaces w on w.id = t.workspace_id", rowVals: []any{"ws1"}},
		{frag: "select role from workspace_members", rowVals: []any{"admin"}},
		{frag: "join accounts a2", rowVals: []any{nil}}, // the post-commit wake probe
	}
	rules = append(rules, extra...)
	return &fakePool{t: t, rules: rules}
}

// updateIssueTx wires the transaction of the update path.
func updateIssueTx(t *testing.T, reloadRow []any) *fakeTx {
	t.Helper()
	return &fakeTx{t: t, rules: []fakeRule{
		{frag: "from issues i where i.id = $1", rowVals: reloadRow},
		{frag: "insert into issue_history", rowVals: []any{"h1"}},
		{frag: "created_at from issue_history", rowVals: []any{time.Now()}},
		{frag: "insert into sync_sequences", rowVals: []any{int64(12)}},
		{frag: "coalesce((select array_agg(label_id)", rowVals: []any{[]string{}}},
	}}
}

func TestUpdateIssueContract(t *testing.T) {
	base := issueRowFixture(7, "Test", "st2", int(2))

	t.Run("an unknown issue 404s", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from issues i where i.id = $1", rowErr: pgx.ErrNoRows},
		}}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues/i1", `{"title":"x"}`, "id", "i1"), a.handleUpdateIssue)
		wantError(t, rec, 404, "not found")
	})
	t.Run("an agent on a paused issue 422s", func(t *testing.T) {
		row := issueRowFixture(7, "Test", "st2", int(2))
		row[14] = true // AgentPaused
		a := apiForTests(t, updateIssuePool(t, row))
		rec := record(t, a, requestFor(t, agentPrincipal("ag1"), "POST", "http://x/api/v1/issues/i1", `{"title":"x"}`, "id", "i1"), a.handleUpdateIssue)
		wantError(t, rec, 422, "issue is paused for human review; agents cannot act on it")
	})
	t.Run("an agent on a Human Review card 422s", func(t *testing.T) {
		pool := updateIssuePool(t, base, fakeRule{
			frag: "lower(ws.name)", rowVals: []any{true},
		})
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, agentPrincipal("ag1"), "POST", "http://x/api/v1/issues/i1", `{"title":"x"}`, "id", "i1"), a.handleUpdateIssue)
		wantError(t, rec, 422, "issue is in Human Review; agents cannot act on it")
	})
	t.Run("a status outside the workflow 422s", func(t *testing.T) {
		pool := updateIssuePool(t, base, fakeRule{
			frag: "exists(select 1 from workflow_statuses", rowVals: []any{false},
		})
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues/i1", `{"stateId":"stX"}`, "id", "i1"), a.handleUpdateIssue)
		wantError(t, rec, 422, "stateId must be one of the team workflow statuses")
	})
	t.Run("an assignee outside the workspace 422s", func(t *testing.T) {
		pool := updateIssuePool(t, base, fakeRule{
			frag: "exists(select 1 from workspace_members", rowVals: []any{false},
		})
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues/i1", `{"assigneeId":"u2"}`, "id", "i1"), a.handleUpdateIssue)
		wantError(t, rec, 422, "assigneeId is not a workspace member")
	})
	t.Run("a parent outside the workspace 422s", func(t *testing.T) {
		pool := updateIssuePool(t, base, fakeRule{
			frag: "where i.id = $1 and t.workspace_id = $2", rowVals: []any{false},
		})
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues/i1", `{"parentId":"i2"}`, "id", "i1"), a.handleUpdateIssue)
		wantError(t, rec, 422, "parentId is not in this workspace")
	})
	t.Run("a label outside the workspace 422s", func(t *testing.T) {
		pool := updateIssuePool(t, base, fakeRule{
			frag: "exists(select 1 from labels", rowVals: []any{false},
		})
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues/i1", `{"labelIds":["l2"]}`, "id", "i1"), a.handleUpdateIssue)
		wantError(t, rec, 422, "labelIds contains a label outside this workspace")
	})
	t.Run("two projects 422s", func(t *testing.T) {
		pool := updateIssuePool(t, base)
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues/i1", `{"projectIds":["p1","p2"]}`, "id", "i1"), a.handleUpdateIssue)
		wantError(t, rec, 422, "v1 supports a single project per issue")
	})
	t.Run("a relation to self 409s", func(t *testing.T) {
		// The id must be a canonical UUID (isUUID runs first); the
		// self-test compares it against the related id.
		id := "11111111-1111-1111-1111-111111111111"
		pool := updateIssuePool(t, base)
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues/"+id,
			`{"issueRelation":{"relatedIssueId":"`+id+`","type":"RELATED"}}`, "id", id), a.handleUpdateIssue)
		wantError(t, rec, 409, "an issue cannot be related to itself")
	})
	t.Run("a disallowed relation type 422s", func(t *testing.T) {
		// PARENT is a reserved word: parent edges ride parentId, not
		// issueRelation (a second hierarchy would fork the tree).
		id := "11111111-1111-1111-1111-111111111111"
		related := "22222222-2222-2222-2222-222222222222"
		pool := updateIssuePool(t, base, fakeRule{
			frag: "where i.id = $1 and t.workspace_id = $2", rowVals: []any{true},
		})
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues/"+id,
			`{"issueRelation":{"relatedIssueId":"`+related+`","type":"PARENT"}}`, "id", id), a.handleUpdateIssue)
		wantError(t, rec, 422, "parent relations use parentId, not issueRelation")
	})
	t.Run("a title change writes, broadcasts, resumes", func(t *testing.T) {
		row := issueRowFixture(7, "Test", "st2", int(2))
		row[14] = true // parked: the human mutation must resume it
		reload := issueRowFixture(7, "New title", "st2", int(2))
		pool := updateIssuePool(t, row)
		pool.txs = []*fakeTx{updateIssueTx(t, reload)}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues/i1", `{"title":"  New title  "}`, "id", "i1"), a.handleUpdateIssue)
		checkStatus(t, rec, 200)

		tx := pool.txs[0]
		if up := txSQL(tx, "update issues set title"); len(up) != 1 || up[0].args[1] != "New title" {
			t.Fatalf("title update = %+v, want the trimmed value", up)
		}
		// D1: a human mutation on a paused issue resumes the swarm.
		if !hasSQL(tx, "set agent_paused = false") {
			t.Fatal("the human mutation must clear the pause flag (swarm resumes)")
		}
		if !hasSQL(tx, "insert into sync_outbox") {
			t.Fatal("the change must land in the outbox (the delta's source)")
		}
		if tx.commitCount != 1 {
			t.Fatalf("commits = %d, want exactly one", tx.commitCount)
		}
		if !poolExecutedQuery(pool, "join accounts a2") {
			t.Fatal("the mutation path must probe for a runtime wake")
		}
	})
	t.Run("an identical patch is a no-op commit", func(t *testing.T) {
		row := issueRowFixture(7, "Test", "st2", int(2))
		pool := updateIssuePool(t, row)
		pool.txs = []*fakeTx{updateIssueTx(t, row)}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues/i1",
			`{"title":"Test","priority":2}`, "id", "i1"), a.handleUpdateIssue)
		checkStatus(t, rec, 200)
		tx := pool.txs[0]
		if hasSQL(tx, "update issues") {
			t.Fatalf("an identical patch wrote: %v", tx.execs)
		}
		if hasSQL(tx, "insert into sync_outbox") {
			t.Fatal("a no-op must not emit an outbox record (a phantom delta)")
		}
		if tx.commitCount != 1 {
			t.Fatalf("commits = %d, want one (the empty transaction closes)", tx.commitCount)
		}
	})
}

// ---------------------------------------------------------------------
// applyIssuePatchTx and sameLabelSet, direct over the fake tx.

func TestApplyIssuePatchTx(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	ctx := context.Background()
	p := humanPrincipal("u1")
	mkTx := func(row issueRow) *fakeTx {
		return &fakeTx{t: t, rules: []fakeRule{
			{frag: "insert into issue_history", rowVals: []any{"h1"}},
			{frag: "created_at from issue_history", rowVals: []any{time.Now()}},
			{frag: "insert into sync_sequences", rowVals: []any{int64(1)}},
			{frag: "coalesce((select array_agg(label_id)", rowVals: []any{row.LabelIDs}},
		}}
	}
	t.Run("a no-op patch reports nothing changed", func(t *testing.T) {
		row := issueRow{ID: "i1", TeamID: "t1", Title: "Same", StatusID: str("st2"), Priority: intPtr(2), LabelIDs: []string{"l1"}}
		tx := mkTx(row)
		changed, recs, err := a.applyIssuePatchTx(ctx, tx, p, "ws1", row, issueRequest{
			Title: str("Same"), Priority: intPtr(2), StateID: str("st2"), LabelIDs: []string{"l1"},
		})
		if err != nil || changed || len(recs) != 0 || len(tx.execs) != 0 {
			t.Fatalf("changed = %v recs = %d execs = %d err = %v, want an untouched row", changed, len(recs), len(tx.execs), err)
		}
	})
	t.Run("a priority move from null records both ends", func(t *testing.T) {
		row := issueRow{ID: "i1", TeamID: "t1", Title: "x"}
		tx := mkTx(row)
		changed, recs, err := a.applyIssuePatchTx(ctx, tx, p, "ws1", row, issueRequest{Priority: intPtr(1)})
		if err != nil || !changed || len(recs) != 1 {
			t.Fatalf("changed = %v recs = %d err = %v", changed, len(recs), err)
		}
		hist := txSQL(tx, "insert into issue_history")
		if hist[0].args[6] != "" || hist[0].args[7] != "1" {
			t.Fatalf("priority history from/to = %q/%q, want empty/1", hist[0].args[6], hist[0].args[7])
		}
	})
	t.Run("a human mutation resumes a paused issue", func(t *testing.T) {
		row := issueRow{ID: "i1", TeamID: "t1", Title: "old", AgentPaused: true}
		tx := mkTx(row)
		_, _, err := a.applyIssuePatchTx(ctx, tx, p, "ws1", row, issueRequest{Title: str("new")})
		if err != nil {
			t.Fatal(err)
		}
		if !hasSQL(tx, "set agent_paused = false") {
			t.Fatal("the resume write is missing (D1 escalation recovery)")
		}
		// The function takes the row by value, so the flag flip is
		// internal to it; the handler re-reads the row. The write is
		// the contract the test pins.
	})
}

// TestSameLabelSet pins the set-equality the patch uses: order- and
// duplicate-insensitive, size-first.
func TestSameLabelSet(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	tx := func(current []string) *fakeTx {
		return &fakeTx{t: t, rules: []fakeRule{
			{frag: "coalesce((select array_agg(label_id)", rowVals: []any{current}},
		}}
	}
	cases := []struct {
		name     string
		current  []string
		next     []string
		wantSame bool
	}{
		{"same order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"shuffled", []string{"a", "b"}, []string{"b", "a"}, true},
		{"added", []string{"a"}, []string{"a", "b"}, false},
		{"swapped", []string{"a", "b"}, []string{"a", "c"}, false},
		{"cleared", []string{"a"}, nil, false},
	}
	for _, c := range cases {
		same, err := a.sameLabelSet(context.Background(), tx(c.current), "i1", c.next)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if same != c.wantSame {
			t.Errorf("%s: same = %v, want %v", c.name, same, c.wantSame)
		}
	}
}

// ---------------------------------------------------------------------
// Delete and subscribe.

func TestDeleteAndSubscribe(t *testing.T) {
	row := issueRowFixture(7, "Test", "st2", int(2))
	tx := &fakeTx{t: t, rules: []fakeRule{
		{frag: "insert into issue_history", rowVals: []any{"h1"}},
		{frag: "created_at from issue_history", rowVals: []any{time.Now()}},
		{frag: "insert into sync_sequences", rowVals: []any{int64(13)}},
	}}
	pool := updateIssuePool(t, row)
	pool.txs = []*fakeTx{tx}
	a := apiForTests(t, pool)

	t.Run("delete soft-deletes and records it", func(t *testing.T) {
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "DELETE", "http://x/api/v1/issues/i1", "", "id", "i1"), a.handleDeleteIssue)
		checkStatus(t, rec, 200)
		if !hasSQL(tx, "set status = 'deleted'") {
			t.Fatal("delete must be a soft delete (the status column is the tombstone)")
		}
		// Two outbox rows: the IssueHistory (the delete row) and the
		// Issue DELETE. Find the Issue one by its model, not its index.
		outs := txSQL(tx, "insert into sync_outbox")
		var issueOut *fakeExecCall
		for i := range outs {
			if outs[i].args[2] == "Issue" {
				issueOut = &outs[i]
			}
		}
		if issueOut == nil || issueOut.args[4] != "DELETE" {
			t.Fatalf("issue outbox action = %v, want DELETE (the client removes the object)", issueOut)
		}
	})
	t.Run("subscribe acknowledges with the current object", func(t *testing.T) {
		pool2 := updateIssuePool(t, row)
		a2 := apiForTests(t, pool2)
		rec := record(t, a2, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issues/i1/subscribe", "", "id", "i1"), a.handleSubscribeIssue)
		checkStatus(t, rec, 200)
		var body struct {
			Title string `json:"title"`
		}
		decodeBody(t, rec, &body)
		if body.Title != "Test" {
			t.Fatalf("body = %+v, want the current issue", body)
		}
	})
}
