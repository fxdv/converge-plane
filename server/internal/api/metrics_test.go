// metrics_test.go — the metrics plane's pins.
//
// The endpoint itself is four read-only aggregates plus two in-memory
// snapshots, so the testable surface is what the plane promises: the
// wire contract the client page renders (the exact JSON keys, so a
// server rename fails in CI instead of breaking a page mysteriously in
// a browser) and the proxy registry's aggregation semantics (a dead
// node is a row of zeros; a failure keeps its lastError; a later
// success clears it).
package api

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestMetricsWireContract pins the exact JSON shape the client's
// metrics page renders. The client's TypeScript interface
// (web/src/services/workspace/metrics.ts) mirrors these keys, so a
// rename or deletion here is a test failure before it is a broken page.
func TestMetricsWireContract(t *testing.T) {
	now := time.Now().UTC()
	resp := metricsResponse{
		WorkspaceID: "ws-1",
		GeneratedAt: now,
		Product: productMetrics{
			Members: 3, MembersActive: 2,
			Teams: 2, Workflows: 3, Labels: 4, Projects: 1, Views: 2, Issues: 12,
			States:   []stateCount{{Name: "In Progress", Category: "IN_PROGRESS", Count: 3}},
			Comments: 45, HistoryRows: 200,
			IssuesCreated24h: 1, IssuesCreated7d: 5,
			IssuesDone24h: 2, IssuesDone7d: 9,
			Comments24h: 4, Comments7d: 11,
			Handoffs24h: 6, Handoffs7d: 20,
		},
		Codebase: codebaseMetrics{
			Version:        "1.2.0",
			GitSHA:         "abc123",
			BuildTime:      "2025-09-13T12:00:00Z",
			Platform:       "darwin/arm64",
			Uptime:         3600 * time.Second,
			Goroutines:     42,
			Pool:           poolGauges{Acquired: 2, Idle: 3, Max: 20},
			SSESubscribers: 2,
			FeedDepth:      37,
			FeedSequence:   "18211",
		},
		Swarm: swarmMetrics{
			Agents: 5, ActiveAgents: 4, Busy: 2,
			OpenIssues: 9, PausedIssues: 2,
			Tokens24h: 10000, Ops24h: 34, Handoffs24h: 6,
			Pauses24h: 2, PauseRate24h: 0.1, MeanResumeMs: 1234,
			MedianResumeMs:      987,
			Completions24h:      18,
			CostMedianTokens24h: 2200, CostMeanTokens24h: 3100, CostedIssues24h: 9,
			FallbackRate24h: 0.05, LongestHandoffChain24h: 3,
			CompletedByAgents7d: 11, CompletedByHumans7d: 3, AgentShare7d: 0.79,
			Roster: []swarmAgent{{
				ID: "a1", Name: "bravo-1", Status: "ACTIVE", Busy: true,
				Tokens24h: 2500, Ops24h: 8, Handoffs24h: 1,
			}},
		},
		Proxy: proxyMetrics{
			Mode: "llm", Model: "qwen3.8-27b", Timeout: 90 * time.Second,
			Nodes: []FleetNodeStat{{
				Node: 1, URL: "http://localhost:8000",
				Requests: 7, Successes: 6, Failures: 1,
				AvgLatency: 8000 * time.Millisecond, MaxLatency: 12 * time.Second,
				Tokens: 10000, LastError: "status 503", LastErrorAt: &now, LastSuccessAt: &now,
			}},
			AgentBurn: []agentBurnRow{{AccountID: "a1", Name: "bravo-1", Status: "ACTIVE", Usage24h: 341}},
			RateRPS:   10, RateBurst: 50,
		},
	}

	equalStrings(t, swarmKeys(t, resp),
		[]string{"codebase", "generatedAt", "product", "proxy", "swarm", "workspaceId"})
	equalStrings(t, swarmKeys(t, resp.Product),
		[]string{"comments", "comments24h", "comments7d", "handoffs24h", "handoffs7d",
			"historyRows", "issues", "issuesCreated24h", "issuesCreated7d",
			"issuesDone24h", "issuesDone7d", "labels", "members", "membersActive",
			"projects", "states", "teams", "views", "workflows"})
	equalStrings(t, swarmKeys(t, resp.Product.States[0]),
		[]string{"category", "count", "name"})
	equalStrings(t, swarmKeys(t, resp.Codebase),
		[]string{"buildTime", "feedDepth", "feedSequence", "gitSha",
			"goroutines", "platform", "pool", "sseSubscribers", "uptime", "version"})
	equalStrings(t, swarmKeys(t, resp.Codebase.Pool),
		[]string{"acquired", "idle", "max"})
	equalStrings(t, swarmKeys(t, resp.Swarm),
		[]string{"activeAgents", "agentShare7d", "agents", "busy",
			"completedByAgents7d", "completedByHumans7d", "completions24h",
			"costMeanTokens24h", "costMedianTokens24h", "costedIssues24h",
			"fallbackRate24h", "handoffs24h", "longestHandoffChain24h",
			"meanResumeMs", "medianResumeMs", "openIssues", "ops24h",
			"pauseRate24h", "pausedIssues", "pauses24h", "roster", "tokens24h"})
	equalStrings(t, swarmKeys(t, resp.Proxy),
		[]string{"agentBurn", "mode", "model", "nodes", "rateBurst", "rateRps", "timeout"})
	equalStrings(t, swarmKeys(t, resp.Proxy.Nodes[0]),
		[]string{"avgLatency", "failures", "lastError", "lastErrorAt",
			"lastSuccessAt", "maxLatency", "node", "requests", "successes", "tokens", "url"})
	equalStrings(t, swarmKeys(t, resp.Proxy.AgentBurn[0]),
		[]string{"accountId", "name", "status", "usage24h"})
}

