// sync_seam_test.go — the sync engine's contract over in-memory fakes
// (wave 2): the wire action vocabulary, the role mapping, the
// handleSync access rules, bootstrap's dedupe and null-array contract,
// delta's watermark/filter/stale matrix, the outbox collector's model
// filter, and the transactional writers (sequence claim, outbox row,
// refresh) — the shape every mutation path depends on.
package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestWireAction pins the internal→client action vocabulary. The client
// switches on exactly I|U|D; anything else is silently dropped, which
// would make a committed mutation invisible in the UI.
func TestWireAction(t *testing.T) {
	cases := map[string]string{"CREATE": "I", "UPDATE": "U", "DELETE": "D", "NOPE": "NOPE"}
	for in, want := range cases {
		if got := wireAction(in); got != want {
			t.Errorf("wireAction(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestClientRole pins the role mapping that keeps the client's strict
// Role enum happy: owner/admin collapse to ADMIN, agent maps to AGENT,
// everything else (member, "") to USER. "OWNER" must never cross the
// wire (it crashed mobx-state-tree in the browser).
func TestClientRole(t *testing.T) {
	cases := map[string]string{
		"owner": "ADMIN", "Owner": "ADMIN", "admin": "ADMIN",
		"agent": "AGENT", "Agent": "AGENT",
		"member": "USER", "": "USER", "something": "USER",
	}
	for in, want := range cases {
		if got := clientRole(in); got != want {
			t.Errorf("clientRole(%q) = %q, want %q", in, got, want)
		}
	}
}

// teamRowVals is one canned team row in teamColumns' order.
func teamRowVals(id, workspaceID string) []any {
	now := time.Now()
	return []any{id, "Engineering", "ENG", "active", workspaceID, now, now, []byte("{}")}
}

// syncHandlerPool wires the pool queries handleSync issues.
func syncHandlerPool(t *testing.T, roleErr error, serverSeq int64, outboxRows [][]any, oldestVals []any) *fakePool {
	t.Helper()
	rules := []fakeRule{
		{frag: "select role from workspace_members", rowVals: []any{"admin"}, rowErr: roleErr},
		{frag: "coalesce((select last_sequence", rowVals: []any{serverSeq}},
	}
	if outboxRows != nil {
		rules = append(rules, fakeRule{frag: "sequence_id > $2", rows: outboxRows})
	}
	if oldestVals != nil {
		rules = append(rules, fakeRule{frag: "min(sequence_id)", rowVals: oldestVals})
	}
	return &fakePool{t: t, rules: rules}
}

// TestSyncHandlerAccess pins the tenant rules at the door: no principal
// 401s, no workspaceId 400s, a foreign workspace 404s (existence is
// hidden: 404, never 403).
func TestSyncHandlerAccess(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		a := apiForTests(t, &fakePool{t: t})
		rec := record(t, a, requestFor(t, nil, "GET", "http://x/api/v1/sync_actions/bootstrap?workspaceId=ws1", ""), a.handleSync)
		checkStatus(t, rec, 401)
	})
	t.Run("missing workspace", func(t *testing.T) {
		a := apiForTests(t, &fakePool{t: t})
		req := requestFor(t, humanPrincipal("u1"), "GET", "http://x/api/v1/sync_actions/bootstrap", "")
		rec := record(t, a, req, a.handleSync)
		wantError(t, rec, 400, "workspaceId is required")
	})
	t.Run("foreign workspace 404s", func(t *testing.T) {
		pool := syncHandlerPool(t, pgx.ErrNoRows, 0, nil, nil)
		a := apiForTests(t, pool)
		req := requestFor(t, humanPrincipal("u1"), "GET", "http://x/api/v1/sync_actions/bootstrap?workspaceId=ws1", "")
		rec := record(t, a, req, a.handleSync)
		wantError(t, rec, 404, "not found")
	})
}

// TestSyncBootstrap pins the bootstrap contract: the model list is
// deduplicated (a duplicate model is collected once), unknown models
// yield no records and no error, the array is never null, and the
// watermark reported is the server's (never the client's).
func TestSyncBootstrap(t *testing.T) {
	issue := []any{
		"i1", "t1", 7, 2, 3, "Test", `"{}"`, "active",
		time.Now(), time.Now(), "u1", "ag1", nil, "st2", false,
		[]string{}, []string{}, []string{}, json.RawMessage("[]"),
	}
	team := teamRowVals("t1", "ws1")
	pool := &fakePool{t: t, rules: []fakeRule{
		{frag: "select role from workspace_members", rowVals: []any{"admin"}},
		{frag: "coalesce((select last_sequence", rowVals: []any{int64(42)}},
		{frag: "i.status <> 'deleted'", rows: [][]any{issue}},
		{frag: "from teams where workspace_id = $1 and status = 'active'", rows: [][]any{team}},
	}}
	a := apiForTests(t, pool)
	req := requestFor(t, humanPrincipal("u1"), "GET",
		"http://x/api/v1/sync_actions/bootstrap?workspaceId=ws1&modelNames=Issue,%20Issue%20,Team,Nope", "")
	rec := checkStatus(t, record(t, a, req, a.handleSync), 200)

	// The array is always present (null crashed the client's iteration).
	if !strings.Contains(rec.Body.String(), `"syncActions":[`) {
		t.Fatalf("syncActions is not a JSON array: %s", rec.Body.String())
	}
	var resp struct {
		SyncActions    []syncActionRecord `json:"syncActions"`
		LastSequenceID string             `json:"lastSequenceId"`
		Stale          bool               `json:"stale"`
	}
	decodeBody(t, rec, &resp)
	if len(resp.SyncActions) != 2 {
		t.Fatalf("records = %d, want 2 (Issue + Team; duplicates and unknown models collapse)", len(resp.SyncActions))
	}
	byModel := map[string]syncActionRecord{}
	for _, r := range resp.SyncActions {
		byModel[r.ModelName] = r
	}
	iss, ok := byModel["Issue"]
	if !ok || iss.ModelID != "i1" || iss.Action != "I" || iss.SequenceID != "1" {
		t.Fatalf("issue record = %+v, want i1/I/1", iss)
	}
	tea, ok := byModel["Team"]
	if !ok || tea.ModelID != "t1" || tea.Action != "I" {
		t.Fatalf("team record = %+v, want t1/I", tea)
	}
	if resp.LastSequenceID != "42" {
		t.Fatalf("lastSequenceId = %q, want the server's 42", resp.LastSequenceID)
	}
	if resp.Stale {
		t.Fatal("a bootstrap is never stale")
	}
	// The team record's data speaks the client's vocabulary.
	var teamData map[string]any
	if err := json.Unmarshal(tea.Data, &teamData); err != nil {
		t.Fatalf("team data is not JSON: %v", err)
	}
	if teamData["identifier"] != "ENG" || teamData["workspaceId"] != "ws1" {
		t.Fatalf("team data = %v, want the client's Team shape", teamData)
	}
}

// TestSyncDelta pins the delta contract: the watermark (the client's
// lastSequenceId, falling back to the server's), the model filter, and
// the stale rule — a delta that reaches behind the outbox's retained
// window is incomplete and the client must re-bootstrap.
func TestSyncDelta(t *testing.T) {
	outbox := [][]any{
		{int64(6), "Issue", "i1", "UPDATE", json.RawMessage(`{"id":"i1"}`)},
		{int64(7), "Team", "t1", "CREATE", json.RawMessage(`{"id":"t1"}`)},
	}
	t.Run("empty outbox with a live watermark is stale", func(t *testing.T) {
		pool := syncHandlerPool(t, nil, 42, [][]any{}, []any{nil})
		a := apiForTests(t, pool)
		req := requestFor(t, humanPrincipal("u1"), "GET",
			"http://x/api/v1/sync_actions/delta?workspaceId=ws1&lastSequenceId=5", "")
		rec := checkStatus(t, record(t, a, req, a.handleSync), 200)
		var resp struct {
			SyncActions []syncActionRecord `json:"syncActions"`
			Stale       bool               `json:"stale"`
		}
		decodeBody(t, rec, &resp)
		if len(resp.SyncActions) != 0 {
			t.Fatalf("records = %d, want 0", len(resp.SyncActions))
		}
		if !resp.Stale {
			t.Fatal("an empty retained window behind the watermark must be stale (the client re-bootstraps)")
		}
		if !strings.Contains(rec.Body.String(), `"syncActions":[]`) {
			t.Fatalf("the empty delta must serialize an empty array, not null: %s", rec.Body.String())
		}
	})
	t.Run("a gap in the window is stale; the filter applies", func(t *testing.T) {
		pool := syncHandlerPool(t, nil, 42, outbox, []any{int64(6)})
		a := apiForTests(t, pool)
		req := requestFor(t, humanPrincipal("u1"), "GET",
			"http://x/api/v1/sync_actions/delta?workspaceId=ws1&lastSequenceId=5&modelNames=Issue", "")
		rec := checkStatus(t, record(t, a, req, a.handleSync), 200)
		var resp struct {
			SyncActions []syncActionRecord `json:"syncActions"`
			Stale       bool               `json:"stale"`
		}
		decodeBody(t, rec, &resp)
		if len(resp.SyncActions) != 1 || resp.SyncActions[0].ModelName != "Issue" {
			t.Fatalf("records = %+v, want only the Issue", resp.SyncActions)
		}
		if !resp.Stale {
			t.Fatal("the oldest retained record (6) is newer than the watermark (5): the delta cannot be complete")
		}
	})
	t.Run("a full window is not stale", func(t *testing.T) {
		pool := syncHandlerPool(t, nil, 42, outbox, []any{int64(3)})
		a := apiForTests(t, pool)
		req := requestFor(t, humanPrincipal("u1"), "GET",
			"http://x/api/v1/sync_actions/delta?workspaceId=ws1&lastSequenceId=5", "")
		rec := checkStatus(t, record(t, a, req, a.handleSync), 200)
		var resp struct {
			SyncActions []syncActionRecord `json:"syncActions"`
			Stale       bool               `json:"stale"`
		}
		decodeBody(t, rec, &resp)
		if len(resp.SyncActions) != 2 {
			t.Fatalf("records = %d, want 2 (no model filter)", len(resp.SyncActions))
		}
		if resp.Stale {
			t.Fatal("the watermark sits inside the retained window: not stale")
		}
		if resp.SyncActions[0].Action != "U" || resp.SyncActions[1].Action != "I" {
			t.Fatalf("actions = %v %v, want U then I", resp.SyncActions[0].Action, resp.SyncActions[1].Action)
		}
	})
	t.Run("a malformed watermark re-anchors and reports stale when the window moved on", func(t *testing.T) {
		// The client says "abc"; the server re-anchors on its own 42.
		// The retained window starts at 43 — a client with an unparseable
		// cursor is assumed to hold the anchor, and the window behind it
		// has moved on, so the delta is reported stale: safe (one wasted
		// bootstrap), never incomplete.
		rows := [][]any{{int64(43), "Issue", "i2", "UPDATE", json.RawMessage(`{"id":"i2"}`)}}
		pool := syncHandlerPool(t, nil, 42, rows, []any{int64(43)})
		a := apiForTests(t, pool)
		req := requestFor(t, humanPrincipal("u1"), "GET",
			"http://x/api/v1/sync_actions/delta?workspaceId=ws1&lastSequenceId=abc", "")
		rec := checkStatus(t, record(t, a, req, a.handleSync), 200)
		var resp struct {
			SyncActions    []syncActionRecord `json:"syncActions"`
			LastSequenceID string             `json:"lastSequenceId"`
			Stale          bool               `json:"stale"`
		}
		decodeBody(t, rec, &resp)
		if len(resp.SyncActions) != 1 || resp.SyncActions[0].ModelID != "i2" {
			t.Fatalf("records = %+v, want the record newer than the server anchor", resp.SyncActions)
		}
		if resp.LastSequenceID != "42" || !resp.Stale {
			t.Fatalf("lastSequenceId = %q stale = %v, want the server's 42 anchor and a stale report", resp.LastSequenceID, resp.Stale)
		}
	})
	t.Run("a malformed watermark is not stale while the window still covers it", func(t *testing.T) {
		// Same re-anchoring, but the outbox still retains records behind
		// the anchor (oldest 41 <= 42): the delta is complete, no
		// bootstrap tax.
		rows := [][]any{{int64(43), "Issue", "i2", "UPDATE", json.RawMessage(`{"id":"i2"}`)}}
		pool := syncHandlerPool(t, nil, 42, rows, []any{int64(41)})
		a := apiForTests(t, pool)
		req := requestFor(t, humanPrincipal("u1"), "GET",
			"http://x/api/v1/sync_actions/delta?workspaceId=ws1&lastSequenceId=abc", "")
		rec := checkStatus(t, record(t, a, req, a.handleSync), 200)
		var resp struct {
			SyncActions []syncActionRecord `json:"syncActions"`
			Stale       bool               `json:"stale"`
		}
		decodeBody(t, rec, &resp)
		if len(resp.SyncActions) != 1 || resp.Stale {
			t.Fatalf("records = %+v stale = %v, want the complete delta without a bootstrap tax", resp.SyncActions, resp.Stale)
		}
	})
}

// TestCollectOutbox pins the collector's model filter and the record
// shape: an empty model list means no filter; the sequence is the
// decimal string the client compares as a number.
func TestCollectOutbox(t *testing.T) {
	rows := [][]any{
		{int64(6), "Issue", "i1", "UPDATE", json.RawMessage(`{"id":"i1"}`)},
		{int64(7), "Team", "t1", "CREATE", json.RawMessage(`{"id":"t1"}`)},
		{int64(8), "Issue", "i2", "DELETE", json.RawMessage(`{"id":"i2"}`)},
	}
	a := apiForTests(t, &fakePool{t: t, rules: []fakeRule{
		{frag: "sequence_id > $2", rows: rows},
	}})
	ctx := context.Background()

	all, err := a.collectOutbox(ctx, "ws1", 5, "")
	if err != nil || len(all) != 3 {
		t.Fatalf("no filter: %d records (%v), want 3", len(all), err)
	}
	issues, err := a.collectOutbox(ctx, "ws1", 5, "Issue")
	if err != nil || len(issues) != 2 {
		t.Fatalf("Issue filter: %d records (%v), want 2", len(issues), err)
	}
	if issues[0].SequenceID != "6" || issues[0].Action != "U" || issues[1].Action != "D" {
		t.Fatalf("records = %+v, want seq 6/U and D", issues)
	}
	if issues[0].WorkspaceID != "ws1" {
		t.Fatalf("workspace = %q, want ws1 stamped on every record", issues[0].WorkspaceID)
	}
}

// TestEmitChange pins the outbox writer inside a transaction: one
// sequence per record (the atomic upsert claim), the wire action, and
// the NULL binding of an empty model id (the nullif-free *string trick).
func TestEmitChange(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	tx := &fakeTx{t: t, rules: []fakeRule{
		{frag: "insert into sync_sequences", rowVals: []any{int64(7)}},
	}}
	ctx := context.Background()

	rec, err := a.emitChange(ctx, tx, "ws1", "Issue", "i1", "CREATE", map[string]any{"id": "i1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.SequenceID != "7" || rec.ModelName != "Issue" || rec.ModelID != "i1" || rec.Action != "I" {
		t.Fatalf("record = %+v, want 7/Issue/i1/I", rec)
	}
	if string(rec.Data) != `{"id":"i1"}` {
		t.Fatalf("data = %s, want the marshaled payload", rec.Data)
	}
	outbox := txSQL(tx, "insert into sync_outbox")
	if len(outbox) != 1 {
		t.Fatalf("outbox writes = %d, want 1", len(outbox))
	}
	args := outbox[0].args
	if len(args) != 6 || args[0] != "ws1" || args[1] != int64(7) || args[2] != "Issue" || args[4] != "CREATE" {
		t.Fatalf("outbox args = %v, want ws1/7/Issue/…/CREATE", args)
	}
	if args[3] == nil {
		t.Fatal("a non-empty model id must bind, not NULL")
	}

	// An empty model id (a row-less event) binds as SQL NULL.
	tx2 := &fakeTx{t: t, rules: []fakeRule{{frag: "insert into sync_sequences", rowVals: []any{int64(8)}}}}
	if _, err := a.emitChange(ctx, tx2, "ws1", "SwarmSignal", "", "CREATE", map[string]any{"kind": "x"}); err != nil {
		t.Fatal(err)
	}
	args2 := txSQL(tx2, "insert into sync_outbox")[0].args
	// A nil *string (not an untyped nil in the interface) is how the
	// NULL binding rides the pgx arg list: pgx encodes it as SQL NULL.
	if v, _ := args2[3].(*string); v != nil {
		t.Fatalf("empty model id bound to %q, want a nil *string (SQL NULL)", *v)
	}
}

// TestRefreshOutboxTx pins the second half of the mutation contract:
// the outbox row is rewritten with the final payload under the claimed
// sequence, so the delta and the broadcast carry one byte-identical
// record.
func TestRefreshOutboxTx(t *testing.T) {
	a := apiForTests(t, &fakePool{t: t})
	tx := &fakeTx{t: t}
	rec := syncActionRecord{SequenceID: "7"}
	data := map[string]any{"id": "i1", "title": "done"}
	if err := a.refreshOutboxTx(context.Background(), tx, "ws1", &rec, data); err != nil {
		t.Fatal(err)
	}
	if string(rec.Data) != `{"id":"i1","title":"done"}` {
		t.Fatalf("rec.Data = %s, want the refreshed payload (the broadcast's source)", rec.Data)
	}
	upd := txSQL(tx, "update sync_outbox set data")
	if len(upd) != 1 || upd[0].args[0] != "ws1" || upd[0].args[1] != int64(7) {
		t.Fatalf("refresh = %+v, want workspace + sequence 7", upd)
	}
	// A non-numeric sequence (a hand-built record) rewrites nothing:
	// the row cannot be located, so the update is a no-op, not a write
	// to the wrong row.
	tx2 := &fakeTx{t: t}
	if err := a.refreshOutboxTx(context.Background(), tx2, "ws1", &syncActionRecord{SequenceID: "nan"}, data); err != nil {
		t.Fatal(err)
	}
	if got := txSQL(tx2, "update sync_outbox set data")[0].args[1]; got != int64(0) {
		t.Fatalf("bad sequence bound to %v, want 0 (matches no row)", got)
	}
}

// intPtr is the seam tests' *int builder.
func intPtr(v int) *int { return &v }
