// workflow.go — M5: workflow status CRUD on the client's quirky
// /{teamId}/workflows path.
//
// Permission model (docs/spec/07): "Create/update/reorder/archive
// status: WO/WA yes, TM yes, members no." Reads: team members and
// workspace admins.
package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// workflowCategories is the client's category enum (uppercase); the
// database stores the same values (migration 0003 normalized them).
var workflowCategories = map[string]bool{
	"BACKLOG": true, "UNSTARTED": true, "STARTED": true,
	"COMPLETED": true, "CANCELED": true, "TRIAGE": true,
}

// workflowRow is a workflow_statuses row with everything the client
// Workflow shape needs. description is nullable and stays nullable on
// the wire (the client model is union(string, null)).
type workflowRow struct {
	ID, Name, Color, Category, TeamID string
	Description                       *string
	Position                          int
	CreatedAt, UpdatedAt              time.Time
}

// workflowColumns is the shared SELECT list for workflow rows.
const workflowColumns = `
	id, name, description, position, color, category, team_id, created_at, updated_at`

// scanWorkflowRow fills a workflowRow from a row or rowset cursor.
func scanWorkflowRow(s scanner, r *workflowRow) error {
	return s.Scan(&r.ID, &r.Name, &r.Description, &r.Position, &r.Color,
		&r.Category, &r.TeamID, &r.CreatedAt, &r.UpdatedAt)
}

// workflowData serializes a workflow row in the exact shape of the
// client's Workflow model.
func (a *API) workflowData(r workflowRow) map[string]any {
	return map[string]any{
		"id":          r.ID,
		"createdAt":   r.CreatedAt.Format(iso),
		"updatedAt":   r.UpdatedAt.Format(iso),
		"name":        r.Name,
		"description": r.Description,
		"position":    r.Position,
		"color":       r.Color,
		"category":    r.Category,
		"teamId":      r.TeamID,
	}
}

// workflowRequest is the client's workflow create/patch payload
// (CreateWorkflowDTO requires name/position/color/category; everything
// on the update DTO is optional).
type workflowRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Position    *int    `json:"position"`
	Color       *string `json:"color"`
	Category    *string `json:"category"`
}

// workflowTeam loads the team a workflow request targets and verifies
// the principal may act on it. The team must exist and be active
// (archived teams accept no new configuration).
func (a *API) workflowTeam(ctx context.Context, p *Principal, teamID string, canRead bool) (teamRow, bool) {
	if !isUUID(teamID) {
		return teamRow{}, false
	}
	row, err := a.teamByID(ctx, teamID)
	if err != nil || row.Status != "active" {
		return teamRow{}, false
	}
	if _, ok := a.workspaceRole(ctx, p, row.WorkspaceID); !ok {
		return teamRow{}, false
	}
	if canRead && !a.teamManager(ctx, p, row.ID, row.WorkspaceID) {
		// Readers must be team members or workspace admins; writers must
		// be team managers or workspace admins (same predicate here: a
		// plain team member can read but not write).
		var inTeam bool
		err := a.pool.QueryRow(ctx,
			"select exists(select 1 from team_members where team_id = $1 and account_id = $2)",
			row.ID, p.AccountID).Scan(&inTeam)
		if err != nil || !inTeam {
			return teamRow{}, false
		}
	}
	return row, true
}

// handleCreateWorkflow implements POST /api/v1/{teamId}/workflows
// (team manager or workspace admin).
func (a *API) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	teamID := chi.URLParam(r, "teamId")
	var req workflowRequest
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(strval(req.Name))
	color := strings.TrimSpace(strval(req.Color))
	category := strings.ToUpper(strings.TrimSpace(strval(req.Category)))
	if !validateLabelName(name) {
		writeError(w, http.StatusUnprocessableEntity, "name must be 1-100 printable characters")
		return
	}
	if color == "" || !colorPattern.MatchString(color) {
		writeError(w, http.StatusUnprocessableEntity, "color must be a 1-100 char CSS color value")
		return
	}
	if !workflowCategories[category] {
		writeError(w, http.StatusUnprocessableEntity, "category must be one of BACKLOG, UNSTARTED, STARTED, COMPLETED, CANCELED, TRIAGE")
		return
	}
	if req.Position == nil || *req.Position < 0 || *req.Position > 1000 {
		writeError(w, http.StatusUnprocessableEntity, "position must be 0-1000")
		return
	}
	team, ok := a.workflowTeam(ctx, p, teamID, false)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.teamManager(ctx, p, team.ID, team.WorkspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id string
	err = tx.QueryRow(ctx, `
		insert into workflow_statuses (team_id, name, description, position, color, category)
		values ($1, $2, $3, $4, $5, $6)
		returning id`,
		team.ID, name, nullForEmpty(req.Description), *req.Position, color, category).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a status with this name already exists in the team")
			return
		}
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, team.WorkspaceID, p.AccountID, "workflow.created", "Workflow", id); err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, team.WorkspaceID, "Workflow", id, "CREATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	var row workflowRow
	if err := scanWorkflowRow(tx.QueryRow(ctx, "select "+workflowColumns+" from workflow_statuses where id = $1", id), &row); err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, team.WorkspaceID, &rec, a.workflowData(row)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusCreated, a.workflowData(row))
}

