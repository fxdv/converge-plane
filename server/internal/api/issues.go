// Issue mutations (M2).
//
// Each handler follows the same shape:
//  1. resolve the tenant and verify the principal's active membership
//     (workspace boundaries hide existence: 404, never 403);
//  2. apply the change, write activity history, and record the change in
//     the sync outbox inside one transaction;
//  3. commit, then broadcast the committed record to realtime subscribers;
//  4. return the object in the exact shape the client's stores validate.
//
// Optimistic concurrency note: the forked client does not send expected
// versions, so this milestone bumps the row version but does not enforce
// it (enforcement lands with the client rewrite, spec 08 "Mutation
// concurrency").
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// teamWorkspace resolves the team's workspace and verifies the principal
// is an active member. ok is false for unknown teams or non-members.
func (a *API) teamWorkspace(ctx context.Context, principal *Principal, teamID string) (workspaceID string, ok bool) {
	err := a.pool.QueryRow(ctx, `
		select w.id
		from teams t
		join workspaces w on w.id = t.workspace_id
		where t.id = $1`, teamID).Scan(&workspaceID)
	if err != nil {
		return "", false
	}
	if _, ok := a.workspaceRole(ctx, principal, workspaceID); !ok {
		return "", false
	}
	return workspaceID, true
}

// issueAccess loads an issue and verifies the principal can access its
// workspace. ok is false for unknown issues or non-members.
func (a *API) issueAccess(ctx context.Context, principal *Principal, issueID string) (row issueRow, workspaceID string, ok bool) {
	row, err := a.issueByID(ctx, issueID)
	if err != nil {
		return issueRow{}, "", false
	}
	ws, ok := a.teamWorkspace(ctx, principal, row.TeamID)
	if !ok {
		return issueRow{}, "", false
	}
	return row, ws, true
}

// writeHistoryTx appends one activity row inside the caller's
// transaction and emits it on the sync feed (IssueHistory model),
// returning the record for the caller to broadcast after commit.
// from/to are opaque strings (ids or JSON arrays); summary carries the
// handoff note (D1) and stays null on every other row.
func (a *API) writeHistoryTx(ctx context.Context, tx pgx.Tx, workspaceID, teamID, issueID, actorID, action, field, from, to, summary string) (syncActionRecord, error) {
	var id string
	err := tx.QueryRow(ctx, `
		insert into issue_history (workspace_id, team_id, issue_id, actor_id, action, field, from_value, to_value, summary)
		values ($1, $2, $3, $4, $5, $6, nullif($7, ''), nullif($8, ''), nullif($9, ''))
		returning id`,
		workspaceID, teamID, issueID, actorID, action, field, from, to, summary).Scan(&id)
	if err != nil {
		return syncActionRecord{}, err
	}
	// created_at doubles as updated_at (append-only); read it back for
	// the wire payload rather than trusting the client's clock.
	var createdAt time.Time
	if err := tx.QueryRow(ctx, "select created_at from issue_history where id = $1", id).Scan(&createdAt); err != nil {
		return syncActionRecord{}, err
	}
	return a.emitChange(ctx, tx, workspaceID, "IssueHistory", id, "CREATE",
		historyData(id, createdAt, createdAt, &actorID, issueID, action, field, ptrOrNull(from), ptrOrNull(to), ptrOrNull(summary)))
}

// issueRequest is the client's create/patch payload (partial updates:
// only the fields the client sent are applied).
type issueRequest struct {
	Title       *string  `json:"title"`
	Description *string  `json:"description"`
	Priority    *int     `json:"priority"`
	StateID     *string  `json:"stateId"`
	AssigneeID  *string  `json:"assigneeId"`
	LabelIDs    []string `json:"labelIds"`
	ParentID    *string  `json:"parentId"`
	TeamID      *string  `json:"teamId"`
	SortOrder   *int     `json:"sortOrder"`
}

// toJSONB renders a client string for a jsonb column: valid JSON is
// stored verbatim (the rich-text document format), anything else is
// stored as a JSON string scalar.
func toJSONB(v string) string {
	if v == "" {
		return "null"
	}
	if json.Valid([]byte(v)) {
		return v
	}
	s, _ := json.Marshal(v)
	return string(s)
}

