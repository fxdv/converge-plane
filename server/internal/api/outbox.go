// outbox.go — the sync engine's outbox seams
// (docs/spec/product-spec.tex, ch. 5): the delta reader (collectOutbox)
// and the transactional writers every mutation path shares (audit row,
// sequence claim, outbox row, realtime broadcast).
// spec cs:arch:sync

package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// collectOutbox serves the delta endpoint: outbox records for the
// workspace newer than afterSeq, restricted to the requested models.
func (a *API) collectOutbox(ctx context.Context, workspaceID string, afterSeq int64, modelNames string) ([]syncActionRecord, error) {
	// An empty model list means "no filter" (every record); a non-empty
	// list restricts delivery to the requested models.
	allowed := make(map[string]bool)
	for _, name := range strings.Split(modelNames, ",") {
		if name = strings.TrimSpace(name); name != "" {
			allowed[name] = true
		}
	}
	rows, err := a.pool.Query(ctx, `
		select sequence_id, model_name, model_id, action, data
		from sync_outbox
		where workspace_id = $1 and sequence_id > $2
		order by sequence_id`, workspaceID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil: a Go nil slice marshals as JSON null, and the client
	// contract (SyncActionRecord[]) is an array — null crashes its
	// iteration in saveSocketData.
	out := []syncActionRecord{}
	for rows.Next() {
		var (
			seqNum        int64
			modelID       string
			model, action string
			data          json.RawMessage
		)
		if err := rows.Scan(&seqNum, &model, &modelID, &action, &data); err != nil {
			return nil, err
		}
		if len(allowed) > 0 && !allowed[model] {
			continue
		}
		out = append(out, syncActionRecord{
			Data:        data,
			ModelName:   model,
			ModelID:     strval(&modelID),
			Action:      wireAction(action),
			WorkspaceID: workspaceID,
			SequenceID:  strconv.FormatInt(seqNum, 10),
		})
	}
	return out, rows.Err()
}

// auditTx appends a workspace audit row inside the caller's transaction
// (docs/spec/07 audit taxonomy: team lifecycle, membership and invite
// changes, suspension). objectID may be empty for row-less events.
func (a *API) auditTx(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action, objectType, objectID string) error {
	_, err := tx.Exec(ctx, `
		insert into audit_events (workspace_id, actor_id, action, object_type, object_id)
		values ($1, $2, $3, $4, $5)`,
		workspaceID, actorID, action, objectType, nullForEmpty(&objectID))
	return err
}

// claimSequenceTx claims the next sync sequence for the workspace
// inside the caller's transaction: one row per workspace, updated
// atomically. Both outbox writers (emitChange for mutations,
// emitOutboxDirect for swarm signals) claim through here so the
// "one sequence per record" rule has a single home.
func (a *API) claimSequenceTx(ctx context.Context, tx pgx.Tx, workspaceID string) (int64, error) {
	var seq int64
	err := tx.QueryRow(ctx, `
		insert into sync_sequences (workspace_id, last_sequence)
		values ($1, 1)
		on conflict (workspace_id)
		do update set last_sequence = sync_sequences.last_sequence + 1
		returning last_sequence`, workspaceID).Scan(&seq)
	return seq, err
}

// emitChange claims the next sync sequence for the workspace, writes the
// outbox row inside the caller's transaction, and returns the wire record
// to broadcast after commit.
func (a *API) emitChange(ctx context.Context, tx pgx.Tx, workspaceID, model, modelID, action string, data map[string]any) (syncActionRecord, error) {
	seq, err := a.claimSequenceTx(ctx, tx, workspaceID)
	if err != nil {
		return syncActionRecord{}, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return syncActionRecord{}, err
	}
	rec := syncActionRecord{
		Data:        raw,
		ModelName:   model,
		ModelID:     modelID,
		Action:      wireAction(action),
		WorkspaceID: workspaceID,
		SequenceID:  strconv.FormatInt(seq, 10),
	}
	// model_id is a *string so an empty id binds as SQL NULL: pgx sends
	// Go strings as unknown type, and a bare parameter infers the target
	// uuid type, while nullif() would pin it to text (no implicit
	// text->uuid cast exists).
	if _, err := tx.Exec(ctx, `
		insert into sync_outbox (workspace_id, sequence_id, model_name, model_id, action, data)
		values ($1, $2, $3, $4, $5, $6)`,
		workspaceID, seq, model, nullForEmpty(&modelID), action, raw); err != nil {
		return syncActionRecord{}, err
	}
	return rec, nil
}

// broadcastRecord publishes a committed record to the workspace's realtime
// subscribers. Best-effort: realtime is a hint, the delta endpoint is
// authoritative (spec R-8 / doc 08).
func (a *API) broadcastRecord(rec syncActionRecord) {
	raw, err := json.Marshal(rec)
	if err != nil {
		return
	}
	n := a.bcast.Publish(rec.WorkspaceID, raw)
	a.log.Debug("realtime publish", "workspace", rec.WorkspaceID,
		"model", rec.ModelName, "action", rec.Action, "delivered", n)
}
