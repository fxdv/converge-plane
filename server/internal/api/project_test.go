// project_test.go — the Projects contract pins (spec cs:api:projects).
//
// Pure functions are pinned directly; the transactional surface runs
// through the in-memory pgx fakes (the same pattern as the relations
// and review tests): the Execs are recorded for assertion, the writes
// are the contract.

package api

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestProjectDataWireContract pins the Project record against the
// client's ProjectType (web/src/common/types/project.ts, as-is): every
// key present, status the v1 constant, teams an array, the lead and
// dates explicit ("" / null, never a missing key).
func TestProjectDataWireContract(t *testing.T) {
	a := API{}
	now := time.Now()
	desc := "The v1.1 cut"
	row := projectRow{
		ID: "p1", Name: "V1.1", Color: "#8884d8", WorkspaceID: "w1",
		Description: &desc, StartDate: strvalptr("2026-01-01"),
		Teams: []string{"t1"}, LeadID: strvalptr("u1"),
		CreatedAt: now, UpdatedAt: now,
	}
	m := wirePayload(t, a.projectData(row))
	for _, k := range []string{"id", "createdAt", "updatedAt", "name", "workspaceId", "status", "color", "leadUserId", "description", "startDate"} {
		requireString(t, m, k)
	}
	if m["status"] != "ACTIVE" {
		t.Errorf("status must be the v1 constant ACTIVE, got %v", m["status"])
	}
	requireArray(t, m, "teams")
	teams := m["teams"].([]any)
	if len(teams) != 1 || teams[0] != "t1" {
		t.Errorf("teams must pass through, got %v", m["teams"])
	}
	if m["endDate"] != nil {
		t.Errorf("an unset date serializes null, got %v", m["endDate"])
	}
	// A lead-less project serializes an explicit null (the client's
	// leadUserId is optional; a missing key would be a contract break).
	row2 := projectRow{ID: "p2", Name: "N", Color: "#000", WorkspaceID: "w1", CreatedAt: now, UpdatedAt: now}
	m2 := wirePayload(t, a.projectData(row2))
	if m2["leadUserId"] != nil {
		t.Errorf("a null lead serializes null, got %v", m2["leadUserId"])
	}
}

// TestProjectColumnsContract pins the shared SELECT list: the client
// fields, and the dates cast to the client's text format.
func TestProjectColumnsContract(t *testing.T) {
	for _, want := range []string{
		"id, name, color, workspace_id, description",
		"(start_date)::text",
		"(end_date)::text",
		"teams, lead_id",
	} {
		if !strings.Contains(projectColumns, want) {
			t.Errorf("projectColumns lost %q", want)
		}
	}
}

// TestDeleteProjectTx pins the deletion's record set: the project off
// the feed with its pre-delete payload, the membership cleared on the
// members, and each member's refreshed Issue record (the cleared
// array, the derived null projectId).
func TestDeleteProjectTx(t *testing.T) {
	now := time.Now()
	row := projectRow{
		ID: "p1", Name: "V1.1", Color: "#8884d8", WorkspaceID: "w1",
		Teams: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	// The canned member re-loads: 19 issueColumns values with the
	// membership already cleared (the update lands before the read).
	issueVals := func(id, title string, number int) []any {
		return []any{id, "t1", number, nil, 0, title, "", "active", now, now,
			"hum1", nil, nil, "st1", false, []string{}, nil, nil, nil}
	}
	tx := &fakeTx{
		t: t,
		rules: []fakeRule{
			{frag: "select i.id from issues i", rows: [][]any{{"i1"}, {"i2"}}},
			{frag: "from issues i where i.id", rowVals: issueVals("i1", "T1", 7)},
			{frag: "insert into sync_sequences", rowVals: []any{int64(1)}},
		},
	}
	a := &API{log: discardLogger()}
	recs, err := a.deleteProjectTx(context.Background(), tx, "w1", "hum1", row)
	if err != nil {
		t.Fatalf("deleteProjectTx: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3 (delete + 2 issue refreshes): %v", len(recs), recs)
	}
	if recs[0].ModelName != "Project" || recs[0].ModelID != "p1" || recs[0].Action != "D" {
		t.Errorf("rec[0] = %s/%s/%s, want Project/p1/D", recs[0].ModelName, recs[0].ModelID, recs[0].Action)
	}
	del := decodeWire(t, recs[0])
	if del["name"] != "V1.1" {
		t.Errorf("the DELETE record carries the pre-delete payload: %v", del["name"])
	}
	for i, member := range []string{"i1", "i2"} {
		rec := recs[1+i]
		if rec.ModelName != "Issue" || rec.ModelID != member || rec.Action != "U" {
			t.Errorf("rec[%d] = %s/%s/%s, want Issue/%s/U", 1+i, rec.ModelName, rec.ModelID, rec.Action, member)
			continue
		}
		issue := decodeWire(t, rec)
		if ids, _ := issue["projectIds"].([]any); len(ids) != 0 {
			t.Errorf("%s must refresh with an empty projectIds: %v", member, issue["projectIds"])
		}
		if issue["projectId"] != nil {
			t.Errorf("%s: projectId must derive null from an empty membership, got %v", member, issue["projectId"])
		}
	}
	// The writes are the contract: the soft delete and the membership
	// clear both landed.
	joined := ""
	for _, e := range tx.execs {
		joined += e.sql + "\n"
	}
	for _, want := range []string{"update projects set deleted_at", "update issues set project_ids"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the deletion never ran %q:\n%s", want, joined)
		}
	}
}

// TestProjectKey pins the history from/to collapse: the single v1
// membership value, empty when the issue is project-less.
func TestProjectKey(t *testing.T) {
	if projectKey([]string{"p1"}) != "p1" {
		t.Errorf("projectKey must return the member id")
	}
	if projectKey(nil) != "" {
		t.Errorf("projectKey of an empty membership must be empty")
	}
}

// TestValidDate pins the date format: the client's YYYY-MM-DD, and
// nothing else (a timestamp or prose is a 422, not a bad row).
func TestValidDate(t *testing.T) {
	for _, d := range []struct {
		in   string
		want bool
	}{
		{"2026-01-02", true},
		{"2026-1-2", false},
		{"2026-01-02T00:00:00Z", false},
		{"yesterday", false},
		{"", false},
	} {
		if got := validDate(d.in); got != d.want {
			t.Errorf("validDate(%q) = %v, want %v", d.in, got, d.want)
		}
	}
}
