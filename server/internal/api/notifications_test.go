// notifications_test.go — the in-app inbox over in-memory fakes
// (SWR-13): the trigger matrix's noise rules (the actor self-exclusion,
// the 24h-epoch dedup, the human-only recipients), the addressed route
// contracts, the bootstrap's recipient filter, and the retention
// horizon.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"converge/internal/auth"
)

// notifIssue is one issue for the notifier tests: the id, number, and
// the people the matrix addresses (nil = nobody).
func notifIssue(assignee, creator *string) issueRow {
	row := issueRow{ID: "iss1", TeamID: "team1", Number: 7, Status: "active"}
	row.AssigneeID = assignee
	row.CreatedByID = creator
	return row
}

// notifHumanTx pins the notifier's seams with a human recipient: the
// upsert returns its row (pending), the emit claims the sequence.
func notifHumanTx(t *testing.T) *fakeTx {
	t.Helper()
	return &fakeTx{t: t, rules: []fakeRule{
		{frag: "insert into notifications", rowVals: []any{"n1", time.Now(), nil}},
		{frag: "insert into sync_sequences", rowVals: []any{int64(9)}},
	}}
}

// TestNotifyIssueTx pins the one notifier: the trigger's noise rules
// live in the upsert's args and in the accounts join, so the fake's
// rules — and only them — express the contract.
func TestNotifyIssueTx(t *testing.T) {
	ctx := context.Background()
	a := reviewAPI()

	t.Run("the actor is never told about its own action", func(t *testing.T) {
		tx := notifHumanTx(t)
		recs, err := a.notifyIssueTx(ctx, tx, "ws1", humanPrincipal("u1"),
			notifIssue(str("u2"), str("u1")), notifAssigned, []string{"u1", "u2"})
		if err != nil {
			t.Fatal(err)
		}
		calls := txSQL(tx, "insert into notifications")
		if len(calls) != 1 {
			t.Fatalf("upserts = %d, want 1 (u1 acted; only u2 is addressed)", len(calls))
		}
		if calls[0].args[2] != "u2" {
			t.Fatalf("recipient = %v, want u2", calls[0].args[2])
		}
		if len(recs) != 1 || recs[0].ModelName != notifModel {
			t.Fatalf("records = %+v, want one addressed %s record", recs, notifModel)
		}
	})

	t.Run("a non-human recipient no-ops without a record", func(t *testing.T) {
		// The fake's accounts join knows no human by that id; and no
		// sync_sequences rule — an emit would fail loudly.
		tx := &fakeTx{t: t, rules: []fakeRule{
			{frag: "insert into notifications", rowErr: pgx.ErrNoRows},
		}}
		recs, err := a.notifyIssueTx(ctx, tx, "ws1", agentPrincipal("ag1"),
			notifIssue(str("ag2"), nil), notifAssigned, []string{"ag2"})
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) != 0 {
			t.Fatalf("records = %+v, want none — agents have no inbox", recs)
		}
		if hasSQL(tx, "insert into sync_outbox") {
			t.Fatal("a non-human recipient must not land in the outbox")
		}
	})

	t.Run("the upsert joins on the human kind only", func(t *testing.T) {
		tx := notifHumanTx(t)
		if _, err := a.notifyIssueTx(ctx, tx, "ws1", humanPrincipal("u1"),
			notifIssue(nil, nil), notifAssigned, []string{"u2"}); err != nil {
			t.Fatal(err)
		}
		calls := txSQL(tx, "insert into notifications")
		if len(calls) != 1 {
			t.Fatalf("upserts = %d, want 1", len(calls))
		}
		if calls[0].args[8] != auth.AccountKindHuman {
			t.Fatalf("kind bound = %v, want %q (agents have no inbox)", calls[0].args[8], auth.AccountKindHuman)
		}
		if !strings.Contains(calls[0].sql, "from accounts") ||
			!strings.Contains(calls[0].sql, "on conflict (workspace_id, account_id, dedup_key)") ||
			!strings.Contains(calls[0].sql, "where read_at is null") {
			t.Fatalf("upsert = %q — the accounts join and the partial unique index are the noise guard", calls[0].sql)
		}
	})

	t.Run("two same-bucket events share the dedup key", func(t *testing.T) {
		tx := notifHumanTx(t)
		row := notifIssue(nil, nil)
		for i := 0; i < 2; i++ {
			if _, err := a.notifyIssueTx(ctx, tx, "ws1", humanPrincipal("u1"), row, notifComment, []string{"u2"}); err != nil {
				t.Fatal(err)
			}
		}
		calls := txSQL(tx, "insert into notifications")
		if len(calls) != 2 {
			t.Fatalf("upserts = %d, want 2 (one per event; the index collapses them)", len(calls))
		}
		if calls[0].args[7] != calls[1].args[7] {
			t.Fatalf("dedup keys %v / %v — same issue+type+actor in one 24h bucket must be one row",
				calls[0].args[7], calls[1].args[7])
		}
	})

	t.Run("the record is addressed and shaped for the client", func(t *testing.T) {
		tx := notifHumanTx(t)
		recs, err := a.notifyIssueTx(ctx, tx, "ws1", humanPrincipal("u1"),
			notifIssue(str("u2"), str("u1")), notifAssigned, []string{"u2"})
		if err != nil || len(recs) != 1 {
			t.Fatalf("records = %+v err = %v, want one addressed record", recs, err)
		}
		var d map[string]any
		if err := json.Unmarshal(recs[0].Data, &d); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]any{
			"id": "n1", "workspaceId": "ws1", "issueId": "iss1",
			"issueNumber": float64(7), "type": notifAssigned,
			"actorId": "u1", "actorName": "Human", "recipientId": "u2",
			"readAt": nil,
		} {
			if d[key] != want {
				t.Fatalf("key %q = %v, want %v", key, d[key], want)
			}
		}
		if recs[0].Action != "I" {
			t.Fatalf("action = %q, want I (the client's create verb)", recs[0].Action)
		}
	})
}

