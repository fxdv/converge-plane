// comments_seam_test.go — the comment mutation contract over in-memory
// fakes (wave 2): the body fence at the wire, the pause guard, the
// reply-parent rule, and the create/update/delete shapes.
package api

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// commentRowVals is one canned comments-table row (7 values, the
// commentAccess scan order). parent may be nil (top-level).
func commentRowVals(id, body, author, issue string, parent any, createdAt, updatedAt time.Time) []any {
	return []any{id, body, author, issue, parent, createdAt, updatedAt}
}

// TestCommentData pins the client IssueComment shape: sourceMetadata is
// always present (null) — the client field is union(string, null)
// without undefined — and a JSON string scalar body unwraps to text.
func TestCommentData(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	d := a.commentData(commentRow{ID: "c1", Body: `"hello"`, AuthorID: "u1", IssueID: "i1"}, "t1", "t2")
	if d["sourceMetadata"] != nil {
		t.Fatalf("sourceMetadata = %v, must be present (null)", d["sourceMetadata"])
	}
	if d["body"] != "hello" {
		t.Fatalf("body = %v, want the unwrapped text", d["body"])
	}
	if d["parentId"] != nil {
		t.Fatalf("parentId = %v, want null for a top-level comment", d["parentId"])
	}
	// A rich-text document rides through verbatim.
	d2 := a.commentData(commentRow{ID: "c2", Body: `{"type":"doc"}`, AuthorID: "u1", IssueID: "i1", ParentID: str("c1")}, "t1", "t2")
	if d2["body"] != `{"type":"doc"}` || d2["parentId"] != "c1" {
		t.Fatalf("body = %v parentId = %v, want the doc verbatim and the parent id", d2["body"], d2["parentId"])
	}
}

// commentCreatePool wires the pool reads of the create path.
func commentCreatePool(t *testing.T, row []any, parentInIssue any) *fakePool {
	t.Helper()
	rules := []fakeRule{
		{frag: "from comments cm where cm.id", rowVals: commentRowVals("c1", `"x"`, "u0", "i1", nil, time.Now(), time.Now())},
		{frag: "from issues i where i.id = $1", rowVals: row},
		{frag: "join workspaces w on w.id = t.workspace_id", rowVals: []any{"ws1"}},
		{frag: "select role from workspace_members", rowVals: []any{"admin"}},
		{frag: "select exists(select 1 from comments where id = $1", rowVals: []any{parentInIssue}},
	}
	return &fakePool{t: t, rules: rules}
}

// commentCreateTx wires the transaction of the create path.
func commentCreateTx(t *testing.T, reloaded []any) *fakeTx {
	t.Helper()
	return &fakeTx{t: t, rules: []fakeRule{
		{frag: "insert into comments (", rowVals: []any{"c9"}},
		{frag: "from comments cm where cm.id", rowVals: reloaded},
		{frag: "insert into sync_sequences", rowVals: []any{int64(41)}},
	}}
}

func TestCreateCommentContract(t *testing.T) {
	humanRow := issueRowFixture(7, "Test", "st2", int(2))
	issue := "11111111-1111-1111-1111-111111111111"
	body := `{"body":"  Hello there  ","issueId":"` + issue + `"}`

	t.Run("unauthenticated 401s", func(t *testing.T) {
		a := apiForTests(t, &fakePool{t: t})
		rec := record(t, a, requestFor(t, nil, "POST", "http://x/api/v1/issue_comments?issueId=i1", `{"body":"x"}`), a.handleCreateComment)
		checkStatus(t, rec, 401)
	})
	t.Run("an empty body 400s", func(t *testing.T) {
		a := apiForTests(t, &fakePool{t: t})
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issue_comments?issueId=i1", `{"body":""}`), a.handleCreateComment)
		wantError(t, rec, 400, "body is required")
	})
	t.Run("an issue outside the workspace 404s", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from issues i where i.id = $1", rowErr: pgx.ErrNoRows},
		}}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issue_comments?issueId=i1", `{"body":"x"}`), a.handleCreateComment)
		wantError(t, rec, 404, "not found")
	})
	t.Run("an agent on a paused issue 422s", func(t *testing.T) {
		row := issueRowFixture(7, "Test", "st2", int(2))
		row[14] = true
		pool := commentCreatePool(t, row, nil)
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, agentPrincipal("ag1"), "POST", "http://x/api/v1/issue_comments?issueId=i1", `{"body":"x"}`), a.handleCreateComment)
		wantError(t, rec, 422, "issue is paused for human review; agents cannot act on it")
	})
	t.Run("a parent outside the issue 422s", func(t *testing.T) {
		pool := commentCreatePool(t, humanRow, false)
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issue_comments?issueId=i1",
			`{"body":"x","parentId":"cX"}`), a.handleCreateComment)
		wantError(t, rec, 422, "parentId is not a comment on this issue")
	})
	t.Run("the happy path fences the body and lands in the outbox", func(t *testing.T) {
		reloaded := commentRowVals("c9", `"  Hello there  "`, "u1", "i1", nil, time.Now(), time.Now())
		pool := commentCreatePool(t, humanRow, nil)
		pool.txs = []*fakeTx{commentCreateTx(t, reloaded)}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issue_comments?issueId=i1", body), a.handleCreateComment), 201)
		tx := pool.txs[0]
		ins := txSQL(tx, "insert into comments (")
		// The plain-text body binds as a JSON string scalar (toJSONB).
		if len(ins) != 1 || ins[0].args[2] != `"  Hello there  "` {
			t.Fatalf("body bound = %v, want the verbatim text as a JSON scalar", ins[0].args)
		}
		var out struct {
			Body       string `json:"body"`
			SourceMeta any    `json:"sourceMetadata"`
			IssueID    string `json:"issueId"`
		}
		decodeBody(t, rec, &out)
		if out.Body != "  Hello there  " || out.SourceMeta != nil {
			t.Fatalf("response = %+v, want the fenced body and a null sourceMetadata", out)
		}
	})
}

