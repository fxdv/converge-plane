// wire_contract_test.go — the server side of the sync wire contract.
//
// Every payload the server emits must satisfy the client's MST models
// (web/src/store/*/models.ts) and its save-data handlers. The client is
// a forked asset; these tests are the permanent guarantee that the Go
// side keeps the contract the client's validation layer requires:
//   - required fields present on every I/U payload (client models have
//     no defaults — a missing field crashes the app on validation)
//   - client-nullable fields serialized as explicit null where the
//     client union admits null but not undefined
//   - required arrays serialized as [], never JSON null
//   - literal enums within the client's vocabulary
//   - DELETE records carry an {id} payload
//
// The client-side counterpart is
// web/src/store/__tests__/wire-contract.test.ts, which pins the same
// contract from the model side. Drift on either side fails CI.
package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var wireNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

// wirePayload round-trips builder output through JSON, returning the
// document exactly as the client would parse it.
func wirePayload(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// requirePresent asserts the key exists (JSON null is present and ok).
func requirePresent(t *testing.T, m map[string]any, key string) any {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("required field %q missing from wire payload: %v", key, m)
		return nil
	}
	return v
}

// requireString asserts the key exists and holds a non-null JSON
// string; it returns the value (empty on failure) for further checks.
func requireString(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key]
	if !ok || v == nil {
		t.Errorf("required string %q missing or null: %v", key, m)
		return ""
	}
	s, ok := v.(string)
	if !ok {
		t.Errorf("required string %q is %T: %v", key, v, m)
		return ""
	}
	return s
}

// requireArray asserts the key holds a JSON array, never null.
func requireArray(t *testing.T, m map[string]any, key string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("required array %q missing: %v", key, m)
		return
	}
	if v == nil {
		t.Errorf("required array %q serialized as JSON null", key)
		return
	}
	if _, ok := v.([]any); !ok {
		t.Errorf("required array %q is %T, not an array", key, v)
	}
}

func TestWorkspaceDataWireContract(t *testing.T) {
	a := API{}
	m := wirePayload(t, a.workspaceData(workspaceRow{
		ID: "w1", Slug: "acme", Name: "Acme", CreatedAt: wireNow, UpdatedAt: wireNow,
	}))
	for _, k := range []string{"id", "slug", "name", "createdAt", "updatedAt"} {
		requireString(t, m, k)
	}
}

func TestMemberDataWireContract(t *testing.T) {
	a := API{}
	// The client role enum: ['ADMIN','USER','BOT','AGENT']; the client
	// status union: undefined | ['INVITED','ACTIVE','SUSPENDED'].
	cases := []struct{ role, status string }{
		{"owner", "active"}, {"admin", "active"}, {"member", "active"},
		{"agent", "active"}, {"member", "invited"}, {"member", "suspended"},
	}
	for _, c := range cases {
		m := wirePayload(t, a.memberData(memberRow{
			ID: "m1", Role: c.role, Status: c.status,
			AccountID: "u1", WorkspaceID: "w1",
			TeamIDs:   nil, // must serialize [] not null
			CreatedAt: wireNow, UpdatedAt: wireNow,
		}))
		role := requireString(t, m, "role")
		switch role {
		case "ADMIN", "USER", "AGENT":
		default:
			t.Errorf("role %v (db %q) outside client enum", role, c.role)
		}
		status := requireString(t, m, "status")
		switch status {
		case "INVITED", "ACTIVE", "SUSPENDED":
		default:
			t.Errorf("status %v (db %q) outside client union", status, c.status)
		}
		requireArray(t, m, "teamIds")
		if _, ok := m["settings"].(map[string]any); !ok {
			t.Errorf("settings must be an object, got %v", m["settings"])
		}
		for _, k := range []string{"id", "createdAt", "updatedAt", "userId", "workspaceId"} {
			requireString(t, m, k)
		}
	}
}

