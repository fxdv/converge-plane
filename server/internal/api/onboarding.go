package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// onboardingRequest matches the web client's CreateInitialResourcesDto.
type onboardingRequest struct {
	Fullname       string `json:"fullname"`
	WorkspaceName  string `json:"workspaceName"`
	TeamName       string `json:"teamName"`
	TeamIdentifier string `json:"teamIdentifier"`
}

// workspaceDTO matches the web client's WorkspaceType.
type workspaceDTO struct {
	ID             string    `json:"id"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Slug           string    `json:"slug"`
	Name           string    `json:"name"`
	ActionsEnabled bool      `json:"actionsEnabled"`
}

var slugInvalid = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns a display name into a URL-safe workspace slug.
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugInvalid.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "workspace"
	}
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

// handleOnboarding implements POST /api/v1/workspaces/onboarding:
// create the workspace, admit the principal as Owner, create the first
// team, seed the default workflow statuses and labels — atomically.
func (a *API) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	var req onboardingRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	req.WorkspaceName = strings.TrimSpace(req.WorkspaceName)
	req.TeamName = strings.TrimSpace(req.TeamName)
	req.TeamIdentifier = strings.ToUpper(strings.TrimSpace(req.TeamIdentifier))
	if len(req.WorkspaceName) < 3 || len(req.TeamName) < 1 || len(req.TeamIdentifier) < 1 {
		writeError(w, http.StatusUnprocessableEntity, "workspace name, team name and team identifier are required")
		return
	}
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if req.Fullname != "" {
		if _, err := a.pool.Exec(r.Context(),
			"update accounts set name = $1 where id = $2", req.Fullname, p.AccountID); err != nil {
			a.internalError(w, err)
			return
		}
	}
	ctx := r.Context()

	slug := slugify(req.WorkspaceName)
	if err := a.uniqueWorkspaceSlug(ctx, &slug); err != nil {
		a.internalError(w, err)
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var ws workspaceDTO
	err = tx.QueryRow(ctx, `
		insert into workspaces (name, slug, created_by)
		values ($1, $2, $3)
		returning id, created_at, updated_at, slug, name`,
		req.WorkspaceName, slug, p.AccountID).
		Scan(&ws.ID, &ws.CreatedAt, &ws.UpdatedAt, &ws.Slug, &ws.Name)
	if err != nil {
		a.internalError(w, err)
		return
	}
	ws.ActionsEnabled = false

	if _, err := tx.Exec(ctx, `
		insert into workspace_members (workspace_id, account_id, role, status, joined_at)
		values ($1, $2, 'owner', 'active', now())`,
		ws.ID, p.AccountID); err != nil {
		a.internalError(w, err)
		return
	}

	var teamID string
	err = tx.QueryRow(ctx, `
		insert into teams (workspace_id, name, identifier)
		values ($1, $2, $3)
		returning id`, ws.ID, req.TeamName, req.TeamIdentifier).Scan(&teamID)
	if err != nil {
		a.internalError(w, err)
		return
	}

	if _, err := tx.Exec(ctx, `
		insert into team_members (team_id, account_id, role)
		values ($1, $2, 'manager')`, teamID, p.AccountID); err != nil {
		a.internalError(w, err)
		return
	}

	statuses := []struct {
		name, category, color string
	}{
		{"Backlog", "BACKLOG", "#8d8d8d"},
		{"To Do", "UNSTARTED", "#8884d8"},
		{"In Progress", "STARTED", "#4484d5"},
		{"Done", "COMPLETED", "#36b37e"},
		{"Canceled", "CANCELED", "#cf222e"},
	}
	for i, st := range statuses {
		if _, err := tx.Exec(ctx, `
			insert into workflow_statuses (team_id, name, category, color, position)
			values ($1, $2, $3, $4, $5)`,
			teamID, st.name, st.category, st.color, i); err != nil {
			a.internalError(w, err)
			return
		}
	}

	labels := []struct {
		name, color string
	}{
		{"Bug", "#cf222e"},
		{"Feature", "#8884d8"},
		{"Improvement", "#4484d5"},
	}
	for _, l := range labels {
		if _, err := tx.Exec(ctx, `
			insert into labels (workspace_id, name, color)
			values ($1, $2, $3)
			on conflict (workspace_id, name) do nothing`,
			ws.ID, l.name, l.color); err != nil {
			a.internalError(w, err)
			return
		}
	}

	// Every issue created from here on belongs to a numbered team.
	if _, err := tx.Exec(ctx,
		"insert into issue_counters (team_id) values ($1)", teamID); err != nil {
		a.internalError(w, err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

// uniqueWorkspaceSlug appends a numeric suffix until the slug is free.
func (a *API) uniqueWorkspaceSlug(ctx context.Context, slug *string) error {
	base := *slug
	for i := 2; ; i++ {
		var exists bool
		err := a.pool.QueryRow(ctx,
			"select exists(select 1 from workspaces where slug = $1)", *slug).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		*slug = fmt.Sprintf("%s-%d", base, i)
		if i > 100 {
			return fmt.Errorf("could not allocate a unique workspace slug")
		}
	}
}

// decodeJSON decodes the request body into v.
func decodeJSON(r *http.Request, v any) error {
	return jsonDecode(r, v)
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}
