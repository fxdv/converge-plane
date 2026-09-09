// Project module (v1.1): workspace-scoped meaning labels that group
// issues on the board.
// spec cs:api:projects
//
// A project is a grouping, never a state: an issue belongs to at most
// one project (issues.project_ids, enforced at the table) and moves
// between the project rail and its workflow column by membership
// change on the issue patch. Every mutation follows the established
// shape: validate -> single transaction (domain write + sync outbox) ->
// commit -> broadcast.
//
// Permission model: every workspace member sees projects; create,
// rename, and delete are owner/admin. Deleting a project clears
// project_ids from its member issues in the same transaction.
package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// projectRow is a projects-table row with everything the client Project
// shape needs.
type projectRow struct {
	ID, Name, Color, WorkspaceID string
	Description                  *string
	StartDate, EndDate           *string
	Teams                        []string
	LeadID                       *string
	CreatedAt, UpdatedAt         time.Time
}

// projectData serializes a project row in the exact shape of the
// client's Project model (web/src/common/types/project.ts, as-is):
// status is the v1 constant ACTIVE (v2 adds the lifecycle), and color
// is additive — the client type gains the field with the UI card.
func (a *API) projectData(r projectRow) map[string]any {
	return map[string]any{
		"id":          r.ID,
		"createdAt":   r.CreatedAt.Format(iso),
		"updatedAt":   r.UpdatedAt.Format(iso),
		"name":        r.Name,
		"description": strval(r.Description),
		"color":       r.Color,
		"startDate":   r.StartDate,
		"endDate":     r.EndDate,
		"status":      "ACTIVE",
		"leadUserId":  nullOrEmpty(strval(r.LeadID)),
		"teams":       r.Teams,
		"workspaceId": r.WorkspaceID,
	}
}

// projectColumns is the shared SELECT list for project rows.
const projectColumns = `
	id, name, color, workspace_id, description,
	(start_date)::text, (end_date)::text, teams, lead_id,
	created_at, updated_at`

// scanner covers both pgx.Row and pgx.Rows (identical Scan signatures).
func scanProjectRow(s scanner, r *projectRow) error {
	return s.Scan(&r.ID, &r.Name, &r.Color, &r.WorkspaceID, &r.Description,
		&r.StartDate, &r.EndDate, &r.Teams, &r.LeadID,
		&r.CreatedAt, &r.UpdatedAt)
}

// projectByIDTx loads one project inside the caller's transaction.
func (a *API) projectByIDTx(ctx context.Context, tx pgx.Tx, id string) (projectRow, error) {
	var r projectRow
	return r, scanProjectRow(tx.QueryRow(ctx,
		"select "+projectColumns+" from projects where id = $1", id), &r)
}

// projectInWorkspace reports whether the project exists and is live in
// the tenant (a soft-deleted or foreign project reads as absent).
func (a *API) projectInWorkspace(ctx context.Context, id, workspaceID string) bool {
	if !isUUID(id) {
		return false
	}
	var count int
	err := a.pool.QueryRow(ctx, `
		select count(*) from projects where id = $1 and workspace_id = $2 and deleted_at is null`,
		id, workspaceID).Scan(&count)
	return err == nil && count > 0
}

// projectRequest is the create/patch payload (partial updates).
type projectRequest struct {
	Name        *string  `json:"name"`
	Color       *string  `json:"color"`
	Description *string  `json:"description"`
	StartDate   *string  `json:"startDate"`
	EndDate     *string  `json:"endDate"`
	Teams       []string `json:"teams"`
	LeadID      *string  `json:"leadId"`
	WorkspaceID *string  `json:"workspaceId"`
}

// validateProjectRequest checks the mutable fields: the name bounds,
// the date formats, team tenancy, and lead membership.
func (a *API) validateProjectRequest(ctx context.Context, p *Principal, workspaceID string, req projectRequest) (string, string) {
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if len(name) < 1 || len(name) > 100 {
			return "", "name must be 1-100 chars"
		}
	}
	for _, d := range []struct {
		v   *string
		why string
	}{
		{req.StartDate, "startDate"}, {req.EndDate, "endDate"},
	} {
		if d.v != nil && *d.v != "" && !validDate(*d.v) {
			return "", d.why + " must be a YYYY-MM-DD date"
		}
	}
	for _, teamID := range req.Teams {
		if !isUUID(teamID) {
			return "", "teams contains an invalid team id"
		}
		if ws, ok := a.teamWorkspace(ctx, p, teamID); !ok || ws != workspaceID {
			return "", "teams contains a team outside this workspace"
		}
	}
	if req.LeadID != nil && *req.LeadID != "" && !a.isWorkspaceMember(ctx, workspaceID, *req.LeadID) {
		return "", "leadId is not a workspace member"
	}
	return "", ""
}

