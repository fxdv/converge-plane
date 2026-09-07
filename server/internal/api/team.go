// Team module (M5): team CRUD, team membership, and the shared
// workspace-membership data builders.
//
// Permission model (docs/spec/07 matrices):
//   - create team: workspace owner/admin
//   - update team: workspace owner/admin or team manager
//   - archive team: workspace owner/admin
//   - manage team membership: workspace owner/admin or team manager
//
// Every mutation follows the established shape: validate -> single
// transaction (domain write + sync outbox) -> commit -> broadcast.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// teamRow is a teams-table row with everything the client Team shape
// needs (Status feeds the archive filters, not the wire).
type teamRow struct {
	ID, Name, Identifier, Status, WorkspaceID string
	CreatedAt, UpdatedAt                      time.Time
	Preferences                               []byte // jsonb
}

// teamData serializes a team row in the exact shape of the client's Team
// model (web store/teams/models.ts). Bootstrap and mutation responses share
// this so both speak the identical vocabulary. preferences is a strict
// sub-model on the client, so it must always be present ({} when empty).
func (a *API) teamData(r teamRow) map[string]any {
	prefs, err := parsePreferences(r.Preferences)
	if err != nil {
		prefs = map[string]any{}
	}
	return map[string]any{
		"id":           r.ID,
		"createdAt":    r.CreatedAt.Format(iso),
		"updatedAt":    r.UpdatedAt.Format(iso),
		"name":         r.Name,
		"identifier":   r.Identifier,
		"workspaceId":  r.WorkspaceID,
		"currentCycle": nil, // nil until cycles ship (v1.1)
		"preferences":  prefs,
	}
}

// parsePreferences decodes the stored preferences jsonb. An empty object
// falls back to the client's default team profile; malformed input degrades
// to the same default rather than failing the whole sync.
func parsePreferences(raw []byte) (map[string]any, error) {
	prefs := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &prefs); err != nil {
			return map[string]any{}, err
		}
	}
	if len(prefs) == 0 {
		prefs = map[string]any{"teamType": "engineering", "cyclesEnabled": false}
	}
	return prefs, nil
}

// teamColumns is the shared SELECT list for team rows.
const teamColumns = `
	id, name, identifier, status, workspace_id, created_at, updated_at, preferences`

// scanner covers both pgx.Row and pgx.Rows (identical Scan signatures).
type scanner interface {
	Scan(dest ...any) error
}

func scanTeamRow(s scanner, r *teamRow) error {
	return s.Scan(&r.ID, &r.Name, &r.Identifier, &r.Status, &r.WorkspaceID,
		&r.CreatedAt, &r.UpdatedAt, &r.Preferences)
}

// teamByID loads one team (any lifecycle state).
func (a *API) teamByID(ctx context.Context, id string) (teamRow, error) {
	var r teamRow
	return r, scanTeamRow(a.pool.QueryRow(ctx, "select "+teamColumns+" from teams where id = $1", id), &r)
}

// teamManager reports whether the principal may manage a team: a workspace
// owner/admin always can; otherwise the principal must be a manager of that
// team. Unknown principals or non-members get false.
func (a *API) teamManager(ctx context.Context, p *Principal, teamID, workspaceID string) bool {
	role, ok := a.workspaceRole(ctx, p, workspaceID)
	if !ok {
		return false
	}
	if adminRole(role) {
		return true
	}
	var mgr string
	err := a.pool.QueryRow(ctx,
		"select role from team_members where team_id = $1 and account_id = $2",
		teamID, p.AccountID).Scan(&mgr)
	return err == nil && strings.EqualFold(mgr, "manager")
}

// adminRole reports whether a workspace role is an admin role (owner or
// admin). The backend distinguishes owner; the client maps both to ADMIN.
func adminRole(role string) bool {
	switch strings.ToLower(role) {
	case "owner", "admin":
		return true
	}
	return false
}

var identifierPattern = regexp.MustCompile(`^[A-Z0-9-]{1,12}$`)

// teamRequest is the client's team create/patch payload (partial updates:
// only the fields the client sent are applied).
type teamRequest struct {
	WorkspaceID *string `json:"workspaceId"`
	Name        *string `json:"name"`
	Identifier  *string `json:"identifier"`
	Icon        *string `json:"icon"` // accepted, ignored: no icon column in v1
	Preferences any     `json:"preferences"`
}

