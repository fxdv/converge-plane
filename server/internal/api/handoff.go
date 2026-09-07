// handoff.go — D1: the agent handoff protocol (docs/spec/12).
// spec cs:swarm:handoff
// spec cs:swarm:guards
// spec cs:swarm:escalation
//
// A handoff is an atomic work transition on an issue: new assignee +
// state move + bounded summary, in one transaction and one board-visible
// event. It is the only agent-to-agent channel the system has (rule 1:
// mediated communication — the actor is stamped from the session, never
// taken from the request), and the trace it leaves (issue_handoffs +
// issue_history) is what the swarm panel and LLM runtime build on.
//
// Escalation (confirmed owner default): when the quiet guards trip the
// issue PAUSES — no agent may act on it, a human is pointed at it (the
// client's "needs human" badge), and a human mutation resumes it. A
// stuck swarm stops spending; it does not decorate the board.
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"converge/internal/auth"
)

// The quiet guards (spec 12 rule 3).
const (
	// humanReviewStateName is the reserved parking column: where a paused
	// card waits for its human. It is a product contract, not a label —
	// the escalation, the swarm queue, the agent guard, and the panel all
	// key off this one name (docs/spec/12 escalation):
	//
	//   - Escalation moves the card there: every pause (quiet guard, LLM
	//     pause, deterministic dead-end) parks the issue in the team's
	//     Human Review state when the team has one, so the board shows
	//     where it waits instead of leaving the signal scattered.
	//   - The swarm's queue excludes it: no worker picks a card out of
	//     Human Review, and agents get a 422 on every mutation of a card
	//     in it — parked means parked, whatever the pause flag says.
	//   - A human leaves it only on purpose: a human mutation on a paused
	//     card resumes the swarm and wakes the assignee; a card a human
	//     parked there (flag already clear) waits until the human moves it
	//     back, reassigns it, or closes it.
	//   - The panel's needs-human set is the pause flag OR the column: a
	//     card shows up for as long as it waits on a human, by either
	//     signal.
	humanReviewStateName = "Human Review"

	// handoffSummaryMaxBytes caps a handoff summary (spec 12 rule 2:
	// bounded context for the next agent; the same cap is the consumer's
	// prompt-injection budget in the D3 runtime).
	handoffSummaryMaxBytes = 4096
	// guardWindow bounds the quiet guards.
	guardWindow = 24 * time.Hour
	// guardWindowSQL is guardWindow as a SQL interval literal (Postgres
	// parses text intervals; keeping the pair together keeps the Go and
	// SQL windows from drifting apart).
	guardWindowSQL = "24 hours"
	// handoffLoopThreshold: an account receiving the issue this many
	// times within guardWindow is the ping-pong signature. The
	// threshold-1th delivery still lands; the next one trips the
	// breaker. A two-agent ping-pong trips within ~7 hops.
	handoffLoopThreshold = 3
	// issueOpBudget: agent-authored activity rows per issue within
	// guardWindow. Per-account rate limiting (M6) bounds one agent;
	// this bounds the swarm's combined output on one issue.
	issueOpBudget = 50
)

var (
	// humanReviewStateSQL is the reserved name lower-cased as a SQL
	// literal. It is a server constant — never user input — so embedding
	// it in queries is safe; the parameterized lookups below reuse it.
	humanReviewStateSQL = "'" + strings.ToLower(humanReviewStateName) + "'"
	// needsHumanSQL is the panel's needs-human predicate (issue alias i,
	// status alias ws): the swarm paused the card, or it sits in the
	// Human Review column.
	needsHumanSQL = "(i.agent_paused or lower(coalesce(ws.name, '')) = " + humanReviewStateSQL + ")"
	// notHumanReviewSQL is the swarm queue's exclusion clause (the same
	// aliases): an agent never works a card in Human Review.
	notHumanReviewSQL = "not (lower(coalesce(ws.name, '')) = " + humanReviewStateSQL + ")"
)

