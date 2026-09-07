// runtime.go — D3: the agent runtime (docs/spec/12).
// spec cs:swarm:runtime
// spec cs:swarm:guards
//
// The runtime is what the handoff protocol is for: an in-process swarm
// engine that makes agents with work act. It runs inside the API
// process (the arch:shape contract: one artifact, in-process workers)
// and leans on the existing seams — no new tables, no new dependencies:
//
//   - The inbox is the database. An agent's work is "issues assigned to
//     it that are open, not paused, and not terminal" (issue rows) plus
//     the incoming handoff summaries that carry context (issue_handoffs,
//     D1). The issue table is the queue: durable, crash-safe, and the
//     single source of truth.
//   - The dispatcher is a backstop scan. One goroutine ticks (default
//     5s, CONVERGE_RUNTIME_TICK) and reconciles the fleet: an agent with
//     actionable work gets a worker; an agent without one (or a
//     suspended/removed agent) loses its worker. Work created while the
//     process is down is picked up on the first tick after boot.
//   - Wakeups are the fast path. The mutation paths that create agent
//     work (handoff applied, reassignment, resume-from-pause) wake the
//     dispatcher non-blocking right after commit, so the board reacts in
//     milliseconds instead of a tick. A lost wakeup costs at most one
//     tick: the scan is the truth, the wake is a hint.
//   - Workers are per-agent and self-terminating. At most one worker
//     exists per agent at a time (one agent works one issue at a time —
//     that is the honest "busy" the panel shows, and it serializes the
//     agent against itself structurally), and a worker exits when its
//     queue drains. Concurrency is bounded by the fleet size, never by
//     the issue count; a 100-agent workspace costs at most 100
//     short-lived goroutines.
//   - Every action takes the D1 transactional shape in two phases: the
//     snapshot transaction and the apply transaction, each holding the
//     issue's team advisory lock — the quiet guards read under it, one
//     history row, one outbox record per changed model, commit,
//     broadcast. The decision sits between the phases, outside any
//     transaction: the LLM policy's model call (tens of seconds) must
//     not hold a connection, a transaction, or a lock, or model latency
//     would serialize the team and freeze transaction-time timestamps
//     (Postgres now() is transaction-scoped). Agents write through the
//     same path humans and the API do, so the trace (spec 12: "the
//     trace is the product") is indistinguishable in origin —
//     distinguishable only by the actor's kind.
//   - Spend is a new in-memory seam: per-agent model tokens over the
//     shared 24h guard window, reported into the swarm panel's burn slot
//     (tokens24h). Deterministic policies spend zero; an LLM policy
//     reports its real usage through the same slot.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"converge/internal/auth"
)

// AgentRuntime is the in-process swarm engine. It is constructed by
// api.New and driven from main (Start/Stop on the process context); the
// API handlers feed it through Wake when they create agent work.
type AgentRuntime struct {
	a      *API
	log    *slog.Logger
	policy Policy // the decision layer (DeterministicPolicy ships; an LLM policy plugs in here)

	mu      sync.Mutex
	workers map[string]*agentWorker // "workspaceID/agentID" -> live worker
	spend   map[string]*spendWindow // agentID -> rolling 24h model tokens
	notify  chan struct{}           // coalesced fast-path trigger (buffer 1)
	done    chan struct{}
}