// TestNotifyCommentMention pins the mention scanner: the body's
// @handles, matched against the workspace's human members, notify the
// addressed ones; the actor's own name never mentions itself.
func TestNotifyCommentMention(t *testing.T) {
	ctx := context.Background()
	a := reviewAPI()
	tx := &fakeTx{t: t, rules: []fakeRule{
		{frag: "select distinct author_id from comments", rows: [][]any{}},
		{frag: "from accounts a", rows: [][]any{{"u2"}}},
		{frag: "insert into notifications", rowVals: []any{"n1", time.Now(), nil}},
		{frag: "insert into sync_sequences", rowVals: []any{int64(9)}},
	}}
	// No assignee, no creator, no participants: only the mention fires.
	recs, err := a.notifyCommentTx(ctx, tx, humanPrincipal("u1"), "ws1",
		notifIssue(nil, nil), "review this, @JaneDoe")
	if err != nil {
		t.Fatal(err)
	}
	calls := txSQL(tx, "insert into notifications")
	if len(calls) != 1 {
		t.Fatalf("upserts = %d, want 1 (the comment trigger is empty; the mention addresses u2)", len(calls))
	}
	if calls[0].args[4] != notifMention {
		t.Fatalf("type = %v, want %q", calls[0].args[4], notifMention)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1 (the one mentioned member)", len(recs))
	}
}

// TestNotificationData pins the wire shape the client's model
// validates: every key present, the nullable ones explicit null.
// The round-trip is the contract (wirePayload): a typed-nil pointer in
// the builder must land as a JSON null the client's union admits.
func TestNotificationData(t *testing.T) {
	a := reviewAPI()
	now := time.Now().UTC().Format(iso)
	doc := wirePayload(t, a.notificationData(&notification{
		ID: "n1", WorkspaceID: "ws1", IssueID: "iss1", IssueNumber: 7,
		Type: "comment", ActorID: str("u1"), ActorName: str("Jane Doe"),
		RecipientID: "u2", CreatedAt: now, ReadAt: nil,
	}))
	for key, want := range map[string]any{
		"id": "n1", "workspaceId": "ws1", "issueId": "iss1",
		"issueNumber": float64(7), "type": "comment",
		"actorId": "u1", "actorName": "Jane Doe", "recipientId": "u2",
		"createdAt": now, "readAt": nil,
	} {
		if doc[key] != want {
			t.Fatalf("key %q = %v, want %v (present or null, never absent)", key, doc[key], want)
		}
	}
}