// agentPausedGuard rejects agent mutations on a paused issue and on an
// issue parked in Human Review. Human mutations are allowed everywhere
// (they resume the issue where it matters); agents are the swarm being
// constrained. Returns false when the response was written and the
// handler must stop.
func (a *API) agentPausedGuard(ctx context.Context, w http.ResponseWriter, p *Principal, row issueRow) bool {
	if p.Kind != auth.AccountKindAgent {
		return true
	}
	if row.AgentPaused {
		writeError(w, http.StatusUnprocessableEntity, "issue is paused for human review; agents cannot act on it")
		return false
	}
	// The column is a signal independent of the flag: a card a human
	// parked in Human Review (pause flag already clear) is still parked.
	if row.StatusID != nil && *row.StatusID != "" && a.issueInHumanReview(ctx, row.ID, row.TeamID) {
		writeError(w, http.StatusUnprocessableEntity, "issue is in Human Review; agents cannot act on it")
		return false
	}
	return true
}

type handoffRequest struct {
	ToAccountID string `json:"toAccountId"`
	StateID     string `json:"stateId"`
	Summary     string `json:"summary"`
}

// handleHandoff implements POST /api/v1/issues/{id}/handoff.
//
// Policy: the requester must be the current assignee or a workspace
// admin. (D3's foreman topology layers dispatch policy on this same
// endpoint; v1 needs no topology concept to be safe.)
func (a *API) handleHandoff(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var req handoffRequest
	if err := jsonDecode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	row, workspaceID, ok := a.issueAccess(ctx, p, id)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !a.agentPausedGuard(ctx, w, p, row) {
		return
	}

	// Rule 2: the summary is the bounded context the next agent works
	// from. Required and size-capped (the server enforces the cap the
	// consumer is told to treat as untrusted data).
	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		writeError(w, http.StatusUnprocessableEntity, "summary is required")
		return
	}
	if len(summary) > handoffSummaryMaxBytes {
		writeError(w, http.StatusUnprocessableEntity, "summary exceeds the 4KB cap")
		return
	}
	// Rule 1: the target is a machine identity — an active agent member
	// of the same workspace, never the requester itself.
	if !isUUID(req.ToAccountID) || req.ToAccountID == p.AccountID {
		writeError(w, http.StatusUnprocessableEntity, "handoff target must be another account")
		return
	}
	if !a.agentMember(ctx, workspaceID, req.ToAccountID) {
		writeError(w, http.StatusUnprocessableEntity, "handoff target must be an active agent member")
		return
	}
	// Optional state move: must be one of the team's workflow statuses.
	stateID := strings.TrimSpace(req.StateID)
	if stateID != "" && !a.statusExistsForTeam(ctx, stateID, row.TeamID) {
		writeError(w, http.StatusUnprocessableEntity, "stateId must be one of the team workflow statuses")
		return
	}
	// Dispatch policy (see above).
	if strval(row.AssigneeID) != p.AccountID {
		role, _ := a.workspaceRole(ctx, p, workspaceID)
		if !adminRole(role) {
			writeError(w, http.StatusUnprocessableEntity, "only the current assignee or a workspace admin can hand off this issue")
			return
		}
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The same per-team lock as every other issue mutation: the guard
	// reads and the apply write are serialized with each other.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_issue_"+row.TeamID); err != nil {
		a.internalError(w, err)
		return
	}

	// Rule 3: the quiet guards, checked under the lock against the
	// freshest state. A trip pauses the issue (committed) and rejects
	// this handoff — the escalation is the response.
	if reason, tripped, err := a.checkHandoffGuardsTx(ctx, tx, row.ID, req.ToAccountID); err != nil {
		a.internalError(w, err)
		return
	} else if tripped {
		recs, err := a.pauseIssueTx(ctx, tx, workspaceID, row, p, reason, guardPauseNote(reason))
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
		a.log.Warn("issue paused by handoff guard", "issue", row.ID,
			"workspace", workspaceID, "reason", reason, "by", p.Email)
		writeError(w, http.StatusUnprocessableEntity, "quiet guard tripped; issue paused for human review: "+reason)
		return
	}

	issueRec, histRec, fresh, err := a.applyHandoffTx(ctx, tx, workspaceID, row,
		p.AccountID, req.ToAccountID, stateID, summary)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(issueRec)
	a.broadcastRecord(histRec)
	// D3 fast path: the handoff's target is (validated above) an active
	// agent — wake its worker now; the tick backstop covers a dropped
	// wake. The pause branch above deliberately does not wake: a paused
	// issue waits for a human, whose mutation re-wakes.
	a.runtime.Wake(workspaceID, req.ToAccountID)
	writeJSON(w, http.StatusOK, a.issueData(fresh))
}

