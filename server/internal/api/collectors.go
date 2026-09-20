// collectors.go — the sync engine's per-model bootstrap collectors
// (docs/spec/product-spec.tex, ch. 5): one function per model, each
// streaming the tenant's rows through the shared emit (one outbox
// record per row). Dispatch is sync.go's collectModel; the wire shapes
// the rows serialize into are built in wire_builders.go.
// spec cs:arch:sync

package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

func (a *API) collectWorkspace(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	var r workspaceRow
	if err := a.pool.QueryRow(ctx,
		"select id, slug, name, created_at, updated_at from workspaces where id = $1",
		workspaceID).Scan(&r.ID, &r.Slug, &r.Name, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	rec, err := emit(r.ID, a.workspaceData(r))
	if err != nil {
		return nil, err
	}
	return []syncActionRecord{rec}, nil
}

func (a *API) collectMembers(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	// Every lifecycle state (active, invited, suspended): the client's
	// members UI renders suspended and invited rows in their own
	// sections, and a status-filtered sync would make them invisible.
	rows, err := a.pool.Query(ctx, `select `+memberColumns+`
		from workspace_members wm
		where wm.workspace_id = $1
		order by wm.created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []syncActionRecord{}
	for rows.Next() {
		var r memberRow
		if err := scanMemberRow(rows, &r); err != nil {
			return nil, err
		}
		rec, err := emit(r.ID, a.memberData(r))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (a *API) collectTeams(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `select `+teamColumns+`
		from teams where workspace_id = $1 and status = 'active'
		order by position, created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []syncActionRecord{}
	for rows.Next() {
		var r teamRow
		if err := scanTeamRow(rows, &r); err != nil {
			return nil, err
		}
		rec, err := emit(r.ID, a.teamData(r))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (a *API) collectWorkflows(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	// No team-status filter on purpose: archived teams keep their
	// workflow statuses in sync because existing issues still resolve
	// their status name against them (same rule as collectIssues).
	// Qualified: the teams join brings its own id/name/timestamp columns
	// into scope, so every select must be table-qualified.
	rows, err := a.pool.Query(ctx, `
		select ws.id, ws.name, ws.description, ws.position, ws.color,
		       ws.category, ws.team_id, ws.created_at, ws.updated_at
		from workflow_statuses ws
		join teams t on t.id = ws.team_id
		where t.workspace_id = $1 and ws.status = 'active'
		order by ws.team_id, ws.position`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []syncActionRecord{}
	for rows.Next() {
		var r workflowRow
		if err := scanWorkflowRow(rows, &r); err != nil {
			return nil, err
		}
		rec, err := emit(r.ID, a.workflowData(r))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (a *API) collectLabels(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `select `+labelColumns+`
		from labels where workspace_id = $1 and status = 'active'
		order by created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []syncActionRecord{}
	for rows.Next() {
		var r labelRow
		if err := scanLabelRow(rows, &r); err != nil {
			return nil, err
		}
		rec, err := emit(r.ID, a.labelData(r))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// descriptionForClient projects the stored rich-text jsonb into the
// string the client's editor consumes. The editor does JSON.parse(value)
// and uses parsed.json ?? parsed, falling back to the raw string — so
// the stored document must come back exactly as it went in:
//   - any JSON object passes through verbatim. The client sends both the
//     {"json":...,"text":...} wrapper and bare ProseMirror docs
//     ({"type":"doc",...}); the editor consumes either form;
//   - a JSON string scalar (plain text, how toJSONB stores non-JSON input)
//     is unquoted back to its text;
//   - anything else (null, numbers) serializes as empty.
func descriptionForClient(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err == nil {
		return s
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err == nil {
		switch v.(type) {
		case map[string]any, []any:
			return raw
		}
	}
	return ""
}

// issueColumns is the shared SELECT list for issue rows: everything the
// client Issue shape needs, including label and child id arrays, the
// project membership, and the denormalized relations (v1.1: the
// reader's-side edges, a JSON array).
const issueColumns = `
		i.id, i.team_id, i.number, i.priority, i.sort_order, i.title,
		i.description, i.status, i.created_at, i.updated_at,
		i.created_by, i.assignee_id, i.parent_id, i.status_id,
		i.agent_paused, i.project_ids,
		coalesce((select array_agg(il.label_id) from issue_labels il where il.issue_id = i.id), '{}'),
		coalesce((select array_agg(c.id) from issues c where c.parent_id = i.id and c.status <> 'deleted'), '{}'),
	` + relationListSQL

// issueRow is one issues-table row with everything the client shape
// needs, shared by the sync collectors and the mutation handlers.
type issueRow struct {
	ID, TeamID              string
	Number, SortOrder       int
	Priority                *int // nullable: the client domain is number | null
	Title, DescRaw          string
	Status                  string
	CreatedAt, UpdatedAt    time.Time
	CreatedByID, AssigneeID *string
	ParentID, StatusID      *string
	LabelIDs, Children      []string
	// RelationRaw is the denormalized relations JSON (the SQL
	// coalesce guarantees an array; zero values in tests are
	// normalized to [] by issueData).
	RelationRaw json.RawMessage
	// AgentPaused is the D1 escalation flag: agents may not act on a
	// paused issue, humans act freely and resume it.
	AgentPaused bool
	// ProjectIds is the v1.1 project membership: at most one project
	// (the table constraint is the authority; the API enforces it for a
	// clean 422). Empty when the issue is not in a project.
	ProjectIds []string
	// Version is the row's optimistic-concurrency anchor. Only the
	// runtime's work cycle scans it (the sync scans and the client
	// shape do not); every issue mutation bumps it, so equality is a
	// complete "nothing changed" test.
	Version int
}

// issueByID loads one issue (any status) for the mutation handlers.
func (a *API) issueByID(ctx context.Context, id string) (issueRow, error) {
	var r issueRow
	if err := a.pool.QueryRow(ctx, "select "+issueColumns+" from issues i where i.id = $1", id).Scan(
		&r.ID, &r.TeamID, &r.Number, &r.Priority, &r.SortOrder,
		&r.Title, &r.DescRaw, &r.Status, &r.CreatedAt, &r.UpdatedAt,
		&r.CreatedByID, &r.AssigneeID, &r.ParentID, &r.StatusID,
		&r.AgentPaused, &r.ProjectIds, &r.LabelIDs, &r.Children, &r.RelationRaw); err != nil {
		return r, err
	}
	return r, nil
}

func (a *API) collectIssues(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, "select "+issueColumns+`
		from issues i
		join teams t on t.id = i.team_id
		where t.workspace_id = $1 and i.status <> 'deleted'
		order by i.created_at, i.number`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []syncActionRecord
	for rows.Next() {
		var r issueRow
		if err := rows.Scan(&r.ID, &r.TeamID, &r.Number, &r.Priority, &r.SortOrder,
			&r.Title, &r.DescRaw, &r.Status, &r.CreatedAt, &r.UpdatedAt,
			&r.CreatedByID, &r.AssigneeID, &r.ParentID, &r.StatusID,
			&r.AgentPaused, &r.ProjectIds, &r.LabelIDs, &r.Children, &r.RelationRaw); err != nil {
			return nil, err
		}
		rec, err := emit(r.ID, a.issueData(r))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (a *API) collectComments(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `
		select c.id, c.created_at, c.updated_at, c.body, c.author_id, c.issue_id, c.parent_id
		from comments c
		join issues i on i.id = c.issue_id
		join teams t on t.id = i.team_id
		where t.workspace_id = $1 and c.status = 'active'
		order by c.created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []syncActionRecord
	for rows.Next() {
		var (
			id, bodyRaw, authorID, issueID string
			parentID                       *string
			createdAt, updatedAt           time.Time
		)
		if err := rows.Scan(&id, &createdAt, &updatedAt, &bodyRaw, &authorID, &issueID, &parentID); err != nil {
			return nil, err
		}
		body := descriptionForClient(bodyRaw)
		// sourceMetadata must be present (null): the client model's field is
		// union(string, null) without undefined, so a missing key would fail
		// mobx-state-tree validation.
		rec, err := emit(id, map[string]any{
			"id":             id,
			"createdAt":      createdAt.Format(iso),
			"updatedAt":      updatedAt.Format(iso),
			"body":           body,
			"userId":         authorID,
			"issueId":        issueID,
			"parentId":       nullOrEmpty(strval(parentID)),
			"sourceMetadata": nil,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// collectArtifacts maps the workspace's live documents (SWR-56, the
// swarm's artifact channel) to the client's IssueArtifact shape. Like
// comments: active rows only — a deleted document reconciles away on
// the client (the live-set prune), it is never replayed.
func (a *API) collectArtifacts(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `
		select a.id, a.created_at, a.updated_at, a.title, a.body, a.author_id, a.issue_id
		from issue_artifacts a
		join issues i on i.id = a.issue_id
		join teams t on t.id = i.team_id
		where t.workspace_id = $1 and a.status = 'active'
		order by a.created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []syncActionRecord
	for rows.Next() {
		var (
			id, title, body, authorID, issueID string
			createdAt, updatedAt               time.Time
		)
		if err := rows.Scan(&id, &createdAt, &updatedAt, &title, &body, &authorID, &issueID); err != nil {
			return nil, err
		}
		rec, err := emit(id, artifactData(id, title, body, authorID, issueID, createdAt, updatedAt))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// collectHistory maps the activity/audit rows to the client's
// IssueHistory shape (see historyData).
func (a *API) collectHistory(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `
		-- issue_history rows are append-only: created_at doubles as updated_at.
		select h.id, h.created_at, h.created_at, h.actor_id, h.issue_id,
		       h.action, h.field, h.from_value, h.to_value, h.summary
		from issue_history h
		where h.workspace_id = $1
		order by h.created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []syncActionRecord
	for rows.Next() {
		var (
			id, issueID, action, field string
			actorID                    *string
			from, to, summary          *string
			createdAt, updatedAt       time.Time
		)
		if err := rows.Scan(&id, &createdAt, &updatedAt, &actorID, &issueID,
			&action, &field, &from, &to, &summary); err != nil {
			return nil, err
		}

		data := historyData(id, createdAt, updatedAt, actorID, issueID, action, field, from, to, summary)

		rec, err := emit(id, data)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