// TestUpdateAndDeleteComment pins the remaining verbs.
func TestUpdateAndDeleteComment(t *testing.T) {
	now := time.Now()
	base := commentRowVals("c1", `"old"`, "u1", "i1", nil, now, now)
	issueRow := issueRowFixture(7, "Test", "st2", int(2))

	t.Run("an agent on a paused issue 422s on update", func(t *testing.T) {
		row := issueRowFixture(7, "Test", "st2", int(2))
		row[14] = true
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from comments cm where cm.id", rowVals: base},
			{frag: "from issues i where i.id = $1", rowVals: row},
			{frag: "join workspaces w on w.id = t.workspace_id", rowVals: []any{"ws1"}},
			{frag: "select role from workspace_members", rowVals: []any{"admin"}},
		}}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, agentPrincipal("ag1"), "POST", "http://x/api/v1/issue_comments/c1", `{"body":"x"}`, "id", "c1"), a.handleUpdateComment)
		wantError(t, rec, 422, "issue is paused for human review; agents cannot act on it")
	})
	t.Run("a human update rewrites the body", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from comments cm where cm.id", rowVals: base},
			{frag: "from issues i where i.id = $1", rowVals: issueRow},
			{frag: "join workspaces w on w.id = t.workspace_id", rowVals: []any{"ws1"}},
			{frag: "select role from workspace_members", rowVals: []any{"admin"}},
		}}
		pool.txs = []*fakeTx{{t: t, rules: []fakeRule{
			{frag: "from comments cm where cm.id", rowVals: commentRowVals("c1", `"new"`, "u1", "i1", nil, now, now)},
			{frag: "insert into sync_sequences", rowVals: []any{int64(42)}},
		}}}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST", "http://x/api/v1/issue_comments/c1", `{"body":"new"}`, "id", "c1"), a.handleUpdateComment), 200)
		tx := pool.txs[0]
		upd := txSQL(tx, "update comments set body")
		if len(upd) != 1 {
			t.Fatalf("update writes = %d, want 1", len(upd))
		}
		var out struct {
			Body string `json:"body"`
		}
		decodeBody(t, rec, &out)
		if out.Body != "new" {
			t.Fatalf("response = %+v, want the new body", out)
		}
	})
	t.Run("a delete is a soft delete", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from comments cm where cm.id", rowVals: base},
			{frag: "from issues i where i.id = $1", rowVals: issueRow},
			{frag: "join workspaces w on w.id = t.workspace_id", rowVals: []any{"ws1"}},
			{frag: "select role from workspace_members", rowVals: []any{"admin"}},
		}}
		pool.txs = []*fakeTx{{t: t, rules: []fakeRule{
			{frag: "insert into sync_sequences", rowVals: []any{int64(43)}},
		}}}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "DELETE", "http://x/api/v1/issue_comments/c1", "", "id", "c1"), a.handleDeleteComment), 200)
		var out struct {
			ID string `json:"id"`
		}
		decodeBody(t, rec, &out)
		if out.ID != "c1" {
			t.Fatalf("delete ack = %+v, want the deleted id", out)
		}
		tx := pool.txs[0]
		if !hasSQL(tx, "set status = 'deleted'") {
			t.Fatal("delete must be a soft delete")
		}
		outs := txSQL(tx, "insert into sync_outbox")
		var issueOut *fakeExecCall
		for i := range outs {
			if outs[i].args[2] == "IssueComment" {
				issueOut = &outs[i]
			}
		}
		if issueOut == nil || issueOut.args[4] != "DELETE" {
			t.Fatalf("comment outbox = %+v, want a DELETE record (the client removes it)", issueOut)
		}
	})
	t.Run("replies list the thread", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "from comments cm where cm.id", rowVals: base},
			{frag: "from issues i where i.id = $1", rowVals: issueRow},
			{frag: "join workspaces w on w.id = t.workspace_id", rowVals: []any{"ws1"}},
			{frag: "select role from workspace_members", rowVals: []any{"admin"}},
			{frag: "cm.parent_id = $1 and cm.status = 'active'", rows: [][]any{
				{"c9", now, now, `"reply"`, "u2", "i1", "c1"},
			}},
		}}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "GET", "http://x/api/v1/issue_comments/c1/replies", "", "id", "c1"), a.handleGetCommentReplies), 200)
		var out []map[string]any
		decodeBody(t, rec, &out)
		if len(out) != 1 || out[0]["body"] != "reply" || out[0]["sourceMetadata"] != nil {
			t.Fatalf("replies = %+v, want one reply with a null sourceMetadata", out)
		}
	})
}
