// governance.go — Phase 3: agents cannot claim Done without proof,
// a team spend cap stops further cost, and the swarm page reads the
// resulting rates.
// spec cs:agents:evidence
package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"converge/internal/auth"
)

// errDoneNeedsEvidence is the board refusing an agent's move to Done.
var errDoneNeedsEvidence = errors.New("moving an issue to Done needs a merged pull request, a green check the server read, or a human's approval")

const governanceWindow = 7 * 24 * time.Hour

func agentActor(p *Principal) bool {
	return p != nil && p.Kind == auth.AccountKindAgent
}

// doneEvidenceTx reports errDoneNeedsEvidence when an agent is moving
// the issue to a COMPLETED state without proof. Humans and the system
// are the proof, so they are not checked. A merged pull request counts
// only when the server has verified it and no linked pull request is
// still pending or open. A human's approval counts until the issue
// leaves Done.
func (a *API) doneEvidenceTx(ctx context.Context, tx pgx.Tx, issueID, teamID, stateID string) error {
	var category string
	err := tx.QueryRow(ctx, `
		select category from workflow_statuses where id = $1 and team_id = $2`, stateID, teamID).Scan(&category)
	if errors.Is(err, pgx.ErrNoRows) || category != "COMPLETED" {
		return nil
	}
	if err != nil {
		return err
	}
	var merged, open, green int
	if err := tx.QueryRow(ctx, `
		select count(*) filter (where state = 'merged'),
		       count(*) filter (where state in ('pending', 'open')),
		       count(*) filter (where ci_state = 'success')
		from issue_pull_requests
		where issue_id = $1 and unlinked_at is null`, issueID).Scan(&merged, &open, &green); err != nil {
		return err
	}
	// A merged pull request is proof only when nothing is still open.
	// A green combined status is proof on its own: the server read it
	// from GitHub. An evidence link of kind ci_run is not this column.
	if (merged > 0 && open == 0) || green > 0 {
		return nil
	}
	var approved bool
	if err := tx.QueryRow(ctx, `
		select done_approved_at is not null from issues where id = $1`, issueID).Scan(&approved); err != nil {
		return err
	}
	if approved {
		return nil
	}
	return errDoneNeedsEvidence
}

// clearDoneApprovalTx forgets a human's approval when the issue leaves
// Done, so the next close needs proof of its own. A move between two
// completed states keeps the approval.
func (a *API) clearDoneApprovalTx(ctx context.Context, tx pgx.Tx, issueID, fromStateID, toStateID string) error {
	if fromStateID == "" {
		return nil
	}
	var fromCat, toCat string
	err := tx.QueryRow(ctx, `select category from workflow_statuses where id = $1`, fromStateID).Scan(&fromCat)
	if errors.Is(err, pgx.ErrNoRows) || fromCat != "COMPLETED" {
		return nil
	}
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `select category from workflow_statuses where id = $1`, toStateID).Scan(&toCat)
	if err == nil && toCat == "COMPLETED" {
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(ctx, `
		update issues set done_approved_at = null, done_approved_by = null where id = $1`, issueID)
	return err
}

// handleApproveDone implements POST /api/v1/issues/{id}/done-approval.
// A human records that this issue may be moved to Done. Agents cannot
// approve their own work.
func (a *API) handleApproveDone(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if agentActor(p) {
		writeError(w, http.StatusUnprocessableEntity, "only a human can approve moving an issue to Done")
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	row, workspaceID, ok := a.issueAccess(ctx, p, id)
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
	if _, err := tx.Exec(ctx, `
		update issues set done_approved_at = now(), done_approved_by = $2,
		       version = version + 1, updated_at = now()
		where id = $1`, id, p.AccountID); err != nil {
		a.internalError(w, err)
		return
	}
	rec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, id, p.AccountID, "updated", "approval",
		"", "approved", "A human approved moving this issue to Done")
	if err != nil {
		a.internalError(w, err)
		return
	}
	issueRec, err := a.emitChange(ctx, tx, workspaceID, "Issue", id, "UPDATE", nil)
	if err != nil {
		a.internalError(w, err)
		return
	}
	fresh, err := a.issueByIDTx(ctx, tx, id)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.refreshOutboxTx(ctx, tx, workspaceID, &issueRec, a.issueData(fresh)); err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	a.broadcastRecord(issueRec)
	writeJSON(w, http.StatusOK, map[string]any{"issueId": id, "approved": true})
}

func budgetFromPrefs(raw []byte) int64 {
	if len(raw) == 0 {
		return 0
	}
	var prefs map[string]any
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return 0
	}
	switch v := prefs["spendBudgetMicros"].(type) {
	case float64:
		if v > 0 {
			return int64(v)
		}
	case json.Number:
		n, _ := v.Int64()
		if n > 0 {
			return n
		}
	}
	return 0
}

