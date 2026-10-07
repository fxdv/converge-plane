// work.go — the work API for external agents (Phase 2).
// spec cs:agents:work
//
// An agent with the external driver works its queue through leases:
//
//   - GET  /agent/queue                  its assigned work plus the pool
//     (unassigned, unstarted issues in its teams) it may take;
//   - POST /issues/{id}/claim            an exclusive, expiring lease on
//     one issue plus its work packet; claiming a pool issue assigns it;
//   - POST /issues/{id}/claim/heartbeat  extends the lease;
//   - POST /issues/{id}/claim/release    ends it.
//
// A claim is a row in issue_claims, open until it ends. It ends when
// released, when its lease runs out, when another claim by the same
// agent supersedes it, or when the issue stops being the agent's work
// (reassigned, paused, parked in Human Review, closed, or the agent is
// suspended, leaves the team, or is handed back to the runtime). claimVerdictSQL is the one
// definition of "still valid", shared by the heartbeat and the sweep.
//
// Every open/end transition bumps the issue version and emits an Issue
// UPDATE (claimedById/claimedAt), so the board shows who holds a card,
// and an agent whose lease was lost gets 412 on its next If-Match
// write. Heartbeats change neither.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"converge/internal/auth"
)

const (
	claimDefaultTTL    = 300 // seconds
	claimMinTTL        = 30
	claimMaxTTL        = 900
	claimSweepInterval = 10 * time.Second
	claimSweepBatch    = 200
	queueAssignedCap   = 100
	queuePoolCap       = 50
	packetCommentCap   = 20
)

// How a claim ended (issue_claims.end_reason).
const (
	claimEndReleased   = "released"
	claimEndExpired    = "expired"
	claimEndSuperseded = "superseded"
	claimEndReassigned = "reassigned"
	claimEndPaused     = "paused"
	claimEndClosed     = "closed"
	claimEndRevoked    = "revoked"
)

// claimVerdictFrom joins an open claim to everything its validity
// depends on (claim c, issue i, status ws, agent ag, membership wm).
const claimVerdictFrom = `
	from issue_claims c
	join issues i on i.id = c.issue_id
	left join workflow_statuses ws on ws.id = i.status_id
	join accounts ag on ag.id = c.agent_id
	left join workspace_members wm on wm.workspace_id = c.workspace_id and wm.account_id = c.agent_id`

// claimVerdictSQL is null while the claim holds, else the reason it
// must end.
var claimVerdictSQL = `
	case
		when c.expires_at <= now() then '` + claimEndExpired + `'
		when i.status <> 'active' then '` + claimEndClosed + `'
		when i.assignee_id is distinct from c.agent_id then '` + claimEndReassigned + `'
		when i.agent_paused or not (` + notHumanReviewSQL + `) then '` + claimEndPaused + `'
		when coalesce(ws.category, 'UNSTARTED') in ('COMPLETED', 'CANCELED') then '` + claimEndClosed + `'
		when ag.status <> 'active' or ag.agent_driver <> '` + agentDriverExternal + `'
		     or wm.status is distinct from 'active'
		     or not exists (select 1 from team_members tm
		                    where tm.team_id = i.team_id and tm.account_id = c.agent_id)
		     then '` + claimEndRevoked + `'
	end`

// claimView is the wire shape of a claim.
type claimView struct {
	ID                string    `json:"id"`
	IssueID           string    `json:"issueId"`
	AgentID           string    `json:"agentId"`
	Mode              string    `json:"mode"`
	ClaimedAt         time.Time `json:"claimedAt"`
	ExpiresAt         time.Time `json:"expiresAt"`
	TTLSeconds        int       `json:"ttlSeconds"`
	HeartbeatInterval int       `json:"heartbeatIntervalSeconds"`
}

func (c *claimView) fill() { c.HeartbeatInterval = max(c.TTLSeconds/3, 10) }

// externalAgentGuard admits agents driven through the work API. The
// runtime's agents never claim: their queue is worked in-process.
func externalAgentGuard(w http.ResponseWriter, p *Principal) bool {
	if p.Kind != auth.AccountKindAgent || p.Driver != agentDriverExternal {
		writeError(w, http.StatusForbidden, "the work API is for agents with the external driver")
		return false
	}
	return true
}