// handleCreateIssue implements POST /api/v1/issues.
func (a *API) handleCreateIssue(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	var req issueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.TeamID == nil || !isUUID(*req.TeamID) {
		writeError(w, http.StatusBadRequest, "teamId is required")
		return
	}
	if req.Title == nil || strings.TrimSpace(*req.Title) == "" || len(*req.Title) > 255 {
		writeError(w, http.StatusUnprocessableEntity, "title is required (1-255 chars)")
		return
	}
	workspaceID, ok := a.teamWorkspace(ctx, p, *req.TeamID)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	// An issue must be created in a status: the client's Issue model types
	// stateId as a strict string, and Tegon's schema requires it.
	if req.StateID == nil || *req.StateID == "" || !a.statusExistsForTeam(ctx, *req.StateID, *req.TeamID) {
		writeError(w, http.StatusUnprocessableEntity, "stateId must be one of the team workflow statuses")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize numbering per team.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_issue_"+*req.TeamID); err != nil {
		a.internalError(w, err)
		return
	}
	var number int
	if err := tx.QueryRow(ctx, `select coalesce(max(number), 0) + 1 from issues where team_id = $1`, *req.TeamID).Scan(&number); err != nil {
		a.internalError(w, err)
		return
	}

	id, err := a.insertIssueTx(ctx, tx, p, workspaceID, *req.TeamID, number, req)
	if err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "Issue", id, "CREATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	_ = rec
	// The data is filled after commit from the authoritative row; the
	// outbox row written above is refreshed below with the same value.
	row, err := a.issueByIDTx(ctx, tx, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	// Activity: the issue was created in its starting status.
	histRec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, id, p.AccountID, "created", "status", "", strval(row.StatusID), "")
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, workspaceID, &rec, a.issueData(row)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	a.broadcastRecord(histRec)
	// D3 fast path: an issue created assigned to an agent is work.
	a.wakeIssueOwner(ctx, workspaceID, id)
	writeJSON(w, http.StatusCreated, a.issueData(row))
}

// insertIssueTx inserts the issue row plus label links inside the
// caller's transaction and returns the new id.
func (a *API) insertIssueTx(ctx context.Context, tx pgx.Tx, p *Principal, workspaceID, teamID string, number int, req issueRequest) (string, error) {
	var (
		id        string
		createdAt time.Time
	)
	// Priority is the client's value verbatim: the new-issue template
	// defaults it to 0 ("no explicit priority") and the client model
	// accepts number | null (Tegon's schema: Int?, no range).
	sortOrder := 0
	if req.SortOrder != nil {
		sortOrder = *req.SortOrder
	}
	assignee := nullForEmpty(req.AssigneeID)
	parent := nullForEmpty(req.ParentID)
	// stateId is guaranteed present and validated by the handler; the
	// column is NOT NULL (the client cannot render a statusless issue).
	stateID := req.StateID
	description := ""
	if req.Description != nil {
		description = *req.Description
	}
	err := tx.QueryRow(ctx, `
		insert into issues (team_id, number, title, description, status_id, priority,
		                            assignee_id, parent_id, sort_order, created_by)
		values ($1, $2, $3, $4::jsonb, $5, $6, $7, $8, $9, $10)
		returning id, created_at`,
		teamID, number, strings.TrimSpace(*req.Title), toJSONB(description),
		stateID, req.Priority, assignee, parent, sortOrder, p.AccountID).Scan(&id, &createdAt)
	if err != nil {
		return "", err
	}
	if req.LabelIDs != nil {
		for _, labelID := range req.LabelIDs {
			if _, err := tx.Exec(ctx, `
				insert into issue_labels (issue_id, label_id)
				select $1, $2 where exists
					(select 1 from labels l where l.id = $2 and l.workspace_id = $3)`,
				id, labelID, workspaceID); err != nil {
				return "", err
			}
		}
	}
	return id, nil
}

// issueByIDTx loads one issue row from inside the caller's transaction.
func (a *API) issueByIDTx(ctx context.Context, tx pgx.Tx, id string) (issueRow, error) {
	var r issueRow
	err := tx.QueryRow(ctx, "select "+issueColumns+" from issues i where i.id = $1", id).Scan(
		&r.ID, &r.TeamID, &r.Number, &r.Priority, &r.SortOrder,
		&r.Title, &r.DescRaw, &r.Status, &r.CreatedAt, &r.UpdatedAt,
		&r.CreatedByID, &r.AssigneeID, &r.ParentID, &r.StatusID,
		&r.AgentPaused, &r.LabelIDs, &r.Children)
	return r, err
}