// validDate accepts the client's date-only wire format.
func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// projectKey collapses the membership array to its single v1 value for
// the history row's from/to strings.
func projectKey(projectIDs []string) string {
	if len(projectIDs) > 0 {
		return projectIDs[0]
	}
	return ""
}

// nullifEmpty converts "" to NULL for nullable text/date/uuid columns.
func nullifEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// handleListProjects implements GET /api/v1/projects?workspaceId=...&teamId=...:
// the projects whose board shows the team (teams[] empty means every team).
func (a *API) handleListProjects(w http.ResponseWriter, r *http.Request) {
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
	teamID := r.URL.Query().Get("teamId")
	teamsFilter := []string{}
	if teamID != "" {
		if !isUUID(teamID) {
			writeError(w, http.StatusBadRequest, "teamId is invalid")
			return
		}
		if ws, ok := a.teamWorkspace(r.Context(), p, teamID); !ok || ws != workspaceID {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		teamsFilter = []string{teamID}
	}
	rows, err := a.pool.Query(r.Context(), `
		select `+projectColumns+`
		from projects
		where workspace_id = $1 and deleted_at is null
		  and (cardinality(teams) = 0 or teams @> $2::uuid[])
		order by name`, workspaceID, &teamsFilter)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var row projectRow
		if err := scanProjectRow(rows, &row); err != nil {
			a.internalError(w, err)
			return
		}
		out = append(out, a.projectData(row))
	}
	if err := rows.Err(); err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateProject implements POST /api/v1/projects (owner/admin).
func (a *API) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var req projectRequest
	if err := jsonDecode(r, &req); err != nil || req.Name == nil {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	ctx := r.Context()
	workspaceID, ok := a.resolveWorkspaceForWrite(ctx, p, strval(req.WorkspaceID))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if role, _ := a.workspaceRole(ctx, p, workspaceID); !adminRole(role) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if _, why := a.validateProjectRequest(ctx, p, workspaceID, req); why != "" {
		writeError(w, http.StatusUnprocessableEntity, why)
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	teams := append([]string{}, req.Teams...)
	var (
		id        string
		createdAt time.Time
	)
	err = tx.QueryRow(ctx, `
		insert into projects (workspace_id, name, color, description, start_date, end_date, teams, lead_id, created_by)
		values ($1, $2, $3, nullif($4, ''), nullif($5, '')::date, nullif($6, '')::date,
		        $7::uuid[], nullif($8, '')::uuid, $9)
		returning id, created_at`,
		workspaceID, strings.TrimSpace(*req.Name), strval(req.Color), strval(req.Description),
		strval(req.StartDate), strval(req.EndDate),
		&teams, strval(req.LeadID), p.AccountID).
		Scan(&id, &createdAt)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a project with this name already exists")
			return
		}
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, workspaceID, p.AccountID, "project.created", "Project", id); err != nil {
		a.internalError(w, err)
		return
	}
	row, err := a.projectByIDTx(ctx, tx, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "Project", id, "CREATE", a.projectData(row))
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusCreated, rec.Data)
}

// projectAccess loads the project and verifies tenant + admin access.
// ok is false for unknown, soft-deleted, foreign, or non-admin access.
func (a *API) projectAccess(ctx context.Context, p *Principal, id, workspaceID string) (projectRow, string, bool) {
	var r projectRow
	err := a.pool.QueryRow(ctx,
		"select "+projectColumns+" from projects where id = $1 and deleted_at is null", id).Scan(
		&r.ID, &r.Name, &r.Color, &r.WorkspaceID, &r.Description,
		&r.StartDate, &r.EndDate, &r.Teams, &r.LeadID,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return r, "", false
	}
	if r.WorkspaceID != workspaceID {
		return r, r.WorkspaceID, false
	}
	role, _ := a.workspaceRole(ctx, p, workspaceID)
	return r, workspaceID, adminRole(role)
}