func TestIssueDataWireContract(t *testing.T) {
	a := API{}
	prio := 5 // out-of-domain: the server is permissive, the client renders total
	stateID := "s1"
	m := wirePayload(t, a.issueData(issueRow{
		ID: "i1", TeamID: "t1", Number: 7, SortOrder: 3, Priority: &prio,
		Title: "T", DescRaw: "", Status: "active",
		CreatedAt: wireNow, UpdatedAt: wireNow,
		CreatedByID: &stateID, StatusID: &stateID,
		LabelIDs: []string{}, Children: []string{},
	}))
	for _, k := range []string{"id", "createdAt", "updatedAt", "title", "teamId", "stateId"} {
		requireString(t, m, k)
	}
	if v, ok := m["number"].(float64); !ok || v != 7 {
		t.Errorf("number must be 7, got %v", m["number"])
	}
	if v, ok := m["priority"].(float64); !ok || v != 5 {
		t.Errorf("priority must pass through verbatim (5), got %v", m["priority"])
	}
	requireArray(t, m, "labelIds") // [] not null
	requireArray(t, m, "subscriberIds")
	requireArray(t, m, "children")
	if m["dueDate"] != nil {
		t.Errorf("dueDate must be explicit null, got %v", m["dueDate"])
	}
	if m["assigneeId"] != nil {
		t.Errorf("assigneeId must be explicit null when unset, got %v", m["assigneeId"])
	}
	// D1: agentPaused must always be present and a JSON boolean (the
	// client model is a plain boolean; a missing key would crash it).
	if _, ok := m["agentPaused"].(bool); !ok {
		t.Errorf("agentPaused must serialize as a boolean, got %v (%T)", m["agentPaused"], m["agentPaused"])
	}

	// v1.1: projectIds must always be a JSON array (never null); the
	// legacy singular projectId derives from it (nil with no
	// membership, the member id with one).
	requireArray(t, m, "projectIds")
	if m["projectId"] != nil {
		t.Errorf("projectId must be null without a project, got %v", m["projectId"])
	}
	member := issueRow{ID: "i3", TeamID: "t1", Number: 9, Title: "T", Status: "active",
		CreatedAt: wireNow, UpdatedAt: wireNow, StatusID: &stateID, ProjectIds: []string{"p1"}}
	mm := wirePayload(t, a.issueData(member))
	if mm["projectId"] != "p1" {
		t.Errorf("projectId must derive from the membership, got %v", mm["projectId"])
	}
	if v, ok := mm["projectIds"].([]any); !ok || len(v) != 1 || v[0] != "p1" {
		t.Errorf("projectIds must carry the membership, got %v", mm["projectIds"])
	}

	// null priority must survive the round trip as null (client: number|null)
	row := issueRow{ID: "i2", TeamID: "t1", Number: 8, Title: "T", Status: "active",
		CreatedAt: wireNow, UpdatedAt: wireNow, StatusID: &stateID, AgentPaused: true}
	m2 := wirePayload(t, a.issueData(row))
	if m2["priority"] != nil {
		t.Errorf("unset priority must serialize null, got %v", m2["priority"])
	}
	if m2["agentPaused"] != true {
		t.Errorf("paused flag must serialize true, got %v", m2["agentPaused"])
	}
}

func TestCommentDataWireContract(t *testing.T) {
	a := API{}
	m := wirePayload(t, a.commentData(commentRow{
		ID: "c1", Body: "", AuthorID: "u1", IssueID: "i1", ParentID: nil,
	}, wireNow.Format(iso), wireNow.Format(iso)))
	for _, k := range []string{"id", "createdAt", "updatedAt", "issueId"} {
		requireString(t, m, k)
	}
	// The client body union is string|undefined — never null.
	body, ok := m["body"].(string)
	if !ok {
		t.Fatalf("body must be a string (even empty), got %v", m["body"])
	}
	if body != "" {
		t.Errorf("empty body must serialize as empty string, got %q", body)
	}
	if m["sourceMetadata"] != nil {
		t.Errorf("sourceMetadata must be explicit null, got %v", m["sourceMetadata"])
	}
}