// applyHandoffTx applies the handoff transition inside the caller's
// transaction: the team lock is held and the quiet guards have already
// been read under it (checkHandoffGuardsTx). It writes the
// issue_handoffs trace row, the assignee (+state) update — resuming the
// issue when a human unblocked it — the history row carrying the
// summary, and the refreshed issue outbox record, and returns the
// refreshed row. Shared by the API endpoint and the runtime (D3): one
// apply path, one trace shape, identical boards.
func (a *API) applyHandoffTx(ctx context.Context, tx pgx.Tx, workspaceID string, row issueRow, actorID, toAccountID, stateID, summary string) (issueRec, histRec syncActionRecord, fresh issueRow, err error) {
	var handoffStateID *string
	if stateID != "" {
		handoffStateID = &stateID
	}
	if _, err = tx.Exec(ctx, `
		insert into issue_handoffs (workspace_id, issue_id, from_account_id, to_account_id, state_id, summary)
		values ($1, $2, $3, $4, $5, $6)`,
		workspaceID, row.ID, actorID, toAccountID, handoffStateID, summary); err != nil {
		return syncActionRecord{}, syncActionRecord{}, issueRow{}, err
	}
	updateSQL := "update issues set assignee_id = $2, version = version + 1, updated_at = now()"
	updateArgs := []any{row.ID, toAccountID}
	if stateID != "" {
		updateSQL += ", status_id = $3"
		updateArgs = append(updateArgs, stateID)
	}
	if row.AgentPaused {
		// A human handoff on a paused issue resumes it (D1 escalation
		// recovery). Agent handoffs always run on unpaused issues.
		updateSQL += ", agent_paused = false"
	}
	updateSQL += " where id = $1"
	if _, err = tx.Exec(ctx, updateSQL, updateArgs...); err != nil {
		return syncActionRecord{}, syncActionRecord{}, issueRow{}, err
	}
	histRec, err = a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID,
		actorID, "handoff", "assignee", strval(row.AssigneeID), toAccountID, summary)
	if err != nil {
		return syncActionRecord{}, syncActionRecord{}, issueRow{}, err
	}
	fresh, err = a.issueByIDTx(ctx, tx, row.ID)
	if err != nil {
		return syncActionRecord{}, syncActionRecord{}, issueRow{}, err
	}
	issueRec, err = a.emitChange(ctx, tx, workspaceID, "Issue", row.ID, "UPDATE", a.issueData(fresh))
	if err != nil {
		return syncActionRecord{}, syncActionRecord{}, issueRow{}, err
	}
	if err = a.refreshOutboxTx(ctx, tx, workspaceID, &issueRec, a.issueData(fresh)); err != nil {
		return syncActionRecord{}, syncActionRecord{}, issueRow{}, err
	}
	return issueRec, histRec, fresh, nil
}

// checkHandoffGuardsTx evaluates the quiet guards (spec 12 rule 3)
// under the caller's lock. It returns a human-readable reason and
// tripped=true when the issue must pause; the pause itself is applied
// by pauseIssueTx in the same transaction.
func (a *API) checkHandoffGuardsTx(ctx context.Context, tx pgx.Tx, issueID, targetAccountID string) (reason string, tripped bool, err error) {
	// Loop guard: the target receiving the issue again and again within
	// the window is the ping-pong signature.
	var loopCount int
	if err = tx.QueryRow(ctx, `
		select count(*) from issue_handoffs
		where issue_id = $1 and to_account_id = $2 and created_at > now() - $3::interval`,
		issueID, targetAccountID, guardWindowSQL).Scan(&loopCount); err != nil {
		return "", false, err
	}
	// Operation budget: the swarm's combined output on one issue.
	var opCount int
	if err = tx.QueryRow(ctx, `
		select count(*) from issue_history h
		join accounts a on a.id = h.actor_id
		where h.issue_id = $1 and a.kind = $2 and h.created_at > now() - $3::interval`,
		issueID, auth.AccountKindAgent, guardWindowSQL).Scan(&opCount); err != nil {
		return "", false, err
	}
	reason, tripped = guardVerdict(loopCount, opCount)
	return
}

