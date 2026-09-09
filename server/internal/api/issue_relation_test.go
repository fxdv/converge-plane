// issue_relation_test.go — v1.1 relation pins: the type vocabulary,
// the reader-side rewrite, the create/delete transaction paths
// (through the fakeTx fake), the cycle guard, and the wire shapes.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestRelationTypeAllowed pins the client enum boundary: the six
// stored types are admitted, PARENT/SUB_ISSUE are pointed at parentId
// (a second hierarchy source would fork issues.parent_id), and
// everything else is a 422.
func TestRelationTypeAllowed(t *testing.T) {
	for _, ok := range []string{relBlocks, relBlocked, relRelated, relDuplicate, relDuplicateOf, relSimilar} {
		if err := relationTypeAllowed(ok); err != nil {
			t.Errorf("type %s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"PARENT", "SUB_ISSUE", "blocks", "", "DEPENDS_ON"} {
		if err := relationTypeAllowed(bad); err == nil {
			t.Errorf("type %q admitted, want rejection", bad)
		}
	}
	if err := relationTypeAllowed("PARENT"); err == nil || !strings.Contains(err.Error(), "parentId") {
		t.Errorf("PARENT must point at parentId: %v", err)
	}
}

// TestReverseRelationType pins the reader-side rewrite: asymmetric
// pairs invert on the target's record, the symmetric types are the
// identity, and the mapping is an involution on the asymmetric pair.
func TestReverseRelationType(t *testing.T) {
	cases := map[string]string{
		relBlocks:      relBlocked,
		relBlocked:     relBlocks,
		relDuplicate:   relDuplicateOf,
		relDuplicateOf: relDuplicate,
		relRelated:     relRelated,
		relSimilar:     relSimilar,
	}
	for stored, want := range cases {
		if got := reverseRelationType(stored); got != want {
			t.Errorf("reverse(%s) = %s, want %s", stored, got, want)
		}
	}
	if reverseRelationType(reverseRelationType(relBlocks)) != relBlocks {
		t.Error("the inversion must be its own inverse")
	}
}

// TestRelationListSQL pins the denormalized subselect's contract:
// the reader-side rewrite is in the SQL (both endpoints read their
// own perspective), edges touching a deleted endpoint are excluded,
// and the result is always an array (the coalesce floor).
func TestRelationListSQL(t *testing.T) {
	for _, want := range []string{
		"'BLOCKS' then 'BLOCKED'", "'BLOCKED' then 'BLOCKS'",
		"'DUPLICATE' then 'DUPLICATE_OF'", "'DUPLICATE_OF' then 'DUPLICATE'",
		"r.deleted_at is null", "i.status <> 'deleted'",
		"'[]'::jsonb", "'issueId', i.id",
		// The reader's-side endpoint: the other side of the edge, never
		// the reader itself (the relatedIssueId self-reference that
		// live smoke caught 2026-09-09).
		"case when r.issue_id = i.id then r.related_issue_id else r.issue_id end",
	} {
		if !strings.Contains(relationListSQL, want) {
			t.Errorf("relationListSQL lost %q", want)
		}
	}
}

// TestCreateRelationTx pins the create path's writes: one edge row
// (actor's perspective, the tenant stamps), one timeline breadcrumb
// (action relation), one sequence claim for the history, and nothing
// else. The duplicate and cycle sentinels map to clean conflicts.
func TestCreateRelationTx(t *testing.T) {
	now := time.Now()
	tx := &fakeTx{
		t: t,
		rules: []fakeRule{
			{frag: "select exists(select 1 from issue_relations", rowVals: []any{false}},
			// The BLOCKS cycle walk runs (an empty graph: no live edges).
			{frag: "select r.related_issue_id", rows: [][]any{}},
			{frag: "insert into issue_relations", rowVals: []any{"rel1", now, now}},
			{frag: "insert into issue_history", rowVals: []any{"hist1"}},
			{frag: "select created_at from issue_history where id", rowVals: []any{now}},
			{frag: "insert into sync_sequences", rowVals: []any{int64(1)}},
		},
	}
	a := &API{log: discardLogger()}
	row := issueRow{ID: "iss1", TeamID: "team1", Number: 7, Status: "active"}
	req := issueRelationRequest{RelatedIssueID: "iss2", Type: relBlocks}

	rel, histRec, err := a.createRelationTx(context.Background(), tx, &Principal{AccountID: "hum1"}, "ws1", row, req)
	if err != nil {
		t.Fatalf("createRelationTx: %v", err)
	}
	if rel.ID != "rel1" || rel.IssueID != "iss1" || rel.RelatedIssueID != "iss2" || rel.Type != relBlocks {
		t.Errorf("edge drifted: %+v", rel)
	}
	// The edge insert carries the tenant + the actor, the stored
	// perspective (actor's side), and the type verbatim.
	ins := findCall(t, tx, "insert into issue_relations")
	wantArgs := []any{"ws1", "team1", "iss1", "iss2", relBlocks, "hum1"}
	for i, w := range wantArgs {
		if ins.args[i] != w {
			t.Errorf("edge insert arg %d = %v, want %v (sql %q)", i, ins.args[i], w, ins.sql)
		}
	}
	// The timeline row is the client's breadcrumb: action relation,
	// the related id and the type in from/to.
	hist := findCall(t, tx, "insert into issue_history")
	histArgs := hist.args
	if histArgs[4] != "relation" || histArgs[5] != "relation" {
		t.Errorf("history action/field = %v/%v, want relation/relation: %v", histArgs[4], histArgs[5], histArgs)
	}
	if histArgs[6] != "iss2" || histArgs[7] != relBlocks {
		t.Errorf("history from/to = %v/%v, want iss2/BLOCKS: %v", histArgs[6], histArgs[7], histArgs)
	}
	// The history record is the edge's own model on the feed.
	if histRec.ModelName != "IssueHistory" || histRec.Action != "I" {
		t.Errorf("history record = %s/%s, want IssueHistory/I", histRec.ModelName, histRec.Action)
	}
}

// TestCreateRelationTxDuplicate pins the pre-check sentinel.
func TestCreateRelationTxDuplicate(t *testing.T) {
	tx := &fakeTx{
		t:     t,
		rules: []fakeRule{{frag: "select exists(select 1 from issue_relations", rowVals: []any{true}}},
	}
	a := &API{log: discardLogger()}
	_, _, err := a.createRelationTx(context.Background(), tx, &Principal{AccountID: "hum1"}, "ws1",
		issueRow{ID: "iss1", TeamID: "team1"}, issueRelationRequest{RelatedIssueID: "iss2", Type: relBlocks})
	if !errors.Is(err, errRelationExists) {
		t.Fatalf("duplicate = %v, want errRelationExists", err)
	}
	if len(tx.execs) != 0 {
		t.Errorf("a duplicate must write nothing, got %v execs", tx.execs)
	}
}

// TestCreateRelationTxCycle pins the blocks cycle guard: a forward
// path from the target back to the source closes the loop.
func TestCreateRelationTxCycle(t *testing.T) {
	now := time.Now()
	cycleTx := &fakeTx{
		t: t,
		rules: []fakeRule{
			{frag: "select exists(select 1 from issue_relations", rowVals: []any{false}},
			{frag: "select r.related_issue_id", rows: [][]any{{"iss1"}}}, // iss2 --BLOCKS--> iss1
			{frag: "insert into issue_relations", rowVals: []any{"rel1", now, now}},
			{frag: "insert into issue_history", rowVals: []any{"hist1"}},
			{frag: "select created_at from issue_history where id", rowVals: []any{now}},
			{frag: "insert into sync_sequences", rowVals: []any{int64(1)}},
		},
	}
	a := &API{log: discardLogger()}
	// iss1 blocks iss2, so iss2 blocking iss1 would loop.
	_, _, err := a.createRelationTx(context.Background(), cycleTx, &Principal{AccountID: "hum1"}, "ws1",
		issueRow{ID: "iss1", TeamID: "team1"}, issueRelationRequest{RelatedIssueID: "iss2", Type: relBlocks})
	if !errors.Is(err, errRelationCycle) {
		t.Fatalf("cycle = %v, want errRelationCycle", err)
	}
	if len(cycleTx.execs) != 0 {
		t.Errorf("a cycle must write nothing, got %v execs", cycleTx.execs)
	}
}

// TestCreateRelationTxNonBlocksSkipsCycle pins that only BLOCKS
// edges form the cycle graph: the related types never walk it.
func TestCreateRelationTxNonBlocksSkipsCycle(t *testing.T) {
	now := time.Now()
	tx := &fakeTx{
		t: t,
		rules: []fakeRule{
			{frag: "select exists(select 1 from issue_relations", rowVals: []any{false}},
			// No "select r.related_issue_id" rule: a RELATED create that
			// walked the cycle graph would fail the fake's matcher.
			{frag: "insert into issue_relations", rowVals: []any{"rel1", now, now}},
			{frag: "insert into issue_history", rowVals: []any{"hist1"}},
			{frag: "select created_at from issue_history where id", rowVals: []any{now}},
			{frag: "insert into sync_sequences", rowVals: []any{int64(1)}},
		},
	}
	a := &API{log: discardLogger()}
	if _, _, err := a.createRelationTx(context.Background(), tx, &Principal{AccountID: "hum1"}, "ws1",
		issueRow{ID: "iss1", TeamID: "team1"}, issueRelationRequest{RelatedIssueID: "iss2", Type: relRelated}); err != nil {
		t.Fatalf("RELATED create = %v — the cycle walk must not run", err)
	}
}

// TestApplyIssueRelationTx pins the full create op's record set:
// the timeline row, the edge record (CREATE, the client's shape), and
// the related endpoint's refreshed Issue record (its denormalized
// array changed).
func TestApplyIssueRelationTx(t *testing.T) {
	now := time.Now()
	relJSON := json.RawMessage(`[{"id":"rel1","type":"BLOCKED","relatedIssueId":"iss1","issueId":"iss2","createdById":"hum1","createdAt":"2026-09-09T00:00:00Z","updatedAt":"2026-09-09T00:00:00Z"}]`)
	tx := &fakeTx{
		t: t,
		rules: []fakeRule{
			{frag: "select exists(select 1 from issue_relations", rowVals: []any{false}},
			{frag: "select r.related_issue_id", rows: [][]any{}},
			{frag: "insert into issue_relations", rowVals: []any{"rel1", now, now}},
			{frag: "insert into issue_history", rowVals: []any{"hist1"}},
			{frag: "select created_at from issue_history where id", rowVals: []any{now}},
			// issueByIDTx on the related endpoint: the 19 issueColumns
			// values (project_ids, labels, children, relations) with the
			// rewritten edge.
			{frag: "from issues i where i.id", rowVals: []any{
				"iss2", "team1", 8, nil, 0, "T2", "", "active", now, now,
				"hum1", nil, nil, "st1", false, nil, nil, nil, relJSON,
			}},
			{frag: "insert into sync_sequences", rowVals: []any{int64(1), int64(2), int64(3)}},
		},
	}
	a := &API{log: discardLogger()}
	recs, err := a.applyIssueRelationTx(context.Background(), tx, &Principal{AccountID: "hum1"}, "ws1",
		issueRow{ID: "iss1", TeamID: "team1", Number: 7, Status: "active"},
		issueRelationRequest{RelatedIssueID: "iss2", Type: relBlocks})
	if err != nil {
		t.Fatalf("applyIssueRelationTx: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3 (history, edge, related issue): %v", len(recs), recs)
	}
	if recs[0].ModelName != "IssueHistory" || recs[0].ModelID != "hist1" {
		t.Errorf("rec[0] = %s/%s, want IssueHistory/hist1", recs[0].ModelName, recs[0].ModelID)
	}
	if recs[1].ModelName != "IssueRelation" || recs[1].ModelID != "rel1" || recs[1].Action != "I" {
		t.Errorf("rec[1] = %s/%s/%s, want IssueRelation/rel1/I", recs[1].ModelName, recs[1].ModelID, recs[1].Action)
	}
	edge := decodeWire(t, recs[1])
	if edge["relatedIssueId"] != "iss2" || edge["issueId"] != "iss1" || edge["type"] != relBlocks {
		t.Errorf("edge perspective drifted (stored = actor's side): %v", edge)
	}
	if recs[2].ModelName != "Issue" || recs[2].ModelID != "iss2" || recs[2].Action != "U" {
		t.Errorf("rec[2] = %s/%s/%s, want Issue/iss2/U", recs[2].ModelName, recs[2].ModelID, recs[2].Action)
	}
	issue := decodeWire(t, recs[2])
	rels, ok := issue["relations"].([]any)
	if !ok || len(rels) != 1 {
		t.Fatalf("refreshed issue must carry the new relations array: %v", issue["relations"])
	}
	if m, _ := rels[0].(map[string]any); m["type"] != relBlocked || m["relatedIssueId"] != "iss1" {
		t.Errorf("the related endpoint must read the edge from its side: %v", rels[0])
	}
}

// TestApplyRelationDeleteTx pins the deletion's record set: the
// timeline's removal breadcrumb, the edge off the feed with {id}, and
// both endpoints' refreshed Issue records (the edge gone from their
// arrays).
func TestApplyRelationDeleteTx(t *testing.T) {
	now := time.Now()
	empty := json.RawMessage(`[]`)
	tx := &fakeTx{
		t: t,
		rules: []fakeRule{
			{frag: "update issue_relations", rowVals: []any{
				"rel1", "ws1", "team1", "iss1", "iss2", relBlocks, "hum1", now, now,
			}},
			{frag: "insert into issue_history", rowVals: []any{"hist1"}},
			{frag: "select created_at from issue_history where id", rowVals: []any{now}},
			{frag: "insert into sync_sequences", rowVals: []any{int64(1), int64(2), int64(3), int64(4), int64(5)}},
			// Both endpoints re-load with the edge gone from their arrays.
			{frag: "from issues i where i.id", rows: [][]any{
				{"iss1", "team1", 7, nil, 0, "T1", "", "active", now, now, "hum1", nil, nil, "st1", false, nil, nil, nil, empty},
				{"iss2", "team1", 8, nil, 0, "T2", "", "active", now, now, "hum1", nil, nil, "st1", false, nil, nil, nil, empty},
			}},
		},
	}
	a := &API{log: discardLogger()}
	rel := relationRow{ID: "rel1", WorkspaceID: "ws1", TeamID: "team1",
		IssueID: "iss1", RelatedIssueID: "iss2", Type: relBlocks,
		CreatedBy: strvalptr("hum1"), CreatedAt: now, UpdatedAt: now}
	updated, recs, err := a.applyRelationDeleteTx(context.Background(), tx, &Principal{AccountID: "hum1"}, rel)
	if err != nil {
		t.Fatalf("applyRelationDeleteTx: %v", err)
	}
	if updated.ID != "rel1" {
		t.Errorf("updated edge = %s, want rel1", updated.ID)
	}
	if len(recs) != 4 {
		t.Fatalf("got %d records, want 4 (history, edge, 2 issue refreshes): %v", len(recs), recs)
	}
	hist := decodeWire(t, recs[0])
	changes, _ := hist["relationChanges"].(map[string]any)
	if changes["isDeleted"] != true || changes["type"] != relBlocks || changes["relatedIssueId"] != "iss2" {
		t.Errorf("removal breadcrumb drifted: %v", hist["relationChanges"])
	}
	if recs[1].ModelName != "IssueRelation" || recs[1].Action != "D" {
		t.Errorf("rec[1] = %s/%s, want IssueRelation/D", recs[1].ModelName, recs[1].Action)
	}
	edge := decodeWire(t, recs[1])
	if edge["id"] != "rel1" {
		t.Errorf("DELETE record must carry {id}: %v", edge)
	}
	for i, endpoint := range []string{"iss1", "iss2"} {
		rec := recs[2+i]
		if rec.ModelName != "Issue" || rec.ModelID != endpoint || rec.Action != "U" {
			t.Errorf("rec[%d] = %s/%s/%s, want Issue/%s/U", 2+i, rec.ModelName, rec.ModelID, rec.Action, endpoint)
		}
		issue := decodeWire(t, rec)
		if rels, _ := issue["relations"].([]any); len(rels) != 0 {
			t.Errorf("%s must refresh with an empty relations array: %v", endpoint, issue["relations"])
		}
	}
}

// TestRelationDataWireContract pins the IssueRelation record against
// the client's IssueRelationType: every field present, the enums
// within the client vocabulary, createdById explicit when the creator
// is gone.
func TestRelationDataWireContract(t *testing.T) {
	a := &API{}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	creator := "hum1"
	m := wirePayload(t, a.relationData(relationRow{
		ID: "rel1", IssueID: "iss1", RelatedIssueID: "iss2", Type: relBlocks,
		CreatedBy: &creator, CreatedAt: now, UpdatedAt: now,
	}))
	for _, k := range []string{"id", "createdAt", "updatedAt", "issueId", "relatedIssueId"} {
		requireString(t, m, k)
	}
	if m["type"] != "BLOCKS" {
		t.Errorf("type = %v, want BLOCKS", m["type"])
	}
	if m["createdById"] != "hum1" {
		t.Errorf("createdById = %v, want hum1", m["createdById"])
	}
	// A deleted creator serializes null, never a missing key.
	m = wirePayload(t, a.relationData(relationRow{ID: "rel1", IssueID: "iss1", RelatedIssueID: "iss2",
		Type: relBlocks, CreatedAt: now, UpdatedAt: now}))
	if m["createdById"] != nil {
		t.Errorf("creatorless edge must carry explicit null createdById, got %v", m["createdById"])
	}
}

// TestIssueDataRelationsWireContract pins the denormalized field on
// the Issue record: always a JSON array (never null) — the coalesce
// floor, and the zero-value normalization for rows built in Go.
func TestIssueDataRelationsWireContract(t *testing.T) {
	a := API{}
	stateID := "s1"
	// A row with no relations (the zero value) serializes [].
	m := wirePayload(t, a.issueData(issueRow{
		ID: "i1", TeamID: "t1", Number: 7, Title: "T", Status: "active",
		CreatedAt: wireNow, UpdatedAt: wireNow, StatusID: &stateID,
	}))
	requireArray(t, m, "relations")
	// A row with relations passes the JSON through verbatim.
	with := issueRow{ID: "i1", TeamID: "t1", Number: 7, Title: "T", Status: "active",
		CreatedAt: wireNow, UpdatedAt: wireNow, StatusID: &stateID}
	with.RelationRaw = json.RawMessage(`[{"id":"rel1"}]`)
	m = wirePayload(t, a.issueData(with))
	rels, ok := m["relations"].([]any)
	if !ok || len(rels) != 1 {
		t.Fatalf("relations must pass through as an array: %v (%T)", m["relations"], m["relations"])
	}
}

// TestHistoryDataRelationWireContract pins the timeline row: added
// and removed edges both carry relationChanges with the client's
// discriminator (isDeleted), and non-relation rows carry explicit
// null (the client model requires the key).
func TestHistoryDataRelationWireContract(t *testing.T) {
	cases := []struct {
		action     string
		field      string
		from, to   string
		wantDelete any
		wantType   any
	}{
		{"relation", "relation", "iss2", "BLOCKS", false, "BLOCKS"},
		{"relation_deleted", "relation", "iss2", "DUPLICATE_OF", true, "DUPLICATE_OF"},
		{"updated", "status", "a", "b", nil, nil}, // a plain row: explicit null
	}
	for _, c := range cases {
		m := wirePayload(t, historyData("h1", wireNow, wireNow, strvalptr("hum1"), "iss1", c.action, c.field,
			ptrOrNull(c.from), ptrOrNull(c.to), nil))
		v, present := m["relationChanges"]
		if c.wantType == nil {
			if present && v != nil {
				t.Errorf("action %s: relationChanges must be explicit null, got %v", c.action, v)
			}
			continue
		}
		changes, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("action %s: relationChanges missing, got %v", c.action, v)
		}
		if changes["isDeleted"] != c.wantDelete {
			t.Errorf("action %s: isDeleted = %v, want %v", c.action, changes["isDeleted"], c.wantDelete)
		}
		if changes["type"] != c.wantType || changes["relatedIssueId"] != c.from || changes["issueId"] != "iss1" {
			t.Errorf("action %s: %v", c.action, changes)
		}
	}
}

// decodeWire round-trips a record's payload through JSON.
func decodeWire(t *testing.T, rec syncActionRecord) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Data, &m); err != nil {
		t.Fatalf("unmarshal %s/%s: %v", rec.ModelName, rec.ModelID, err)
	}
	return m
}