// TestFleetNodeDeadRowShape pins the wire contract of a node the fleet
// never reached: a row of zeros in which the omitempty error fields are
// absent — the client renders "no data" with no null-guarding.
func TestFleetNodeDeadRowShape(t *testing.T) {
	c := newLLMClient([]string{"http://n1", "http://n2"}, "m", time.Second, 1, discardLogger())
	view := c.fleetView()
	if len(view) != 2 {
		t.Fatalf("rows = %d, want 2 (a dead node is a row of zeros)", len(view))
	}
	if view[0].Node != 1 || view[1].Node != 2 {
		t.Fatalf("node indices = %d, %d, want 1-based config order", view[0].Node, view[1].Node)
	}
	if n := view[1]; n.Requests != 0 || n.Successes != 0 || n.Failures != 0 || n.Tokens != 0 || n.LastError != "" || n.LastErrorAt != nil || n.LastSuccessAt != nil {
		t.Fatalf("an unseen node must be a zero row, got %+v", n)
	}
	equalStrings(t, swarmKeys(t, view[1]),
		[]string{"avgLatency", "failures", "maxLatency", "node",
			"requests", "successes", "tokens", "url"})

	// An errored node exposes exactly the two error keys.
	c.record("http://n1", time.Second, false, 0, errors.New("boom"))
	if got := swarmKeys(t, c.fleetView()[0]); len(got) != 10 {
		t.Fatalf("an errored node's keys = %v, want 10 (8 + lastError + lastErrorAt)", got)
	}
}