// handleUpdateWorkflow implements POST /api/v1/{teamId}/workflows/{workflowId}
// (team manager or workspace admin; partial patch).
func (a *API) handleUpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	teamID := chi.URLParam(r, "teamId")
	workflowID := chi.URLParam(r, "workflowId")
	var req workflowRequest
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	team, ok := a.workflowTeam(ctx, p, teamID, false)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.teamManager(ctx, p, team.ID, team.WorkspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !isUUID(workflowID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var row workflowRow
	err := scanWorkflowRow(a.pool.QueryRow(ctx, "select "+workflowColumns+" from workflow_statuses where id = $1 and team_id = $2", workflowID, team.ID), &row)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	changed, err := a.applyWorkflowPatchTx(ctx, tx, &row, &req)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a status with this name already exists in the team")
			return
		}
		if errors.Is(err, errInvalidField) {
			writeError(w, http.StatusUnprocessableEntity, "invalid field value")
			return
		}
		a.internalError(w, err)
		return
	}
	if !changed {
		_ = tx.Commit(ctx)
		writeJSON(w, http.StatusOK, a.workflowData(row))
		return
	}
	rec, err := a.emitChange(ctx, tx, team.WorkspaceID, "Workflow", workflowID, "UPDATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, team.WorkspaceID, &rec, a.workflowData(row)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.workflowData(row))
}

// applyWorkflowPatchTx applies the requested workflow fields inside the
// caller's transaction and reports whether anything changed.
func (a *API) applyWorkflowPatchTx(ctx context.Context, tx pgx.Tx, row *workflowRow, req *workflowRequest) (bool, error) {
	changed := false
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if !validateLabelName(name) {
			return false, errInvalidField
		}
		if name != row.Name {
			if _, err := tx.Exec(ctx, "update workflow_statuses set name = $2, version = version + 1, updated_at = now() where id = $1", row.ID, name); err != nil {
				return false, err // unique (team_id, name) surfaced by the caller
			}
			row.Name = name
			changed = true
		}
	}
	if req.Description != nil {
		desc := strings.TrimSpace(*req.Description)
		next := nullForEmpty(&desc)
		if strval(next) != strval(row.Description) {
			if _, err := tx.Exec(ctx, "update workflow_statuses set description = $2, version = version + 1, updated_at = now() where id = $1", row.ID, next); err != nil {
				return false, err
			}
			row.Description = next
			changed = true
		}
	}
	if req.Position != nil {
		if *req.Position < 0 || *req.Position > 1000 {
			return false, errInvalidField
		}
		if *req.Position != row.Position {
			if _, err := tx.Exec(ctx, "update workflow_statuses set position = $2, version = version + 1, updated_at = now() where id = $1", row.ID, *req.Position); err != nil {
				return false, err
			}
			row.Position = *req.Position
			changed = true
		}
	}
	if req.Color != nil {
		color := strings.TrimSpace(*req.Color)
		if !colorPattern.MatchString(color) {
			return false, errInvalidField
		}
		if color != row.Color {
			if _, err := tx.Exec(ctx, "update workflow_statuses set color = $2, version = version + 1, updated_at = now() where id = $1", row.ID, color); err != nil {
				return false, err
			}
			row.Color = color
			changed = true
		}
	}
	if req.Category != nil {
		category := strings.ToUpper(strings.TrimSpace(*req.Category))
		if !workflowCategories[category] {
			return false, errInvalidField
		}
		if category != row.Category {
			if _, err := tx.Exec(ctx, "update workflow_statuses set category = $2, version = version + 1, updated_at = now() where id = $1", row.ID, category); err != nil {
				return false, err
			}
			row.Category = category
			changed = true
		}
	}
	return changed, nil
}

// handleListWorkflows implements GET /api/v1/{teamId}/workflows for team
// members and workspace admins. Archived workflows are excluded (board
// columns come from the active set); archived teams are not — their
// statuses must stay resolvable for the issues that reference them.
func (a *API) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	team, ok := a.workflowTeam(ctx, p, chi.URLParam(r, "teamId"), true)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	rows, err := a.pool.Query(ctx, "select "+workflowColumns+`
		from workflow_statuses where team_id = $1 and status = 'active'
		order by position, created_at`, team.ID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var row workflowRow
		if err := scanWorkflowRow(rows, &row); err != nil {
			a.internalError(w, err)
			return
		}
		out = append(out, a.workflowData(row))
	}
	if err := rows.Err(); err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