// refreshOutboxTx rewrites the outbox row for rec with the final data
// payload (used when the data is only known after the domain write).
func (a *API) refreshOutboxTx(ctx context.Context, tx pgx.Tx, workspaceID string, rec *syncActionRecord, data map[string]any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	// The broadcast must carry the same final payload as the outbox row;
	// realtime subscribers upsert directly from it.
	rec.Data = raw
	seq, _ := strconv.ParseInt(rec.SequenceID, 10, 64)
	_, err = tx.Exec(ctx, `
		update sync_outbox set data = $3 where workspace_id = $1 and sequence_id = $2`,
		workspaceID, seq, raw)
	return err
}

// handleUpdateIssue implements POST /api/v1/issues/{id}?teamId=...
func (a *API) handleUpdateIssue(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var req issueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	row, workspaceID, ok := a.issueAccess(ctx, p, id)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.agentPausedGuard(w, p, row) {
		return
	}
	// Cross-team updates apply the same move path (the client's patch and
	// move endpoints overlap).
	if req.TeamID != nil && *req.TeamID != row.TeamID {
		if !isUUID(*req.TeamID) {
			writeError(w, http.StatusBadRequest, "teamId is required")
			return
		}
		if destWS, ok := a.teamWorkspace(ctx, p, *req.TeamID); !ok || destWS != workspaceID {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		a.applyMove(w, r, p, row, workspaceID, *req.TeamID)
		return
	}

	// Validate referenced objects against the issue's workspace.
	if req.StateID != nil && (*req.StateID == "" || !a.statusExistsForTeam(ctx, *req.StateID, row.TeamID)) {
		writeError(w, http.StatusUnprocessableEntity, "stateId must be one of the team workflow statuses")
		return
	}
	if req.AssigneeID != nil && *req.AssigneeID != "" {
		ok := a.isWorkspaceMember(ctx, workspaceID, *req.AssigneeID)
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, "assigneeId is not a workspace member")
			return
		}
	}
	if req.ParentID != nil && *req.ParentID != "" {
		ok := a.issueInWorkspace(ctx, *req.ParentID, workspaceID)
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, "parentId is not in this workspace")
			return
		}
	}
	if req.LabelIDs != nil {
		for _, labelID := range req.LabelIDs {
			ok := a.labelInWorkspace(ctx, labelID, workspaceID)
			if !ok {
				writeError(w, http.StatusUnprocessableEntity, "labelIds contains a label outside this workspace")
				return
			}
		}
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_issue_"+row.TeamID); err != nil {
		a.internalError(w, err)
		return
	}

	changed, historyRecs, err := a.applyIssuePatchTx(ctx, tx, p, workspaceID, row, req)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if !changed {
		// Nothing to persist; return the current object.
		_ = tx.Commit(ctx)
		writeJSON(w, http.StatusOK, a.issueData(row))
		return
	}

	rec, err := a.emitChange(ctx, tx, workspaceID, "Issue", id, "UPDATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	fresh, err := a.issueByIDTx(ctx, tx, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, workspaceID, &rec, a.issueData(fresh)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	for i := range historyRecs {
		a.broadcastRecord(historyRecs[i])
	}
	// D3 fast path: a reassignment to an agent, or a human mutation that
	// resumed a paused issue, is new work for the assignee.
	a.wakeIssueOwner(ctx, workspaceID, id)
	writeJSON(w, http.StatusOK, a.issueData(fresh))
}

