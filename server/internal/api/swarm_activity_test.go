package api

import (
	"encoding/json"
	"testing"
	"time"
)

// TestSwarmActivityWireShape pins the server's SwarmActivity wire
// contract: the exact key set and values the client's
// SwarmActivityType validates against. A key renamed on either side
// breaks the client's mobx-state-tree model in the browser — this
// test is the server's half of the wire contract (spec cs:swarm:activity).
func TestSwarmActivityWireShape(t *testing.T) {
	since := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	ref := swarmActivityRef{
		AgentID: "ag1", IssueID: "iss1", IssueNumber: 7,
		IssuePrefix: "ENG", Phase: swarmPhaseDeciding, Since: since,
	}
	raw, err := json.Marshal(ref.data("scout"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantKeys := []string{"id", "agentId", "agentName", "issueId",
		"issueNumber", "issuePrefix", "phase", "since"}
	if len(m) != len(wantKeys) {
		t.Fatalf("wire has %d keys, want %d: %v", len(m), len(wantKeys), m)
	}
	for _, k := range wantKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("wire missing key %q: %v", k, m)
		}
	}
	if m["id"] != "ag1" || m["agentId"] != "ag1" || m["agentName"] != "scout" {
		t.Errorf("identity fields drifted: %v", m)
	}
	if m["issueId"] != "iss1" || m["issueNumber"] != float64(7) || m["issuePrefix"] != "ENG" {
		t.Errorf("issue fields drifted: %v", m)
	}
	if m["phase"] != "deciding" {
		t.Errorf("phase = %v, want deciding", m["phase"])
	}
	if m["since"] != "2026-09-05T12:00:00Z" {
		t.Errorf("since = %v, want RFC3339 UTC", m["since"])
	}
}

// TestActivityForWorkspace scopes the bootstrap snapshot: only this
// workspace's live workers appear, and a worker with nothing in flight
// contributes nothing (a zero ref must not render an empty chip).
func TestActivityForWorkspace(t *testing.T) {
	rt := runtimeForTests(true)

	mkWorker := func(ws, agent, name, issue string, n int) *agentWorker {
		w := &agentWorker{rt: rt, workspaceID: ws, agentID: agent, name: name}
		if issue != "" {
			w.activityMu.Lock()
			w.activity = swarmActivityRef{
				AgentID: agent, IssueID: issue, IssueNumber: n,
				IssuePrefix: "ENG", Phase: swarmPhaseWorking, Since: time.Now(),
			}
			w.activityMu.Unlock()
		}
		rt.workers[ws+"/"+agent] = w
		return w
	}
	mkWorker("w1", "ag1", "scout", "iss1", 7)    // live: appears
	mkWorker("w1", "ag2", "forge", "", 0)        // idle worker: no record
	mkWorker("w2", "ag3", "outsider", "iss9", 9) // other workspace: excluded

	refs := rt.ActivityForWorkspace("w1")
	if len(refs) != 1 {
		t.Fatalf("ActivityForWorkspace(w1) = %d refs, want 1 (live worker only)", len(refs))
	}
	if refs[0].AgentID != "ag1" || refs[0].IssueID != "iss1" ||
		refs[0].IssueNumber != 7 || refs[0].Phase != swarmPhaseWorking {
		t.Fatalf("ref drifted: %+v", refs[0])
	}
	if got := rt.ActivityForWorkspace("w2"); len(got) != 1 || got[0].AgentID != "ag3" {
		t.Fatalf("other workspace leaked: %+v", got)
	}
	if got := rt.ActivityForWorkspace("w3"); len(got) != 0 {
		t.Fatalf("empty workspace reported refs: %+v", got)
	}
}