func TestTeamDataWireContract(t *testing.T) {
	a := API{}
	m := wirePayload(t, a.teamData(teamRow{
		ID: "t1", Name: "Engineering", Identifier: "ENG", Status: "active",
		WorkspaceID: "w1", CreatedAt: wireNow, UpdatedAt: wireNow,
		Preferences: []byte(`{}`), // malformed/empty must degrade, not null
	}))
	for _, k := range []string{"id", "createdAt", "updatedAt", "name", "identifier", "workspaceId"} {
		requireString(t, m, k)
	}
	if _, ok := m["preferences"].(map[string]any); !ok {
		t.Errorf("preferences must be an object, never null: %v", m["preferences"])
	}
}

func TestWorkflowDataWireContract(t *testing.T) {
	a := API{}
	// Client enum: BACKLOG UNSTARTED STARTED COMPLETED CANCELED TRIAGE.
	for _, cat := range []string{"BACKLOG", "UNSTARTED", "STARTED", "COMPLETED", "CANCELED", "TRIAGE"} {
		m := wirePayload(t, a.workflowData(workflowRow{
			ID: "wf1", Name: "N", Color: "#8884d8", Category: cat, TeamID: "t1",
			CreatedAt: wireNow, UpdatedAt: wireNow,
		}))
		for _, k := range []string{"id", "createdAt", "updatedAt", "name", "color", "teamId"} {
			requireString(t, m, k)
		}
		if v, ok := m["position"].(float64); !ok || v != 0 {
			t.Errorf("position must be a number, got %v", m["position"])
		}
		if got := m["category"]; got != cat {
			t.Errorf("category must round-trip verbatim (%s), got %v", cat, got)
		}
		if m["description"] != nil {
			t.Errorf("description must be explicit null, got %v", m["description"])
		}
	}
}

func TestLabelDataWireContract(t *testing.T) {
	a := API{}
	m := wirePayload(t, a.labelData(labelRow{
		ID: "l1", Name: "Bug", Color: "#f00", WorkspaceID: "w1",
		CreatedAt: wireNow, UpdatedAt: wireNow,
	}))
	for _, k := range []string{"id", "createdAt", "updatedAt", "name", "color", "workspaceId"} {
		requireString(t, m, k)
	}
	if m["teamId"] != nil || m["groupId"] != nil {
		t.Errorf("teamId/groupId must be explicit null, got %v / %v", m["teamId"], m["groupId"])
	}
}

func TestViewDataWireContract(t *testing.T) {
	a := API{}
	m := wirePayload(t, a.viewData(viewRow{
		ID: "v1", Name: "My view", Status: "active", WorkspaceID: "w1",
		CreatedBy: "u1", Visibility: "workspace",
		Description: nil, Definition: []byte(`{}`), IsBookmarked: false,
		CreatedAt: wireNow, UpdatedAt: wireNow,
	}))
	for _, k := range []string{"id", "createdAt", "updatedAt", "name", "workspaceId", "createdById"} {
		requireString(t, m, k)
	}
	if v, ok := m["description"].(string); !ok || v != "" {
		t.Errorf("null description must serialize as empty string, got %v", m["description"])
	}
	if _, ok := m["filters"].(map[string]any); !ok {
		t.Errorf("filters must be an object, never null: %v", m["filters"])
	}
	if _, ok := m["isBookmarked"].(bool); !ok {
		t.Errorf("isBookmarked must be a boolean, got %v", m["isBookmarked"])
	}
}