// TestFleetViewAggregation pins the registry's math: rows come back in
// configuration order with 1-based indices; the average latency counts
// successes only (a failed round trip is not a latency sample); tokens
// accumulate on both outcomes (a failed call still spent); a later
// success clears the last error.
func TestFleetViewAggregation(t *testing.T) {
	c := newLLMClient([]string{"http://n1", "http://n2", "http://n3"}, "m", time.Second, 1, discardLogger())

	// n1: success, then a failure, then a success again.
	c.record("http://n1", 10*time.Millisecond, true, 100, nil)
	c.record("http://n1", 30*time.Millisecond, false, 5, errors.New("endpoint down"))
	c.record("http://n1", 50*time.Millisecond, true, 200, nil)
	// n2: one failure only.
	c.record("http://n2", 200*time.Millisecond, false, 0, errors.New("status 503"))
	// n3: never touched.

	view := c.fleetView()
	if len(view) != 3 {
		t.Fatalf("rows = %d, want 3", len(view))
	}
	for i, n := range view {
		if n.Node != i+1 || n.URL != c.urls[i] {
			t.Fatalf("row %d = (node %d, %s), want (%d, %s)", i, n.Node, n.URL, i+1, c.urls[i])
		}
	}

	n1 := view[0]
	if n1.Requests != 3 || n1.Successes != 2 || n1.Failures != 1 {
		t.Fatalf("n1 counts = req %d ok %d err %d, want 3/2/1", n1.Requests, n1.Successes, n1.Failures)
	}
	if n1.AvgLatency != 30*time.Millisecond { // (10+50)/2 — the failure is not a sample
		t.Fatalf("n1 avg = %v, want 30ms", n1.AvgLatency)
	}
	if n1.MaxLatency != 50*time.Millisecond {
		t.Fatalf("n1 max = %v, want 50ms", n1.MaxLatency)
	}
	if n1.Tokens != 305 {
		t.Fatalf("n1 tokens = %d, want 305 (a failed call still spent 5)", n1.Tokens)
	}
	if n1.LastError != "" || n1.LastErrorAt != nil {
		t.Fatalf("a later success must clear the last error, got %q @ %v", n1.LastError, n1.LastErrorAt)
	}
	if n1.LastSuccessAt == nil {
		t.Fatal("n1 lastSuccessAt must be set after a success")
	}

	n2 := view[1]
	if n2.Requests != 1 || n2.Successes != 0 || n2.Failures != 1 {
		t.Fatalf("n2 counts = req %d ok %d err %d, want 1/0/1", n2.Requests, n2.Successes, n2.Failures)
	}
	if n2.AvgLatency != 0 || n2.MaxLatency != 200*time.Millisecond {
		t.Fatalf("n2 latency = avg %v max %v, want 0 / 200ms", n2.AvgLatency, n2.MaxLatency)
	}
	if n2.LastError != "status 503" || n2.LastErrorAt == nil {
		t.Fatalf("n2 lastError = %q @ %v, want status 503", n2.LastError, n2.LastErrorAt)
	}
	if n2.Tokens != 0 {
		t.Fatalf("n2 tokens = %d, want 0 (a failure that spent nothing)", n2.Tokens)
	}
	if n2.LastSuccessAt != nil {
		t.Fatal("n2 lastSuccessAt must stay nil")
	}

	if n3 := view[2]; n3.Requests != 0 || n3.Successes != 0 || n3.Failures != 0 || n3.Tokens != 0 || n3.LastError != "" {
		t.Fatalf("a dead node must be all zeros, got %+v", n3)
	}
}

// TestGuardWindowPair pins the two window constants against drift: the
// guards bind guardWindowSQL as a parameter value, the plane embeds
// guardWindowLiteral — a SQL literal, because a count window cannot be
// parameterized across the concatenated where-clause. If either value
// changes, the spec's quiet-guard section must change with it.
func TestGuardWindowPair(t *testing.T) {
	if guardWindow != 24*time.Hour {
		t.Fatalf("guardWindow = %v, want 24h", guardWindow)
	}
	if guardWindowSQL != "24 hours" {
		t.Fatalf("guardWindowSQL = %q, want the parameter value", guardWindowSQL)
	}
	if strings.Contains(guardWindowSQL, "interval") {
		t.Fatal("guardWindowSQL is bound as a parameter; it must carry no SQL syntax")
	}
	if guardWindowLiteral != "interval '24 hours'" {
		t.Fatalf("guardWindowLiteral = %q, want interval '24 hours'", guardWindowLiteral)
	}
}