// TestListNotifications pins the inbox read: the doors, and the
// recipient-scoped list (the one per-recipient read in the sync world).
func TestListNotifications(t *testing.T) {
	t.Run("an unauthenticated request 401s", func(t *testing.T) {
		a := apiForTests(t, &fakePool{t: t})
		rec := record(t, a, requestFor(t, nil, "GET",
			"http://x/api/v1/notifications?workspaceId=ws1", ""), a.handleListNotifications)
		checkStatus(t, rec, 401)
	})
	t.Run("a missing workspaceId 400s", func(t *testing.T) {
		a := apiForTests(t, &fakePool{t: t})
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "GET",
			"http://x/api/v1/notifications", ""), a.handleListNotifications)
		wantError(t, rec, 400, "workspaceId is required")
	})
	t.Run("a non-member 404s", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "select role from workspace_members", rowErr: pgx.ErrNoRows},
		}}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "GET",
			"http://x/api/v1/notifications?workspaceId=ws1", ""), a.handleListNotifications)
		wantError(t, rec, 404, "not found")
	})
	t.Run("the recipient's own rows 200, newest first", func(t *testing.T) {
		now := time.Now()
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "select role from workspace_members", rowVals: []any{"member"}},
			{frag: "from notifications", rows: [][]any{
				{"n1", "iss1", 7, "comment", "u2", "Jane Doe", now.Add(-time.Hour), nil},
				{"n2", "iss2", 8, "assigned", nil, nil, now.Add(-2 * time.Hour), nil},
			}},
		}}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "GET",
			"http://x/api/v1/notifications?workspaceId=ws1&unreadOnly=true", ""),
			a.handleListNotifications), 200)
		var out []notification
		decodeBody(t, rec, &out)
		if len(out) != 2 {
			t.Fatalf("rows = %d, want 2", len(out))
		}
		for i, n := range out {
			if n.RecipientID != "u1" {
				t.Fatalf("row %d recipientId = %q, want u1 (the caller's own rows only)", i, n.RecipientID)
			}
			if n.WorkspaceID != "ws1" || n.ReadAt != nil {
				t.Fatalf("row %d = %+v, want the ws1 row, still pending", i, n)
			}
		}
		if out[0].ID != "n1" || out[1].ID != "n2" {
			t.Fatalf("order = %s, %s — newest first", out[0].ID, out[1].ID)
		}
		if out[1].ActorID != nil || out[1].ActorName != nil {
			t.Fatalf("row 2 actor = %v %v, want null (a null actor rides as explicit null)",
				out[1].ActorID, out[1].ActorName)
		}
	})
}

// TestMarkNotificationRead pins the addressed write: the row must be
// the caller's (the recipient sits in the WHERE), the read lands in the
// feed, and a double read no-ops.
func TestMarkNotificationRead(t *testing.T) {
	const id = "77777777-7777-7777-7777-777777777777"
	now := time.Now()
	// The row's scan order (the handler's select): id, workspace, issue,
	// number, type, actorId, actorName, createdAt, readAt, account.
	ownRow := []any{id, "ws1", "iss1", 7, "comment", "u2", "Jane Doe", now, nil, "u1"}

	t.Run("an unauthenticated request 401s", func(t *testing.T) {
		a := apiForTests(t, &fakePool{t: t})
		rec := record(t, a, requestFor(t, nil, "POST",
			"http://x/api/v1/notifications/"+id+"/read", "", "id", id), a.handleMarkNotificationRead)
		checkStatus(t, rec, 401)
	})
	t.Run("a row that is not the caller's 404s", func(t *testing.T) {
		pool := &fakePool{t: t, txs: []*fakeTx{{t: t, rules: []fakeRule{
			{frag: "from notifications n", rowErr: pgx.ErrNoRows},
		}}}}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST",
			"http://x/api/v1/notifications/"+id+"/read", "", "id", id), a.handleMarkNotificationRead)
		wantError(t, rec, 404, "not found")
	})
	t.Run("an unread row reads: the write, the feed, the ack", func(t *testing.T) {
		pool := &fakePool{t: t, txs: []*fakeTx{{t: t, rules: []fakeRule{
			{frag: "from notifications n", rowVals: ownRow},
			{frag: "insert into sync_sequences", rowVals: []any{int64(9)}},
		}}}}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST",
			"http://x/api/v1/notifications/"+id+"/read", "", "id", id),
			a.handleMarkNotificationRead), 200)
		tx := pool.txs[0]
		upds := txSQL(tx, "update notifications set read_at")
		if len(upds) != 1 || upds[0].args[0] != id || upds[0].args[1] != "u1" {
			t.Fatalf("read writes = %+v — the UPDATE is scoped to the caller's row", upds)
		}
		if len(txSQL(tx, "insert into sync_outbox")) != 1 {
			t.Fatal("the read must land in the outbox (the badge rides the feed)")
		}
		var out notification
		decodeBody(t, rec, &out)
		if out.ReadAt == nil || out.RecipientID != "u1" || out.ActorID == nil || *out.ActorID != "u2" {
			t.Fatalf("ack = %+v, want read, addressed to u1, by u2", out)
		}
	})
	t.Run("a double read no-ops: 200, no write, no feed", func(t *testing.T) {
		read := now.UTC().Format(iso)
		pool := &fakePool{t: t, txs: []*fakeTx{{t: t, rules: []fakeRule{
			// readAt set: the no-op branch returns before the write.
			{frag: "from notifications n", rowVals: []any{id, "ws1", "iss1", 7, "comment", "u2", "Jane Doe", now, read, "u1"}},
			// No sync_sequences rule: an emit would fail loudly.
		}}}}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST",
			"http://x/api/v1/notifications/"+id+"/read", "", "id", id),
			a.handleMarkNotificationRead), 200)
		if hasSQL(pool.txs[0], "update notifications") || hasSQL(pool.txs[0], "insert into sync_outbox") {
			t.Fatal("a double read must not rewrite or re-emit")
		}
		var out notification
		decodeBody(t, rec, &out)
		if out.ReadAt == nil || *out.ReadAt != read {
			t.Fatalf("ack readAt = %v, want %q (idempotent)", out.ReadAt, read)
		}
	})
}

