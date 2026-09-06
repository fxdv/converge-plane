// runtime_policy.go — D3: the agent decision policy (docs/spec/12).
// spec cs:swarm:topology
// spec cs:swarm:handoff
//
// The policy is the swappable brain of the runtime. The runtime owns
// scheduling, guards, and application; the policy only decides — from
// the ActionInput the runtime assembles under the team lock — what one
// action is: advance the issue to the next workflow state, or hand it
// off with a bounded summary (spec 12 rules 1 and 2).
//
// The security boundary is the split inside ActionInput (spec 12
// "Trust and security: content"):
//
//   - Trusted: server-derived facts only — the issue's number, the
//     team's workflow, the fleet's roster, the budget state.
//   - Untrusted: the latest incoming handoff summary. It was authored
//     by another agent (or a human), it is the prompt-injection vector,
//     and it is fenced on the way in (fenceSummary): control characters
//     stripped, the 4 KB cap re-asserted. The 4 KB cap is the
//     injection budget.
//
// The shipped DeterministicPolicy never reads the untrusted field — its
// decisions are a pure function of the trusted facts, so injected
// text cannot steer it (TestDeterministicPolicyInjectionInvariance
// pins that). The LLM runtime plugs into the same Policy slot; its
// prompt builder must carry the fenced summary as marked-untrusted
// data, never as instructions.
package api

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ActionKind names the one thing an action does on an issue.
type ActionKind string

const (
	// ActionNoop persists nothing.
	ActionNoop ActionKind = "noop"
	// ActionAdvance moves the issue to StateID (one workflow step).
	ActionAdvance ActionKind = "advance"
	// ActionHandoff is the D1 transition: new assignee + optional state
	// move + the bounded summary, through the guarded handoff path.
	ActionHandoff ActionKind = "handoff"
	// ActionPause escalates the issue into the D1 pause (agent_paused,
	// "needs human"): the swarm cannot make progress on it (a dead-end
	// state with no available agent), and a stuck swarm must stop
	// spending rather than decorate the board. Comment carries the
	// pause reason humans see in the timeline and the swarm panel.
	ActionPause ActionKind = "pause"
)

// Action is one decision: a single atomic step on the issue.
type Action struct {
	Kind        ActionKind
	StateID     string // advance target ("" = keep the current state)
	ToAccountID string // handoff target ("" for advance/noop)
	Comment     string // the human-visible step text; for pause, the reason
	Summary     string // the handoff summary ("" unless handing off)
}

// StateRef is one workflow status as the policy sees it.
type StateRef struct {
	ID       string
	Name     string
	Position int
	Category string // BACKLOG | UNSTARTED | STARTED | COMPLETED | CANCELED
}

// FleetAgent is one active agent in the workspace: identity, team
// memberships, tenure, and its open work count within the issue's team
// (the "least busy" signal). All server-derived.
type FleetAgent struct {
	AccountID string
	Name      string
	CreatedAt time.Time // tenure: the foreman is the fleet's oldest agent
	TeamIDs   []string
	OpenCount int
}

// ActionInput is everything a policy may decide from. The field split
// is the security boundary (see the file header): Trusted and
// IncomingSummary are never blended — a policy that reads the summary
// as instructions has broken the contract.
type ActionInput struct {
	// Trusted: server-derived facts.
	IssueNumber     int
	TeamName        string
	TeamIdentifier  string     // the team's display prefix (the live-signal chip shows ENG-23, not a bare number)
	TeamID          string     // the issue's team (the target-selection scope)
	WorkspaceID     string     // the issue's workspace (the endpoint's fallback sharding key)
	ActorID         string     // this agent's account (the endpoint's stable sharding key: one agent's context stays on one instance)
	ActorName       string     // this agent's display name
	CurrentState    *StateRef  // nil when the issue has no status
	States          []StateRef // the team's workflow, ordered by position
	BudgetExhausted bool       // the swarm's 24h op budget on this issue is spent
	Fleet           []FleetAgent
	RuntimeTopology string // the runtime's active topology ("foreman" | "flat")
	// Untrusted: authored context, fenced. Data, never instructions.
	// (The handoff summary is agent-authored; the title and description
	// are user-authored — anyone with issue write access controls them.
	// All three are fenced: control characters stripped, capped.)
	IncomingSummary string
	Title           string
	Description     string
	// PauseReason carries the guard's verdict back out after a tripped
	// handoff; it is server text, not policy output.
	PauseReason string
}