// guardVerdict applies the quiet-guard thresholds (spec 12 rule 3) to
// the two counts the guards read. Pure, so the thresholds are
// unit-testable without a database.
func guardVerdict(loopCount, opCount int) (reason string, tripped bool) {
	if loopCount >= handoffLoopThreshold {
		return fmt.Sprintf("handoff loop detected (the same agent received this issue %d times in 24h)", loopCount), true
	}
	if opCount >= issueOpBudget {
		return fmt.Sprintf("operation budget exhausted (%d agent operations in 24h)", opCount), true
	}
	return "", false
}

// pauseIssueTx escalates an issue in place — the Human Review protocol
// (docs/spec/12 escalation), in the caller's transaction under the team
// lock. It writes, in order:
//
//  1. the pause flag; the issue moves to the team's Human Review state
//     when the team has one — the board shows where the card waits;
//  2. a status history row (when it moved) and the paused history row
//     whose summary IS the reason — the timeline shows why the swarm
//     stopped;
//  3. the human-handoff comment: the reason plus the exact path — reply
//     with your decision, then move the card out of Human Review to
//     resume the swarm (or take it over / close it). On resume the
//     swarm re-decides with the discussion in its prompt context;
//  4. an audit event;
//  5. the refreshed issue record and the comment on the sync feed; the
//     comment carries the reason plus, when the pausing side supplied
//     one, the task-level "where things stand" note (D4).
//
// Returns the wire records for the caller to broadcast after commit.
func (a *API) pauseIssueTx(ctx context.Context, tx pgx.Tx, workspaceID string, row issueRow, p *Principal, reason, note string) (recs []syncActionRecord, err error) {
	// The reserved parking column; "" when the team has none (the pause
	// then flags in place and the comment omits the column claim).
	hrID, err := a.humanReviewStatusIDTx(ctx, tx, row.TeamID)
	if err != nil {
		return nil, err
	}
	statusMove := hrID != "" && hrID != strval(row.StatusID)

	updateSQL := "update issues set agent_paused = true, version = version + 1, updated_at = now()"
	args := []any{row.ID}
	if statusMove {
		updateSQL += ", status_id = $2"
		args = append(args, hrID)
	}
	updateSQL += " where id = $1"
	if _, err = tx.Exec(ctx, updateSQL, args...); err != nil {
		return nil, err
	}

	recs = make([]syncActionRecord, 0, 4)
	if statusMove {
		rec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID, p.AccountID,
			"updated", "status", strval(row.StatusID), hrID, "")
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	histRec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID,
		p.AccountID, "paused", "agent_paused", "false", "true", reason)
	if err != nil {
		return nil, err
	}
	recs = append(recs, histRec)
	if err = a.auditTx(ctx, tx, workspaceID, p.AccountID, "issue.paused", "Issue", row.ID); err != nil {
		return nil, err
	}
	fresh, err := a.issueByIDTx(ctx, tx, row.ID)
	if err != nil {
		return nil, err
	}
	issueRec, err := a.emitChange(ctx, tx, workspaceID, "Issue", row.ID, "UPDATE", a.issueData(fresh))
	if err != nil {
		return nil, err
	}
	recs = append(recs, issueRec)
	if err = a.refreshOutboxTx(ctx, tx, workspaceID, &issueRec, a.issueData(fresh)); err != nil {
		return nil, err
	}
	// Server-composed text: the reason is fenced at its source (the
	// guard's strings, the model's capped comment, the deterministic
	// template) and the path is fixed. Every pause surface — the handoff
	// guard, the runtime guard, the LLM pause, the dead-end pause — goes
	// through this one choke point, so every escalation the swarm can
	// produce carries the same disclosure.
	commentRec, err := a.applyCommentTx(ctx, tx, workspaceID, row.ID, p.AccountID,
		humanHandoffComment(reason, hrID != "", note))
	if err != nil {
		return nil, err
	}
	recs = append(recs, commentRec)
	return recs, nil
}