// newAgentRuntime builds the runtime; it is inert until Start.
func newAgentRuntime(a *API) *AgentRuntime {
	return &AgentRuntime{
		a:       a,
		log:     a.log,
		policy:  selectPolicy(a),
		workers: make(map[string]*agentWorker),
		spend:   make(map[string]*spendWindow),
		notify:  make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
}

// policyName reports the active decision brain for the start line and
// logs: which policy the runtime will drive.
func (rt *AgentRuntime) policyName() string {
	switch rt.policy.(type) {
	case fallbackPolicy:
		return "llm (deterministic fallback)"
	case DeterministicPolicy:
		return "deterministic"
	default:
		return "custom"
	}
}

// enabled reports whether the runtime acts. Disabled deployments still
// serve the D1/D2 surfaces; agents simply wait for their own runtime.
func (rt *AgentRuntime) enabled() bool {
	return rt.a.cfg.RuntimeEnabled
}

// Start launches the dispatcher. It is a no-op when the runtime is
// disabled; it returns immediately (the dispatcher runs on ctx) and is
// safe to call once.
func (rt *AgentRuntime) Start(ctx context.Context) {
	if !rt.enabled() {
		return
	}
	go rt.dispatch(ctx)
	rt.log.Info("agent runtime started",
		"topology", rt.a.cfg.RuntimeTopology, "tick", rt.a.cfg.RuntimeTick.String(),
		"policy", rt.policyName())
}

// Stop signals the dispatcher and every live worker to drain, then
// waits briefly for them to exit. Best-effort: the process context does
// the real forcing on shutdown.
func (rt *AgentRuntime) Stop() {
	select {
	case <-rt.done:
	default:
		close(rt.done)
	}
	rt.mu.Lock()
	for key, w := range rt.workers {
		w.cancel()
		delete(rt.workers, key)
	}
	rt.mu.Unlock()
}

// Wake nudges the dispatcher to reconcile now. Non-blocking and
// coalesced: at most one pending nudge exists at a time, and a lost or
// redundant one is harmless — the tick backstop catches any work it
// misses. Safe on a nil receiver (the disabled-dep pattern).
func (rt *AgentRuntime) Wake(workspaceID, agentID string) {
	if rt == nil || !rt.enabled() {
		return
	}
	select {
	case rt.notify <- struct{}{}:
	default:
	}
}

// RecordSpend adds model tokens to the agent's rolling 24h window — the
// swarm panel's burn slot. Same window economics as the rate limiter's
// request counter and the quiet guards: one shared window, no drift.
// Safe on a nil receiver.
func (rt *AgentRuntime) RecordSpend(agentID string, tokens int) {
	if rt == nil || !rt.enabled() || agentID == "" || tokens <= 0 {
		return
	}
	now := time.Now()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	w, ok := rt.spend[agentID]
	if !ok || now.Sub(w.windowStart) >= guardWindow {
		rt.spend[agentID] = &spendWindow{tokens: int64(tokens), windowStart: now}
		return
	}
	w.tokens += int64(tokens)
}

// SpendCount returns the agent's 24h model-token total (the panel's
// tokens24h); 0 outside the window or for an idle agent. Safe on a nil
// receiver.
func (rt *AgentRuntime) SpendCount(agentID string) int64 {
	if rt == nil || !rt.enabled() {
		return 0
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	w, ok := rt.spend[agentID]
	if !ok || time.Since(w.windowStart) >= guardWindow {
		return 0
	}
	return w.tokens
}

// spendWindow is one agent's rolling token total over the guard window.
// The window resets on first use after expiry: a long-idle agent's
// counter is exact at the cost of at most one window of drift at the
// reset (the same argument as accountUsage).
type spendWindow struct {
	tokens      int64
	windowStart time.Time
}

// dispatch is the reconciler loop: on every tick (or wake) it ensures
// every busy agent has exactly one worker and every idle or retired
// agent has none. One goroutine for all workspaces: the scan is a few
// bounded indexed reads, and v1 is single-instance by contract
// (arch:seams: a multi-instance deployment moves reconciliation to the
// shared broker).
func (rt *AgentRuntime) dispatch(ctx context.Context) {
	ticker := time.NewTicker(rt.a.cfg.RuntimeTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-rt.done:
			return
		case <-ticker.C:
		case <-rt.notify:
		}
		rt.reconcile(ctx)
	}
}

// reconcile scans the fleet and starts/stops workers to match it.
func (rt *AgentRuntime) reconcile(ctx context.Context) {
	workspaces, err := rt.workspacesWithAgents(ctx)
	if err != nil {
		rt.log.Warn("runtime: workspace scan failed", "error", err)
		return
	}
	for _, ws := range workspaces {
		rt.reconcileWorkspace(ctx, ws)
	}
}

// workspacesWithAgents lists the workspaces that currently have at least
// one active agent member.
func (rt *AgentRuntime) workspacesWithAgents(ctx context.Context) ([]string, error) {
	rows, err := rt.a.pool.Query(ctx, `
		select distinct wm.workspace_id::text
		from workspace_members wm
		join accounts a on a.id = wm.account_id
		where a.kind = $1 and wm.status = 'active'`, auth.AccountKindAgent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// fleetAgent is one active agent's reconciliation snapshot.
type fleetAgent struct {
	accountID string
	name      string
	openCount int // open, unpaused, non-terminal issues it can act on
}

// reconcileWorkspace starts workers for agents with work and retires
// workers for agents without. The spawn query mirrors the panel's
// workload read (D2) plus the quiet-guard budget filter: an issue whose
// 24h agent-operation budget is spent is not spawn-worthy work, so a
// swarm that exhausted a board goes quiet instead of re-spawning every
// tick (the window resets on its own).
func (rt *AgentRuntime) reconcileWorkspace(ctx context.Context, workspaceID string) {
	fleet, err := rt.fleetSnapshot(ctx, workspaceID)
	if err != nil {
		rt.log.Warn("runtime: fleet scan failed", "workspace", workspaceID, "error", err)
		return
	}
	rt.mu.Lock()
	started := rt.applyPlanLocked(workspaceID, fleet)
	rt.mu.Unlock()
	for _, w := range started {
		rt.log.Debug("runtime: worker started", "agent", w.name, "workspace", workspaceID)
		go w.run()
	}
}

// applyPlanLocked reconciles the worker map against the fleet snapshot:
// one worker per agent with actionable work, none for the rest (their
// workers are cancelled so they exit after the current action). The
// caller holds rt.mu and starts the returned workers' run loops. The
// bookkeeping is pure with respect to the database — the snapshot is
// already read — so the start/stop logic is unit-testable.
func (rt *AgentRuntime) applyPlanLocked(workspaceID string, fleet []fleetAgent) (started []*agentWorker) {
	for _, ag := range fleet {
		key := workspaceID + "/" + ag.accountID
		w := rt.workers[key]
		if ag.openCount > 0 {
			if w == nil {
				w = &agentWorker{
					rt:          rt,
					workspaceID: workspaceID,
					agentID:     ag.accountID,
					name:        ag.name,
				}
				w.ctx, w.cancelFn = context.WithCancel(context.Background())
				rt.workers[key] = w
				started = append(started, w)
			}
			continue
		}
		if w != nil {
			// No work (or a suspension the roster just dropped): the
			// worker finishes its current action, then exits.
			w.cancel()
			delete(rt.workers, key)
		}
	}
	return started
}

// fleetSnapshot reads the workspace's active agents with their actionable
// work counts. The budget filter (issueOpBudget over the guard window)
// keeps exhausted issues out of the spawn set — the constant pair is
// fmt'd in, the single source of truth staying handoff.go — and the
// Human Review exclusion keeps parked cards out the same way: a card in
// that column waits for a human, not for a worker.
func (rt *AgentRuntime) fleetSnapshot(ctx context.Context, workspaceID string) ([]fleetAgent, error) {
	rows, err := rt.a.pool.Query(ctx, `
		select a.id, a.name,
		       count(*) filter (where not i.agent_paused and `+notHumanReviewSQL+`
			                          and (select count(*)
				                             from issue_history h
				                             join accounts ha on ha.id = h.actor_id
				                           where h.issue_id = i.id
				                             and ha.kind = $2
				                             and h.created_at > now() - $3::interval) < $4)
		from accounts a
		join workspace_members wm on wm.workspace_id = $1
		                               and wm.account_id = a.id
		left join issues i on i.assignee_id = a.id
		                          and i.status = 'active'
		left join teams t on t.id = i.team_id
		                         and t.workspace_id = $1
		left join workflow_statuses ws on ws.id = i.status_id
		where a.kind = $2 and wm.status = 'active'
		  and (i.id is null
		       or coalesce(ws.category, 'UNSTARTED') not in ('COMPLETED', 'CANCELED'))
		group by a.id, a.name`,
		workspaceID, auth.AccountKindAgent, guardWindowSQL, issueOpBudget)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []fleetAgent{}
	for rows.Next() {
		var ag fleetAgent
		if err := rows.Scan(&ag.accountID, &ag.name, &ag.openCount); err != nil {
			return nil, err
		}
		out = append(out, ag)
	}
	return out, rows.Err()
}

// agentWorker is one agent's live work loop: at most one per agent,
// serialized against itself by construction, and self-terminating when
// its queue drains, its account is retired, or it is cancelled.
type agentWorker struct {
	rt          *AgentRuntime
	workspaceID string
	agentID     string
	name        string
	ctx         context.Context
	cancelFn    context.CancelFunc
	// activityMu guards activity: written by the worker's own
	// goroutine, read by the bootstrap collector (another goroutine).
	activityMu sync.Mutex
	activity   swarmActivityRef // zero value: nothing in flight
}

func (w *agentWorker) cancel() {
	w.cancelFn()
}

// finish releases the worker's slot exactly once and stops the
// in-flight signal (the board's live chip must clear when the worker
// is gone, whatever the reason: drained queue, retirement, cancel).
func (w *agentWorker) finish() {
	w.cancelFn()
	w.clearActivity()
	key := w.workspaceID + "/" + w.agentID
	w.rt.mu.Lock()
	if cur := w.rt.workers[key]; cur == w {
		delete(w.rt.workers, key)
	}
	w.rt.mu.Unlock()
}

// run works the agent's queue until it drains. One workOne call is one
// action on one issue; a DB error retires the worker (the dispatcher's
// next tick respawns it if work remains — the tick is the retry).
func (w *agentWorker) run() {
	defer w.finish()
	defer w.rt.log.Debug("runtime: worker exited", "agent", w.name, "workspace", w.workspaceID)
	for {
		select {
		case <-w.ctx.Done():
			return
		default:
		}
		// Retired (suspended/deleted) accounts stop acting: the roster
		// is the source of truth for liveness.
		if !w.rt.agentActive(w.ctx, w.workspaceID, w.agentID) {
			return
		}
		acted, err := w.workOne(w.ctx)
		if err != nil {
			w.rt.log.Warn("runtime: work cycle failed", "agent", w.name,
				"workspace", w.workspaceID, "error", err)
			return
		}
		if !acted {
			return // queue drained
		}
	}
}

// agentActive reports whether the agent is a live (active) workspace
// member with a machine identity.
func (rt *AgentRuntime) agentActive(ctx context.Context, workspaceID, agentID string) bool {
	var ok bool
	err := rt.a.pool.QueryRow(ctx, `
		select exists(
			select 1
			from workspace_members wm
			join accounts a on a.id = wm.account_id
			where wm.workspace_id = $1 and wm.account_id = $2
			  and wm.status = 'active' and a.status = 'active' and a.kind = $3)`,
		workspaceID, agentID, auth.AccountKindAgent).Scan(&ok)
	return err == nil && ok
}

// workOne performs one action on the agent's next actionable issue and
// reports whether it acted. The cycle is three phases (spec 12, the D3+
// shape): snapshot, decide, apply. The two transactional phases hold
// the issue's team advisory lock (the D1 shape); the decision sits
// between them, outside any transaction:
//
//  1. Snapshot — one short transaction under the team lock: the row is
//     read under the lock, the cycle is gated on it still being
//     actionable, the policy's ActionInput is assembled, and the row's
//     version is captured. The lock and the connection die with the
//     phase.
//  2. Decide — the policy call, alone: a pure decision costs
//     microseconds; the LLM decision is one model call bounded by its
//     own timeout and by ctx. No database connection, transaction, or
//     lock is held across it — model latency never serializes the team
//     (other agents' work and human mutations proceed freely), and the
//     apply phase's transaction-time timestamps stay fresh.
//  3. Apply — one short transaction under the same team lock: the row
//     is re-read under the lock and must still be the one the decision
//     was made on (decideStillValid: same owner, still actionable,
//     same version — every issue mutation bumps it). A human mutation
//     during the model call changed the version: the stale decision is
//     discarded — its tokens were still spent, so they are counted —
//     and the worker re-picks. A valid decision applies through the
//     guarded path, commits, and broadcasts.
func (w *agentWorker) workOne(ctx context.Context) (bool, error) {
	issueID, err := w.nextIssue(ctx)
	if err != nil {
		return false, err
	}
	if issueID == "" {
		return false, nil // queue drained
	}

	in, version, ok, err := w.snapshotTx(ctx, issueID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil // stale pick: exit; the dispatcher re-derives
	}

	// The in-flight signal (the trace has no record for work that has
	// not yet happened): the board's live chip and the swarm panel
	// render this, the client TTLs it.
	now := time.Now()
	ref := swarmActivityRef{
		AgentID: w.agentID, IssueID: issueID,
		IssueNumber: in.IssueNumber, IssuePrefix: in.TeamIdentifier,
		Since: now,
	}
	w.setActivity(ref.WithPhase(swarmPhaseWorking))
	if w.rt.hasLLMPolicy() {
		ref.Since = time.Now()
		w.setActivity(ref.WithPhase(swarmPhaseDeciding))
	}

	action, tokens, err := w.rt.policy.Act(ctx, in)
	if err != nil {
		return false, fmt.Errorf("policy: %w", err)
	}

	return w.applyTx(ctx, issueID, in, version, action, tokens)
}

// hasLLMPolicy reports whether the active policy makes model calls.
// The "deciding" signal is only worth emitting when a phase takes
// seconds; the deterministic policy decides in microseconds and its
// working/deciding distinction would only flicker.
func (rt *AgentRuntime) hasLLMPolicy() bool {
	_, ok := rt.policy.(fallbackPolicy)
	return ok
}

// loadIssueRowTx reads one issue row with its version anchor; it is
// pgx.ErrNoRows when the issue is gone.
func (a *API) loadIssueRowTx(ctx context.Context, tx pgx.Tx, issueID string, row *issueRow) error {
	return tx.QueryRow(ctx,
		"select "+issueColumns+", i.version from issues i where i.id = $1", issueID).Scan(
		&row.ID, &row.TeamID, &row.Number, &row.Priority, &row.SortOrder,
		&row.Title, &row.DescRaw, &row.Status, &row.CreatedAt, &row.UpdatedAt,
		&row.CreatedByID, &row.AssigneeID, &row.ParentID, &row.StatusID,
		&row.AgentPaused, &row.LabelIDs, &row.Children, &row.Version)
}

// snapshotTx is the work cycle's first phase: one short transaction
// under the team advisory lock that captures the decision's input. The
// row is read before the lock only for its team id (the lock key); the
// authoritative read happens under the lock, so a human mutation that
// lands in between is already visible when the cycle is gated.
func (w *agentWorker) snapshotTx(ctx context.Context, issueID string) (in ActionInput, version int, ok bool, err error) {
	a := w.rt.a
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return ActionInput{}, 0, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var row issueRow
	if err := a.loadIssueRowTx(ctx, tx, issueID, &row); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Deleted between the pick and the lock: the queue moved on,
			// not a failure. The worker exits; the dispatcher re-derives.
			return ActionInput{}, 0, false, nil
		}
		return ActionInput{}, 0, false, err
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_issue_"+row.TeamID); err != nil {
		return ActionInput{}, 0, false, err
	}
	// The locked read is the decision: stale wakeups (a human paused,
	// reassigned, or completed the issue between the pick and the lock)
	// commit nothing.
	if err := a.loadIssueRowTx(ctx, tx, issueID, &row); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ActionInput{}, 0, false, nil
		}
		return ActionInput{}, 0, false, err
	}
	if row.Status != "active" || row.AgentPaused || strval(row.AssigneeID) != w.agentID {
		_ = tx.Commit(ctx)
		return ActionInput{}, 0, false, nil
	}
	in, err = a.runtimeInputTx(ctx, tx, w, row)
	if err != nil {
		return ActionInput{}, 0, false, err
	}
	return in, row.Version, true, tx.Commit(ctx)
}