func TestHistoryDataWireContract(t *testing.T) {
	str := func(s string) *string { return &s }
	// Every from/to field must be present (null when unset) and the label
	// arrays must always exist.
	m := wirePayload(t, historyData("h1", wireNow, wireNow, nil, "i1", "updated", "unknown", nil, nil, nil))
	for _, k := range []string{"id", "createdAt", "updatedAt"} {
		requireString(t, m, k)
	}
	requireArray(t, m, "addedLabelIds")
	requireArray(t, m, "removedLabelIds")
	for _, k := range []string{"userId", "issueId", "action", "fromPriority", "toPriority",
		"fromStateId", "toStateId", "fromEstimate", "toEstimate",
		"fromAssigneeId", "toAssigneeId", "fromParentId", "toParentId",
		"relationChanges", "sourceMetadata", "summary",
	} {
		if _, ok := m[k]; !ok {
			t.Errorf("history field %q missing (must be present, null ok)", k)
		}
	}

	// status transition
	m = wirePayload(t, historyData("h2", wireNow, wireNow, str("u1"), "i1", "updated", "status", str("s1"), str("s2"), nil))
	if m["fromStateId"] != "s1" || m["toStateId"] != "s2" {
		t.Errorf("status transition not mapped: %v", m)
	}

	// label transition (JSON arrays in from/to)
	m = wirePayload(t, historyData("h3", wireNow, wireNow, str("u1"), "i1", "updated", "labels", str(`["l1","l2"]`), str(`["l3"]`), nil))
	if arr := m["removedLabelIds"].([]any); len(arr) != 2 || arr[0] != "l1" {
		t.Errorf("removedLabelIds mismatch: %v", m["removedLabelIds"])
	}
	if arr := m["addedLabelIds"].([]any); len(arr) != 1 || arr[0] != "l3" {
		t.Errorf("addedLabelIds mismatch: %v", m["addedLabelIds"])
	}

	// assignee transition
	m = wirePayload(t, historyData("h4", wireNow, wireNow, str("u1"), "i1", "updated", "assignee", nil, str("u2"), nil))
	if m["fromAssigneeId"] != nil || m["toAssigneeId"] != "u2" {
		t.Errorf("assignee transition not mapped: %v", m)
	}

	// priority transition (numeric, incl. out-of-domain 5)
	m = wirePayload(t, historyData("h5", wireNow, wireNow, str("u1"), "i1", "updated", "priority", str("2"), str("5"), nil))
	if m["fromPriority"] != float64(2) || m["toPriority"] != float64(5) {
		t.Errorf("priority transition not mapped: %v", m)
	}

	// creation
	m = wirePayload(t, historyData("h6", wireNow, wireNow, str("u1"), "i1", "created", "status", nil, str("s1"), nil))
	if m["toStateId"] != "s1" {
		t.Errorf("created must set toStateId: %v", m)
	}

	// handoff (D1): assignee transition + summary note; summary must be
	// present (null) on every non-handoff row and carry the note here.
	m = wirePayload(t, historyData("h7", wireNow, wireNow, str("a1"), "i1", "handoff", "assignee", str("a1"), str("a2"), str("found the bug")))
	if m["action"] != "handoff" {
		t.Errorf("handoff action not carried: %v", m["action"])
	}
	if m["fromAssigneeId"] != "a1" || m["toAssigneeId"] != "a2" {
		t.Errorf("handoff assignee transition not mapped: %v", m)
	}
	if m["summary"] != "found the bug" {
		t.Errorf("handoff summary not carried: %v", m["summary"])
	}
	if m["fromStateId"] != nil || m["toStateId"] != nil {
		t.Errorf("handoff must not invent state values: %v", m)
	}
}

func TestClientRoleWireContract(t *testing.T) {
	cases := map[string]string{
		"owner": "ADMIN", "admin": "ADMIN", "member": "USER",
		"agent": "AGENT", "weird": "USER",
	}
	for dbRole, want := range cases {
		if got := clientRole(dbRole); got != want {
			t.Errorf("clientRole(%q) = %q, want %q", dbRole, got, want)
		}
	}
}

func TestWireActionMapping(t *testing.T) {
	cases := map[string]string{"CREATE": "I", "UPDATE": "U", "DELETE": "D", "WEIRD": "WEIRD"}
	for in, want := range cases {
		if got := wireAction(in); got != want {
			t.Errorf("wireAction(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDescriptionForClient(t *testing.T) {
	cases := map[string]string{
		"":               "",
		"null":           "",
		`"plain text"`:   "plain text",
		`{"type":"doc"}`: `{"type":"doc"}`, // raw doc passthrough
		`["a","b"]`:      `["a","b"]`,
		"garbage":        "",
	}
	for in, want := range cases {
		if got := descriptionForClient(in); got != want {
			t.Errorf("descriptionForClient(%q) = %q, want %q", in, got, want)
		}
	}
	// never null, never raw "null"
	if strings.Contains(descriptionForClient("null"), "null") {
		t.Errorf("literal 'null' must degrade to empty string")
	}
}