// humanReviewStatusIDTx resolves the team's Human Review state (the
// reserved name, case-insensitive); "" when the team has none.
func (a *API) humanReviewStatusIDTx(ctx context.Context, q queryer, teamID string) (string, error) {
	var id string
	err := q.QueryRow(ctx,
		"select id from workflow_statuses where team_id = $1 and lower(name) = $2",
		teamID, strings.ToLower(humanReviewStateName)).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return id, nil
}

// issueInHumanReview reports whether the issue sits in the team's Human
// Review state (the reserved name, case-insensitive).
func (a *API) issueInHumanReview(ctx context.Context, issueID, teamID string) bool {
	var exists bool
	err := a.pool.QueryRow(ctx, `
		select exists(
			select 1 from issues i
			join workflow_statuses ws on ws.id = i.status_id
			where i.id = $1 and i.team_id = $2 and lower(ws.name) = $3)`,
		issueID, teamID, strings.ToLower(humanReviewStateName)).Scan(&exists)
	return err == nil && exists
}

// humanHandoffComment renders the human path an escalation leaves on
// the card: why the swarm stopped (the reason), where things stand
// (the task-level note, when the pausing side supplied one), and
// exactly what a human does next. The comment is the breadcrumb the
// human was missing — the signal (badge / panel) says WHERE; this says
// WHAT TO DO. inReview is false for teams without a Human Review state
// (the pause flags in place; the column claim is omitted, not wrong).
func humanHandoffComment(reason string, inReview bool, note string) string {
	var b strings.Builder
	if inReview {
		b.WriteString("🧑 Human handoff — the swarm parked this card in Human Review for you.\n\n")
	} else {
		b.WriteString("⚠️ Human handoff — the swarm paused this card for you.\n\n")
	}
	b.WriteString("Why: " + reason + "\n\n")
	if note != "" {
		b.WriteString("Where things stand: " + note + "\n\n")
	}
	b.WriteString("To resume the swarm:\n")
	b.WriteString("- Reply here with your decision \u2014 the swarm reads your comments when it resumes.\n")
	if inReview {
		b.WriteString("- Then move the card back into the workflow (e.g. In Progress). That move is the resume.\n")
	} else {
		b.WriteString("- Then make any change to the card (move it, or reassign it). That change is the resume.\n")
	}
	b.WriteString("- Or assign the card to yourself to take it over \u2014 or move it to Done / Canceled to close it.\n\n")
	if inReview {
		b.WriteString("While the card sits in Human Review, no agent can act on it.")
	} else {
		b.WriteString("While the card is paused, no agent can act on it.")
	}
	return b.String()
}

// agentMember reports whether the account is an active member of the
// workspace with the machine identity (kind = agent).
func (a *API) agentMember(ctx context.Context, workspaceID, accountID string) bool {
	var exists bool
	err := a.pool.QueryRow(ctx, `
		select exists(
			select 1
			from workspace_members wm
			join accounts a on a.id = wm.account_id
			where wm.workspace_id = $1 and wm.account_id = $2 and wm.status = 'active' and a.kind = $3)`,
		workspaceID, accountID, auth.AccountKindAgent).Scan(&exists)
	return err == nil && exists
}

// guardPauseNote renders the "where things stand" block for a
// quiet-guard pause (D4): the guard is a policy limit, not a failure
// of the work — the note tells a human with zero context what stopped
// the swarm and what the decision is, including the rolling-window
// trap.
func guardPauseNote(reason string) string {
	return "A quiet guard stopped the swarm on this card — a safety limit, not a failure of the work. " + reason + ". The card is exactly where the swarm left it, only paused; nothing was lost. Note the guard counts a rolling 24-hour window: resuming while the pattern is still in the window will pause the card again, so change what keeps tripping it (re-route the handoffs, or let the window clear) before you resume."
}