// applyIssuePatchTx applies the requested fields inside the caller's
// transaction and writes activity history for user-visible changes.
// It reports whether anything changed.
func (a *API) applyIssuePatchTx(ctx context.Context, tx pgx.Tx, p *Principal, workspaceID string, row issueRow, req issueRequest) (bool, []syncActionRecord, error) {
	changed := false
	historyRecs := make([]syncActionRecord, 0, 4)
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" && len(*req.Title) <= 255 && *req.Title != row.Title {
		if _, err := tx.Exec(ctx, `update issues set title = $2, version = version + 1, updated_at = now() where id = $1`, row.ID, strings.TrimSpace(*req.Title)); err != nil {
			return false, nil, err
		}
		row.Title = strings.TrimSpace(*req.Title)
		changed = true
	}
	if req.Description != nil {
		next := toJSONB(*req.Description)
		if next != row.DescRaw {
			if _, err := tx.Exec(ctx, `update issues set description = $2::jsonb, version = version + 1, updated_at = now() where id = $1`, row.ID, next); err != nil {
				return false, nil, err
			}
			row.DescRaw = next
			changed = true
		}
	}
	if req.StateID != nil && strval(req.StateID) != strval(row.StatusID) {
		if _, err := tx.Exec(ctx, `update issues set status_id = $2, version = version + 1, updated_at = now() where id = $1`, row.ID, *req.StateID); err != nil {
			return false, nil, err
		}
		rec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID, p.AccountID, "updated", "status",
			strval(row.StatusID), *req.StateID, "")
		if err != nil {
			return false, nil, err
		}
		historyRecs = append(historyRecs, rec)
		row.StatusID = ptrOrNull(*req.StateID)
		changed = true
	}
	if req.AssigneeID != nil && strval(req.AssigneeID) != strval(row.AssigneeID) {
		if _, err := tx.Exec(ctx, `update issues set assignee_id = $2, version = version + 1, updated_at = now() where id = $1`, row.ID, nullForEmpty(req.AssigneeID)); err != nil {
			return false, nil, err
		}
		rec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID, p.AccountID, "updated", "assignee",
			strval(row.AssigneeID), *req.AssigneeID, "")
		if err != nil {
			return false, nil, err
		}
		historyRecs = append(historyRecs, rec)
		row.AssigneeID = ptrOrNull(*req.AssigneeID)
		changed = true
	}
	if req.Priority != nil && (row.Priority == nil || *row.Priority != *req.Priority) {
		if _, err := tx.Exec(ctx, `update issues set priority = $2, version = version + 1, updated_at = now() where id = $1`, row.ID, req.Priority); err != nil {
			return false, nil, err
		}
		from := ""
		if row.Priority != nil {
			from = strconv.Itoa(*row.Priority)
		}
		rec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID, p.AccountID, "updated", "priority",
			from, strconv.Itoa(*req.Priority), "")
		if err != nil {
			return false, nil, err
		}
		historyRecs = append(historyRecs, rec)
		next := *req.Priority
		row.Priority = &next
		changed = true
	}
	if req.SortOrder != nil && *req.SortOrder != row.SortOrder {
		if _, err := tx.Exec(ctx, `update issues set sort_order = $2, version = version + 1, updated_at = now() where id = $1`, row.ID, *req.SortOrder); err != nil {
			return false, nil, err
		}
		row.SortOrder = *req.SortOrder
		changed = true
	}
	if req.ParentID != nil && strval(req.ParentID) != strval(row.ParentID) {
		if _, err := tx.Exec(ctx, `update issues set parent_id = $2, version = version + 1, updated_at = now() where id = $1`, row.ID, nullForEmpty(req.ParentID)); err != nil {
			return false, nil, err
		}
		row.ParentID = ptrOrNull(*req.ParentID)
		changed = true
	}
	if req.LabelIDs != nil {
		old, _ := json.Marshal(row.LabelIDs)
		same, err := a.sameLabelSet(ctx, tx, row.ID, req.LabelIDs)
		if err != nil {
			return false, nil, err
		}
		if !same {
			if _, err := tx.Exec(ctx, `delete from issue_labels where issue_id = $1`, row.ID); err != nil {
				return false, nil, err
			}
			for _, labelID := range req.LabelIDs {
				if _, err := tx.Exec(ctx, `insert into issue_labels (issue_id, label_id) values ($1, $2) on conflict do nothing`, row.ID, labelID); err != nil {
					return false, nil, err
				}
			}
			row.LabelIDs = append([]string(nil), req.LabelIDs...)
			rec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID, p.AccountID, "updated", "labels", string(old), mustJSONArray(req.LabelIDs), "")
			if err != nil {
				return false, nil, err
			}
			historyRecs = append(historyRecs, rec)
			changed = true
		}
	}
	// D1 escalation recovery: a human mutation on a paused issue resumes
	// the swarm. (Agents never reach this: the paused guard rejects
	// them before the transaction opens.)
	if changed && row.AgentPaused {
		if _, err := tx.Exec(ctx, "update issues set agent_paused = false where id = $1", row.ID); err != nil {
			return false, nil, err
		}
		row.AgentPaused = false
	}
	return changed, historyRecs, nil
}

// sameLabelSet reports whether the issue's current label set equals next.
func (a *API) sameLabelSet(ctx context.Context, tx pgx.Tx, issueID string, next []string) (bool, error) {
	var current []string
	if err := tx.QueryRow(ctx, `select coalesce((select array_agg(label_id) from issue_labels where issue_id = $1), '{}')`, issueID).Scan(&current); err != nil {
		return false, err
	}
	if len(current) != len(next) {
		return false, nil
	}
	have := make(map[string]bool, len(current))
	for _, id := range current {
		have[id] = true
	}
	for _, id := range next {
		if !have[id] {
			return false, nil
		}
	}
	return true, nil
}

