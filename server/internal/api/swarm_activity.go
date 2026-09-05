// swarm_activity.go — the SwarmActivity sync model (docs/spec/12).
// spec cs:swarm:activity
//
// The in-flight work signal the trace cannot provide: a worker that is
// currently acting on (or deciding about) an issue has written nothing
// to the trace tables yet, so "what is the swarm doing right now" has
// no record to read. SwarmActivity fills that gap as a first-class
// sync model on the existing outbox/SSE seam:
//
//   - One record per agent (model id = agent id): {agent, issue,
//     phase, since}. Phase is "working" (the transactional phases —
//     milliseconds) or "deciding" (inside the LLM model call — the
//     only phase that takes long enough for a human to notice).
//   - The runtime emits on worker transitions: pick → working, model
//     call → deciding, worker exit → delete. The outbox row gives the
//     delta endpoint the same model for free; the broadcast record is
//     the realtime hint.
//   - Ephemeral by design: no new table (the outbox row is the record,
//     trimmed with the rest of the window), and the bootstrap
//     collector replays the current in-memory state, so a
//     reconnecting client always converges on the truth. The client
//     TTLs stale entries; a crashed process simply stops emitting.
package api

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
)

// ModelSwarmActivity is the sync model name (the client's
// MODELS.SwarmActivity; the client only asks for models it ships).
const ModelSwarmActivity = "SwarmActivity"

// swarmActivityPhase is the one thing a record reports: what the agent
// is doing to the issue right now.
type swarmActivityPhase string

const (
	// swarmPhaseWorking: the worker is in a transactional phase
	// (snapshot, apply) on the issue — milliseconds.
	swarmPhaseWorking swarmActivityPhase = "working"
	// swarmPhaseDeciding: the worker is inside the policy decision —
	// the LLM's model call, tens of seconds. This is the phase a
	// human watching the board can perceive.
	swarmPhaseDeciding swarmActivityPhase = "deciding"
)

// swarmActivityRef is one worker's current in-flight signal. Written
// by the worker's own goroutine, read by the bootstrap collector
// (another goroutine) — the worker's activityMu guards it.
type swarmActivityRef struct {
	AgentID     string
	IssueID     string
	IssueNumber int
	IssuePrefix string // the team identifier (ENG-23 display)
	Phase       swarmActivityPhase
	Since       time.Time
}

// WithPhase returns a copy of the ref in the given phase (the ref is
// a value; the worker's guarded field keeps its identity).
func (r swarmActivityRef) WithPhase(p swarmActivityPhase) swarmActivityRef {
	r.Phase = p
	return r
}

// swarmActivityData is the client's SwarmActivity shape (the sync wire
// contract — the client's SwarmActivityType mirrors it).
type swarmActivityData struct {
	ID          string `json:"id"`
	AgentID     string `json:"agentId"`
	AgentName   string `json:"agentName"`
	IssueID     string `json:"issueId"`
	IssueNumber int    `json:"issueNumber"`
	IssuePrefix string `json:"issuePrefix"`
	Phase       string `json:"phase"`
	Since       string `json:"since"`
}

func (r swarmActivityRef) data(agentName string) swarmActivityData {
	return swarmActivityData{
		ID:          r.AgentID,
		AgentID:     r.AgentID,
		AgentName:   agentName,
		IssueID:     r.IssueID,
		IssueNumber: r.IssueNumber,
		IssuePrefix: r.IssuePrefix,
		Phase:       string(r.Phase),
		Since:       r.Since.Format(iso),
	}
}

// activityEmitTimeout bounds one signal emission. Signals are tiny
// single-row transactions; the bound exists so a wedged database can
// never stall the worker's goroutine.
const activityEmitTimeout = 5 * time.Second

// setActivity records the worker's current in-flight signal and
// broadcasts it. Best-effort by contract: a failed emission is logged
// and dropped — realtime is a hint, the bootstrap replay is the
// authority, and a signal must never fail the work it signals. A fresh
// context is deliberate: the worker's own context cancels on
// retirement, but the signal must still fly.
func (w *agentWorker) setActivity(ref swarmActivityRef) {
	w.activityMu.Lock()
	w.activity = ref
	w.activityMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), activityEmitTimeout)
	defer cancel()
	w.rt.a.emitSwarmActivity(ctx, w.workspaceID, w.agentID, w.name, ref)
}

