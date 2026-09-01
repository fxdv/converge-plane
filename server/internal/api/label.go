// label.go — M5: label CRUD.
//
// Permission model (docs/spec/07): "Create/update/archive label:
// WO/WA yes; TM no — labels are workspace-shared." Reads: any active
// workspace member.
//
// Archive, not drop: issue_labels.label_id cascades on a hard delete
// (and the label history would vanish with it); an archived label
// disappears from listings while its issue links survive.
package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// colorPattern bounds the color value: any CSS color syntax the client's
// pickers can produce (hex, oklch(), rgb(), var(--token)) is accepted;
// the guard rejects only empty or oversized values. The value is rendered
// verbatim by the browser as a style property, so this is a data-hygiene
// bound, not palette enforcement.
var colorPattern = regexp.MustCompile(`^[^\x00-\x1f]{1,100}$`)

var labelNamePattern = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._-]{0,99}$`)

// labelRow is a labels-table row with everything the client Label shape
// needs. description is nullable in the schema and stays nullable on the
// wire (the client model is union(null, string)).
type labelRow struct {
	ID, Name, Color, WorkspaceID string
	Description                  *string
	CreatedAt, UpdatedAt         time.Time
}

// labelColumns is the shared SELECT list for label rows.
const labelColumns = `
	id, name, color, description, workspace_id, created_at, updated_at`

// scanLabelRow fills a labelRow from a row or rowset cursor.
func scanLabelRow(s scanner, r *labelRow) error {
	return s.Scan(&r.ID, &r.Name, &r.Color, &r.Description, &r.WorkspaceID,
		&r.CreatedAt, &r.UpdatedAt)
}

// labelData serializes a label row in the exact shape of the client's
// Label model: description nullable; teamId/groupId are null in v1
// (labels are workspace-shared and label groups do not exist).
func (a *API) labelData(r labelRow) map[string]any {
	return map[string]any{
		"id":          r.ID,
		"createdAt":   r.CreatedAt.Format(iso),
		"updatedAt":   r.UpdatedAt.Format(iso),
		"name":        r.Name,
		"color":       r.Color,
		"description": r.Description,
		"workspaceId": r.WorkspaceID,
		"teamId":      nil,
		"groupId":     nil,
	}
}

// labelByID / labelByIDTx load one label (any lifecycle state).
func (a *API) labelByID(ctx context.Context, id string) (labelRow, error) {
	var r labelRow
	return r, scanLabelRow(a.pool.QueryRow(ctx, "select "+labelColumns+" from labels where id = $1", id), &r)
}

func (a *API) labelByIDTx(ctx context.Context, tx pgx.Tx, id string) (labelRow, error) {
	var r labelRow
	return r, scanLabelRow(tx.QueryRow(ctx, "select "+labelColumns+" from labels where id = $1", id), &r)
}

// labelRequest is the client's label create/patch payload. teamId and
// groupId are accepted and ignored: labels are workspace-shared in v1 and
// label groups are not a v1 concept, but the DTO carries both.
type labelRequest struct {
	WorkspaceID *string `json:"workspaceId"`
	Name        *string `json:"name"`
	Color       *string `json:"color"`
	Description *string `json:"description"`
	GroupID     *string `json:"groupId"`
	TeamID      *string `json:"teamId"`
}

func validateLabelName(name string) bool {
	return labelNamePattern.MatchString(name)
}

// handleCreateLabel implements POST /api/v1/labels (workspace admin only).
func (a *API) handleCreateLabel(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var req labelRequest
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(strval(req.Name))
	color := strings.TrimSpace(strval(req.Color))
	if !validateLabelName(name) {
		writeError(w, http.StatusUnprocessableEntity, "name must be 1-100 printable characters")
		return
	}
	if color == "" || !colorPattern.MatchString(color) {
		writeError(w, http.StatusUnprocessableEntity, "color must be a 1-100 char CSS color value")
		return
	}
	ctx := r.Context()
	workspaceID, ok := a.resolveWorkspaceForWrite(ctx, p, strval(req.WorkspaceID))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	role, _ := a.workspaceRole(ctx, p, workspaceID)
	if !adminRole(role) {
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
		insert into labels (workspace_id, name, color, description)
		values ($1, $2, $3, $4)
		returning id`, workspaceID, name, color, nullForEmpty(req.Description)).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a label with this name already exists")
			return
		}
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, workspaceID, p.AccountID, "label.created", "Label", id); err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "Label", id, "CREATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	row, err := a.labelByIDTx(ctx, tx, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, workspaceID, &rec, a.labelData(row)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusCreated, a.labelData(row))
}