// applyTx is the work cycle's third phase: one short transaction under
// the same team lock that applies the decision. The row is re-read
// under the lock and must still be the one the decision was made on;
// a stale decision is discarded (its tokens are counted as spend — the
// model call happened) and the worker re-picks.
func (w *agentWorker) applyTx(ctx context.Context, issueID string, in ActionInput, version int, action Action, tokens int) (bool, error) {
	a := w.rt.a
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The snapshot's team key: if a team move happened during the model
	// call the issue is in another team, but the move bumped the
	// version, so the anchor below discards the decision either way.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_issue_"+in.TeamID); err != nil {
		return false, err
	}
	var row issueRow
	if err := a.loadIssueRowTx(ctx, tx, issueID, &row); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Deleted during the model call: nothing to apply. The work
			// moved on; the worker re-picks (or drains).
			_ = tx.Commit(ctx)
			return true, nil
		}
		return false, err
	}
	if !decideStillValid(row, version, w.agentID) {
		// The row changed while the decision was being made (a human
		// mutation bumped the version): the decision is stale. Commit
		// nothing; the worker re-picks and re-decides from fresh facts.
		w.rt.RecordSpend(w.agentID, tokens)
		w.rt.log.Debug("runtime: stale decision discarded", "issue", issueID,
			"agent", w.name, "workspace", w.workspaceID, "tokens", tokens)
		_ = tx.Commit(ctx)
		return true, nil
	}
	recs, tripped, reason, err := a.applyActionTx(ctx, tx, w, row, in, action)
	if err != nil {
		return false, err
	}
	if tripped {
		// The issue paused (a quiet guard, or the decision IS the pause —
		// the Human Review protocol committed it): the worker moves on;
		// the paused issue is no longer actionable.
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		for i := range recs {
			a.broadcastRecord(recs[i])
		}
		w.rt.RecordSpend(w.agentID, tokens)
		w.rt.log.Warn("issue paused for human review (escalation)", "issue", row.ID,
			"workspace", w.workspaceID, "agent", w.name, "reason", reason)
		return true, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	for i := range recs {
		a.broadcastRecord(recs[i])
	}
	w.rt.RecordSpend(w.agentID, tokens)
	w.rt.log.Info("agent action", "agent", w.name, "issue", row.ID,
		"workspace", w.workspaceID, "action", string(action.Kind))
	return true, nil
}