// clearActivity stops the signal (the worker is done: queue drained,
// retired, or cancelled).
func (w *agentWorker) clearActivity() {
	w.activityMu.Lock()
	active := w.activity.IssueID != ""
	w.activity = swarmActivityRef{}
	w.activityMu.Unlock()
	if !active {
		return // never worked in this life: no record to delete
	}
	ctx, cancel := context.WithTimeout(context.Background(), activityEmitTimeout)
	defer cancel()
	w.rt.a.emitSwarmActivityStop(ctx, w.workspaceID, w.agentID)
}

// emitSwarmActivity publishes one in-flight signal: one outbox row
// (the delta's copy) and one realtime broadcast, on a fresh short
// transaction. Never fails the caller.
func (a *API) emitSwarmActivity(ctx context.Context, workspaceID, agentID, agentName string, ref swarmActivityRef) {
	data := ref.data(agentName)
	raw, err := json.Marshal(data)
	if err != nil {
		a.log.Debug("swarm activity: marshal", "error", err)
		return
	}
	rec, err := a.emitOutboxDirect(ctx, workspaceID, ModelSwarmActivity, agentID, "UPDATE", raw)
	if err != nil {
		a.log.Debug("swarm activity: outbox", "error", err)
		return
	}
	a.broadcastRecord(rec)
}

// emitSwarmActivityStop deletes the agent's signal (a DELETE record
// carries only the id; the client removes its entry by id).
func (a *API) emitSwarmActivityStop(ctx context.Context, workspaceID, agentID string) {
	id := agentID
	raw, _ := json.Marshal(map[string]any{"id": id})
	rec, err := a.emitOutboxDirect(ctx, workspaceID, ModelSwarmActivity, agentID, "DELETE", raw)
	if err != nil {
		a.log.Debug("swarm activity: outbox", "error", err)
		return
	}
	a.broadcastRecord(rec)
}

// emitOutboxDirect is the signal path's outbox writer: one sequence
// claim, one outbox row, one commit — on its own short transaction,
// because a signal is not part of the mutation it describes (it must
// not live or die with it), returning the wire record for broadcast.
func (a *API) emitOutboxDirect(ctx context.Context, workspaceID, model, modelID, action string, raw json.RawMessage) (syncActionRecord, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return syncActionRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := a.claimSequenceTx(ctx, tx, workspaceID)
	if err != nil {
		return syncActionRecord{}, err
	}
	if _, err := tx.Exec(ctx, `
		insert into sync_outbox (workspace_id, sequence_id, model_name, model_id, action, data)
		values ($1, $2, $3, $4, $5, $6)`,
		workspaceID, seq, model, nullForEmpty(&modelID), action, raw); err != nil {
		return syncActionRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return syncActionRecord{}, err
	}
	return syncActionRecord{
		Data:        raw,
		ModelName:   model,
		ModelID:     modelID,
		Action:      wireAction(action),
		WorkspaceID: workspaceID,
		SequenceID:  strconv.FormatInt(seq, 10),
	}, nil
}

// ActivityForWorkspace snapshots the live in-flight signals of the
// workspace's workers — one entry per worker with work in flight.
// This is the bootstrap's SwarmActivity source: a reconnecting client
// replays exactly what the runtime currently holds, so the board's
// live chips converge on the truth after any gap.
func (rt *AgentRuntime) ActivityForWorkspace(workspaceID string) []swarmActivityRef {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var out []swarmActivityRef
	for _, w := range rt.workers {
		if w.workspaceID != workspaceID {
			continue
		}
		w.activityMu.Lock()
		ref := w.activity
		w.activityMu.Unlock()
		if ref.IssueID == "" {
			continue
		}
		out = append(out, ref)
	}
	return out
}

// collectSwarmActivity is the bootstrap collector for the model: it
// replays the runtime's current in-flight signals (CREATE records).
// The stream and the delta carry the runtime's UPDATE/DELETE
// emissions through the shared outbox.
func (a *API) collectSwarmActivity(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	if a.runtime == nil {
		return nil, nil
	}
	refs := a.runtime.ActivityForWorkspace(workspaceID)
	if len(refs) == 0 {
		return nil, nil
	}
	// Agent display names in one query (the refs carry only ids).
	agentIDs := make([]string, 0, len(refs))
	for _, ref := range refs {
		agentIDs = append(agentIDs, ref.AgentID)
	}
	names := map[string]string{}
	rows, err := a.pool.Query(ctx,
		"select id, name from accounts where id = any($1::uuid[])", agentIDs)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err == nil {
				names[id] = name
			}
		}
	}
	var out []syncActionRecord
	for _, ref := range refs {
		name := names[ref.AgentID]
		if name == "" {
			name = "agent"
		}
		rec, err := emit(ref.AgentID, ref.data(name))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}
