// Issue relations (v1.1) — first-class directed edges between issues.
// spec cs:api:relations
// spec cs:api:wire
//
// The client contract (the law): creation rides the issue update
// (POST /api/v1/issues/{id} with an issueRelation field — the related
// picker in the issue side panel), deletion is
// DELETE /api/v1/issue_relation/{id}, and the read surfaces are the
// denormalized relations array on the Issue record plus one
// issue_history row per change (action relation / relation_deleted,
// the client renders it as a RelatedActivity timeline entry).
//
// The edge is stored from the actor's perspective and rewritten on
// read from the reader's side (BLOCKS <-> BLOCKED, DUPLICATE <->
// DUPLICATE_OF; RELATED and SIMILAR are symmetric). PARENT and
// SUB_ISSUE are client enum members that never reach this file:
// hierarchy rides on issues.parent_id, the single source of truth.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// The stored vocabulary: six directed relation types.
const (
	relBlocks      = "BLOCKS"
	relBlocked     = "BLOCKED"
	relRelated     = "RELATED"
	relDuplicate   = "DUPLICATE"
	relDuplicateOf = "DUPLICATE_OF"
	relSimilar     = "SIMILAR"
)

// relationDepthCap bounds the blocks-cycle walk (spec api:relations:
// 64 hops is far beyond any real board; the cap makes a pathological
// graph a no-op instead of a spin).
const relationDepthCap = 64

var (
	errRelationExists = errors.New("relation already exists")
	errRelationCycle  = errors.New("blocks cycle")
)

// relationTypeAllowed validates the client's type literal. PARENT and
// SUB_ISSUE are rejected with a pointer at parentId: accepting them
// here would fork the hierarchy off issues.parent_id into a second,
// divergent source of truth.
func relationTypeAllowed(t string) error {
	switch t {
	case relBlocks, relBlocked, relRelated, relDuplicate, relDuplicateOf, relSimilar:
		return nil
	case "PARENT", "SUB_ISSUE":
		return errors.New("parent relations use parentId, not issueRelation")
	default:
		return fmt.Errorf("type must be one of %s", "BLOCKS, BLOCKED, RELATED, DUPLICATE, DUPLICATE_OF, SIMILAR")
	}
}

// reverseRelationType maps a stored edge to the reader's perspective
// when the reader is the edge's TARGET (BLOCKS <-> BLOCKED,
// DUPLICATE <-> DUPLICATE_OF; the symmetric types are the identity).
func reverseRelationType(t string) string {
	switch t {
	case relBlocks:
		return relBlocked
	case relBlocked:
		return relBlocks
	case relDuplicate:
		return relDuplicateOf
	case relDuplicateOf:
		return relDuplicate
	default:
		return t
	}
}

// relationListSQL is the denormalized relations subselect, shared by
// the Issue wire record and the standalone list endpoint. The
// reader-side rewrite lives here so every read surface speaks one
// vocabulary: an edge appears on both endpoints' records, each from
// its own side. Edges touching a soft-deleted endpoint are excluded
// (a deleted issue links to nothing and is linked to by nothing).
const relationListSQL = `
	coalesce((
		select jsonb_agg(jsonb_build_object(
			'id', r.id,
			'createdAt', to_char(r.created_at at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
			'updatedAt', to_char(r.updated_at at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
			'issueId', i.id,
			'createdById', r.created_by,
			'relatedIssueId', case when r.issue_id = i.id then r.related_issue_id else i.id end,
			'type', case when r.issue_id = i.id then r.type else
				case r.type
					when 'BLOCKS' then 'BLOCKED'
					when 'BLOCKED' then 'BLOCKS'
					when 'DUPLICATE' then 'DUPLICATE_OF'
					when 'DUPLICATE_OF' then 'DUPLICATE'
					else r.type
				end
			end)
		order by r.created_at)
		from issue_relations r
		where r.deleted_at is null
		  and i.status <> 'deleted'
		  and (r.issue_id = i.id or r.related_issue_id = i.id)
		  and exists (select 1 from issues x where x.id = r.issue_id and x.status <> 'deleted')
		  and exists (select 1 from issues y where y.id = r.related_issue_id and y.status <> 'deleted')
	), '[]'::jsonb)`

