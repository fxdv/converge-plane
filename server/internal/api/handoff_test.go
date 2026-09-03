// handoff_test.go — the D1 quiet-guard thresholds pinned in tests.
package api

import (
	"strings"
	"testing"
)

func TestGuardVerdict(t *testing.T) {
	// Quiet: well inside both budgets.
	if reason, tripped := guardVerdict(0, 0); tripped {
		t.Errorf("quiet issue tripped: %q", reason)
	}
	// One under each threshold: still allowed (the threshold-th
	// delivery is the one that trips on the next attempt).
	if reason, tripped := guardVerdict(handoffLoopThreshold-1, issueOpBudget-1); tripped {
		t.Errorf("threshold-minus-one tripped: %q", reason)
	}
	// Loop: the same agent receiving the issue K times in the window.
	reason, tripped := guardVerdict(handoffLoopThreshold, 0)
	if !tripped {
		t.Fatal("loop at threshold must trip")
	}
	if !strings.Contains(reason, "loop") {
		t.Errorf("loop reason must name the loop: %q", reason)
	}
	// Budget: the swarm's combined output on one issue.
	reason, tripped = guardVerdict(0, issueOpBudget)
	if !tripped {
		t.Fatal("budget at limit must trip")
	}
	if !strings.Contains(reason, "budget") {
		t.Errorf("budget reason must name the budget: %q", reason)
	}
	// Above the loop threshold: still the loop (checked first).
	reason, tripped = guardVerdict(handoffLoopThreshold+5, issueOpBudget+10)
	if !tripped || !strings.Contains(reason, "loop") {
		t.Errorf("loop takes precedence over budget, got %q", reason)
	}
}

func TestGuardConstants(t *testing.T) {
	// The confirmed owner constants (spec 12): 4 KB summary cap,
	// 3x/24h loop, 50 ops/24h budget. If these ever change, the spec
	// and the swarm briefing (web) must change with them.
	if handoffSummaryMaxBytes != 4096 {
		t.Errorf("summary cap = %d, want 4096", handoffSummaryMaxBytes)
	}
	if handoffLoopThreshold != 3 {
		t.Errorf("loop threshold = %d, want 3", handoffLoopThreshold)
	}
	if issueOpBudget != 50 {
		t.Errorf("op budget = %d, want 50", issueOpBudget)
	}
}