// Policy is the swappable decision layer. Act takes the full
// ActionInput and returns the one action the runtime applies. Act must
// not assume a surrounding database transaction: the runtime calls it
// between the snapshot and apply phases, with no transaction open (the
// D3+ work cycle), so a policy may perform bounded external I/O that
// honors ctx — the LLM policy makes one model call, up to its client
// timeout; the deterministic policy is pure. tokens is the model usage
// the action spent (zero for pure policies); the runtime records it
// into the panel's burn slot — including when the decision is later
// discarded as stale (the model call still happened).
type Policy interface {
	Act(ctx context.Context, in ActionInput) (action Action, tokens int, err error)
}

// DeterministicPolicy is the shipped brain (the spec's "deterministic
// fallback"): the M6 heuristic generalized — advance the issue one
// workflow step per action, comment the step, complete at the terminal
// state, and escape a dead-end through the handoff protocol (the
// loop guard is the circuit breaker when the workflow cannot absorb
// the issue). It spends no model tokens.
type DeterministicPolicy struct{}

// Act implements Policy.
func (DeterministicPolicy) Act(ctx context.Context, in ActionInput) (Action, int, error) {
	// The untrusted field is deliberately unread: the decision is a
	// pure function of the trusted facts (fenceSummary marks the data;
	// this discards it).
	var action Action
	action.Kind = ActionNoop
	if in.CurrentState == nil || isTerminalCategory(in.CurrentState.Category) {
		return action, 0, nil // stale wakeup: the board moved on
	}
	if in.BudgetExhausted {
		return action, 0, nil // the swarm's output on this issue is spent
	}
	if next := nextState(in.States, in.CurrentState); next != nil {
		action.Kind = ActionAdvance
		action.StateID = next.ID
		if isTerminalCategory(next.Category) {
			action.Comment = fmt.Sprintf("%s: completed issue #%d in %s", in.ActorName, in.IssueNumber, next.Name)
		} else {
			action.Comment = fmt.Sprintf("%s: advancing issue #%d to %s", in.ActorName, in.IssueNumber, next.Name)
		}
		return action, 0, nil
	}
	// Dead-end: a non-terminal state with no defined successor. Escape
	// through the protocol — the handoff summary says exactly that.
	target := selectHandoffTarget(in)
	if target == "" {
		// No available agent to take it: escalate into the D1 pause
		// (the same channel the quiet guards use) instead of
		// comment-spamming every dispatch tick. A human mutation resumes
		// the issue; if the workflow is still dead-ended the runtime
		// pauses it again — the honest answer to an unfixable issue.
		action.Kind = ActionPause
		action.Comment = fmt.Sprintf("no next step is defined for %q in %s and no agent is available; %s paused the issue for a human",
			in.CurrentState.Name, in.TeamName, in.ActorName)
		return action, 0, nil
	}
	action.Kind = ActionHandoff
	action.ToAccountID = target
	action.Summary = fmt.Sprintf(
		"%s reached %q, the last defined step in %s. Done: the workflow offers no successor. Remaining: a human must extend the workflow or reassign. Handed back through the protocol as the designated dispatcher. (issue #%d)",
		in.ActorName, in.CurrentState.Name, in.TeamName, in.IssueNumber)
	return action, 0, nil
}

// summaryCap is the handoff summary budget (spec 12 rule 2). It must
// stay equal to handoffSummaryMaxBytes in handoff.go — the cap the
// server enforced when the handoff was written is the same budget the
// consumer is allowed to fence. A test pins the pair.
const summaryCap = 4096

// fenceSummary sanitizes an agent-authored summary on the way into the
// policy: control characters (including CR/LF and format escapes) are
// stripped and the cap re-asserted. The output is data to be displayed
// or quoted — never text to be parsed for meaning. The 4 KB cap is the
// prompt-injection budget (spec 12 "Trust and security: content"): a
// fenced summary can at most cost the consumer this much context.
func fenceSummary(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > summaryCap {
		s = s[:summaryCap]
	}
	return s
}