// relationRow is one issue_relations row with its tenant stamps.
type relationRow struct {
	ID, WorkspaceID, TeamID, IssueID, RelatedIssueID, Type string
	CreatedBy                                              *string
	CreatedAt, UpdatedAt                                   time.Time
}

// issueRelationRequest is the client's issueRelation field on the
// issue update payload. IssueID echoes the URL's issue and is
// ignored: the URL is the tenant-resolved identity.
type issueRelationRequest struct {
	IssueID        string `json:"issueId"`
	RelatedIssueID string `json:"relatedIssueId"`
	Type           string `json:"type"`
}

// relationData serializes the stored edge in the client's
// IssueRelation shape (the standalone IssueRelation sync record; the
// edge's own perspective, not a reader rewrite).
func (a *API) relationData(rel relationRow) map[string]any {
	return map[string]any{
		"id":             rel.ID,
		"createdAt":      rel.CreatedAt.Format(iso),
		"updatedAt":      rel.UpdatedAt.Format(iso),
		"issueId":        rel.IssueID,
		"createdById":    nullOrEmpty(strval(rel.CreatedBy)),
		"relatedIssueId": rel.RelatedIssueID,
		"type":           rel.Type,
	}
}

// loadRelationTx loads an edge inside the caller's transaction.
func (a *API) loadRelationTx(ctx context.Context, tx pgx.Tx, id string) (relationRow, error) {
	var rel relationRow
	err := tx.QueryRow(ctx, `
		select id, workspace_id, team_id, issue_id, related_issue_id, type,
		       created_by, created_at, updated_at
		from issue_relations where id = $1`, id).Scan(
		&rel.ID, &rel.WorkspaceID, &rel.TeamID, &rel.IssueID, &rel.RelatedIssueID, &rel.Type,
		&rel.CreatedBy, &rel.CreatedAt, &rel.UpdatedAt)
	return rel, err
}

// blocksCycleTx reports whether adding a BLOCKS edge issueID ->
// relatedIssueID would close a cycle: it walks the live blocks graph
// forward from the target. Only BLOCKS edges form the cycle graph
// (a BLOCKED edge is the reader's alias of a BLOCKS edge from the
// other side; walking it would double-count).
func (a *API) blocksCycleTx(ctx context.Context, tx pgx.Tx, issueID, relatedIssueID string) (bool, error) {
	frontier := []string{relatedIssueID}
	seen := map[string]bool{relatedIssueID: true}
	for depth := 0; len(frontier) > 0 && depth < relationDepthCap; depth++ {
		rows, err := tx.Query(ctx, `
			select r.related_issue_id
			from issue_relations r
			where r.type = $1 and r.deleted_at is null
			  and r.issue_id = any($2::uuid[])
			  and exists (select 1 from issues s where s.id = r.issue_id and s.status <> 'deleted')
			  and exists (select 1 from issues t where t.id = r.related_issue_id and t.status <> 'deleted')`,
			relBlocks, frontier)
		if err != nil {
			return false, err
		}
		next := make([]string, 0, len(frontier))
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return false, err
			}
			if n == issueID {
				rows.Close()
				return true, nil
			}
			if !seen[n] {
				seen[n] = true
				next = append(next, n)
			}
		}
		scanErr := rows.Err()
		rows.Close()
		if scanErr != nil {
			return false, scanErr
		}
		frontier = next
	}
	return false, nil
}

