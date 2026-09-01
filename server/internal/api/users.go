package api

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// workspaceSummary matches the web client's User.workspaces entry.
type workspaceSummary struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Icon           string `json:"icon"`
	Status         string `json:"status"`
	ActionsEnabled bool   `json:"actionsEnabled"`
}

type inviteSummary struct {
	ID          string           `json:"id"`
	WorkspaceID string           `json:"workspaceId"`
	Workspace   workspaceSummary `json:"workspace"`
	Status      string           `json:"status"`
}

// userResponse matches the web client's User type.
type userResponse struct {
	Fullname   string             `json:"fullname"`
	Email      string             `json:"email"`
	ID         string             `json:"id"`
	Username   string             `json:"username"`
	Workspaces []workspaceSummary `json:"workspaces"`
	Invites    []inviteSummary    `json:"invites"`
	Role       string             `json:"role"`
	Image      string             `json:"image"`
}

// publicUser matches the web client's User shape for bulk lookups.
// It mirrors Tegon's getUsersbyId, which returns the workspace
// members' names for assignee/member renderers.
type publicUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Fullname string `json:"fullname"`
	Email    string `json:"email"`
	Image    string `json:"image"`
	Role     string `json:"role"`
}

// handleGetUser implements GET /api/v1/users. With a userIds query
// parameter (even an empty one) it returns a JSON array of public users;
// the client's hooks call .find/.filter on the result, so the response
// must always be an array on that path. Without it, it returns the
// caller's account with workspaces and pending invites.
func (a *API) handleGetUser(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if v, ok := r.URL.Query()["userIds"]; ok {
		a.handleGetUsersByIds(w, r, parseUserIds(v[0]))
		return
	}
	ctx := r.Context()
	resp := userResponse{
		ID:       p.AccountID,
		Email:    p.Email,
		Fullname: p.Fullname,
		Username: strings.SplitN(p.Email, "@", 2)[0],
		Role:     "USER",
		Image:    "",
		// Non-nil by wire contract: a brand-new account (no memberships,
		// no invites) must serialize [] not null — the client calls
		// .find/.length on both collections, and a JSON null would crash
		// the sign-in -> onboarding journey.
		Workspaces: []workspaceSummary{},
		Invites:    []inviteSummary{},
	}

	// Active and suspended memberships are both listed, and the
	// membership state is what the client renders: a suspended member
	// (workspaceRes.status === 'SUSPENDED' in user-data-wrapper) sees
	// the suspended screen on their next load instead of a silently
	// empty workspace list.
	rows, err := a.pool.Query(ctx, `
		select w.id, w.name, w.slug, wm.status, wm.role
		from workspaces w
		join workspace_members wm on wm.workspace_id = w.id
		where wm.account_id = $1 and wm.status in ('active', 'suspended')
		order by w.created_at`, p.AccountID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	firstRoleSet := false
	for rows.Next() {
		var ws workspaceSummary
		var status, role string
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.Slug, &status, &role); err != nil {
			rows.Close()
			a.internalError(w, err)
			return
		}
		ws.Status = strings.ToUpper(status)
		ws.Icon = ""
		ws.ActionsEnabled = false
		resp.Workspaces = append(resp.Workspaces, ws)
		// Tegon takes the first membership's role as the user-level role;
		// the query is ordered by workspace created_at, so the first row is
		// the oldest workspace.
		if !firstRoleSet {
			resp.Role = clientRole(role)
			firstRoleSet = true
		}
	}
	rows.Close()

	var inviteRows pgx.Rows
	inviteRows, err = a.pool.Query(ctx, `
		select i.id, i.workspace_id, w.id, w.name, w.slug, w.status
		from invitations i
		join workspaces w on w.id = i.workspace_id
		where i.email = $1 and i.consumed_at is null and i.revoked_at is null
		and i.expires_at > now()
		order by i.created_at`, p.Email)
	if err != nil {
		a.internalError(w, err)
		return
	}
	for inviteRows.Next() {
		var inv inviteSummary
		var status string
		if err := inviteRows.Scan(&inv.ID, &inv.WorkspaceID, &inv.Workspace.ID, &inv.Workspace.Name, &inv.Workspace.Slug, &status); err != nil {
			inviteRows.Close()
			a.internalError(w, err)
			return
		}
		inv.Workspace.Status = strings.ToUpper(status)
		inv.Workspace.Icon = ""
		inv.Workspace.ActionsEnabled = false
		inv.Status = "INVITED"
		resp.Invites = append(resp.Invites, inv)
	}
	inviteRows.Close()

	writeJSON(w, http.StatusOK, resp)
}

// handleUpdateUser implements PUT /api/v1/users. Only the display name
// is mutable: the email is the identity key of the magic-link auth flow,
// and the username the client shows is derived from it, so neither is
// accepted as input. The refreshed account (with workspaces and invites)
// is returned by reusing handleGetUser so both endpoints stay in lockstep.
func (a *API) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var req struct {
		Fullname *string `json:"fullname"`
	}
	if err := jsonDecode(r, &req); err != nil || req.Fullname == nil {
		writeError(w, http.StatusBadRequest, "fullname is required")
		return
	}
	name := strings.TrimSpace(*req.Fullname)
	if name == "" || len(name) > 255 {
		writeError(w, http.StatusUnprocessableEntity, "fullname must be 1-255 chars")
		return
	}
	if _, err := a.pool.Exec(r.Context(),
		"update accounts set name = $2, version = version + 1, updated_at = now() where id = $1",
		p.AccountID, name); err != nil {
		a.internalError(w, err)
		return
	}
	a.handleGetUser(w, r)
}

// handleGetUsersByIds implements GET /api/v1/users?userIds=a,b,c —
// the client's bulk user lookup for rendering member/assignee names.
// It mirrors Tegon's getUsersbyId: the users' first (oldest-workspace)
// membership provides the user-level role.
func (a *API) handleGetUsersByIds(w http.ResponseWriter, r *http.Request, ids []string) {
	out := make([]publicUser, 0, len(ids))
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, out)
		return
	}
	rows, err := a.pool.Query(r.Context(), `
		select a.id, a.name, a.email, a.avatar_url,
		       coalesce((
		         select wm.role
		         from workspace_members wm
		         join workspaces w on w.id = wm.workspace_id
		         where wm.account_id = a.id and wm.status = 'active'
		         order by w.created_at
		         limit 1
		       ), 'member')
		from accounts a
		where a.id = any($1::uuid[])
		order by a.name`, ids)
	if err != nil {
		a.internalError(w, err)
		return
	}
	for rows.Next() {
		var u publicUser
		var avatar *string
		var role string
		if err := rows.Scan(&u.ID, &u.Fullname, &u.Email, &avatar, &role); err != nil {
			rows.Close()
			a.internalError(w, err)
			return
		}
		u.Username = strings.SplitN(u.Email, "@", 2)[0]
		u.Image = strval(avatar)
		u.Role = clientRole(role)
		out = append(out, u)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// parseUserIds splits a comma-separated userIds query parameter,
// dropping empty and malformed values. Malformed ids are excluded
// rather than rejected: Tegon's findMany by id simply ignores
// values it cannot match, and the client joins real uuids.
func parseUserIds(raw string) []string {
	var ids []string
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id != "" && isUUID(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// isUUID reports whether s is a canonical 8-4-4-4-12 UUID.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