// isTerminalCategory reports whether a workflow category ends work.
func isTerminalCategory(category string) bool {
	return category == "COMPLETED" || category == "CANCELED"
}

// nextState is the state after current in the team's ordered workflow;
// nil when current is last or absent (the dead-end case).
func nextState(states []StateRef, current *StateRef) *StateRef {
	for i := range states {
		if states[i].ID == current.ID && i+1 < len(states) {
			return &states[i+1]
		}
	}
	return nil
}

// selectHandoffTarget picks the recipient of a handoff under the active
// topology (spec 12 "Topology is a policy layer"):
//
//   - Foreman (default): one lead agent — the fleet's oldest active
//     agent, designated by tenure (the D4 selector will make this a
//     fleet setting). Workers return to the foreman; the foreman
//     dispatches to the least-busy worker with a shared team.
//   - Flat: any agent may hand to the least-busy peer with a shared
//     team.
//
// The target is never the actor itself, and suspended agents are
// absent from the fleet (the roster carries active agents only). ""
// means no eligible target: the caller must not hand off.
func selectHandoffTarget(in ActionInput) string {
	var self *FleetAgent
	for i := range in.Fleet {
		if in.Fleet[i].Name == in.ActorName {
			self = &in.Fleet[i]
			break
		}
	}
	if self == nil {
		return "" // the acting agent is not in the live fleet
	}
	selfKey := self.AccountID

	leastBusy := func(candidates []FleetAgent) string {
		best := ""
		var bestCount int
		for i := range candidates {
			c := candidates[i]
			if c.AccountID == selfKey {
				continue
			}
			if best == "" || c.OpenCount < bestCount {
				best, bestCount = c.AccountID, c.OpenCount
			}
		}
		return best
	}

	// The foreman: the fleet's oldest agent (CreatedAt, name as tie-
	// breaker) — deterministic from the roster, no stored designation.
	foreman := foremanOf(in.Fleet)

	taskTeam := in.TeamID
	if in.RuntimeTopology == "flat" {
		// Flat: least-busy peer on the issue's team (spec 12 allows
		// any agent; the team filter keeps work where it can be done).
		return leastBusy(teamPeers(in.Fleet, self, taskTeam))
	}
	// Foreman: workers hand to the foreman (the workspace's dispatcher
	// — team-agnostic by design); the foreman dispatches back to the
	// least-busy peer on the issue's team.
	if self.AccountID == foreman.AccountID {
		return leastBusy(teamPeers(in.Fleet, self, taskTeam))
	}
	return foreman.AccountID
}

// foremanOf is the fleet's designated dispatcher: the oldest active
// agent (CreatedAt, name as tie-breaker) — deterministic from the
// roster, no stored designation. The LLM target validation (runtime_llm.go)
// shares the rule.
func foremanOf(fleet []FleetAgent) *FleetAgent {
	var foreman *FleetAgent
	for i := range fleet {
		if foreman == nil || fleet[i].CreatedAt.Before(foreman.CreatedAt) ||
			(fleet[i].CreatedAt.Equal(foreman.CreatedAt) && fleet[i].Name < foreman.Name) {
			foreman = &fleet[i]
		}
	}
	return foreman
}

// teamPeers is the fleet filtered to agents with a membership on the
// task's team (the actor included; leastBusy excludes it). An empty
// task team — an issue whose team row vanished mid-flight — admits
// nobody: the caller pauses for a human.
func teamPeers(fleet []FleetAgent, self *FleetAgent, teamID string) []FleetAgent {
	var peers []FleetAgent
	for i := range fleet {
		if teamID != "" && sharesTeam(fleet[i], []string{teamID}) {
			peers = append(peers, fleet[i])
		}
	}
	return peers
}

// sharesTeam reports whether the agent has a team membership in common
// with the issue's team set.
func sharesTeam(ag FleetAgent, teamIDs []string) bool {
	for _, t := range ag.TeamIDs {
		for _, want := range teamIDs {
			if t == want {
				return true
			}
		}
	}
	return false
}