// handleUpdateProject implements POST /api/v1/projects/{id}
// (owner/admin; partial patch of the mutable fields).
func (a *API) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	workspaceID := r.URL.Query().Get("workspaceId")
	if workspaceID == "" || !a.memberOf(ctx, p, workspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	_, _, ok := a.projectAccess(ctx, p, id, workspaceID)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var req projectRequest
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, why := a.validateProjectRequest(ctx, p, workspaceID, req); why != "" {
		writeError(w, http.StatusUnprocessableEntity, why)
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		writeError(w, http.StatusUnprocessableEntity, "name must be 1-100 chars")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fields := []string{}
	args := []any{}
	add := func(col, sqlType string, v any) {
		args = append(args, v)
		fields = append(fields, col+" = $"+strconv.Itoa(len(args))+sqlType)
	}
	if req.Name != nil {
		add("name", "", strings.TrimSpace(*req.Name))
	}
	if req.Color != nil {
		add("color", "", *req.Color)
	}
	if req.Description != nil {
		add("description", "", nullifEmpty(*req.Description))
	}
	if req.StartDate != nil {
		add("start_date", "::date", nullifEmpty(*req.StartDate))
	}
	if req.EndDate != nil {
		add("end_date", "::date", nullifEmpty(*req.EndDate))
	}
	if req.Teams != nil {
		teams := append([]string{}, req.Teams...)
		add("teams", "::uuid[]", &teams)
	}
	if req.LeadID != nil {
		add("lead_id", "", nullifEmpty(*req.LeadID))
	}
	if len(fields) > 0 {
		args = append(args, id)
		_, err = tx.Exec(ctx, `
			update projects set `+strings.Join(fields, ", ")+`, updated_at = now()
			where id = $`+strconv.Itoa(len(args))+` and deleted_at is null`, args...)
		if err != nil {
			if isUniqueViolation(err) {
				writeError(w, http.StatusConflict, "a project with this name already exists")
				return
			}
			a.internalError(w, err)
			return
		}
	}
	row, err := a.projectByIDTx(ctx, tx, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "Project", id, "UPDATE", a.projectData(row))
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.projectData(row))
}

// handleDeleteProject implements POST /api/v1/projects/{id}/delete
// (owner/admin). The project is soft-deleted and its member issues have
// project_ids cleared in the same transaction; the member records
// refresh, so the board rail and the cards converge on one picture.
func (a *API) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	workspaceID := r.URL.Query().Get("workspaceId")
	if workspaceID == "" || !a.memberOf(ctx, p, workspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	row, _, ok := a.projectAccess(ctx, p, id, workspaceID)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	recs, err := a.deleteProjectTx(ctx, tx, workspaceID, p.AccountID, row)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	for i := range recs {
		a.broadcastRecord(recs[i])
	}
	writeJSON(w, http.StatusOK, a.projectData(row))
}

// deleteProjectTx soft-deletes the project and clears its issues'
// membership inside the caller's transaction: the project leaves the
// feed with its pre-delete payload, and each member issue refreshes
// with the cleared array (the board rail drops the stack; the card is
// back in its column with no project). Returns the records to
// broadcast.
func (a *API) deleteProjectTx(ctx context.Context, tx pgx.Tx, workspaceID, actorID string, row projectRow) ([]syncActionRecord, error) {
	if _, err := tx.Exec(ctx,
		"update projects set deleted_at = now(), updated_at = now() where id = $1 and deleted_at is null", row.ID); err != nil {
		return nil, err
	}
	// Clear the membership on every live member issue (any team in the
	// workspace): the membership is the single source of truth, so the
	// delete cannot strand cards on a dead project.
	var memberIDs []string
	mrows, err := tx.Query(ctx, `
		select i.id from issues i
		join teams t on t.id = i.team_id
		where t.workspace_id = $1 and i.status = 'active'
		  and $2 = any(i.project_ids)`, workspaceID, row.ID)
	if err != nil {
		return nil, err
	}
	for mrows.Next() {
		var mid string
		if err := mrows.Scan(&mid); err != nil {
			mrows.Close()
			return nil, err
		}
		memberIDs = append(memberIDs, mid)
	}
	mrows.Close()
	if err := mrows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`update issues set project_ids = '{}', version = version + 1, updated_at = now()
		 where id = any($1::uuid[])`, &memberIDs); err != nil {
		return nil, err
	}
	if err := a.auditTx(ctx, tx, workspaceID, actorID, "project.deleted", "Project", row.ID); err != nil {
		return nil, err
	}
	recs := make([]syncActionRecord, 0, len(memberIDs)+1)
	delRec, err := a.emitChange(ctx, tx, workspaceID, "Project", row.ID, "DELETE", a.projectData(row))
	if err != nil {
		return nil, err
	}
	recs = append(recs, delRec)
	for _, mid := range memberIDs {
		fresh, err := a.issueByIDTx(ctx, tx, mid)
		if err != nil {
			return nil, err
		}
		issueRec, err := a.emitChange(ctx, tx, workspaceID, "Issue", mid, "UPDATE", nil)
		if err != nil {
			return nil, err
		}
		if err := a.refreshOutboxTx(ctx, tx, workspaceID, &issueRec, a.issueData(fresh)); err != nil {
			return nil, err
		}
		recs = append(recs, issueRec)
	}
	return recs, nil
}

// collectProjects is the sync bootstrap collector for the Project model.
func (a *API) collectProjects(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `
		select `+projectColumns+`
		from projects where workspace_id = $1 and deleted_at is null
		order by name`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []syncActionRecord
	for rows.Next() {
		var r projectRow
		if err := scanProjectRow(rows, &r); err != nil {
			return nil, err
		}
		rec, err := emit(r.ID, a.projectData(r))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
