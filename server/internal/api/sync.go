// sync.go — the sync engine: bootstrap, delta, stream, and the wire
// builders for every model (docs/spec/product-spec.tex, ch. 5).
// spec cs:arch:sync
// spec cs:api:wire
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"
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
	// Stale marks the delta as incomplete: the client's watermark is
	// older than the outbox's retained window, so some records were
	// trimmed away before delivery. The client must fall back to a
	// full bootstrap to reconstruct its object set.
	Stale bool `json:"stale,omitempty"`
}

// handleSync serves both /sync_actions/bootstrap and /sync_actions/delta.
//
// Tenant rule: the workspaceId parameter must be a workspace the
// principal actively belongs to; otherwise the response is a plain 404
// (no existence leak). Records are serialized in the exact shapes the
// web client's stores expect.
//
// Bootstrap returns the full tenant object set; delta returns outbox
// records newer than the client's lastSequenceId watermark (true gap
// recovery for M2: everything changed while the client was away).
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

	// The server's current watermark for the workspace: the cursor
	// reported back to the client so it can advance past what was
	// delivered. Never echo the client's own (stale) cursor.
	var serverSeq int64
	if err := a.pool.QueryRow(r.Context(),
		"select coalesce((select last_sequence from sync_sequences where workspace_id = $1), 0)",
		workspaceID).Scan(&serverSeq); err != nil {
		serverSeq = 0
	}

	// Non-nil: the client contract is an array, and a Go nil slice
	// marshals as JSON null, which crashes the client's iteration.
	records := []syncActionRecord{}
	stale := false
	if strings.HasSuffix(r.URL.Path, "/delta") {
		// Delta: outbox records newer than the client's watermark, filtered
		// to the models it asked for. The client upserts/removes by model
		// id, so delivery is idempotent.
		afterSeq := serverSeq
		if v := q.Get("lastSequenceId"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				afterSeq = n
			}
		}
		var err error
		records, err = a.collectOutbox(r.Context(), workspaceID, afterSeq, q.Get("modelNames"))
		if err != nil {
			a.internalError(w, err)
			return
		}
		// The outbox is trimmed per workspace; if the client's cursor
		// predates the oldest retained record, the delta cannot be
		// complete. An empty outbox with a watermark behind it is stale
		// for the same reason, as is a zero cursor: a client that holds
		// no watermark cannot be satisfied by a forward-only delta, so it
		// must fall back to a full bootstrap.
		var oldest *int64
		if err := a.pool.QueryRow(r.Context(),
			"select min(sequence_id) from sync_outbox where workspace_id = $1",
			workspaceID).Scan(&oldest); err == nil {
			if oldest == nil || *oldest > afterSeq {
				stale = true
			}
		}
	} else {
		// Bootstrap: the client passes a comma-separated MODELS list.
		// Unknown or not-yet-shipped models simply yield no records.
		//
		// Collectors run concurrently: each is one independent indexed
		// query, so wall time is the slowest collector rather than the
		// sum of all of them (the sequential loop made a 9-model
		// bootstrap pay 9 serial round-trips). Record order is not
		// significant: the client upserts by model id and takes the
		// watermark from lastSequenceId.
		names := make([]string, 0, 8)
		seen := make(map[string]bool, 8)
		for _, name := range strings.Split(q.Get("modelNames"), ",") {
			name = strings.TrimSpace(name)
			if name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
		byModel := make([][]syncActionRecord, len(names))
		var g errgroup.Group
		for i, name := range names {
			g.Go(func() error {
				recs, err := a.collectModel(r.Context(), name, workspaceID)
				if err != nil {
					return err
				}
				byModel[i] = recs
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			a.internalError(w, err)
			return
		}
		for _, recs := range byModel {
			records = append(records, recs...)
		}
	}

	writeJSON(w, http.StatusOK, syncResponse{
		SyncActions:    records,
		LastSequenceID: strconv.FormatInt(serverSeq, 10),
		Stale:          stale,
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
	case "IssueRelation":
		return a.collectIssueRelations(ctx, workspaceID, emit)
	case "IssueHistory":
		return a.collectHistory(ctx, workspaceID, emit)
	case ModelSwarmActivity:
		return a.collectSwarmActivity(ctx, workspaceID, emit)
	case "View":
		return a.collectViews(ctx, workspaceID, emit)
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
	case "agent":
		return "AGENT"
	default:
		return "USER"
	}
}

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
// client Issue shape needs, including label and child id arrays and the
// denormalized relations (v1.1: the reader's-side edges, a JSON array).
const issueColumns = `
		i.id, i.team_id, i.number, i.priority, i.sort_order, i.title,
		i.description, i.status, i.created_at, i.updated_at,
		i.created_by, i.assignee_id, i.parent_id, i.status_id,
		i.agent_paused,
		coalesce((select array_agg(il.label_id) from issue_labels il where il.issue_id = i.id), '{}'),
		coalesce((select array_agg(c.id) from issues c where c.parent_id = i.id and c.status <> 'deleted'), '{}')` +
	relationListSQL

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
	// Version is the row's optimistic-concurrency anchor. Only the
	// runtime's work cycle scans it (the sync scans and the client
	// shape do not); every issue mutation bumps it, so equality is a
	// complete "nothing changed" test.
	Version int
}

// issueData serializes an issue row in the exact shape of the client's
// Issue model. Bootstrap and mutation responses share it so both speak
// the identical vocabulary. (stateId is a required string in the client
// model; an issue without a status serializes as empty, never null.)
func (a *API) issueData(r issueRow) map[string]any {
	// The client's v1 Issue model has no relations field (MST drops
	// it); the client-state rewrite (SWR-15) consumes it. Always an
	// array: a JSON null here would crash a strict model.
	relations := r.RelationRaw
	if len(relations) == 0 {
		relations = []byte(`[]`)
	}
	return map[string]any{
		"id":                 r.ID,
		"createdAt":          r.CreatedAt.Format(iso),
		"updatedAt":          r.UpdatedAt.Format(iso),
		"title":              r.Title,
		"number":             r.Number,
		"description":        descriptionForClient(r.DescRaw),
		"priority":           r.Priority,
		"dueDate":            nil,
		"sortOrder":          r.SortOrder,
		"estimate":           0,
		"teamId":             r.TeamID,
		"createdById":        nullOrEmpty(strval(r.CreatedByID)),
		"assigneeId":         nullOrEmpty(strval(r.AssigneeID)),
		"labelIds":           r.LabelIDs,
		"parentId":           nullOrEmpty(strval(r.ParentID)),
		"stateId":            strval(r.StatusID),
		"subscriberIds":      []string{},
		"cycleId":            nil,
		"projectId":          nil,
		"projectMilestoneId": nil,
		"sourceMetadata":     nil,
		"children":           r.Children,
		"agentPaused":        r.AgentPaused,
		"relations":          relations,
	}
}

// issueByID loads one issue (any status) for the mutation handlers.
func (a *API) issueByID(ctx context.Context, id string) (issueRow, error) {
	var r issueRow
	if err := a.pool.QueryRow(ctx, "select "+issueColumns+" from issues i where i.id = $1", id).Scan(
		&r.ID, &r.TeamID, &r.Number, &r.Priority, &r.SortOrder,
		&r.Title, &r.DescRaw, &r.Status, &r.CreatedAt, &r.UpdatedAt,
		&r.CreatedByID, &r.AssigneeID, &r.ParentID, &r.StatusID,
		&r.AgentPaused, &r.LabelIDs, &r.Children, &r.RelationRaw); err != nil {
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
			&r.AgentPaused, &r.LabelIDs, &r.Children, &r.RelationRaw); err != nil {
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

// historyData maps a generic activity/audit row to the client's
// IssueHistory shape (pure, so the wire contract is unit-testable;
// collectHistory feeds it the issue_history columns). Only
// status/assignee/priority/labels transitions are user-visible in v1;
// summary carries the handoff note (D1) — null on every other row.
func historyData(id string, createdAt, updatedAt time.Time, actorID *string, issueID, action, field string, from, to, summary *string) map[string]any {
	// Every from/to field is union(..., null) without undefined in the
	// client model, so all of them must be present (null when unset).
	data := map[string]any{
		"id":        id,
		"createdAt": createdAt.Format(iso),
		"updatedAt": updatedAt.Format(iso),
		"userId":    nullOrEmpty(strval(actorID)),
		"issueId":   nullOrEmpty(issueID),
		// D1: the client history model needs the action to tell a
		// handoff from a pause (both carry a summary).
		"action":          action,
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
		"summary":         nullOrEmpty(strval(summary)),
	}
	switch field {
	case "status":
		data["fromStateId"] = nullOrEmpty(strval(from))
		data["toStateId"] = nullOrEmpty(strval(to))
	case "labels":
		// from/to carry JSON arrays of label ids.
		if f := strval(from); f != "" {
			var arr []string
			if json.Unmarshal([]byte(f), &arr) == nil {
				data["removedLabelIds"] = arr
			}
		}
		if t := strval(to); t != "" {
			var arr []string
			if json.Unmarshal([]byte(t), &arr) == nil {
				data["addedLabelIds"] = arr
			}
		}
	case "parent":
		data["fromParentId"] = nullOrEmpty(strval(from))
		data["toParentId"] = nullOrEmpty(strval(to))
	case "relation", "relation_deleted":
		// The timeline's RelatedActivity entry: the client renders
		// from relationChanges (added vs removed) with the related
		// issue's identifier.
		data["relationChanges"] = map[string]any{
			"isDeleted":      action == "relation_deleted",
			"issueId":        nullOrEmpty(issueID),
			"relatedIssueId": nullOrEmpty(strval(from)),
			"type":           strval(to),
		}
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
	return data
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

func strval(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// wireAction maps the internal outbox action to the client's
// SyncActionRecord action vocabulary (const enum Action = 'I'|'U'|'D',
// data-loader.ts). The client's save-data handlers switch on exactly
// these values; anything else is silently dropped, which would make
// mutations invisible in the UI. The database keeps the descriptive
// CREATE/UPDATE/DELETE names for operators.
func wireAction(action string) string {
	switch action {
	case "CREATE":
		return "I"
	case "UPDATE":
		return "U"
	case "DELETE":
		return "D"
	}
	return action
}

// collectOutbox serves the delta endpoint: outbox records for the
// workspace newer than afterSeq, restricted to the requested models.
func (a *API) collectOutbox(ctx context.Context, workspaceID string, afterSeq int64, modelNames string) ([]syncActionRecord, error) {
	// An empty model list means "no filter" (every record); a non-empty
	// list restricts delivery to the requested models.
	allowed := make(map[string]bool)
	for _, name := range strings.Split(modelNames, ",") {
		if name = strings.TrimSpace(name); name != "" {
			allowed[name] = true
		}
	}
	rows, err := a.pool.Query(ctx, `
		select sequence_id, model_name, model_id, action, data
		from sync_outbox
		where workspace_id = $1 and sequence_id > $2
		order by sequence_id`, workspaceID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Non-nil: a Go nil slice marshals as JSON null, and the client
	// contract (SyncActionRecord[]) is an array — null crashes its
	// iteration in saveSocketData.
	out := []syncActionRecord{}
	for rows.Next() {
		var (
			seqNum        int64
			modelID       string
			model, action string
			data          json.RawMessage
		)
		if err := rows.Scan(&seqNum, &model, &modelID, &action, &data); err != nil {
			return nil, err
		}
		if len(allowed) > 0 && !allowed[model] {
			continue
		}
		out = append(out, syncActionRecord{
			Data:        data,
			ModelName:   model,
			ModelID:     strval(&modelID),
			Action:      wireAction(action),
			WorkspaceID: workspaceID,
			SequenceID:  strconv.FormatInt(seqNum, 10),
		})
	}
	return out, rows.Err()
}

// auditTx appends a workspace audit row inside the caller's transaction
// (docs/spec/07 audit taxonomy: team lifecycle, membership and invite
// changes, suspension). objectID may be empty for row-less events.
func (a *API) auditTx(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action, objectType, objectID string) error {
	_, err := tx.Exec(ctx, `
		insert into audit_events (workspace_id, actor_id, action, object_type, object_id)
		values ($1, $2, $3, $4, $5)`,
		workspaceID, actorID, action, objectType, nullForEmpty(&objectID))
	return err
}

// claimSequenceTx claims the next sync sequence for the workspace
// inside the caller's transaction: one row per workspace, updated
// atomically. Both outbox writers (emitChange for mutations,
// emitOutboxDirect for swarm signals) claim through here so the
// "one sequence per record" rule has a single home.
func (a *API) claimSequenceTx(ctx context.Context, tx pgx.Tx, workspaceID string) (int64, error) {
	var seq int64
	err := tx.QueryRow(ctx, `
		insert into sync_sequences (workspace_id, last_sequence)
		values ($1, 1)
		on conflict (workspace_id)
		do update set last_sequence = sync_sequences.last_sequence + 1
		returning last_sequence`, workspaceID).Scan(&seq)
	return seq, err
}

// emitChange claims the next sync sequence for the workspace, writes the
// outbox row inside the caller's transaction, and returns the wire record
// to broadcast after commit.
func (a *API) emitChange(ctx context.Context, tx pgx.Tx, workspaceID, model, modelID, action string, data map[string]any) (syncActionRecord, error) {
	seq, err := a.claimSequenceTx(ctx, tx, workspaceID)
	if err != nil {
		return syncActionRecord{}, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return syncActionRecord{}, err
	}
	rec := syncActionRecord{
		Data:        raw,
		ModelName:   model,
		ModelID:     modelID,
		Action:      wireAction(action),
		WorkspaceID: workspaceID,
		SequenceID:  strconv.FormatInt(seq, 10),
	}
	// model_id is a *string so an empty id binds as SQL NULL: pgx sends
	// Go strings as unknown type, and a bare parameter infers the target
	// uuid type, while nullif() would pin it to text (no implicit
	// text->uuid cast exists).
	if _, err := tx.Exec(ctx, `
		insert into sync_outbox (workspace_id, sequence_id, model_name, model_id, action, data)
		values ($1, $2, $3, $4, $5, $6)`,
		workspaceID, seq, model, nullForEmpty(&modelID), action, raw); err != nil {
		return syncActionRecord{}, err
	}
	return rec, nil
}

// broadcastRecord publishes a committed record to the workspace's realtime
// subscribers. Best-effort: realtime is a hint, the delta endpoint is
// authoritative (spec R-8 / doc 08).
func (a *API) broadcastRecord(rec syncActionRecord) {
	raw, err := json.Marshal(rec)
	if err != nil {
		return
	}
	n := a.bcast.Publish(rec.WorkspaceID, raw)
	a.log.Debug("realtime publish", "workspace", rec.WorkspaceID,
		"model", rec.ModelName, "action", rec.Action, "delivered", n)
}