// TestMarkNotificationsRead pins the clear-all: the doors, the no-op
// sweep, and the pending set (the database marks every row; the feed
// fan-out is capped, the state is not).
func TestMarkNotificationsRead(t *testing.T) {
	t.Run("an unauthenticated request 401s", func(t *testing.T) {
		a := apiForTests(t, &fakePool{t: t})
		rec := record(t, a, requestFor(t, nil, "POST",
			"http://x/api/v1/notifications/read_all?workspaceId=ws1", ""), a.handleMarkNotificationsRead)
		checkStatus(t, rec, 401)
	})
	t.Run("a missing workspaceId 400s", func(t *testing.T) {
		a := apiForTests(t, &fakePool{t: t})
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST",
			"http://x/api/v1/notifications/read_all", ""), a.handleMarkNotificationsRead)
		wantError(t, rec, 400, "workspaceId is required")
	})
	t.Run("a non-member 404s", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "select role from workspace_members", rowErr: pgx.ErrNoRows},
		}}
		a := apiForTests(t, pool)
		rec := record(t, a, requestFor(t, humanPrincipal("u1"), "POST",
			"http://x/api/v1/notifications/read_all?workspaceId=ws1", ""), a.handleMarkNotificationsRead)
		wantError(t, rec, 404, "not found")
	})
	t.Run("zero pending: 200 updated 0, no sweep", func(t *testing.T) {
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "select role from workspace_members", rowVals: []any{"member"}},
		}, txs: []*fakeTx{{t: t, rules: []fakeRule{
			{frag: "from notifications n", rows: [][]any{}},
		}}}}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST",
			"http://x/api/v1/notifications/read_all?workspaceId=ws1", ""),
			a.handleMarkNotificationsRead), 200)
		var v struct {
			Updated int `json:"updated"`
		}
		decodeBody(t, rec, &v)
		if v.Updated != 0 {
			t.Fatalf("updated = %d, want 0", v.Updated)
		}
		if hasSQL(pool.txs[0], "update notifications") {
			t.Fatal("an empty inbox must not sweep")
		}
	})
	t.Run("the pending set reads in full; the fan-out is capped", func(t *testing.T) {
		now := time.Now()
		// 102 pending rows: two past the fan-out cap — the database
		// marks them all, the feed takes the newest 100.
		rows := make([][]any, 0, 102)
		for i := 0; i < 102; i++ {
			rows = append(rows, []any{
				fmt.Sprintf("n%03d", i), "iss1", 7, "comment", "u2", "Jane Doe", now, "u1",
			})
		}
		pool := &fakePool{t: t, rules: []fakeRule{
			{frag: "select role from workspace_members", rowVals: []any{"member"}},
		}, txs: []*fakeTx{{t: t, rules: []fakeRule{
			{frag: "from notifications n", rows: rows},
			{frag: "insert into sync_sequences", rowVals: []any{int64(9)}},
		}}}}
		a := apiForTests(t, pool)
		rec := checkStatus(t, record(t, a, requestFor(t, humanPrincipal("u1"), "POST",
			"http://x/api/v1/notifications/read_all?workspaceId=ws1", ""),
			a.handleMarkNotificationsRead), 200)
		var v struct {
			Updated int `json:"updated"`
		}
		decodeBody(t, rec, &v)
		if v.Updated != 102 {
			t.Fatalf("updated = %d, want 102 (the badge's truth is the database, not the feed)", v.Updated)
		}
		if len(txSQL(pool.txs[0], "update notifications")) != 1 {
			t.Fatal("the sweep is one bounded update, not per row")
		}
		outs := txSQL(pool.txs[0], "insert into sync_outbox")
		if len(outs) != 102 {
			t.Fatalf("outbox records = %d, want 102 (the database state is complete)", len(outs))
		}
		for i, e := range outs {
			if e.args[4] != "UPDATE" {
				t.Fatalf("outbox %d action = %v, want UPDATE", i, e.args[4])
			}
		}
		// The newest record's data carries the full key set.
		var d map[string]any
		if err := json.Unmarshal(outs[0].args[5].([]byte), &d); err != nil {
			t.Fatal(err)
		}
		if d["recipientId"] != "u1" || d["readAt"] == nil || d["actorId"] != "u2" {
			t.Fatalf("record data = %v — addressed, read, and shaped", d)
		}
	})
}