// handleCreateTeam implements POST /api/v1/teams (workspace admin only).
func (a *API) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var req teamRequest
	if err := jsonDecode(r, &req); err != nil || req.Name == nil || req.Identifier == nil {
		writeError(w, http.StatusBadRequest, "name and identifier are required")
		return
	}
	name := strings.TrimSpace(*req.Name)
	if len(name) < 1 || len(name) > 64 {
		writeError(w, http.StatusUnprocessableEntity, "name must be 1-64 chars")
		return
	}
	identifier := strings.ToUpper(strings.TrimSpace(*req.Identifier))
	if !identifierPattern.MatchString(identifier) {
		writeError(w, http.StatusUnprocessableEntity, "identifier must be 1-12 chars of A-Z, 0-9 or -")
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

	// Serialize position allocation per workspace: concurrent creates
	// would otherwise read the same max(position) and tie.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_team_"+workspaceID); err != nil {
		a.internalError(w, err)
		return
	}
	var (
		id        string
		createdAt time.Time
	)
	err = tx.QueryRow(ctx, `
		insert into teams (workspace_id, name, identifier, position)
		values ($1, $2, $3, (select coalesce(max(position), 0) + 1 from teams where workspace_id = $1))
		returning id, created_at`, workspaceID, name, identifier).Scan(&id, &createdAt)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a team with this identifier already exists")
			return
		}
		a.internalError(w, err)
		return
	}
	if req.Preferences != nil {
		raw, _ := json.Marshal(req.Preferences)
		if _, err := tx.Exec(ctx, "update teams set preferences = $2 where id = $1", id, raw); err != nil {
			a.internalError(w, err)
			return
		}
	}
	if err := a.auditTx(ctx, tx, workspaceID, p.AccountID, "team.created", "Team", id); err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "Team", id, "CREATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	row, err := a.teamByIDTx(ctx, tx, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, workspaceID, &rec, a.teamData(row)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusCreated, a.teamData(row))
}

// teamByIDTx loads a team row from inside the caller's transaction.
func (a *API) teamByIDTx(ctx context.Context, tx pgx.Tx, id string) (teamRow, error) {
	var r teamRow
	return r, scanTeamRow(tx.QueryRow(ctx, "select "+teamColumns+" from teams where id = $1", id), &r)
}