// decideStillValid reports whether the row re-read under the lock in the
// apply phase is the one the decision was made on: still actionable for
// the agent and unchanged since the snapshot. Every issue mutation
// (human or agent) bumps version, so version equality is a complete
// anchor — a state move, a pause, a reassignment, or a team move all
// invalidate the decision.
func decideStillValid(row issueRow, version int, agentID string) bool {
	return row.Status == "active" && !row.AgentPaused &&
		strval(row.AssigneeID) == agentID && row.Version == version
}

// nextIssue picks the agent's oldest actionable issue: assigned to it,
// active, not paused, not in a terminal workflow state, and not in
// Human Review (a parked card waits for its human — the swarm's queue
// excludes it by the reserved name, whatever the pause flag says).
func (w *agentWorker) nextIssue(ctx context.Context) (string, error) {
	var id *string
	err := w.rt.a.pool.QueryRow(ctx, `
		select i.id
		from issues i
		join teams t on t.id = i.team_id
		left join workflow_statuses ws on ws.id = i.status_id
		where t.workspace_id = $1
		  and i.status = 'active'
		  and i.assignee_id = $2
		  and not i.agent_paused
		  and `+notHumanReviewSQL+`
		  and coalesce(ws.category, 'UNSTARTED') not in ('COMPLETED', 'CANCELED')
		order by i.created_at, i.number
		limit 1`, w.workspaceID, w.agentID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Queue drained: the worker exits quietly (a drained queue is
			// not an error; the dispatcher respawns only real work).
			return "", nil
		}
		return "", err
	}
	return strval(id), nil
}