// TestCollectNotifications pins the bootstrap's recipient filter: the
// inbox is the only per-recipient collector in the sync world — the
// feed is per-workspace, the caller gets its own rows.
func TestCollectNotifications(t *testing.T) {
	now := time.Now()
	pool := &fakePool{t: t, rules: []fakeRule{
		{frag: "from notifications n", rows: [][]any{
			{"n1", "iss1", 7, "comment", "u2", "Jane Doe", now, nil},
			{"n2", "iss2", 8, "assigned", nil, nil, now, now.UTC().Format(iso)},
		}},
	}}
	a := apiForTests(t, pool)
	got := map[string]map[string]any{}
	_, err := a.collectNotifications(context.Background(), "ws1", "u1",
		func(id string, data any) (syncActionRecord, error) {
			got[id] = data.(map[string]any)
			return syncActionRecord{ModelName: notifModel, ModelID: id, Action: "I"}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("records = %d, want 2", len(got))
	}
	for _, d := range got {
		if d["recipientId"] != "u1" {
			t.Fatalf("recipientId = %v, want u1 (the requesting account's own rows)", d["recipientId"])
		}
		if _, ok := d["readAt"]; !ok {
			t.Fatal("readAt missing — the wire discipline is present, never absent")
		}
	}
	// JSON is the contract: a null actor rides as explicit null (the
	// typed pointer is not an interface nil — round-trip to check).
	raw, _ := json.Marshal(got["n2"])
	if !strings.Contains(string(raw), `"actorId":null`) || !strings.Contains(string(raw), `"actorName":null`) {
		t.Fatalf("n2 wire = %s, want explicit null actor keys", raw)
	}
}

// TestInboxConstants pins the inbox's horizons (the guard-constant
// house pattern): the retention window, the sweep's clock, the feed
// fan-out cap, and the bootstrap bound.
func TestInboxConstants(t *testing.T) {
	if notifRetention != "interval '90 days'" {
		t.Fatalf("retention = %q, want the 90-day horizon", notifRetention)
	}
	if inboxRetentionInterval != 30*time.Minute {
		t.Fatalf("sweep clock = %v, want 30m (coarse by design)", inboxRetentionInterval)
	}
	if readAllBroadcastCap != 100 {
		t.Fatalf("fan-out cap = %d, want 100 (the feed is bounded; the state is not)", readAllBroadcastCap)
	}
	if notifBootstrapLimit != 500 {
		t.Fatalf("bootstrap bound = %d, want 500 rows (recent history, not a ledger)", notifBootstrapLimit)
	}
}
