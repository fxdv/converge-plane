// view.go — M5: saved views.
//
// Permission model (docs/spec/07):
//   - create: any member of the workspace (a team-scoped view requires
//     membership of that team)
//   - edit/delete: the creator, or a workspace admin, or a manager of
//     the view's team
//   - bookmark: the creator
//
// The view definition (client FiltersModel) is opaque server-side: it is
// stored and returned verbatim, because the server must never
// reinterpret or re-serialize the client's own query model.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// viewRow is a saved_views-table row. definition is the stored client
// FiltersModel JSON; it is passed through to the wire uninterpreted.
type viewRow struct {
	ID, Name, Status, WorkspaceID, CreatedBy, Visibility string
	Description                                          *string
	TeamID                                               *string
	Definition                                           []byte
	IsBookmarked                                         bool
	Position                                             int
	CreatedAt, UpdatedAt                                 time.Time
}

// viewColumns is the shared SELECT list for view rows.
const viewColumns = `
	id, name, status, description, definition, is_bookmarked, position,
	visibility, created_by, workspace_id, team_id, created_at, updated_at`

// scanViewRow fills a viewRow from a row or rowset cursor.
func scanViewRow(s scanner, r *viewRow) error {
	return s.Scan(&r.ID, &r.Name, &r.Status, &r.Description, &r.Definition,
		&r.IsBookmarked, &r.Position, &r.Visibility, &r.CreatedBy, &r.WorkspaceID,
		&r.TeamID, &r.CreatedAt, &r.UpdatedAt)
}

// viewData serializes a view row in the exact shape of the client's View
// model. Every key is present: description as a plain string (the model
// is non-nullable), teamId null for workspace-level views, and filters
// always an object ({} when the stored definition is empty or malformed
// — a missing key would fail mobx-state-tree validation).
func (a *API) viewData(r viewRow) map[string]any {
	filters := map[string]any{}
	if len(r.Definition) > 0 {
		if err := json.Unmarshal(r.Definition, &filters); err != nil {
			filters = map[string]any{}
		}
	}
	return map[string]any{
		"id":           r.ID,
		"createdAt":    r.CreatedAt.Format(iso),
		"updatedAt":    r.UpdatedAt.Format(iso),
		"name":         r.Name,
		"description":  strval(r.Description),
		"filters":      filters,
		"isBookmarked": r.IsBookmarked,
		"workspaceId":  r.WorkspaceID,
		"teamId":       r.TeamID,
		"createdById":  r.CreatedBy,
	}
}

// viewByID / viewByIDTx load one view (any lifecycle state).
func (a *API) viewByID(ctx context.Context, id string) (viewRow, error) {
	var r viewRow
	return r, scanViewRow(a.pool.QueryRow(ctx, "select "+viewColumns+" from saved_views where id = $1", id), &r)
}

func (a *API) viewByIDTx(ctx context.Context, tx pgx.Tx, id string) (viewRow, error) {
	var r viewRow
	return r, scanViewRow(tx.QueryRow(ctx, "select "+viewColumns+" from saved_views where id = $1", id), &r)
}

// viewRequest is the client's view create/patch payload. filters is the
// client FiltersModel, accepted as opaque JSON.
type viewRequest struct {
	WorkspaceID  *string         `json:"workspaceId"`
	Name         *string         `json:"name"`
	Description  *string         `json:"description"`
	Filters      json.RawMessage `json:"filters"`
	TeamID       *string         `json:"teamId"`
	IsBookmarked *bool           `json:"isBookmarked"`
}

// viewWritable reports whether the principal may edit/delete/bookmark a
// view: the creator always; workspace admins always; otherwise managers
// of the view's team.
func (a *API) viewWritable(ctx context.Context, p *Principal, v viewRow) bool {
	if v.CreatedBy == p.AccountID {
		return true
	}
	role, ok := a.workspaceRole(ctx, p, v.WorkspaceID)
	if !ok {
		return false
	}
	if adminRole(role) {
		return true
	}
	if strval(v.TeamID) == "" {
		return false
	}
	return a.teamManager(ctx, p, strval(v.TeamID), v.WorkspaceID)
}