// runtimeInputTx assembles the policy's ActionInput under the caller's
// lock: the trusted server facts (issue, workflow, fleet) and the
// fenced untrusted context (the latest incoming handoff summary).
func (a *API) runtimeInputTx(ctx context.Context, tx pgx.Tx, w *agentWorker, row issueRow) (ActionInput, error) {
	in := ActionInput{}
	var teamName, teamIdentifier string
	if err := tx.QueryRow(ctx, "select name, identifier from teams where id = $1", row.TeamID).Scan(&teamName, &teamIdentifier); err != nil {
		return ActionInput{}, err
	}
	in.TeamName = teamName
	in.TeamIdentifier = teamIdentifier
	in.TeamID = row.TeamID
	in.WorkspaceID = w.workspaceID
	in.IssueNumber = row.Number
	in.ActorID = w.agentID
	in.ActorName = w.name
	// Untrusted, fenced: user-authored issue content (anyone with issue
	// write access controls it). The deterministic policy ignores both;
	// the LLM prompt carries them as marked data.
	in.Title = fenceSummary(row.Title)
	in.Description = fenceSummary(descToPlain(row.DescRaw))
	if row.StatusID != nil && *row.StatusID != "" {
		ref, err := a.stateRefTx(ctx, tx, *row.StatusID, row.TeamID)
		if err != nil {
			return ActionInput{}, err
		}
		in.CurrentState = ref
	}
	states, err := a.teamStatesTx(ctx, tx, row.TeamID)
	if err != nil {
		return ActionInput{}, err
	}
	in.States = states
	// The swarm's combined output on this issue over the guard window:
	// the same count the handoff guard reads (handoff.go). A spent
	// budget means the agent stops working the issue (the issue itself
	// keeps its state; a human or the window's expiry unblocks it).
	var opCount int
	if err := tx.QueryRow(ctx, `
		select count(*)::int
		from issue_history h
		join accounts a2 on a2.id = h.actor_id
		where h.issue_id = $1 and a2.kind = $2 and h.created_at > now() - $3::interval`,
		row.ID, auth.AccountKindAgent, guardWindowSQL).Scan(&opCount); err != nil {
		return ActionInput{}, err
	}
	in.BudgetExhausted = opCount >= issueOpBudget

	// Untrusted context: the newest handoff delivered to this agent on
	// this issue. Fenced on the way in — the cap was enforced when the
	// handoff was written (D1), sanitization happens here, and the
	// policy is contractually barred from reading it as instructions.
	in.RuntimeTopology = a.cfg.RuntimeTopology
	// D4: the workspace's saved swarm settings (the Swarm page) override
	// the deploy-time env for this decision: the effective topology and
	// the human's foreman designation ("" = the auto tenure rule).
	topology, designated, err := a.swarmSettingsTx(ctx, tx, w.workspaceID)
	if err != nil {
		return ActionInput{}, err
	}
	if topology != "" {
		in.RuntimeTopology = topology
	}
	in.ForemanAccountID = designated
	var summary *string
	err = tx.QueryRow(ctx, `
		select summary from issue_handoffs
		where issue_id = $1 and to_account_id = $2
		order by created_at desc limit 1`, row.ID, w.agentID).Scan(&summary)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ActionInput{}, err
	}
	// ErrNoRows keeps summary nil: no incoming handoff is the common
	// case for freshly assigned work, not a failure.
	in.IncomingSummary = fenceSummary(strval(summary))

	// Untrusted context: the issue's recent discussion (anyone who can
	// comment authored it — human or agent). This is how a resumed swarm
	// reads a human's answer to a parked card: on the next decision the
	// prompt carries the last few exchanges, fenced and capped, marked
	// data-only (the deterministic policy never reads the field).
	in.RecentComments = a.recentCommentsTx(ctx, tx, row.ID)

	fleet, err := a.fleetRosterTx(ctx, tx, w.workspaceID, row.TeamID)
	if err != nil {
		return ActionInput{}, err
	}
	in.Fleet = fleet
	return in, nil
}

