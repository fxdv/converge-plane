package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// syncActionRecord matches the web client's SyncActionRecord.
type syncActionRecord struct {
	Data        json.RawMessage `json:"data"`
	ModelName   string          `json:"modelName"`
	ModelID     string          `json:"modelId"`
	Action      string          `json:"action"`
	WorkspaceID string          `json:"workspaceId"`
	SequenceID  string          `json:"sequenceId"`
}

// syncResponse matches the web client's BootstrapResponse.
type syncResponse struct {
	SyncActions    []syncActionRecord `json:"syncActions"`
	LastSequenceID string             `json:"lastSequenceId"`
}

// handleSync serves both /sync_actions/bootstrap and /sync_actions/delta.
//
// Tenant rule: the workspaceId parameter must be a workspace the
// principal actively belongs to; otherwise the response is a plain 404
// (no existence leak). Records are serialized in the exact shapes the
// web client's stores expect.
//
// For the v1 demo the delta returns the same record set as bootstrap:
// the client upserts by model id, so re-delivery is idempotent. A
// proper change-feed builds on the same endpoint in a later milestone.
func (a *API) handleSync(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	q := r.URL.Query()
	workspaceID := q.Get("workspaceId")
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspaceId is required")
		return
	}
	if _, ok := a.workspaceRole(r.Context(), p, workspaceID); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var lastSeq int64
	if err := a.pool.QueryRow(r.Context(),
		"select coalesce((select last_sequence from sync_sequences where workspace_id = $1), 0)",
		workspaceID).Scan(&lastSeq); err != nil {
		lastSeq = 0
	}

	// The client passes a comma-separated MODELS list. Unknown or not-yet
	// shipped models simply yield no records.
	var records []syncActionRecord
	for _, name := range strings.Split(q.Get("modelNames"), ",") {
		recs, err := a.collectModel(r.Context(), strings.TrimSpace(name), workspaceID)
		if err != nil {
			a.internalError(w, err)
			return
		}
		records = append(records, recs...)
	}

	writeJSON(w, http.StatusOK, syncResponse{
		SyncActions:    records,
		LastSequenceID: strconv.FormatInt(lastSeq, 10),
	})
}

// collectModel serializes one model for a tenant-scoped workspace.
func (a *API) collectModel(ctx context.Context, model, workspaceID string) ([]syncActionRecord, error) {
	seq := 0
	emit := func(id string, data any) (syncActionRecord, error) {
		seq++
		raw, err := json.Marshal(data)
		if err != nil {
			return syncActionRecord{}, err
		}
		return syncActionRecord{
			Data:        raw,
			ModelName:   model,
			ModelID:     id,
			Action:      "I",
			WorkspaceID: workspaceID,
			SequenceID:  strconv.Itoa(seq),
		}, nil
	}

	switch model {
	case "Workspace":
		return a.collectWorkspace(ctx, workspaceID, emit)
	case "UsersOnWorkspaces":
		return a.collectMembers(ctx, workspaceID, emit)
	case "Team":
		return a.collectTeams(ctx, workspaceID, emit)
	case "Workflow":
		return a.collectWorkflows(ctx, workspaceID, emit)
	case "Label":
		return a.collectLabels(ctx, workspaceID, emit)
	case "Issue":
		return a.collectIssues(ctx, workspaceID, emit)
	case "IssueComment":
		return a.collectComments(ctx, workspaceID, emit)
	case "IssueHistory":
		return a.collectHistory(ctx, workspaceID, emit)
	default:
		return nil, nil
	}
}

// emitFn is the record emitter bound to one model + workspace.
type emitFn func(id string, data any) (syncActionRecord, error)

const iso = time.RFC3339

// clientRole maps internal workspace roles to the client's role enum.
// The client model accepts only ADMIN | USER | BOT | AGENT (mirroring the
// backend Role enum); "owner" is a backend-only concept that serializes
// as ADMIN. Sending anything else (e.g. "OWNER") crashes the
// mobx-state-tree validation in the browser.
func clientRole(role string) string {
	switch strings.ToLower(role) {
	case "owner", "admin":
		return "ADMIN"
	default:
		return "USER"
	}
}