// errSpendBudget is the sentinel a spend stop unwraps to. The text an
// agent sees is spendStop.Error, which names the team and the remainder.
var errSpendBudget = errors.New("spend budget")

const (
	auditBudgetRefused       = "economy.budget_refused"
	auditDoneEvidenceRefused = "economy.done_evidence_refused"
)

// spendStop is a cap refusal. Remaining is the room left before this
// report or claim, never a negative number.
type spendStop struct {
	teamName      string
	spent, budget int64
}

func (e *spendStop) Error() string {
	remaining := e.budget - e.spent
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("%s spend budget: %d of %d micro-USD used in the last 24 hours, %d remaining",
		e.teamName, e.spent, e.budget, remaining)
}

func (e *spendStop) Unwrap() error { return errSpendBudget }

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func teamSpendLabel(name, identifier string) string {
	if identifier != "" && identifier != name {
		return identifier + " (" + name + ")"
	}
	if name != "" {
		return name
	}
	return identifier
}

// spendStopFor reports a stop when a claim (claim is true) finds the
// window already at the cap, or when a report's new total would pass it.
// exceptRun is omitted from the sum so a report can replace its own total.
// A team with no positive cap never stops.
func (a *API) spendStopFor(ctx context.Context, q queryRower, teamID, exceptRun string, next int64, claim bool) (*spendStop, error) {
	var name, identifier string
	var raw []byte
	if err := q.QueryRow(ctx, `select name, identifier, preferences from teams where id = $1`, teamID).Scan(&name, &identifier, &raw); err != nil {
		return nil, err
	}
	budget := budgetFromPrefs(raw)
	if budget <= 0 {
		return nil, nil
	}
	var spent int64
	var err error
	if exceptRun == "" {
		err = q.QueryRow(ctx, `
			select coalesce(sum(r.cost_micros), 0)
			from agent_runs r
			join issues i on i.id = r.issue_id
			where i.team_id = $1 and r.updated_at > now() - interval '24 hours'`, teamID).Scan(&spent)
	} else {
		err = q.QueryRow(ctx, `
			select coalesce(sum(r.cost_micros), 0)
			from agent_runs r
			join issues i on i.id = r.issue_id
			where i.team_id = $1 and r.id <> $2::uuid
			  and r.updated_at > now() - interval '24 hours'`, teamID, exceptRun).Scan(&spent)
	}
	if err != nil {
		return nil, err
	}
	over := spent >= budget
	if !claim {
		over = spent+next > budget
	}
	if !over {
		return nil, nil
	}
	return &spendStop{teamName: teamSpendLabel(name, identifier), spent: spent, budget: budget}, nil
}

// spendAllowsTx reports whether recording next as the run's cost total
// stays inside the team's 24 hour cap. No cap always allows it.
func (a *API) spendAllowsTx(ctx context.Context, tx pgx.Tx, teamID, runID string, next int64) error {
	stop, err := a.spendStopFor(ctx, tx, teamID, runID, next, false)
	if err != nil || stop == nil {
		return err
	}
	return stop
}

// teamOverBudget reports a stop when a new claim would open on a team
// that has already spent its cap. The check uses the pool, before the
// claim transaction opens.
func (a *API) teamOverBudget(ctx context.Context, teamID string) (*spendStop, error) {
	return a.spendStopFor(ctx, a.pool, teamID, "", 0, true)
}

// noteEconomyRefusal records a cap or Done-evidence refusal outside the
// caller's transaction, so the row survives the rollback of the refused
// write. A failure to record does not change the refusal.
func (a *API) noteEconomyRefusal(ctx context.Context, workspaceID, actorID, teamID, action string) {
	if a == nil || a.pool == nil || !isUUID(workspaceID) || !isUUID(teamID) {
		return
	}
	var actor any
	if isUUID(actorID) {
		actor = actorID
	}
	if _, err := a.pool.Exec(ctx, `
		insert into audit_events (workspace_id, team_id, actor_id, action, object_type, object_id)
		values ($1, $2, $3, $4, 'Team', $2)`,
		workspaceID, teamID, actor, action); err != nil && a.log != nil {
		a.log.Warn("economy refusal not recorded", "action", action, "err", err)
	}
}

type governanceView struct {
	WindowDays        int     `json:"windowDays"`
	Completed         int     `json:"completed"`
	CostMicros        int64   `json:"costMicros"`
	CostPerDoneMicros int64   `json:"costPerDoneMicros"`
	ReworkRate        float64 `json:"reworkRate"`
	FalseDoneRate     float64 `json:"falseDoneRate"`
}