// recentCommentsTx loads the issue's recent comments (body + the
// author's trusted name, newest first) and renders them fenced for the
// prompt. Best-effort by design: discussion is context, not authority —
// a failed read degrades to "no discussion", not a failed decision.
func (a *API) recentCommentsTx(ctx context.Context, tx pgx.Tx, issueID string) string {
	rows, err := tx.Query(ctx, `
		select c.body, an.name
		from comments c
		join accounts an on an.id = c.author_id
		where c.issue_id = $1
		order by c.created_at desc
		limit 5`, issueID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var entries []recentComment
	for rows.Next() {
		var e recentComment
		if err := rows.Scan(&e.body, &e.author); err != nil {
			return ""
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return ""
	}
	return renderRecentComments(entries)
}

// stateIsHumanReviewTx reports whether a status is the team's Human
// Review state (the reserved name, case-insensitive) — the check behind
// the advance-to-pause conversion in applyActionTx.
func (a *API) stateIsHumanReviewTx(ctx context.Context, tx pgx.Tx, teamID, statusID string) bool {
	if statusID == "" {
		return false
	}
	var exists bool
	err := tx.QueryRow(ctx,
		"select exists(select 1 from workflow_statuses where team_id = $1 and id = $2 and lower(name) = $3)",
		teamID, statusID, strings.ToLower(humanReviewStateName)).Scan(&exists)
	return err == nil && exists
}

// stateRefTx loads one workflow status as the policy's state view.
func (a *API) stateRefTx(ctx context.Context, tx pgx.Tx, statusID, teamID string) (*StateRef, error) {
	var ref StateRef
	var category string
	err := tx.QueryRow(ctx,
		"select id, name, position, category from workflow_statuses where id = $1 and team_id = $2",
		statusID, teamID).Scan(&ref.ID, &ref.Name, &ref.Position, &category)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The status row is gone (removed from the workflow): no
			// current state. The policy treats nil as no work.
			return nil, nil
		}
		return nil, err
	}
	ref.Category = strings.ToUpper(category)
	return &ref, nil
}

