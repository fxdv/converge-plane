package api

import (
	"context"
	"math"
	"testing"
)

// A system move into Done has no actor. It still belongs in the 7-day
// denominator, and it counts for the agent when that issue has a run.
// Reported micro-USD on the run is the cost figure, not model tokens.
func TestSystemDoneCountsWhenAnAgentRan(t *testing.T) {
	f := newWorkFixture(t)
	withRun := f.issue(f.t1, f.done, "")
	systemOnly := f.issue(f.t1, f.done, "")
	human := f.issue(f.t1, f.done, "")
	hist := func(issue, actor string) {
		t.Helper()
		var who any
		kind := "system"
		if actor != "" {
			who = actor
			kind = "user"
		}
		f.exec(`insert into issue_history
			(workspace_id, team_id, issue_id, actor_id, actor_type, action, field, to_value)
			values ($1, $2, $3, $4, $5, 'updated', 'status', $6)`,
			f.ws, f.t1, issue, who, kind, f.done)
	}
	hist(withRun, "")
	hist(systemOnly, "")
	hist(human, f.owner)
	f.exec(`insert into agent_runs
		(workspace_id, issue_id, agent_id, cost_micros, ended_at, end_reason)
		values ($1, $2, $3, 2500, now(), 'closed')`,
		f.ws, withRun, f.ext1)

	var swarm swarmMetrics
	if err := f.a.swarmSection(context.Background(), f.ws, &swarm); err != nil {
		t.Fatal(err)
	}
	if swarm.Completions7d != 3 || swarm.CompletedByAgents7d != 1 || swarm.CompletedByHumans7d != 1 {
		t.Fatalf("completions=%d agents=%d humans=%d, want 3, 1, 1",
			swarm.Completions7d, swarm.CompletedByAgents7d, swarm.CompletedByHumans7d)
	}
	if math.Abs(swarm.AgentShare7d-1.0/3) > 1e-9 {
		t.Fatalf("share = %v, want 1/3", swarm.AgentShare7d)
	}
	if swarm.ReportedCostIssues24h != 1 || swarm.ReportedCostPerDoneMicros24h != 2500 {
		t.Fatalf("reported cost issues=%d micros=%d, want 1 and 2500",
			swarm.ReportedCostIssues24h, swarm.ReportedCostPerDoneMicros24h)
	}
	if swarm.CostedIssues24h != 0 {
		t.Fatalf("model-token sample = %d, want 0", swarm.CostedIssues24h)
	}

	var product productMetrics
	if err := f.a.productSection(context.Background(), f.ws, &product); err != nil {
		t.Fatal(err)
	}
	if product.IssuesDone7d != 3 || product.IssuesDoneAll != 3 {
		t.Fatalf("done 7d=%d all=%d, want 3 and 3", product.IssuesDone7d, product.IssuesDoneAll)
	}
}