// governanceStats is the last 7 days: cost per issue currently Done,
// the share of Done issues that were later moved out (rework), and the
// share of agent Done moves that a later move undid (false done).
func (a *API) governanceStats(ctx context.Context, workspaceID string) (governanceView, error) {
	out := governanceView{WindowDays: 7}
	err := a.pool.QueryRow(ctx, `
		select count(distinct i.id), coalesce(sum(r.cost_micros), 0)
		from issues i
		join teams t on t.id = i.team_id
		join workflow_statuses ws on ws.id = i.status_id and ws.category = 'COMPLETED'
		left join agent_runs r on r.issue_id = i.id
		where t.workspace_id = $1 and i.updated_at > now() - interval '7 days'`, workspaceID).Scan(&out.Completed, &out.CostMicros)
	if err != nil {
		return out, err
	}
	if out.Completed > 0 {
		out.CostPerDoneMicros = out.CostMicros / int64(out.Completed)
	}
	rows, err := a.pool.Query(ctx, `
		select h.issue_id, h.created_at, ws.category, coalesce(ac.kind, '')
		from issue_history h
		join issues i on i.id = h.issue_id
		join teams t on t.id = i.team_id
		join workflow_statuses ws on ws.id::text = h.to_value
		left join accounts ac on ac.id = h.actor_id
		where t.workspace_id = $1 and h.field = 'status' and h.action = 'updated'
		  and h.created_at > now() - interval '7 days'
		order by h.issue_id, h.created_at`, workspaceID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	type move struct {
		issue, category, kind string
		at                    time.Time
	}
	var moves []move
	for rows.Next() {
		var m move
		if err := rows.Scan(&m.issue, &m.at, &m.category, &m.kind); err != nil {
			return out, err
		}
		moves = append(moves, m)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	completedIssues := map[string]bool{}
	reworkIssues := map[string]bool{}
	agentDones := 0
	falseDones := 0
	seenAgentDone := map[string]bool{}
	for i, m := range moves {
		if m.category == "COMPLETED" {
			completedIssues[m.issue] = true
			if m.kind == auth.AccountKindAgent && !seenAgentDone[m.issue] {
				seenAgentDone[m.issue] = true
				agentDones++
				for _, later := range moves[i+1:] {
					if later.issue != m.issue {
						break
					}
					if later.category != "COMPLETED" {
						falseDones++
						break
					}
				}
			}
		}
	}
	for i, m := range moves {
		if m.category == "COMPLETED" {
			continue
		}
		for _, earlier := range moves[:i] {
			if earlier.issue == m.issue && earlier.category == "COMPLETED" {
				reworkIssues[m.issue] = true
				break
			}
		}
	}
	if n := len(completedIssues); n > 0 {
		out.ReworkRate = float64(len(reworkIssues)) / float64(n)
	}
	if agentDones > 0 {
		out.FalseDoneRate = float64(falseDones) / float64(agentDones)
	}
	return out, nil
}

// economyView is the 24-hour spend desk on GET /workspaces/{id}/swarm.
// Cost is the agent-reported micro-USD already stored on agent_runs.
// Teams lists only teams with a positive spendBudgetMicros cap.
type economyView struct {
	WindowHours          int            `json:"windowHours"`
	Teams                []economyTeam  `json:"teams"`
	Agents               []economyAgent `json:"agents"`
	BudgetRefusals       int            `json:"budgetRefusals"`
	DoneEvidenceRefusals int            `json:"doneEvidenceRefusals"`
}

type economyTeam struct {
	TeamID          string `json:"teamId"`
	Name            string `json:"name"`
	Identifier      string `json:"identifier"`
	BudgetMicros    int64  `json:"budgetMicros"`
	SpentMicros     int64  `json:"spentMicros"`
	RemainingMicros int64  `json:"remainingMicros"`
}

type economyAgent struct {
	AgentID    string `json:"agentId"`
	Name       string `json:"name"`
	CostMicros int64  `json:"costMicros"`
	Tokens     int64  `json:"tokens"`
	Runs       int    `json:"runs"`
	Issues     int    `json:"issues"`
}

func (a *API) economyDesk(ctx context.Context, workspaceID string) (economyView, error) {
	out := economyView{
		WindowHours: 24,
		Teams:       []economyTeam{},
		Agents:      []economyAgent{},
	}
	rows, err := a.pool.Query(ctx, `
		select t.id, t.name, t.identifier, t.preferences,
		       coalesce(sum(r.cost_micros), 0)
		from teams t
		left join issues i on i.team_id = t.id
		left join agent_runs r on r.issue_id = i.id
		  and r.updated_at > now() - interval '24 hours'
		where t.workspace_id = $1 and t.status = 'active'
		group by t.id, t.name, t.identifier, t.preferences
		order by t.name`, workspaceID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var team economyTeam
		var raw []byte
		var spent int64
		if err := rows.Scan(&team.TeamID, &team.Name, &team.Identifier, &raw, &spent); err != nil {
			return out, err
		}
		budget := budgetFromPrefs(raw)
		if budget <= 0 {
			continue
		}
		team.BudgetMicros = budget
		team.SpentMicros = spent
		team.RemainingMicros = budget - spent
		if team.RemainingMicros < 0 {
			team.RemainingMicros = 0
		}
		out.Teams = append(out.Teams, team)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows.Close()

	arows, err := a.pool.Query(ctx, `
		select r.agent_id, a.name,
		       coalesce(sum(r.cost_micros), 0),
		       coalesce(sum(r.input_tokens), 0) + coalesce(sum(r.output_tokens), 0),
		       count(*),
		       count(distinct r.issue_id)
		from agent_runs r
		join issues i on i.id = r.issue_id
		join teams t on t.id = i.team_id
		join accounts a on a.id = r.agent_id
		where t.workspace_id = $1 and r.updated_at > now() - interval '24 hours'
		group by r.agent_id, a.name
		order by sum(r.cost_micros) desc, a.name`, workspaceID)
	if err != nil {
		return out, err
	}
	defer arows.Close()
	for arows.Next() {
		var agent economyAgent
		if err := arows.Scan(&agent.AgentID, &agent.Name, &agent.CostMicros, &agent.Tokens, &agent.Runs, &agent.Issues); err != nil {
			return out, err
		}
		out.Agents = append(out.Agents, agent)
	}
	if err := arows.Err(); err != nil {
		return out, err
	}

	rrows, err := a.pool.Query(ctx, `
		select action, count(*)
		from audit_events
		where workspace_id = $1
		  and created_at > now() - interval '24 hours'
		  and action in ($2, $3)
		group by action`, workspaceID, auditBudgetRefused, auditDoneEvidenceRefused)
	if err != nil {
		return out, err
	}
	defer rrows.Close()
	for rrows.Next() {
		var action string
		var n int
		if err := rrows.Scan(&action, &n); err != nil {
			return out, err
		}
		switch action {
		case auditBudgetRefused:
			out.BudgetRefusals = n
		case auditDoneEvidenceRefused:
			out.DoneEvidenceRefusals = n
		}
	}
	return out, rrows.Err()
}

// handleTraceExport implements GET /api/v1/workspaces/{id}/trace.
// The body is JSONL. X-Converge-Signature is hex HMAC-SHA256 of the
// body under CONVERGE_TRACE_SIGNING_KEY. Without that key the export
// is not served.
func (a *API) handleTraceExport(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if a.cfg.TraceSigningKey == "" {
		writeError(w, http.StatusNotFound, "trace signing is not configured")
		return
	}
	if agentActor(p) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	ctx := r.Context()
	workspaceID := chi.URLParam(r, "id")
	if !isUUID(workspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	role, ok := a.workspaceRole(ctx, p, workspaceID)
	if !ok || !adminRole(role) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	rows, err := a.pool.Query(ctx, `
		select r.id, r.issue_id, r.agent_id, coalesce(r.model, ''),
		       r.input_tokens, r.output_tokens, r.cost_micros,
		       coalesce(r.outcome, ''), coalesce(r.summary, ''), r.updated_at
		from agent_runs r
		where r.workspace_id = $1
		order by r.updated_at, r.id`, workspaceID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer rows.Close()
	var body []byte
	for rows.Next() {
		var (
			id, issueID, agentID, model, outcome, summary string
			in, out, cost                                 int64
			updated                                       time.Time
		)
		if err := rows.Scan(&id, &issueID, &agentID, &model, &in, &out, &cost, &outcome, &summary, &updated); err != nil {
			a.internalError(w, err)
			return
		}
		line, err := json.Marshal(map[string]any{
			"id": id, "issueId": issueID, "agentId": agentID, "model": model,
			"inputTokens": in, "outputTokens": out, "costMicros": cost,
			"outcome": outcome, "summary": summary,
			"updatedAt": updated.UTC().Format(time.RFC3339),
		})
		if err != nil {
			a.internalError(w, err)
			return
		}
		body = append(body, line...)
		body = append(body, '\n')
	}
	if err := rows.Err(); err != nil {
		a.internalError(w, err)
		return
	}
	mac := hmac.New(sha256.New, []byte(a.cfg.TraceSigningKey))
	_, _ = mac.Write(body)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Converge-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// signTrace is the export's signature, split out so tests can recompute it.
func signTrace(key string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