// teamStatesTx loads the team's workflow states ordered by position.
func (a *API) teamStatesTx(ctx context.Context, tx pgx.Tx, teamID string) ([]StateRef, error) {
	rows, err := tx.Query(ctx,
		"select id, name, position, category from workflow_statuses where team_id = $1 order by position",
		teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StateRef
	for rows.Next() {
		var ref StateRef
		var category string
		if err := rows.Scan(&ref.ID, &ref.Name, &ref.Position, &category); err != nil {
			return nil, err
		}
		ref.Category = strings.ToUpper(category)
		out = append(out, ref)
	}
	return out, rows.Err()
}

// fleetRosterTx loads the workspace's active agents as the policy's fleet
// view: identity, team memberships, and the open (unpaused, non-terminal,
// not in Human Review) work count within the issue's team — the
// "least busy" signal. Parked cards count for no agent: they wait on a
// human, so an agent whose whole load is parked is honestly idle.
func (a *API) fleetRosterTx(ctx context.Context, tx pgx.Tx, workspaceID, teamID string) ([]FleetAgent, error) {
	rows, err := tx.Query(ctx, `
		select a.id, a.name, a.created_at,
		       coalesce((select array_agg(tm.team_id) from team_members tm
		                where tm.account_id = a.id), '{}'),
		       count(*) filter (where i.id is not null and not i.agent_paused and `+notHumanReviewSQL+`)::int
		from accounts a
		join workspace_members wm on wm.workspace_id = $1 and wm.account_id = a.id
		left join issues i on i.assignee_id = a.id
		                          and i.team_id = $2
		                          and i.status = 'active'
		left join workflow_statuses ws on ws.id = i.status_id
		where a.kind = $3 and wm.status = 'active' and a.status = 'active'
		  and (i.id is null or coalesce(ws.category, 'UNSTARTED') not in ('COMPLETED', 'CANCELED'))
		group by a.id, a.name, a.created_at
		order by a.created_at, a.name`, workspaceID, teamID, auth.AccountKindAgent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FleetAgent{}
	for rows.Next() {
		var ag FleetAgent
		var teamIDs []string
		var open int
		if err := rows.Scan(&ag.AccountID, &ag.Name, &ag.CreatedAt, &teamIDs, &open); err != nil {
			return nil, err
		}
		ag.TeamIDs = teamIDs
		ag.OpenCount = open
		out = append(out, ag)
	}
	return out, rows.Err()
}

// applyActionTx executes the policy's Action inside the caller's
// transaction (the lock is held). It returns the broadcast records and
// tripped=true when the issue paused (the action IS the pause, or a
// quiet guard tripped on the handoff) — the pause is part of the same
// transaction, so the escalation commits or rolls back with the
// decision. reason carries the human-visible pause text back out for
// the caller's log line (ActionInput is a value; a reason set inside
// would not propagate).
func (a *API) applyActionTx(ctx context.Context, tx pgx.Tx, w *agentWorker, row issueRow, in ActionInput, action Action) (recs []syncActionRecord, tripped bool, reason string, err error) {
	// Human Review is reached by escalation or by a human — never by an
	// agent advance: a team whose state after the current one is Human
	// Review would otherwise receive unflagged cards (no pause flag, no
	// path comment, no needs-human signal). Convert to the pause path:
	// the same flag, move, and disclosure as any other escalation.
	if action.Kind == ActionAdvance && action.StateID != "" && a.stateIsHumanReviewTx(ctx, tx, in.TeamID, action.StateID) {
		ref, _ := a.stateRefTx(ctx, tx, action.StateID, in.TeamID)
		name := humanReviewStateName
		if ref != nil {
			name = ref.Name
		}
		action = Action{Kind: ActionPause, Comment: fmt.Sprintf("%s reached %s — the card is parked for a human", in.ActorName, name),
			Note: fmt.Sprintf("%s reported the work in %q finished and left the card for a human to verify. Nothing is blocked: check the result, then move the card to Done to close it, or back into the workflow if more is needed.", in.ActorName, name)}
	}

	switch action.Kind {
	case ActionNoop:
		// Nothing to persist.
		return recs, false, "", nil

	case ActionPause:
		// Escalation through the one choke point (the Human Review
		// protocol): the flag, the column move, the history rows, the
		// human-handoff comment, the audit row, the refreshed issue
		// record. tripped=true: the worker moves on (a paused issue is
		// no longer actionable).
		if action.Comment == "" {
			action.Comment = "agent runtime paused the issue for a human (no progress possible)"
		}
		in.PauseReason = action.Comment
		pauseRecs, err := a.pauseIssueTx(ctx, tx, w.workspaceID, row,
			&Principal{AccountID: w.agentID, Kind: auth.AccountKindAgent}, action.Comment, action.Note)
		if err != nil {
			return nil, false, "", err
		}
		return pauseRecs, true, action.Comment, nil

	case ActionAdvance:
		if action.StateID == "" {
			return nil, false, "", fmt.Errorf("advance without a target state")
		}
		issueRec, histRec, err := a.applyStatusTx(ctx, tx, w.workspaceID, row, w.agentID, action.StateID)
		if err != nil {
			return nil, false, "", err
		}
		recs = append(recs, issueRec, histRec)
		if action.Comment != "" {
			crec, err := a.applyCommentTx(ctx, tx, w.workspaceID, row.ID, w.agentID, action.Comment)
			if err != nil {
				return nil, false, "", err
			}
			recs = append(recs, crec)
		}
		return recs, false, "", nil

	case ActionHandoff:
		if action.ToAccountID == "" {
			return nil, false, "", fmt.Errorf("handoff without a target")
		}
		if reason, tripped, err := a.checkHandoffGuardsTx(ctx, tx, row.ID, action.ToAccountID); err != nil {
			return nil, false, "", err
		} else if tripped {
			in.PauseReason = reason
			pauseRecs, err := a.pauseIssueTx(ctx, tx, w.workspaceID, row, &Principal{AccountID: w.agentID, Kind: auth.AccountKindAgent}, reason, guardPauseNote(reason))
			if err != nil {
				return nil, false, "", err
			}
			return pauseRecs, true, reason, nil
		}
		stateID := action.StateID
		issueRec, histRec, _, err := a.applyHandoffTx(ctx, tx, w.workspaceID, row, w.agentID, action.ToAccountID, stateID, action.Summary)
		if err != nil {
			return nil, false, "", err
		}
		return []syncActionRecord{issueRec, histRec}, false, "", nil
	}
	return nil, false, "", fmt.Errorf("unknown action kind %q", string(action.Kind))
}

// applyStatusTx moves the issue to a workflow status inside the caller's
// transaction: the row update, one history row (the trace), and the
// refreshed issue outbox record. The caller holds the team lock.
func (a *API) applyStatusTx(ctx context.Context, tx pgx.Tx, workspaceID string, row issueRow, actorID, toStatusID string) (issueRec, histRec syncActionRecord, err error) {
	if _, err = tx.Exec(ctx,
		"update issues set status_id = $2, version = version + 1, updated_at = now() where id = $1",
		row.ID, toStatusID); err != nil {
		return syncActionRecord{}, syncActionRecord{}, err
	}
	histRec, err = a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID, actorID,
		"updated", "status", strval(row.StatusID), toStatusID, "")
	if err != nil {
		return syncActionRecord{}, syncActionRecord{}, err
	}
	fresh, err := a.issueByIDTx(ctx, tx, row.ID)
	if err != nil {
		return syncActionRecord{}, syncActionRecord{}, err
	}
	issueRec, err = a.emitChange(ctx, tx, workspaceID, "Issue", row.ID, "UPDATE", a.issueData(fresh))
	if err != nil {
		return syncActionRecord{}, syncActionRecord{}, err
	}
	return issueRec, histRec, a.refreshOutboxTx(ctx, tx, workspaceID, &issueRec, a.issueData(fresh))
}

// applyCommentTx appends a comment inside the caller's transaction and
// emits it on the sync feed. The body is the rendered step text, one
// line per paragraph (the agent's step text is a single line; the
// human-handoff comment is several); the jsonb document shape is the
// client's TipTap format (the same shape the rich text editor writes).
func (a *API) applyCommentTx(ctx context.Context, tx pgx.Tx, workspaceID, issueID, authorID, body string) (syncActionRecord, error) {
	content := make([]map[string]any, 0, 4)
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			continue
		}
		content = append(content, map[string]any{
			"type":    "paragraph",
			"content": []map[string]any{{"type": "text", "text": line}},
		})
	}
	bodyDoc, err := json.Marshal(map[string]any{
		"type":    "doc",
		"content": content,
	})
	if err != nil {
		return syncActionRecord{}, err
	}
	var id string
	err = tx.QueryRow(ctx, `
		insert into comments (issue_id, author_id, body, parent_id)
		values ($1, $2, $3::jsonb, null)
		returning id`, issueID, authorID, string(bodyDoc)).Scan(&id)
	if err != nil {
		return syncActionRecord{}, err
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "IssueComment", id, "CREATE", nil)
	if err != nil {
		return syncActionRecord{}, err
	}
	fresh, createdAt, updatedAt, err := a.commentByID(ctx, tx, id)
	if err != nil {
		return syncActionRecord{}, err
	}
	return rec, a.refreshOutboxTx(ctx, tx, workspaceID, &rec, a.commentData(fresh, createdAt, updatedAt))
}
