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

// handleGetUser implements GET /api/v1/users — the client's "who am I"
// call, returning the account with its workspaces and pending invites.
func (a *API) handleGetUser(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
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
	}

	rows, err := a.pool.Query(ctx, `
		select w.id, w.name, w.slug, w.status, wm.role
		from workspaces w
		join workspace_members wm on wm.workspace_id = w.id
		where wm.account_id = $1 and wm.status = 'active'
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
		select i.id, i.workspace_id, w.name, w.slug, w.status
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
		if err := inviteRows.Scan(&inv.ID, &inv.WorkspaceID, &inv.Workspace.Name, &inv.Workspace.Slug, &status); err != nil {
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