// agentWorkspace is the one workspace an agent account belongs to
// (agent identities are minted per workspace).
func (a *API) agentWorkspace(ctx context.Context, accountID string) (string, bool) {
	var ws string
	err := a.pool.QueryRow(ctx, `
		select workspace_id from workspace_members
		where account_id = $1 and status = 'active'
		order by joined_at limit 1`, accountID).Scan(&ws)
	return ws, err == nil
}

// handleAgentQueue implements GET /api/v1/agent/queue.
func (a *API) handleAgentQueue(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if !externalAgentGuard(w, p) {
		return
	}
	ctx := r.Context()
	workspaceID, ok := a.agentWorkspace(ctx, p.AccountID)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var grants []string
	if p.Token.TeamLimited() {
		grants = p.Token.TeamIDs
	}
	actionable := `
		from issues i
		join teams t on t.id = i.team_id
		left join workflow_statuses ws on ws.id = i.status_id
		where t.workspace_id = $1
		  and i.status = 'active'
		  and not i.agent_paused
		  and ` + notHumanReviewSQL + `
		  and ($3::uuid[] is null or i.team_id = any($3::uuid[]))
		  and exists (select 1 from team_members tm where tm.team_id = i.team_id and tm.account_id = $2)`
	assigned, err := a.queueIssues(ctx, "select "+issueColumns+actionable+`
		  and i.assignee_id = $2
		  and coalesce(ws.category, 'UNSTARTED') not in ('COMPLETED', 'CANCELED')
		order by i.created_at, i.number
		limit `+strconv.Itoa(queueAssignedCap), workspaceID, p.AccountID, grants)
	if err != nil {
		a.internalError(w, err)
		return
	}
	available, err := a.queueIssues(ctx, "select "+issueColumns+actionable+`
		  and i.assignee_id is null
		  and ws.category = 'UNSTARTED'
		  and not exists (select 1 from issue_claims c where c.issue_id = i.id and c.ended_at is null and c.expires_at > now())
		order by case when i.priority between 1 and 4 then i.priority else 5 end, i.created_at, i.number
		limit `+strconv.Itoa(queuePoolCap), workspaceID, p.AccountID, grants)
	if err != nil {
		a.internalError(w, err)
		return
	}
	claims, err := a.openClaims(ctx, p.AccountID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workspaceId": workspaceID,
		"assigned":    assigned,
		"available":   available,
		"claims":      claims,
	})
}

func (a *API) queueIssues(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	rows, err := a.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var row issueRow
		if err := rows.Scan(row.scanDest()...); err != nil {
			return nil, err
		}
		out = append(out, a.issueData(row))
	}
	return out, rows.Err()
}