func (a *API) collectWorkspace(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	var (
		id, slug, name string
		createdAt      time.Time
		updatedAt      time.Time
	)
	err := a.pool.QueryRow(ctx,
		"select id, slug, name, created_at, updated_at from workspaces where id = $1",
		workspaceID).Scan(&id, &slug, &name, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	rec, err := emit(id, map[string]any{
		"id":             id,
		"slug":           slug,
		"name":           name,
		"createdAt":      createdAt.Format(iso),
		"updatedAt":      updatedAt.Format(iso),
		"actionsEnabled": false,
	})
	if err != nil {
		return nil, err
	}
	return []syncActionRecord{rec}, nil
}

func (a *API) collectMembers(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `
		select wm.id, wm.created_at, wm.updated_at, wm.role, wm.status,
		       wm.account_id, wm.workspace_id,
		       coalesce((select array_agg(tm.team_id) from team_members tm
		                 where tm.account_id = wm.account_id), '{}')
		from workspace_members wm
		where wm.workspace_id = $1 and wm.status = 'active'
		order by wm.created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []syncActionRecord
	for rows.Next() {
		var (
			id, role, status, accountID, workspaceIDRow string
			createdAt, updatedAt                        time.Time
			teamIDs                                     []string
		)
		if err := rows.Scan(&id, &createdAt, &updatedAt, &role, &status,
			&accountID, &workspaceIDRow, &teamIDs); err != nil {
			return nil, err
		}
		rec, err := emit(id, map[string]any{
			"id":          id,
			"createdAt":   createdAt.Format(iso),
			"updatedAt":   updatedAt.Format(iso),
			"role":        clientRole(role),
			"status":      strings.ToUpper(status),
			"userId":      accountID,
			"workspaceId": workspaceIDRow,
			"teamIds":     teamIDs,
			"settings":    map[string]any{},
		})
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (a *API) collectTeams(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `
		select id, created_at, updated_at, name, identifier, workspace_id
		from teams where workspace_id = $1 and status = 'active'
		order by position, created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []syncActionRecord
	for rows.Next() {
		var (
			id, name, identifier, wsID string
			createdAt, updatedAt       time.Time
		)
		if err := rows.Scan(&id, &createdAt, &updatedAt, &name, &identifier, &wsID); err != nil {
			return nil, err
		}
		rec, err := emit(id, map[string]any{
			"id":           id,
			"createdAt":    createdAt.Format(iso),
			"updatedAt":    updatedAt.Format(iso),
			"name":         name,
			"identifier":   identifier,
			"workspaceId":  wsID,
			"currentCycle": nil,
			"preferences":  map[string]any{"teamType": "engineering", "cyclesEnabled": false},
		})
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (a *API) collectWorkflows(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `
		select ws.id, ws.created_at, ws.updated_at, ws.name, ws.position, ws.color, ws.category, ws.team_id
		from workflow_statuses ws
		join teams t on t.id = ws.team_id
		where t.workspace_id = $1 and ws.status = 'active'
		order by ws.team_id, ws.position`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []syncActionRecord
	for rows.Next() {
		var (
			id, name, color, category, teamID string
			position                          int
			createdAt, updatedAt              time.Time
		)
		if err := rows.Scan(&id, &createdAt, &updatedAt, &name, &position, &color, &category, &teamID); err != nil {
			return nil, err
		}
		rec, err := emit(id, map[string]any{
			"id":          id,
			"createdAt":   createdAt.Format(iso),
			"updatedAt":   updatedAt.Format(iso),
			"name":        name,
			"position":    position,
			"description": "",
			"color":       color,
			"category":    category,
			"teamId":      teamID,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (a *API) collectLabels(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `
		select id, created_at, updated_at, name, color, workspace_id
		from labels where workspace_id = $1 and status = 'active'
		order by created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []syncActionRecord
	for rows.Next() {
		var (
			id, name, color, wsID string
			createdAt, updatedAt  time.Time
		)
		if err := rows.Scan(&id, &createdAt, &updatedAt, &name, &color, &wsID); err != nil {
			return nil, err
		}
		rec, err := emit(id, map[string]any{
			"id":          id,
			"createdAt":   createdAt.Format(iso),
			"updatedAt":   updatedAt.Format(iso),
			"name":        name,
			"color":       color,
			"description": "",
			"workspaceId": wsID,
			"teamId":      nil,
			"groupId":     nil,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// descriptionToText projects the stored rich-text document (a JSON string
// scalar or an object with a "plain" field) to the plain string the client
// renders in issue fields.
func descriptionToText(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err == nil {
		return s
	}
	var obj struct {
		Plain string `json:"plain"`
	}
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		return obj.Plain
	}
	return ""
}

func (a *API) collectIssues(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `
		select i.id, i.created_at, i.updated_at, i.title, i.number,
		       i.description, i.priority, i.sort_order, i.team_id,
		       i.created_by, i.assignee_id, i.parent_id, i.status_id, i.status,
		       coalesce((select array_agg(il.label_id) from issue_labels il where il.issue_id = i.id), '{}')
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
		var (
			id, title, descRaw                          string
			createdAt, updatedAt                        time.Time
			number, priority, sortOrder                 int
			teamID                                      string
			createdByID, assigneeID, parentID, statusID *string
			status                                      string
			labelIDs                                    []string
		)
		if err := rows.Scan(&id, &createdAt, &updatedAt, &title, &number,
			&descRaw, &priority, &sortOrder, &teamID, &createdByID,
			&assigneeID, &parentID, &statusID, &status, &labelIDs); err != nil {
			return nil, err
		}
		desc := ""
		if descRaw != "" && descRaw != "null" {
			desc = descriptionToText(descRaw)
		}
		rec, err := emit(id, map[string]any{
			"id":          id,
			"createdAt":   createdAt.Format(iso),
			"updatedAt":   updatedAt.Format(iso),
			"title":       title,
			"number":      number,
			"description": desc,
			"priority":    priority,
			"dueDate":     nil,
			"sortOrder":   sortOrder,
			"estimate":    0,
			"teamId":      teamID,
			"createdById": nullOrEmpty(strval(createdByID)),
			"assigneeId":  nullOrEmpty(strval(assigneeID)),
			"labelIds":    labelIDs,
			"parentId":    nullOrEmpty(strval(parentID)),
			// stateId is a required string in the client model; an issue
			// without a status serializes as empty rather than null.
			"stateId":            strval(statusID),
			"subscriberIds":      []string{},
			"cycleId":            nil,
			"projectId":          nil,
			"projectMilestoneId": nil,
			"sourceMetadata":     nil,
			"children":           []string{},
		})
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func nullOrEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
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
		body := ""
		if bodyRaw != "" && bodyRaw != "null" {
			body = descriptionToText(bodyRaw)
		}
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

// collectHistory maps the activity/audit rows to the client's
// IssueHistory shape. Only status/assignee/priority transitions are
// user-visible in v1.
func (a *API) collectHistory(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `
		-- issue_history rows are append-only: created_at doubles as updated_at.
		select h.id, h.created_at, h.created_at, h.actor_id, h.issue_id,
		       h.action, h.field, h.from_value, h.to_value
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
			from, to                   *string
			createdAt, updatedAt       time.Time
		)
		if err := rows.Scan(&id, &createdAt, &updatedAt, &actorID, &issueID,
			&action, &field, &from, &to); err != nil {
			return nil, err
		}

		// Every from/to field is union(..., null) without undefined in the
		// client model, so all of them must be present (null when unset).
		data := map[string]any{
			"id":              id,
			"createdAt":       createdAt.Format(iso),
			"updatedAt":       updatedAt.Format(iso),
			"userId":          nullOrEmpty(strval(actorID)),
			"issueId":         nullOrEmpty(issueID),
			"addedLabelIds":   []string{},
			"removedLabelIds": []string{},
			"fromPriority":    nil,
			"toPriority":      nil,
			"fromStateId":     nil,
			"toStateId":       nil,
			"fromEstimate":    nil,
			"toEstimate":      nil,
			"fromAssigneeId":  nil,
			"toAssigneeId":    nil,
			"fromParentId":    nil,
			"toParentId":      nil,
			"relationChanges": nil,
			"sourceMetadata":  nil,
		}
		switch field {
		case "status":
			data["fromStateId"] = nullOrEmpty(strval(from))
			data["toStateId"] = nullOrEmpty(strval(to))
		case "assignee":
			data["fromAssigneeId"] = nullOrEmpty(strval(from))
			data["toAssigneeId"] = nullOrEmpty(strval(to))
		case "priority":
			if f := strval(from); f != "" {
				data["fromPriority"], _ = strconv.Atoi(f)
			}
			if t := strval(to); t != "" {
				data["toPriority"], _ = strconv.Atoi(t)
			}
		}
		if action == "created" {
			data["toStateId"] = nullOrEmpty(strval(to))
		}

		rec, err := emit(id, data)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func strval(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var _ = fmt.Sprintf