// createRelationTx inserts the edge inside the caller's transaction:
// the pre-check gives a clean conflict, the partial unique index is
// the authority under concurrency, the cycle guard runs under the
// caller's advisory lock, and the timeline gets its breadcrumb.
func (a *API) createRelationTx(ctx context.Context, tx pgx.Tx, p *Principal, workspaceID string, row issueRow, req issueRelationRequest) (relationRow, syncActionRecord, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		select exists(select 1 from issue_relations
			where issue_id = $1 and related_issue_id = $2 and type = $3 and deleted_at is null)`,
		row.ID, req.RelatedIssueID, req.Type).Scan(&exists)
	if err != nil {
		return relationRow{}, syncActionRecord{}, err
	}
	if exists {
		return relationRow{}, syncActionRecord{}, errRelationExists
	}
	if req.Type == relBlocks {
		cyclic, err := a.blocksCycleTx(ctx, tx, row.ID, req.RelatedIssueID)
		if err != nil {
			return relationRow{}, syncActionRecord{}, err
		}
		if cyclic {
			return relationRow{}, syncActionRecord{}, errRelationCycle
		}
	}
	// The partial unique index backs the conflict: a concurrent create
	// that races past the pre-check lands here, not in a 500.
	var (
		rel       relationRow
		createdAt time.Time
		updatedAt time.Time
	)
	err = tx.QueryRow(ctx, `
		insert into issue_relations (workspace_id, team_id, issue_id, related_issue_id, type, created_by)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (issue_id, related_issue_id, type) where deleted_at is null do nothing
		returning id, created_at, updated_at`,
		workspaceID, row.TeamID, row.ID, req.RelatedIssueID, req.Type, p.AccountID).
		Scan(&rel.ID, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return relationRow{}, syncActionRecord{}, errRelationExists
	}
	if err != nil {
		return relationRow{}, syncActionRecord{}, err
	}
	rel.WorkspaceID = workspaceID
	rel.TeamID = row.TeamID
	rel.IssueID = row.ID
	rel.RelatedIssueID = req.RelatedIssueID
	rel.Type = req.Type
	rel.CreatedBy = &p.AccountID
	rel.CreatedAt, rel.UpdatedAt = createdAt, updatedAt
	// The timeline breadcrumb: the client's RelatedActivity renders
	// this row (added, from the actor's side).
	histRec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID, p.AccountID,
		"relation", "relation", req.RelatedIssueID, req.Type, "")
	if err != nil {
		return relationRow{}, syncActionRecord{}, err
	}
	return rel, histRec, nil
}

// applyIssueRelationTx runs the client's issueRelation op end to end
// inside the caller's transaction and returns the records to
// broadcast: the timeline row, the edge record, and the related
// endpoint's refreshed Issue record. The caller's own issue record is
// emitted by the caller (it must refresh either way) — both
// endpoints' denormalized arrays changed, so both feed records carry
// the new picture.
func (a *API) applyIssueRelationTx(ctx context.Context, tx pgx.Tx, p *Principal, workspaceID string, row issueRow, req issueRelationRequest) ([]syncActionRecord, error) {
	rel, histRec, err := a.createRelationTx(ctx, tx, p, workspaceID, row, req)
	if err != nil {
		return nil, err
	}
	relRec, err := a.emitChange(ctx, tx, workspaceID, "IssueRelation", rel.ID, "CREATE", a.relationData(rel))
	if err != nil {
		return nil, err
	}
	fresh, err := a.issueByIDTx(ctx, tx, req.RelatedIssueID)
	if err != nil {
		return nil, err
	}
	issueRec, err := a.emitChange(ctx, tx, workspaceID, "Issue", req.RelatedIssueID, "UPDATE", nil)
	if err != nil {
		return nil, err
	}
	if err := a.refreshOutboxTx(ctx, tx, workspaceID, &issueRec, a.issueData(fresh)); err != nil {
		return nil, err
	}
	return []syncActionRecord{histRec, relRec, issueRec}, nil
}

// handleListIssueRelations implements GET /api/v1/issues/{id}/relations:
// the reader's-side list (the design's reader-perspective rewrite).
func (a *API) handleListIssueRelations(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	id := chi.URLParam(r, "id")
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if _, _, ok := a.issueAccess(r.Context(), p, id); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var raw []byte
	err := a.pool.QueryRow(r.Context(), `select `+relationListSQL+` from issues i where i.id = $1`, id).Scan(&raw)
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(raw))
}

// handleDeleteIssueRelation implements DELETE /api/v1/issue_relation/{id}
// (soft delete). Permission: the edge's creator or a workspace
// owner/admin; anyone else gets a plain 404 (no existence leak).
func (a *API) handleDeleteIssueRelation(w http.ResponseWriter, r *http.Request) {
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
	rel, err := a.loadRelation(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	role, ok := a.workspaceRole(ctx, p, rel.WorkspaceID)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if strval(rel.CreatedBy) != p.AccountID && role != "admin" && role != "owner" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, recs, err := a.applyRelationDeleteTx(ctx, tx, p, rel)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if recs == nil {
		// Already deleted: the op is idempotent, the record stands as-is.
		writeJSON(w, http.StatusOK, a.relationData(rel))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	for i := range recs {
		a.broadcastRecord(recs[i])
	}
	// The client's service resolves the deletion to the pre-delete shape.
	writeJSON(w, http.StatusOK, a.relationData(rel))
}

// applyRelationDeleteTx soft-deletes the edge inside the caller's
// transaction and returns the records to broadcast: the timeline's
// removal breadcrumb, the edge off the feed ({id} — the DELETE
// discipline), and both endpoints' refreshed Issue records (their
// denormalized arrays changed). Nil records when the edge is already
// gone (the op is idempotent).
func (a *API) applyRelationDeleteTx(ctx context.Context, tx pgx.Tx, p *Principal, rel relationRow) (relationRow, []syncActionRecord, error) {
	var updated relationRow
	err := tx.QueryRow(ctx, `
		update issue_relations
		set deleted_at = now(), updated_at = now()
		where id = $1 and deleted_at is null
		returning id, workspace_id, team_id, issue_id, related_issue_id, type,
		        created_by, created_at, updated_at`, rel.ID).Scan(
		&updated.ID, &updated.WorkspaceID, &updated.TeamID, &updated.IssueID, &updated.RelatedIssueID, &updated.Type,
		&updated.CreatedBy, &updated.CreatedAt, &updated.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rel, nil, nil
	}
	if err != nil {
		return rel, nil, err
	}
	recs := make([]syncActionRecord, 0, 4)
	// The timeline breadcrumb (removed, from the edge's side).
	histRec, err := a.writeHistoryTx(ctx, tx, rel.WorkspaceID, rel.TeamID, rel.IssueID, p.AccountID,
		"relation_deleted", "relation", rel.RelatedIssueID, rel.Type, "")
	if err != nil {
		return rel, nil, err
	}
	recs = append(recs, histRec)
	// The edge off the feed (DELETE records carry {id}).
	relRec, err := a.emitChange(ctx, tx, rel.WorkspaceID, "IssueRelation", rel.ID, "DELETE",
		map[string]any{"id": rel.ID})
	if err != nil {
		return rel, nil, err
	}
	recs = append(recs, relRec)
	// Both endpoints' denormalized arrays changed — refresh both Issue
	// records (the client renders from them; the edge record alone
	// reaches no v1 store).
	for _, endpoint := range []string{rel.IssueID, rel.RelatedIssueID} {
		fresh, err := a.issueByIDTx(ctx, tx, endpoint)
		if err != nil {
			return rel, nil, err
		}
		rec, err := a.emitChange(ctx, tx, rel.WorkspaceID, "Issue", endpoint, "UPDATE", nil)
		if err != nil {
			return rel, nil, err
		}
		if err := a.refreshOutboxTx(ctx, tx, rel.WorkspaceID, &rec, a.issueData(fresh)); err != nil {
			return rel, nil, err
		}
		recs = append(recs, rec)
	}
	return updated, recs, nil
}

// loadRelation loads an edge outside a transaction (read path).
func (a *API) loadRelation(ctx context.Context, id string) (relationRow, error) {
	var rel relationRow
	err := a.pool.QueryRow(ctx, `
		select id, workspace_id, team_id, issue_id, related_issue_id, type,
		       created_by, created_at, updated_at
		from issue_relations where id = $1`, id).Scan(
		&rel.ID, &rel.WorkspaceID, &rel.TeamID, &rel.IssueID, &rel.RelatedIssueID, &rel.Type,
		&rel.CreatedBy, &rel.CreatedAt, &rel.UpdatedAt)
	return rel, err
}

// collectIssueRelations serves the bootstrap's IssueRelation model
// (v1 clients do not request it — they render relations from the Issue
// record and history — but the delta serves any client that does).
func (a *API) collectIssueRelations(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, `
		select r.id, r.created_at, r.updated_at, r.issue_id, r.related_issue_id, r.type, r.created_by
		from issue_relations r
		join issues i on i.id = r.issue_id
		join teams t on t.id = i.team_id
		where t.workspace_id = $1 and r.deleted_at is null and i.status <> 'deleted'
		order by r.created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []syncActionRecord
	for rows.Next() {
		var rel relationRow
		if err := rows.Scan(&rel.ID, &rel.CreatedAt, &rel.UpdatedAt,
			&rel.IssueID, &rel.RelatedIssueID, &rel.Type, &rel.CreatedBy); err != nil {
			return nil, err
		}
		rec, err := emit(rel.ID, a.relationData(rel))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