// openClaims lists the agent's live claims.
func (a *API) openClaims(ctx context.Context, agentID string) ([]claimView, error) {
	rows, err := a.pool.Query(ctx, `
		select id, issue_id, agent_id, mode, claimed_at, expires_at, ttl_seconds
		from issue_claims
		where agent_id = $1 and ended_at is null and expires_at > now()
		order by claimed_at`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []claimView{}
	for rows.Next() {
		var c claimView
		if err := rows.Scan(&c.ID, &c.IssueID, &c.AgentID, &c.Mode, &c.ClaimedAt, &c.ExpiresAt, &c.TTLSeconds); err != nil {
			return nil, err
		}
		c.fill()
		out = append(out, c)
	}
	return out, rows.Err()
}

// handleClaimIssue implements POST /api/v1/issues/{id}/claim.
func (a *API) handleClaimIssue(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if !externalAgentGuard(w, p) {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if !isUUID(id) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var req struct {
		TTLSeconds *int `json:"ttlSeconds"`
	}
	if err := decodeOptional(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ttl := claimDefaultTTL
	if req.TTLSeconds != nil {
		ttl = *req.TTLSeconds
	}
	if ttl < claimMinTTL || ttl > claimMaxTTL {
		writeError(w, http.StatusUnprocessableEntity, "ttlSeconds must be between 30 and 900")
		return
	}
	row, workspaceID, ok := a.issueAccess(ctx, p, id)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if over, err := a.teamOverBudget(ctx, row.TeamID); err != nil {
		a.internalError(w, err)
		return
	} else if over != nil {
		a.noteEconomyRefusal(ctx, workspaceID, p.AccountID, row.TeamID, auditBudgetRefused)
		writeError(w, http.StatusUnprocessableEntity, over.Error())
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_issue_"+row.TeamID); err != nil {
		a.internalError(w, err)
		return
	}
	var (
		status, category string
		assignee         *string
		paused, parked   bool
		teamMember       bool
	)
	err = tx.QueryRow(ctx, `
		select i.status, i.assignee_id::text, i.agent_paused,
		       not (`+notHumanReviewSQL+`),
		       coalesce(ws.category, 'UNSTARTED'),
		       exists (select 1 from team_members tm where tm.team_id = i.team_id and tm.account_id = $2)
		from issues i
		left join workflow_statuses ws on ws.id = i.status_id
		where i.id = $1
		for update of i`, id, p.AccountID).Scan(&status, &assignee, &paused, &parked, &category, &teamMember)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	mode := ""
	switch {
	case status != "active" || category == "COMPLETED" || category == "CANCELED":
		writeError(w, http.StatusConflict, "the issue is closed")
		return
	case paused || parked:
		writeError(w, http.StatusConflict, "the issue waits for a human")
		return
	case assignee != nil && *assignee == p.AccountID:
		mode = "assigned"
	case assignee == nil && category == "UNSTARTED" && teamMember:
		mode = "pool"
	default:
		writeError(w, http.StatusConflict, "the issue is neither assigned to this agent nor open in its teams' pool")
		return
	}

	var recs []syncActionRecord
	var holderID, openID string
	var live bool
	err = tx.QueryRow(ctx, `
		select id, agent_id, expires_at > now()
		from issue_claims where issue_id = $1 and ended_at is null
		for update`, id).Scan(&openID, &holderID, &live)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		a.internalError(w, err)
		return
	case live && holderID != p.AccountID:
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":       "the issue is claimed by another agent",
			"claimedById": holderID,
		})
		return
	default:
		// A dead lease, or this agent's own (a restarted process takes
		// over; the old process is fenced by the new claim id).
		reason := claimEndExpired
		if live {
			reason = claimEndSuperseded
		}
		if _, err := tx.Exec(ctx,
			"update issue_claims set ended_at = now(), end_reason = $2 where id = $1",
			openID, reason); err != nil {
			a.internalError(w, err)
			return
		}
		ended, err := a.endRunTx(ctx, tx, openID, reason)
		if err != nil {
			a.internalError(w, err)
			return
		}
		recs = append(recs, ended...)
	}

	if mode == "pool" {
		if _, err := tx.Exec(ctx,
			"update issues set assignee_id = $2, version = version + 1, updated_at = now() where id = $1",
			id, p.AccountID); err != nil {
			a.internalError(w, err)
			return
		}
		hist, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, id, p.AccountID, "updated", "assignee", "", p.AccountID, "")
		if err != nil {
			a.internalError(w, err)
			return
		}
		recs = append(recs, hist)
	} else if _, err := tx.Exec(ctx, "update issues set version = version + 1 where id = $1", id); err != nil {
		a.internalError(w, err)
		return
	}

	var tokenID *string
	if p.Token != nil {
		tokenID = &p.Token.ID
	}
	claim := claimView{IssueID: id, AgentID: p.AccountID, Mode: mode, TTLSeconds: ttl}
	if err := tx.QueryRow(ctx, `
		insert into issue_claims (issue_id, workspace_id, agent_id, token_id, mode, ttl_seconds, expires_at)
		values ($1, $2, $3, $4, $5, $6::int, now() + $6::int * interval '1 second')
		returning id, claimed_at, expires_at`,
		id, workspaceID, p.AccountID, tokenID, mode, ttl).Scan(&claim.ID, &claim.ClaimedAt, &claim.ExpiresAt); err != nil {
		a.internalError(w, err)
		return
	}
	claim.fill()
	runRec, err := a.openRunTx(ctx, tx, claim.ID, workspaceID, id, p.AccountID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	recs = append(recs, runRec)
	fresh, err := a.issueByIDTx(ctx, tx, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "Issue", id, "UPDATE", a.issueData(fresh))
	if err != nil {
		a.internalError(w, err)
		return
	}
	recs = append([]syncActionRecord{rec}, recs...)
	packet, err := a.workPacketTx(ctx, tx, fresh, p.AccountID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.auditTx(ctx, tx, workspaceID, p.AccountID, "issue.claimed", "Issue", id); err != nil {
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
	w.Header().Set("ETag", issueETag(fresh.Version))
	writeJSON(w, http.StatusOK, map[string]any{"claim": claim, "packet": packet})
}

// handleClaimHeartbeat implements POST /api/v1/issues/{id}/claim/heartbeat.
func (a *API) handleClaimHeartbeat(w http.ResponseWriter, r *http.Request) {
	a.claimTransition(w, r, false)
}

// handleClaimRelease implements POST /api/v1/issues/{id}/claim/release.
// Releasing is idempotent: a claim that already ended answers 200 with
// how it ended. The assignment stays; setting assigneeId to null is how
// an agent hands pool work back.
func (a *API) handleClaimRelease(w http.ResponseWriter, r *http.Request) {
	a.claimTransition(w, r, true)
}

// claimTransition is the shared heartbeat/release path: both name the
// claim, both end a claim the verdict has already doomed, and both may
// carry a run report. A heartbeat applies its report only while the
// claim holds; a release applies it before ending the claim, or to a run
// that already ended within the report grace (the final numbers belong
// in the release).
func (a *API) claimTransition(w http.ResponseWriter, r *http.Request, release bool) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if !externalAgentGuard(w, p) {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var req runReport
	if err := decodeOptional(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if msg := req.normalize(); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	hasReport := !req.empty()
	if !isUUID(id) || !isUUID(req.ClaimID) {
		writeError(w, http.StatusNotFound, "claim not found")
		return
	}
	row, _, ok := a.issueAccess(ctx, p, id)
	if !ok {
		writeError(w, http.StatusNotFound, "claim not found")
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_issue_"+row.TeamID); err != nil {
		a.internalError(w, err)
		return
	}
	var (
		agentID, endReason, verdict string
		ended                       bool
		claim                       claimView
	)
	err = tx.QueryRow(ctx, `
		select c.agent_id, c.ended_at is not null, coalesce(c.end_reason, ''),
		       coalesce(`+claimVerdictSQL+`, ''),
		       c.id, c.issue_id, c.mode, c.claimed_at, c.expires_at, c.ttl_seconds
		`+claimVerdictFrom+`
		where c.id = $1 and c.issue_id = $2
		for update of c`, req.ClaimID, id).Scan(&agentID, &ended, &endReason, &verdict,
		&claim.ID, &claim.IssueID, &claim.Mode, &claim.ClaimedAt, &claim.ExpiresAt, &claim.TTLSeconds)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && agentID != p.AccountID) {
		writeError(w, http.StatusNotFound, "claim not found")
		return
	}
	if err != nil {
		a.internalError(w, err)
		return
	}
	claim.AgentID = agentID
	claim.fill()
	if ended && !release {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "the claim has ended", "endReason": endReason})
		return
	}
	if !release && verdict != "" {
		recs, err := a.endClaimTx(ctx, tx, claim.ID, verdict)
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
		writeJSON(w, http.StatusConflict, map[string]any{"error": "the claim has ended", "endReason": verdict})
		return
	}

	var (
		recs    []syncActionRecord
		dropped int
	)
	if hasReport {
		n, status, body, linked, err := a.reportRunTx(ctx, tx, claim.ID, id, p.AccountID, req)
		if err != nil {
			a.internalError(w, err)
			return
		}
		if status != 0 {
			writeJSON(w, status, body)
			return
		}
		dropped = n
		recs = append(recs, linked...)
	}
	resp := map[string]any{}
	switch {
	case release && !ended:
		out, err := a.endClaimTx(ctx, tx, claim.ID, claimEndReleased)
		if err != nil {
			a.internalError(w, err)
			return
		}
		recs = append(recs, out...)
		resp["released"], resp["endReason"] = true, claimEndReleased
	case release:
		resp["released"], resp["endReason"] = false, endReason
	default:
		if err := tx.QueryRow(ctx, `
			update issue_claims
			set heartbeat_at = now(), expires_at = now() + ttl_seconds * interval '1 second'
			where id = $1
			returning expires_at`, claim.ID).Scan(&claim.ExpiresAt); err != nil {
			a.internalError(w, err)
			return
		}
		resp["claim"] = claim
	}
	// endClaimTx already emitted the run when it ended it.
	if hasReport && !(release && !ended) {
		rec, _, err := a.emitRunTx(ctx, tx, claim.ID, "UPDATE")
		if err != nil {
			a.internalError(w, err)
			return
		}
		recs = append(recs, rec)
	}
	if release || hasReport {
		run, err := a.runByIDTx(ctx, tx, claim.ID)
		switch {
		case err == nil:
			resp["run"] = runData(run)
		case !errors.Is(err, pgx.ErrNoRows):
			a.internalError(w, err)
			return
		}
		if hasReport {
			resp["droppedEvents"] = dropped
		}
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	for i := range recs {
		a.broadcastRecord(recs[i])
	}
	writeJSON(w, http.StatusOK, resp)
}

// endClaimTx ends one open claim and emits the issue's new state. It
// returns no records when the claim had already ended.
func (a *API) endClaimTx(ctx context.Context, tx pgx.Tx, claimID, reason string) ([]syncActionRecord, error) {
	var issueID, workspaceID string
	err := tx.QueryRow(ctx, `
		update issue_claims set ended_at = now(), end_reason = $2
		where id = $1 and ended_at is null
		returning issue_id, workspace_id`, claimID, reason).Scan(&issueID, &workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "update issues set version = version + 1 where id = $1", issueID); err != nil {
		return nil, err
	}
	recs, err := a.endRunTx(ctx, tx, claimID, reason)
	if err != nil {
		return nil, err
	}
	fresh, err := a.issueByIDTx(ctx, tx, issueID)
	if err != nil {
		return nil, err
	}
	rec, err := a.emitChange(ctx, tx, workspaceID, "Issue", issueID, "UPDATE", a.issueData(fresh))
	if err != nil {
		return nil, err
	}
	return append(recs, rec), nil
}

// endAgentClaimsTx ends every open claim the agent holds.
func (a *API) endAgentClaimsTx(ctx context.Context, tx pgx.Tx, agentID, reason string) ([]syncActionRecord, error) {
	rows, err := tx.Query(ctx,
		"select id from issue_claims where agent_id = $1 and ended_at is null for update", agentID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Lock the runs before the first sync sequence is taken: a concurrent
	// report locks its run and then the sequence, so taking them in the
	// other order here could deadlock.
	if _, err := tx.Exec(ctx,
		"select 1 from agent_runs where claim_id = any($1::uuid[]) for update", ids); err != nil {
		return nil, err
	}
	var recs []syncActionRecord
	for _, id := range ids {
		out, err := a.endClaimTx(ctx, tx, id, reason)
		if err != nil {
			return nil, err
		}
		recs = append(recs, out...)
	}
	return recs, nil
}

// SweepClaims ends every open claim whose verdict is no longer null: the
// expiry backstop and the board's source of truth for claims an agent
// stopped heartbeating or a human took back. Each claim ends in its own
// short transaction under its team lock, re-checked there, so a
// heartbeat that landed in between wins.
func (a *API) SweepClaims(ctx context.Context) (int, error) {
	rows, err := a.pool.Query(ctx, `
		select c.id, i.team_id
		`+claimVerdictFrom+`
		where c.ended_at is null and (`+claimVerdictSQL+`) is not null
		limit `+strconv.Itoa(claimSweepBatch))
	if err != nil {
		return 0, err
	}
	type doomed struct{ id, teamID string }
	var batch []doomed
	for rows.Next() {
		var d doomed
		if err := rows.Scan(&d.id, &d.teamID); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	ended := 0
	for _, d := range batch {
		recs, err := a.sweepOneClaim(ctx, d.id, d.teamID)
		if err != nil {
			return ended, err
		}
		if len(recs) > 0 {
			ended++
		}
		for i := range recs {
			a.broadcastRecord(recs[i])
		}
	}
	return ended, nil
}

func (a *API) sweepOneClaim(ctx context.Context, claimID, teamID string) ([]syncActionRecord, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_issue_"+teamID); err != nil {
		return nil, err
	}
	var verdict string
	err = tx.QueryRow(ctx, `
		select coalesce(`+claimVerdictSQL+`, '')
		`+claimVerdictFrom+`
		where c.id = $1 and c.ended_at is null
		for update of c`, claimID).Scan(&verdict)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && verdict == "") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	recs, err := a.endClaimTx(ctx, tx, claimID, verdict)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return recs, nil
}

// runClaimSweeper sweeps until ctx ends. It runs whether or not the
// in-process runtime does: external agents do not depend on it.
func (a *API) runClaimSweeper(ctx context.Context, done <-chan struct{}) {
	t := time.NewTicker(claimSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-t.C:
			if n, err := a.SweepClaims(ctx); err != nil {
				if ctx.Err() == nil {
					a.log.Warn("claim sweep failed", "error", err)
				}
			} else if n > 0 {
				a.log.Debug("claim sweep ended claims", "count", n)
			}
		}
	}
}