// handleDeleteIssue implements DELETE /api/v1/issues/{id} (soft delete).
func (a *API) handleDeleteIssue(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	row, workspaceID, ok := a.issueAccess(ctx, p, id)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.agentPausedGuard(w, p, row) {
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `update issues set status = 'deleted', version = version + 1, updated_at = now() where id = $1`, id); err != nil {
		a.internalError(w, err)
		return
	}
	histRec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, id, p.AccountID, "deleted", "status", strval(row.StatusID), "", "")
	if err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "Issue", id, "DELETE", map[string]any{"id": id})
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	a.broadcastRecord(histRec)
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// handleMoveIssue implements POST /api/v1/issues/{id}/move (team change).
func (a *API) handleMoveIssue(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var req struct {
		TeamID string `json:"teamId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !isUUID(req.TeamID) {
		writeError(w, http.StatusBadRequest, "teamId is required")
		return
	}
	row, workspaceID, ok := a.issueAccess(ctx, p, id)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.agentPausedGuard(w, p, row) {
		return
	}
	// The destination must be in the same workspace.
	destWS, ok := a.teamWorkspace(ctx, p, req.TeamID)
	if !ok || destWS != workspaceID {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	a.applyMove(w, r, p, row, workspaceID, req.TeamID)
}

// applyMove runs the team move: renumber in the destination team,
// write the outbox record, commit, broadcast, and respond with the
// refreshed issue.
func (a *API) applyMove(w http.ResponseWriter, r *http.Request, p *Principal, row issueRow, workspaceID, destTeamID string) {
	ctx := r.Context()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_issue_"+destTeamID); err != nil {
		a.internalError(w, err)
		return
	}
	// Renumber in the destination team (numbers are unique per team).
	var number int
	if err := tx.QueryRow(ctx, `select coalesce(max(number), 0) + 1 from issues where team_id = $1`, destTeamID).Scan(&number); err != nil {
		a.internalError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `update issues set team_id = $2, number = $3, version = version + 1, updated_at = now() where id = $1`, row.ID, destTeamID, number); err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "Issue", row.ID, "UPDATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	fresh, err := a.issueByIDTx(ctx, tx, row.ID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, workspaceID, &rec, a.issueData(fresh)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	// D3 fast path: a team move keeps the assignee; wake when it is an
	// agent (its team context changed with the move).
	a.wakeIssueOwner(ctx, workspaceID, row.ID)
	writeJSON(w, http.StatusOK, a.issueData(fresh))
}

// handleSubscribeIssue implements POST /api/v1/issues/{id}/subscribe.
//
// The v1 schema has no subscription table (subscribers are a NEXT-slice
// feature); the endpoint acknowledges so the forked client's local state
// updates, and returns the current issue object.
func (a *API) handleSubscribeIssue(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	row, _, ok := a.issueAccess(ctx, p, id)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, a.issueData(row))
}

// statusExistsForTeam verifies a workflow status id belongs to a team.
func (a *API) statusExistsForTeam(ctx context.Context, statusID, teamID string) bool {
	var exists bool
	err := a.pool.QueryRow(ctx,
		"select exists(select 1 from workflow_statuses where id = $1 and team_id = $2)",
		statusID, teamID).Scan(&exists)
	return err == nil && exists
}

// isWorkspaceMember reports whether the account is an active member.
func (a *API) isWorkspaceMember(ctx context.Context, workspaceID, accountID string) bool {
	var exists bool
	err := a.pool.QueryRow(ctx, `
		select exists(select 1 from workspace_members where workspace_id = $1 and account_id = $2 and status = 'active')`,
		workspaceID, accountID).Scan(&exists)
	return err == nil && exists
}

// issueInWorkspace reports whether an issue exists in the workspace.
func (a *API) issueInWorkspace(ctx context.Context, issueID, workspaceID string) bool {
	var exists bool
	err := a.pool.QueryRow(ctx, `
		select exists(
			select 1 from issues i join teams t on t.id = i.team_id
			where i.id = $1 and t.workspace_id = $2)`,
		issueID, workspaceID).Scan(&exists)
	return err == nil && exists
}

// labelInWorkspace reports whether a label exists in the workspace.
func (a *API) labelInWorkspace(ctx context.Context, labelID, workspaceID string) bool {
	var exists bool
	err := a.pool.QueryRow(ctx,
		"select exists(select 1 from labels where id = $1 and workspace_id = $2)",
		labelID, workspaceID).Scan(&exists)
	return err == nil && exists
}

func nullForEmpty(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	v := *s
	return &v
}

func ptrOrNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func mustJSONArray(ids []string) string {
	s, _ := json.Marshal(ids)
	return string(s)
}
