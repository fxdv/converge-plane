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

// TestHumanReviewProtocol pins the escalation's reserved parking column
// (spec 12 escalation): the name, the SQL fragments derived from it
// (one source of truth), and the human-handoff comment a pause leaves
// on the card (the path a human was missing).
func TestHumanReviewProtocol(t *testing.T) {
	if humanReviewStateName != "Human Review" {
		t.Fatalf("reserved state name = %q, want %q", humanReviewStateName, "Human Review")
	}
	wantLower := strings.ToLower(humanReviewStateName)
	if !strings.Contains(needsHumanSQL, wantLower) || !strings.Contains(needsHumanSQL, "i.agent_paused") {
		t.Fatalf("needsHumanSQL must be (flag OR column): %q", needsHumanSQL)
	}
	if !strings.Contains(notHumanReviewSQL, wantLower) || strings.Contains(notHumanReviewSQL, "agent_paused") {
		t.Fatalf("notHumanReviewSQL must be the column exclusion alone: %q", notHumanReviewSQL)
	}
	if !strings.HasPrefix(needsHumanSQL, "(") || !strings.HasSuffix(needsHumanSQL, ")") {
		t.Fatalf("needsHumanSQL must parenthesize for use inside larger predicates: %q", needsHumanSQL)
	}

	// The parked variant: the column claim and the column-specific resume
	// gesture are both present.
	parked := humanHandoffComment("handoff loop detected (3x in 24h)", true, "")
	for _, want := range []string{"Human Review", "handoff loop detected (3x in 24h)",
		"Reply here with your decision", "move the card back into the workflow", "no agent can act"} {
		if !strings.Contains(parked, want) {
			t.Errorf("parked comment missing %q:\n%s", want, parked)
		}
	}
	// The in-place variant (a team without the column): the reason and
	// the generic resume gesture, no column claim.
	inPlace := humanHandoffComment("budget exhausted", false, "")
	for _, want := range []string{"budget exhausted", "make any change to the card", "no agent can act"} {
		if !strings.Contains(inPlace, want) {
			t.Errorf("in-place comment missing %q:\n%s", want, inPlace)
		}
	}
	if strings.Contains(inPlace, "Human Review") {
		t.Errorf("in-place comment must not claim a column the team lacks:\n%s", inPlace)
	}

	// The task-level note (D4): it renders between the reason and the
	// resume path, and it must not drag the column claim into the
	// in-place variant.
	rich := humanHandoffComment("budget exhausted", true, "Did the repro; blocked on the deploy credentials; a human must provide them.")
	for _, want := range []string{"Where things stand:", "Did the repro", "a human must provide them"} {
		if !strings.Contains(rich, want) {
			t.Errorf("rich comment missing %q:\n%s", want, rich)
		}
	}
	inPlaceNote := humanHandoffComment("budget exhausted", false, "some note")
	if strings.Contains(inPlaceNote, "Human Review") {
		t.Errorf("in-place comment must not claim a column the team lacks:\n%s", inPlaceNote)
	}
}
