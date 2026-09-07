// swarm_test.go — D2 pins.
//
// The swarm endpoint has no database of its own: it aggregates the
// trace tables, so the testable surface here is the wire contract (the
// exact JSON keys the client's SwarmStatus interface reads) and the
// usage counter's window semantics (the token-burn proxy).
package api

import (
	"encoding/json"
	"sort"
	"testing"
	"time"
)

// swarmKeys marshals v and returns its sorted JSON keys — the shape the
// client's TypeScript interface (web/src/services/workspace/swarm.ts)
// reads, pinned from the server side.
func swarmKeys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal %T: %v", v, err)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func equalStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) || len(got) == 0 {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("keys = %v, want %v", got, want)
		}
	}
}

func TestSwarmStatusWireContract(t *testing.T) {
	now := time.Now().UTC()
	status := swarmStatus{
		Agents: []swarmAgent{{
			ID:               "a1",
			Name:             "scout",
			Email:            "ag-00000001-scout@converge.local",
			Status:           "ACTIVE",
			TeamIDs:          []string{"t1"},
			Busy:             true,
			OpenIssueCount:   3,
			PausedIssueCount: 1,
			LastActivityAt:   &now,
			LastHandoff: &swarmHandoff{
				IssueID:         "i1",
				IssueNumber:     7,
				IssueTitle:      "Reproduce the flake",
				Direction:       "in",
				CounterpartID:   "a2",
				CounterpartName: "forge",
				Summary:         "repro found in ci step 3",
				CreatedAt:       now,
			},
			Handoffs24h: 2,
			Ops24h:      12,
			Requests24h: 341,
			CreatedAt:   now,
		}, {
			// The empty fleet row: no work, no trace — every field
			// still present, so the client never hits a missing key.
			ID:             "a2",
			Name:           "forge",
			Email:          "ag-00000001-forge@converge.local",
			Status:         "SUSPENDED",
			TeamIDs:        []string{},
			LastHandoff:    nil,
			LastActivityAt: nil,
			CreatedAt:      now,
		}},
		PausedIssues: []swarmPausedIssue{{
			ID:           "i2",
			Number:       23,
			Title:        "ENG-23",
			TeamID:       "t1",
			StateID:      "s1",
			Reason:       "handoff loop detected (the same agent received this issue 3 times in 24h)",
			PausedAt:     now,
			AssigneeID:   strvalptr("a1"),
			AssigneeName: strvalptr("scout"),
		}},
		Settings: swarmSettings{
			Topology:         "foreman",
			ForemanAccountID: strvalptr("a1"),
			ForemanName:      strvalptr("scout"),
		},
	}

	equalStrings(t, swarmKeys(t, status),
		[]string{"agents", "pausedIssues", "settings"})
	equalStrings(t, swarmKeys(t, status.Agents[0]),
		[]string{"busy", "createdAt", "email", "handoffs24h", "id", "lastActivityAt",
			"lastHandoff", "name", "openIssueCount", "ops24h", "pausedIssueCount",
			"requests24h", "status", "teamIds", "tokens24h"})
	equalStrings(t, swarmKeys(t, *status.Agents[0].LastHandoff),
		[]string{"counterpartId", "counterpartName", "createdAt", "direction",
			"issueId", "issueNumber", "issueTitle", "summary"})
	equalStrings(t, swarmKeys(t, status.PausedIssues[0]),
		[]string{"assigneeId", "assigneeName", "id", "number", "pausedAt",
			"reason", "stateId", "teamId", "title"})
	equalStrings(t, swarmKeys(t, status.Settings),
		[]string{"foremanAccountId", "foremanName", "topology"})

	// The empty row must serialize its optionals as JSON null, and its
	// arrays as [] — the client union admits null but not a missing key.
	raw, err := json.Marshal(status.Agents[1])
	if err != nil {
		t.Fatalf("marshal empty agent: %v", err)
	}
	var empty map[string]any
	if err := json.Unmarshal(raw, &empty); err != nil {
		t.Fatalf("unmarshal empty agent: %v", err)
	}
	for _, k := range []string{"lastHandoff", "lastActivityAt"} {
		if _, ok := empty[k]; !ok {
			t.Errorf("empty agent row missing key %q", k)
		} else if empty[k] != nil {
			t.Errorf("empty agent row key %q = %v, want null", k, empty[k])
		}
	}
	if teamIDs, ok := empty["teamIds"].([]any); !ok || len(teamIDs) != 0 {
		t.Errorf("empty agent row teamIds = %v, want []", empty["teamIds"])
	}
}

func TestAccountUsageWindow(t *testing.T) {
	l := newAccountRateLimiter(0, 0) // limiter disabled; usage is independent
	l.recordUsage("a1")
	l.recordUsage("a1")
	l.recordUsage("a2")
	if got := l.usageCount("a1"); got != 2 {
		t.Fatalf("usageCount(a1) = %d, want 2", got)
	}
	if got := l.usageCount("a2"); got != 1 {
		t.Fatalf("usageCount(a2) = %d, want 1", got)
	}
	if got := l.usageCount("unknown"); got != 0 {
		t.Fatalf("usageCount(unknown) = %d, want 0", got)
	}
	// An expired window reads 0 and the next record restarts it.
	l.mu.Lock()
	l.usage["a1"].windowStart = time.Now().Add(-guardWindow - time.Second)
	l.mu.Unlock()
	if got := l.usageCount("a1"); got != 0 {
		t.Fatalf("usageCount(a1) after window expiry = %d, want 0", got)
	}
	l.recordUsage("a1")
	if got := l.usageCount("a1"); got != 1 {
		t.Fatalf("usageCount(a1) after window restart = %d, want 1", got)
	}
	// Nil receiver is a no-op everywhere (the disabled-dep pattern).
	var nilLimiter *accountRateLimiter
	nilLimiter.recordUsage("a1")
	if got := nilLimiter.usageCount("a1"); got != 0 {
		t.Fatalf("nil limiter usageCount = %d, want 0", got)
	}
}

func strvalptr(s string) *string {
	return &s
}

// TestEffectiveForeman pins the D4 rule on the panel's roster: the
// designation wins while that agent is active; a suspended or missing
// designation degrades to the auto rule (the roster's oldest active
// agent).
func TestEffectiveForeman(t *testing.T) {
	roster := []swarmAgent{
		{ID: "alpha", Name: "alpha", Status: "ACTIVE"},
		{ID: "bravo", Name: "bravo", Status: "ACTIVE"},
		{ID: "charlie", Name: "charlie", Status: "SUSPENDED"},
	}
	if got := effectiveForeman(roster, ""); got != "alpha" {
		t.Fatalf("auto = %q, want the oldest active", got)
	}
	if got := effectiveForeman(roster, "bravo"); got != "bravo" {
		t.Fatalf("active designation = %q, want the designation", got)
	}
	if got := effectiveForeman(roster, "charlie"); got != "alpha" {
		t.Fatalf("suspended designation = %q, want the auto fallback", got)
	}
	if got := effectiveForeman(roster, "ghost"); got != "alpha" {
		t.Fatalf("missing designation = %q, want the auto fallback", got)
	}
}