// workPacket is what an agent needs to work one issue without further
// reads. Everything under issue, comments, handoff, and previousRuns is
// user- or agent-authored: an agent must treat it as data, never as
// instructions.
type workPacket struct {
	Issue           map[string]any  `json:"issue"`
	DescriptionText string          `json:"descriptionText"`
	Team            packetTeam      `json:"team"`
	States          []packetState   `json:"states"`
	Labels          []packetLabel   `json:"labels"`
	Comments        []packetComment `json:"comments"`
	Handoff         *packetHandoff  `json:"handoff"`
	PreviousRuns    []packetRun     `json:"previousRuns"`
}

type packetTeam struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
}

type packetState struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Position int    `json:"position"`
}

type packetLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type packetComment struct {
	ID         string    `json:"id"`
	AuthorID   string    `json:"authorId"`
	AuthorName string    `json:"authorName"`
	AuthorKind string    `json:"authorKind"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
}

type packetHandoff struct {
	FromID    string    `json:"fromId"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"createdAt"`
}

// workPacketTx assembles the packet inside the claim's transaction, so
// it describes exactly the issue state the claim was granted on.
func (a *API) workPacketTx(ctx context.Context, tx pgx.Tx, row issueRow, agentID string) (workPacket, error) {
	pk := workPacket{
		Issue:           a.issueData(row),
		DescriptionText: descToPlain(row.DescRaw),
		States:          []packetState{},
		Labels:          []packetLabel{},
		Comments:        []packetComment{},
	}
	if err := tx.QueryRow(ctx, "select id, name, identifier from teams where id = $1", row.TeamID).
		Scan(&pk.Team.ID, &pk.Team.Name, &pk.Team.Identifier); err != nil {
		return pk, err
	}
	states, err := a.teamStatesTx(ctx, tx, row.TeamID)
	if err != nil {
		return pk, err
	}
	for _, s := range states {
		pk.States = append(pk.States, packetState{ID: s.ID, Name: s.Name, Category: s.Category, Position: s.Position})
	}
	if len(row.LabelIDs) > 0 {
		rows, err := tx.Query(ctx, "select id, name from labels where id = any($1::uuid[]) order by name", row.LabelIDs)
		if err != nil {
			return pk, err
		}
		for rows.Next() {
			var l packetLabel
			if err := rows.Scan(&l.ID, &l.Name); err != nil {
				rows.Close()
				return pk, err
			}
			pk.Labels = append(pk.Labels, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return pk, err
		}
	}
	rows, err := tx.Query(ctx, `
		select id, author_id, author_name, author_kind, body, created_at from (
			select c.id, c.author_id::text, an.name as author_name, an.kind as author_kind, c.body, c.created_at
			from comments c
			join accounts an on an.id = c.author_id
			where c.issue_id = $1
			order by c.created_at desc
			limit `+strconv.Itoa(packetCommentCap)+`
		) recent order by created_at`, row.ID)
	if err != nil {
		return pk, err
	}
	for rows.Next() {
		var c packetComment
		if err := rows.Scan(&c.ID, &c.AuthorID, &c.AuthorName, &c.AuthorKind, &c.Body, &c.CreatedAt); err != nil {
			rows.Close()
			return pk, err
		}
		c.Body = descToPlain(c.Body)
		pk.Comments = append(pk.Comments, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return pk, err
	}
	var h packetHandoff
	err = tx.QueryRow(ctx, `
		select from_account_id::text, summary, created_at from issue_handoffs
		where issue_id = $1 and to_account_id = $2
		order by created_at desc limit 1`, row.ID, agentID).Scan(&h.FromID, &h.Summary, &h.CreatedAt)
	switch {
	case err == nil:
		pk.Handoff = &h
	case !errors.Is(err, pgx.ErrNoRows):
		return pk, err
	}
	pk.PreviousRuns, err = a.previousRunsTx(ctx, tx, row.ID)
	return pk, err
}

// decodeOptional decodes a JSON body when one is present; an empty body
// leaves v at its zero value.
func decodeOptional(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}
