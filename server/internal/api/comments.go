// Issue comment mutations (M2).
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// commentRow is one comments-table row in client shape.
type commentRow struct {
	ID, Body, AuthorID, IssueID string
	ParentID                    *string
}

// commentData serializes a comment in the exact shape of the client's
// IssueComment model. sourceMetadata must be present (null) — the client
// field is union(string, null) without undefined.
func (a *API) commentData(c commentRow, createdAt, updatedAt string) map[string]any {
	return map[string]any{
		"id":             c.ID,
		"createdAt":      createdAt,
		"updatedAt":      updatedAt,
		"body":           descriptionForClient(c.Body),
		"userId":         c.AuthorID,
		"issueId":        c.IssueID,
		"parentId":       nullOrEmpty(strval(c.ParentID)),
		"sourceMetadata": nil,
	}
}

// commentAccess loads a comment (with timestamps) and verifies the
// principal can access the issue's workspace.
// ok is false for unknown comments or non-members.
func (a *API) commentAccess(ctx context.Context, principal *Principal, commentID string) (commentRow, issueRow, string, string, string, bool) {
	var c commentRow
	var createdAtS, updatedAtS time.Time
	err := a.pool.QueryRow(ctx, `
		select cm.id, cm.body, cm.author_id, cm.issue_id, cm.parent_id, cm.created_at, cm.updated_at
		from comments cm where cm.id = $1`, commentID).
		Scan(&c.ID, &c.Body, &c.AuthorID, &c.IssueID, &c.ParentID, &createdAtS, &updatedAtS)
	if err != nil {
		return c, issueRow{}, "", "", "", false
	}
	row, ws, ok := a.issueAccess(ctx, principal, c.IssueID)
	if !ok {
		return c, row, "", "", "", false
	}
	return c, row, ws, createdAtS.Format(iso), updatedAtS.Format(iso), true
}

// handleCreateComment implements POST /api/v1/issue_comments?issueId=...
func (a *API) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	issueID := r.URL.Query().Get("issueId")
	var req struct {
		Body     string `json:"body"`
		ParentID string `json:"parentId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Body == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}
	row, workspaceID, ok := a.issueAccess(ctx, p, issueID)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.agentPausedGuard(w, p, row) {
		return
	}
	if req.ParentID != "" && !a.commentInIssue(ctx, req.ParentID, row.ID) {
		writeError(w, http.StatusUnprocessableEntity, "parentId is not a comment on this issue")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id string
	if err := tx.QueryRow(ctx, `
		insert into comments (issue_id, author_id, body, parent_id)
		values ($1, $2, $3::jsonb, $4)
		returning id`,
		row.ID, p.AccountID, toJSONB(req.Body), nullForEmpty(&req.ParentID)).Scan(&id); err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "IssueComment", id, "CREATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	fresh, createdAt, updatedAt, err := a.commentByID(ctx, tx, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, workspaceID, &rec, a.commentData(fresh, createdAt, updatedAt)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusCreated, a.commentData(fresh, createdAt, updatedAt))
}

// queryer is satisfied by both pgx.Tx and *pgxpool.Pool.
type queryer interface {
	QueryRow(ctx context.Context, query string, args ...any) pgx.Row
}

// commentByID loads a comment row plus timestamps from the given queryer.
func (a *API) commentByID(ctx context.Context, q queryer, id string) (commentRow, string, string, error) {
	var c commentRow
	var createdAt, updatedAt time.Time
	err := q.QueryRow(ctx, `
		select cm.id, cm.body, cm.author_id, cm.issue_id, cm.parent_id, cm.created_at, cm.updated_at
		from comments cm where cm.id = $1`, id).
		Scan(&c.ID, &c.Body, &c.AuthorID, &c.IssueID, &c.ParentID, &createdAt, &updatedAt)
	return c, createdAt.Format(iso), updatedAt.Format(iso), err
}

// handleUpdateComment implements POST /api/v1/issue_comments/{id}.
func (a *API) handleUpdateComment(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var req struct {
		Body     string `json:"body"`
		ParentID string `json:"parentId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Body == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}
	c, row, workspaceID, _, _, ok := a.commentAccess(ctx, p, id)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.agentPausedGuard(w, p, row) {
		return
	}
	if req.ParentID != "" && !a.commentInIssue(ctx, req.ParentID, row.ID) {
		writeError(w, http.StatusUnprocessableEntity, "parentId is not a comment on this issue")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		update comments set body = $2::jsonb, parent_id = $3, version = version + 1, updated_at = now()
		where id = $1 and status = 'active'`,
		id, toJSONB(req.Body), nullForEmpty(&req.ParentID)); err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "IssueComment", id, "UPDATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	fresh, createdAt, updatedAt, err := a.commentByID(ctx, tx, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	_ = c
	if err := a.refreshOutboxTx(ctx, tx, workspaceID, &rec, a.commentData(fresh, createdAt, updatedAt)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.commentData(fresh, createdAt, updatedAt))
}

// handleDeleteComment implements DELETE /api/v1/issue_comments/{id}
// (soft delete).
func (a *API) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	c, row, workspaceID, _, _, ok := a.commentAccess(ctx, p, id)
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

	if _, err := tx.Exec(ctx, `update comments set status = 'deleted', version = version + 1, updated_at = now() where id = $1`, id); err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "IssueComment", id, "DELETE", map[string]any{"id": id})
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, map[string]string{"id": c.ID})
}

// handleGetComment implements GET /api/v1/issue_comments/{id}.
func (a *API) handleGetComment(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	c, _, _, createdAt, updatedAt, ok := a.commentAccess(ctx, p, id)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, a.commentData(c, createdAt, updatedAt))
}

// handleGetCommentReplies implements GET /api/v1/issue_comments/{id}/replies.
func (a *API) handleGetCommentReplies(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	c, _, _, _, _, ok := a.commentAccess(ctx, p, id)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	rows, err := a.pool.Query(ctx, `
		select cm.id, cm.created_at, cm.updated_at, cm.body, cm.author_id, cm.issue_id, cm.parent_id
		from comments cm
		where cm.parent_id = $1 and cm.status = 'active'
		order by cm.created_at`, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var rc commentRow
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&rc.ID, &createdAt, &updatedAt, &rc.Body, &rc.AuthorID, &rc.IssueID, &rc.ParentID); err != nil {
			a.internalError(w, err)
			return
		}
		out = append(out, a.commentData(rc, createdAt.Format(iso), updatedAt.Format(iso)))
	}
	_ = c
	writeJSON(w, http.StatusOK, out)
}

// commentInIssue reports whether a comment exists on the issue.
func (a *API) commentInIssue(ctx context.Context, commentID, issueID string) bool {
	var exists bool
	err := a.pool.QueryRow(ctx,
		"select exists(select 1 from comments where id = $1 and issue_id = $2)",
		commentID, issueID).Scan(&exists)
	return err == nil && exists
}