// handleUpdateLabel implements POST /api/v1/labels/{id} (workspace admin
// only; partial patch — only the fields the client sent are applied).
func (a *API) handleUpdateLabel(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var req labelRequest
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	row, err := a.labelByID(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	role, ok := a.workspaceRole(ctx, p, row.WorkspaceID)
	if !ok || !adminRole(role) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	changed, err := a.applyLabelPatchTx(ctx, tx, &row, &req)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a label with this name already exists")
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
		writeJSON(w, http.StatusOK, a.labelData(row))
		return
	}
	rec, err := a.emitChange(ctx, tx, row.WorkspaceID, "Label", id, "UPDATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, row.WorkspaceID, &rec, a.labelData(row)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.labelData(row))
}

// applyLabelPatchTx applies the requested label fields inside the
// caller's transaction and reports whether anything changed.
func (a *API) applyLabelPatchTx(ctx context.Context, tx pgx.Tx, row *labelRow, req *labelRequest) (bool, error) {
	changed := false
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if !validateLabelName(name) {
			return false, errInvalidField
		}
		if name != row.Name {
			if _, err := tx.Exec(ctx, "update labels set name = $2, version = version + 1, updated_at = now() where id = $1", row.ID, name); err != nil {
				return false, err // unique violation surfaced by the caller
			}
			row.Name = name
			changed = true
		}
	}
	if req.Color != nil {
		color := strings.TrimSpace(*req.Color)
		if !colorPattern.MatchString(color) {
			return false, errInvalidField
		}
		if color != row.Color {
			if _, err := tx.Exec(ctx, "update labels set color = $2, version = version + 1, updated_at = now() where id = $1", row.ID, color); err != nil {
				return false, err
			}
			row.Color = color
			changed = true
		}
	}
	if req.Description != nil {
		desc := strings.TrimSpace(*req.Description)
		next := nullForEmpty(&desc)
		if strval(next) != strval(row.Description) {
			if _, err := tx.Exec(ctx, "update labels set description = $2, version = version + 1, updated_at = now() where id = $1", row.ID, next); err != nil {
				return false, err
			}
			row.Description = next
			changed = true
		}
	}
	return changed, nil
}

// handleDeleteLabel implements DELETE /api/v1/labels/{id} (workspace
// admin only). Archives: issue label links survive, the label leaves the
// active listings, and a DELETE record drops it from client stores.
func (a *API) handleDeleteLabel(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	row, err := a.labelByID(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	role, ok := a.workspaceRole(ctx, p, row.WorkspaceID)
	if !ok || !adminRole(role) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "update labels set status = 'archived', version = version + 1, updated_at = now() where id = $1", id); err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, row.WorkspaceID, p.AccountID, "label.archived", "Label", id); err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, row.WorkspaceID, "Label", id, "DELETE", map[string]any{"id": id})
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

// handleListLabels implements GET /api/v1/labels?workspaceId=... for
// active workspace members. The teamId parameter is accepted for client
// compatibility but is a no-op: labels are workspace-shared, so every
// team sees the same set.
func (a *API) handleListLabels(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	workspaceID := r.URL.Query().Get("workspaceId")
	if workspaceID == "" || !a.memberOf(r.Context(), p, workspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	rows, err := a.pool.Query(r.Context(), "select "+labelColumns+`
		from labels where workspace_id = $1 and status = 'active'
		order by name`, workspaceID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var r2 labelRow
		if err := scanLabelRow(rows, &r2); err != nil {
			a.internalError(w, err)
			return
		}
		out = append(out, a.labelData(r2))
	}
	if err := rows.Err(); err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