// isUniqueViolation reports a PostgreSQL unique-constraint failure
// (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// handleUpdateTeam implements POST /api/v1/teams/{id} (admin or team
// manager). Identifier changes are accepted: issue keys are derived from
// identifier + number at render time, so a rename never renumbers.
func (a *API) handleUpdateTeam(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var req teamRequest
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	row, err := a.teamByID(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if _, ok := a.workspaceRole(ctx, p, row.WorkspaceID); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.teamManager(ctx, p, row.ID, row.WorkspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	changed, err := a.applyTeamPatchTx(ctx, tx, &row, &req)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a team with this identifier already exists")
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
		writeJSON(w, http.StatusOK, a.teamData(row))
		return
	}
	rec, err := a.emitChange(ctx, tx, row.WorkspaceID, "Team", id, "UPDATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, row.WorkspaceID, &rec, a.teamData(row)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.teamData(row))
}

// applyTeamPatchTx applies the requested team fields inside the caller's
// transaction. It reports whether anything changed.
func (a *API) applyTeamPatchTx(ctx context.Context, tx pgx.Tx, row *teamRow, req *teamRequest) (bool, error) {
	changed := false
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if len(name) < 1 || len(name) > 64 {
			return false, errInvalidField
		}
		if name != row.Name {
			if _, err := tx.Exec(ctx, "update teams set name = $2, version = version + 1, updated_at = now() where id = $1", row.ID, name); err != nil {
				return false, err
			}
			row.Name = name
			changed = true
		}
	}
	if req.Identifier != nil {
		identifier := strings.ToUpper(strings.TrimSpace(*req.Identifier))
		if !identifierPattern.MatchString(identifier) {
			return false, errInvalidField
		}
		if identifier != row.Identifier {
			if _, err := tx.Exec(ctx, "update teams set identifier = $2, version = version + 1, updated_at = now() where id = $1", row.ID, identifier); err != nil {
				return false, err // unique violation surfaced by the caller
			}
			row.Identifier = identifier
			changed = true
		}
	}
	if req.Preferences != nil {
		raw, _ := json.Marshal(req.Preferences)
		if _, err := tx.Exec(ctx, "update teams set preferences = $2, version = version + 1, updated_at = now() where id = $1", row.ID, raw); err != nil {
			return false, err
		}
		row.Preferences = raw
		changed = true
	}
	return changed, nil
}

// handleDeleteTeam implements DELETE /api/v1/teams/{id} (workspace admin
// only per spec: TM may update, only WA/WO archive). The team is archived,
// not dropped: its issues remain (their team_id FK cascades on a hard
// delete), while the team disappears from active listings.
func (a *API) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	row, err := a.teamByID(ctx, id)
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
	if _, err := tx.Exec(ctx, "update teams set status = 'archived', version = version + 1, updated_at = now() where id = $1", id); err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, row.WorkspaceID, p.AccountID, "team.archived", "Team", id); err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, row.WorkspaceID, "Team", id, "DELETE", map[string]any{"id": id})
	if err != nil {
		a.internalError(w, err)
		return
	}
	// Archive the team's active issues with it: the sync feed stops
	// serving them (status='archived') and clients drop them on the D
	// records, so a dropped team leaves no dangling issue rows in the
	// client stores (the issue components resolve their team lookup by
	// id and would lose it, D4 incident).
	rows, err := tx.Query(ctx, `select id from issues where team_id = $1 and status = 'active'`, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	var issueIDs []string
	for rows.Next() {
		var issueID string
		if err := rows.Scan(&issueID); err != nil {
			rows.Close()
			a.internalError(w, err)
			return
		}
		issueIDs = append(issueIDs, issueID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		a.internalError(w, err)
		return
	}
	issueRecs := make([]syncActionRecord, 0, len(issueIDs))
	for _, issueID := range issueIDs {
		if _, err := tx.Exec(ctx, `update issues set status = 'archived', updated_at = now() where id = $1`, issueID); err != nil {
			a.internalError(w, err)
			return
		}
		irect, err := a.emitChange(ctx, tx, row.WorkspaceID, "Issue", issueID, "DELETE", map[string]any{"id": issueID})
		if err != nil {
			a.internalError(w, err)
			return
		}
		issueRecs = append(issueRecs, irect)
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	for _, irect := range issueRecs {
		a.broadcastRecord(irect)
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// handleListTeams implements GET /api/v1/teams?workspaceId=... for active
// workspace members.
func (a *API) handleListTeams(w http.ResponseWriter, r *http.Request) {
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
	rows, err := a.pool.Query(r.Context(), "select "+teamColumns+" from teams where workspace_id = $1 and status = 'active' order by position, created_at", workspaceID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var r2 teamRow
		if err := scanTeamRow(rows, &r2); err != nil {
			a.internalError(w, err)
			return
		}
		out = append(out, a.teamData(r2))
	}
	if err := rows.Err(); err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetTeam implements GET /api/v1/teams/{id}.
func (a *API) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	row, err := a.teamByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.memberOf(r.Context(), p, row.WorkspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, a.teamData(row))
}

// handleGetTeamByName implements GET /api/v1/teams/name/{identifier}.
func (a *API) handleGetTeamByName(w http.ResponseWriter, r *http.Request) {
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
	var r2 teamRow
	err := scanTeamRow(a.pool.QueryRow(r.Context(),
		"select "+teamColumns+" from teams where workspace_id = $1 and identifier = $2 and status = 'active'",
		workspaceID, strings.ToUpper(chi.URLParam(r, "slug"))), &r2)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, a.teamData(r2))
}

// handleAddTeamMember implements POST /api/v1/teams/{id}/add-member
// (workspace admin or team manager). The target must be an active workspace
// member. Emits a UsersOnWorkspaces update: the client derives team
// membership from that record's teamIds.
func (a *API) handleAddTeamMember(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	var req struct {
		UserID string `json:"userId"`
	}
	if err := jsonDecode(r, &req); err != nil || !isUUID(req.UserID) {
		writeError(w, http.StatusBadRequest, "userId is required")
		return
	}
	row, err := a.teamByID(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if _, ok := a.workspaceRole(ctx, p, row.WorkspaceID); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.teamManager(ctx, p, row.ID, row.WorkspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	target, err := a.memberByAccount(ctx, row.WorkspaceID, req.UserID)
	if err != nil || target.Status != "active" {
		writeError(w, http.StatusUnprocessableEntity, "userId must be an active workspace member")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		insert into team_members (id, team_id, account_id, role)
		values (gen_random_uuid(), $1, $2, 'member')
		on conflict (team_id, account_id) do update set role = excluded.role`,
		row.ID, req.UserID); err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, row.WorkspaceID, p.AccountID, "team_membership.added", "Team", row.ID); err != nil {
		a.internalError(w, err)
		return
	}
	rec, fresh, err := a.emitMemberChangeTx(ctx, tx, row.WorkspaceID, req.UserID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, row.WorkspaceID, &rec, a.memberData(fresh)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.memberData(fresh))
}

// handleRemoveTeamMember implements POST /api/v1/teams/{id}/remove-member
// (workspace admin or team manager; may remove any active workspace member).
func (a *API) handleRemoveTeamMember(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	var req struct {
		UserID string `json:"userId"`
	}
	if err := jsonDecode(r, &req); err != nil || !isUUID(req.UserID) {
		writeError(w, http.StatusBadRequest, "userId is required")
		return
	}
	row, err := a.teamByID(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if _, ok := a.workspaceRole(ctx, p, row.WorkspaceID); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.teamManager(ctx, p, row.ID, row.WorkspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	// The target must hold a workspace membership: the sync record is
	// keyed by that row, so a non-member has nothing to update.
	if _, err := a.memberByAccount(ctx, row.WorkspaceID, req.UserID); err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "delete from team_members where team_id = $1 and account_id = $2", row.ID, req.UserID); err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, row.WorkspaceID, p.AccountID, "team_membership.removed", "Team", row.ID); err != nil {
		a.internalError(w, err)
		return
	}
	rec, fresh, err := a.emitMemberChangeTx(ctx, tx, row.WorkspaceID, req.UserID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, row.WorkspaceID, &rec, a.memberData(fresh)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.memberData(fresh))
}

// handleUpdateTeamPreferences implements POST /api/v1/teams/{id}/preferences
// (workspace admin or team manager). The supplied object replaces the stored
// preferences wholesale (the client always sends the full object).
func (a *API) handleUpdateTeamPreferences(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	var req struct {
		CyclesEnabled   *bool   `json:"cyclesEnabled"`
		CyclesFrequency *int    `json:"cyclesFrequency"`
		UpcomingCycles  *int    `json:"upcomingCycles"`
		TeamType        *string `json:"teamType"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := chi.URLParam(r, "id")
	row, err := a.teamByID(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.teamManager(ctx, p, row.ID, row.WorkspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	merged, err := parsePreferences(row.Preferences)
	if err != nil {
		merged = map[string]any{}
	}
	if req.CyclesEnabled != nil {
		merged["cyclesEnabled"] = *req.CyclesEnabled
	}
	if req.CyclesFrequency != nil {
		merged["cyclesFrequency"] = *req.CyclesFrequency
	}
	if req.UpcomingCycles != nil {
		merged["upcomingCycles"] = *req.UpcomingCycles
	}
	if req.TeamType != nil {
		merged["teamType"] = *req.TeamType
	}
	raw, _ := json.Marshal(merged)

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "update teams set preferences = $2, version = version + 1, updated_at = now() where id = $1", id, raw); err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, row.WorkspaceID, "Team", id, "UPDATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	fresh, err := a.teamByIDTx(ctx, tx, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, row.WorkspaceID, &rec, a.teamData(fresh)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	writeJSON(w, http.StatusOK, a.teamData(fresh))
}

// ---- workspace resolution helpers ----------------------------------------

// memberOf reports whether the principal is an active member of the
// workspace (any active role).
func (a *API) memberOf(ctx context.Context, p *Principal, workspaceID string) bool {
	if workspaceID == "" {
		return false
	}
	_, ok := a.workspaceRole(ctx, p, workspaceID)
	return ok
}

// resolveWorkspaceForWrite resolves the workspace a team/workspace write
// applies to. The client does not always send a workspace id, so: an
// explicit id must be one the principal actively belongs to; otherwise a
// principal with exactly one active workspace resolves to it (a window
// function keeps the row+count check to a single round-trip). ok is
// false for anything ambiguous or unauthorized (404 in the handlers).
func (a *API) resolveWorkspaceForWrite(ctx context.Context, p *Principal, explicit string) (string, bool) {
	if explicit != "" {
		if _, ok := a.workspaceRole(ctx, p, explicit); !ok {
			return "", false
		}
		return explicit, true
	}
	var workspaceID string
	var count int
	err := a.pool.QueryRow(ctx, `
		select workspace_id, count(*) over ()
		from workspace_members
		where account_id = $1 and status = 'active'
		order by (select created_at from workspaces w where w.id = workspace_members.workspace_id)
		limit 1`, p.AccountID).Scan(&workspaceID, &count)
	if err != nil || count != 1 {
		return "", false
	}
	return workspaceID, true
}

// errInvalidField marks a patch value that failed validation; handlers map
// it to a 422 rather than a 500.
var errInvalidField = errors.New("invalid field value")