// handleCreateView implements POST /api/v1/views (any active workspace
// member; team-scoped views require membership of that team).
func (a *API) handleCreateView(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	var req viewRequest
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(strval(req.Name))
	if len(name) < 1 || len(name) > 100 {
		writeError(w, http.StatusUnprocessableEntity, "name must be 1-100 chars")
		return
	}
	workspaceID, ok := a.resolveWorkspaceForWrite(ctx, p, strval(req.WorkspaceID))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if _, ok := a.workspaceRole(ctx, p, workspaceID); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	teamID := strval(req.TeamID)
	if teamID != "" {
		if !isUUID(teamID) {
			writeError(w, http.StatusUnprocessableEntity, "teamId must be a uuid")
			return
		}
		team, err := a.teamByID(ctx, teamID)
		if err != nil || team.WorkspaceID != workspaceID || team.Status != "active" {
			writeError(w, http.StatusUnprocessableEntity, "teamId must be an active team in the workspace")
			return
		}
		// A team-scoped view requires team membership (spec: outside
		// team may not create team views; admins bypass).
		role, _ := a.workspaceRole(ctx, p, workspaceID)
		if !adminRole(role) {
			var inTeam bool
			err := a.pool.QueryRow(ctx,
				"select exists(select 1 from team_members where team_id = $1 and account_id = $2)",
				team.ID, p.AccountID).Scan(&inTeam)
			if err != nil || !inTeam {
				writeError(w, http.StatusUnprocessableEntity, "teamId must be a team the requester belongs to")
				return
			}
		}
	}
	// The stored definition is opaque client JSON; normalize a missing
	// or empty payload to the empty object the client model accepts.
	definition := []byte("{}")
	if len(req.Filters) > 0 {
		if !json.Valid(req.Filters) {
			writeError(w, http.StatusUnprocessableEntity, "filters must be a JSON object")
			return
		}
		definition = req.Filters
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_view_"+workspaceID); err != nil {
		a.internalError(w, err)
		return
	}
	var id string
	err = tx.QueryRow(ctx, `
		insert into saved_views (workspace_id, team_id, name, description, definition, visibility, created_by, position)
		values ($1, $2, $3, $4, $5::jsonb, 'workspace', $6,
		        (select coalesce(max(position), 0) + 1 from saved_views where workspace_id = $1))
		returning id`,
		workspaceID, nullForEmpty(&teamID), name, nullForEmpty(req.Description),
		string(definition), p.AccountID).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a view with this name already exists")
			return
		}
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "View", id, "CREATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	row, err := a.viewByIDTx(ctx, tx, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, workspaceID, &rec, a.viewData(row)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusCreated, a.viewData(row))
}

// handleUpdateView implements POST /api/v1/views/{id} (creator, team
// manager, or workspace admin; partial patch).
func (a *API) handleUpdateView(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var req viewRequest
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	row, err := a.viewByID(ctx, id)
	if err != nil || row.Status == "archived" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.viewWritable(ctx, p, row) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	changed, err := a.applyViewPatchTx(ctx, tx, &row, &req)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a view with this name already exists")
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
		writeJSON(w, http.StatusOK, a.viewData(row))
		return
	}
	rec, err := a.emitChange(ctx, tx, row.WorkspaceID, "View", id, "UPDATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, row.WorkspaceID, &rec, a.viewData(row)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.viewData(row))
}

// applyViewPatchTx applies the requested view fields inside the
// caller's transaction and reports whether anything changed.
func (a *API) applyViewPatchTx(ctx context.Context, tx pgx.Tx, row *viewRow, req *viewRequest) (bool, error) {
	changed := false
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if len(name) < 1 || len(name) > 100 {
			return false, errInvalidField
		}
		if name != row.Name {
			if _, err := tx.Exec(ctx, "update saved_views set name = $2, version = version + 1, updated_at = now() where id = $1", row.ID, name); err != nil {
				return false, err // unique (workspace_id, name) surfaced by the caller
			}
			row.Name = name
			changed = true
		}
	}
	if req.Description != nil {
		desc := strings.TrimSpace(*req.Description)
		next := nullForEmpty(&desc)
		if strval(next) != strval(row.Description) {
			if _, err := tx.Exec(ctx, "update saved_views set description = $2, version = version + 1, updated_at = now() where id = $1", row.ID, next); err != nil {
				return false, err
			}
			row.Description = next
			changed = true
		}
	}
	if req.Filters != nil {
		if !json.Valid(req.Filters) {
			return false, errInvalidField
		}
		next := string(req.Filters)
		if next != string(row.Definition) {
			if _, err := tx.Exec(ctx, "update saved_views set definition = $2::jsonb, version = version + 1, updated_at = now() where id = $1", row.ID, next); err != nil {
				return false, err
			}
			row.Definition = req.Filters
			changed = true
		}
	}
	if req.IsBookmarked != nil && *req.IsBookmarked != row.IsBookmarked {
		if _, err := tx.Exec(ctx, "update saved_views set is_bookmarked = $2, version = version + 1, updated_at = now() where id = $1", row.ID, *req.IsBookmarked); err != nil {
			return false, err
		}
		row.IsBookmarked = *req.IsBookmarked
		changed = true
	}
	return changed, nil
}

// handleDeleteView implements DELETE /api/v1/views/{id} (creator or
// workspace admin; team managers included via viewWritable). Archives
// and emits a DELETE record.
func (a *API) handleDeleteView(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	row, err := a.viewByID(ctx, id)
	if err != nil || row.Status == "archived" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.viewWritable(ctx, p, row) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "update saved_views set status = 'archived', version = version + 1, updated_at = now() where id = $1", id); err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, row.WorkspaceID, p.AccountID, "view.archived", "View", id); err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, row.WorkspaceID, "View", id, "DELETE", map[string]any{"id": id})
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// handleGetView implements GET /api/v1/views/{id} for workspace members;
// private views are visible only to their creator (v1 has no private
// view creation path, but the guard holds for imported data).
func (a *API) handleGetView(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	row, err := a.viewByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.memberOf(r.Context(), p, row.WorkspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if row.Visibility == "private" && row.CreatedBy != p.AccountID {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, a.viewData(row))
}

// collectViews serializes the workspace's active views for bootstrap.
// No per-principal scoping: v1 has no private-view creation path, so
// every active view is visible to every member (the route handler still
// guards imported private rows).
func (a *API) collectViews(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, "select "+viewColumns+`
		from saved_views where workspace_id = $1 and status = 'active'
		order by position, created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []syncActionRecord{}
	for rows.Next() {
		var r viewRow
		if err := scanViewRow(rows, &r); err != nil {
			return nil, err
		}
		rec, err := emit(r.ID, a.viewData(r))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
