// review.go — the foreman review protocol (docs/spec/12).
// spec cs:swarm:review
// spec cs:swarm:escalation
//
// A pause is the swarm asking a human a question. The question it asks
// most, unasked, is what happens to a card the human resumed that the
// swarm cannot make progress on: the swarm parks it again, the human
// resumes again, and the card cycles through Human Review — decorating
// the queue with noise the reply never changed.
//
// The review protocol breaks that cycle with one rule, applied in two
// places:
//
//   - The work cycle's circuit breaker (pauseWithBreakerTx): a pause
//     that would put the issue at or over the park threshold within the
//     shared 24h guard window does not park the card — it ESCALATES it
//     to the human foreman: reassign, In Progress, pause cleared, full
//     disclosure. Every pause surface (LLM pause, advance-to-Human
//     Review conversion, quiet-guard trip) flows through the same choke
//     point, so no path can re-park a cycled card.
//   - The standing review tick (reviewLoop): on an interval
//     (CONVERGE_SWARM_REVIEW_INTERVAL, default 30m, 0 = off, an
//     immediate pass on boot) the runtime reviews every card the swarm
//     has paused: a human reply since the last park RESUMES the swarm
//     (one resume per reply — the reply is new input, so the swarm
//     re-decides with it in context); a card at the park threshold
//     without a reply is ESCALATED the same way the breaker does.
//
// Escalation is terminal for the swarm: the card is assigned to a human
// and the swarm's queue is agent-assigned (nextIssue / fleetSnapshot
// require an agent assignee), so no worker can ever touch it again.
// The cycle ends by construction, not by promise. A human who wants the
// swarm back reassigns the card to an agent; the 24h window then
// clears and the swarm gets its full threshold of attempts again.
//
// The tick acts as the workspace's human foreman (the owner, else the
// oldest admin): the standing delegation is the human's, and every
// action it takes is a history row + a comment + the outbox — the same
// trace shape as any other mutation, so the duty is visible in the
// activity feed and the timeline, and the in-memory last-run indicator
// reports it on the swarm plane.

package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"converge/internal/auth"
)

// reviewParkThreshold: an issue parked this many times within the shared
// 24h guard window is out of the swarm's hands — the next pause
// escalates it to the human foreman instead of parking. Three is the
// same rolling-window family as the quiet guards (handoff.go): one park
// is a question, two is a pattern, three is a cycle.
const reviewParkThreshold = 3

// reviewStale: a parked card with no human reply is a question the human
// never answered. After one full work session of silence the standing
// duty resolves it — the card cannot wait forever (a cycle that the 24h
// counter has aged out of would otherwise sit in the queue indefinitely:
// the count rolled off, no reply exists, nothing re-fires). Stale means
// the same as cycled: the card becomes the human's work, with the
// disclosure, and the swarm lets go.
const reviewStale = 4 * time.Hour

// reviewVerdict is what the protocol does with one parked card.
type reviewVerdict int

const (
	// reviewNone: the card waits. It parked below the threshold and no
	// human has replied — a human review (in the UI, or the next tick
	// after they do) is the next step.
	reviewNone reviewVerdict = iota
	// reviewResume: a human replied since the last park; the swarm
	// resumes with the reply in its prompt context.
	reviewResume
	// reviewEscalate: the cycle is broken; the card becomes the human
	// foreman's work.
	reviewEscalate
)

// reviewFacts is the trusted state of one parked card, read under the
// team lock (the same freshness contract as the work cycle's snapshot).
type reviewFacts struct {
	// ParkCount24h is the issue's pause history rows within the guard
	// window — its entries into the parked state.
	ParkCount24h int
	// LastParkAt is the newest pause (zero time when the issue never
	// parked — not a reviewable card).
	LastParkAt time.Time
	// RepliesSincePark is the count of human-authored comments after
	// the last park: the human's answer to the swarm's question.
	RepliesSincePark int
}

// decideReview is the protocol's decision table. Pure, so it is
// unit-testable without a database. The reply rule wins: a human who
// answered the swarm's latest question wins the cycle counter — their
// word is new input, and the swarm may try once more (the re-park it
// then makes, without a further reply, is what the counter and the
// staleness rule catch).
func decideReview(f reviewFacts) reviewVerdict {
	if f.RepliesSincePark > 0 {
		return reviewResume
	}
	if cycleEscalates(f.ParkCount24h, f.LastParkAt, f.RepliesSincePark) {
		return reviewEscalate
	}
	return reviewNone
}

// cycleEscalates is the protocol's one rule: a park (or a card already
// parked) whose question the human never answered is out of the swarm's
// hands. It fires on the cycle counter — parks including the one at
// hand at or over the threshold within the guard window — or on
// staleness: the last park is a full work session old and unanswered.
// The reply condition is the caller's to apply first (a fresh reply is
// new input); this rule assumes there is none. parksIncludingCurrent
// counts the park being written (breaker) or already in place (tick), so
// both call sites share the function. A never-parked card (zero time)
// is never stale: a first park is a question, not a cycle.
func cycleEscalates(parksIncludingCurrent int, lastParkAt time.Time, repliesSincePark int) bool {
	if repliesSincePark > 0 {
		return false
	}
	if parksIncludingCurrent >= reviewParkThreshold {
		return true
	}
	return !lastParkAt.IsZero() && time.Since(lastParkAt) >= reviewStale
}

// reviewFactsTx reads the review facts for one issue in the caller's
// transaction. One query: the windowed pause count, the last park's
// timestamp, and the human replies after it (the card's history is
// small; the scalar subqueries cost nothing).
func (a *API) reviewFactsTx(ctx context.Context, q queryer, issueID string) (reviewFacts, error) {
	var f reviewFacts
	err := q.QueryRow(ctx, `
		with park as (
			select count(*)::int  as n24,
			       coalesce(max(created_at), to_timestamp(0)) as last
			from issue_history
			where issue_id = $1
			  and action = 'paused'
			  and created_at > now() - $2::interval
		)
		select park.n24, park.last,
		       (select count(*)::int
		        from comments c
		        join accounts ac on ac.id = c.author_id
		        where c.issue_id = $1
		          and ac.kind = $3
		          and c.created_at > park.last)::int
		from park`, issueID, guardWindowSQL, auth.AccountKindHuman).Scan(
		&f.ParkCount24h, &f.LastParkAt, &f.RepliesSincePark)
	if err != nil {
		return reviewFacts{}, err
	}
	return f, nil
}

// foremanHumanIDTx resolves the workspace's HUMAN foreman: the owner
// while an active member, else the oldest active admin — the account
// the protocol acts as, and the escalation's assignee. "" when the
// workspace has no humans (an agents-only workspace cannot be
// escalated to; the caller parks as usual).
func (a *API) foremanHumanIDTx(ctx context.Context, q queryer, workspaceID string) (string, error) {
	var id string
	err := q.QueryRow(ctx, `
		select wm.account_id::text
		from workspace_members wm
		join accounts ac on ac.id = wm.account_id
		where wm.workspace_id = $1
		  and wm.status = 'active'
		  and ac.status = 'active'
		  and ac.kind = $2
		  and wm.role in ('owner', 'admin')
		order by case wm.role when 'owner' then 0 else 1 end, wm.created_at
		limit 1`, workspaceID, auth.AccountKindHuman).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return id, nil
}

// inProgressStatusIDTx resolves the team's In Progress state — the
// review's target for both resume and escalate (the card leaves the
// parking column and sits where work lives). The first STARTED state
// in workflow order; "" when the team has none (the action then clears
// the pause and keeps the card in its state).
func (a *API) inProgressStatusIDTx(ctx context.Context, q queryer, teamID string) (string, error) {
	var id string
	err := q.QueryRow(ctx,
		"select id from workflow_statuses where team_id = $1 and category = 'STARTED' order by position limit 1",
		teamID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return id, nil
}

// escalationComment renders the human-visible disclosure the protocol
// writes when it resolves a parked card. Server-composed from fenced
// parts (the reason is already fenced at its source; the fence is
// idempotent): a human with zero context reads why the swarm stopped,
// what the swarm last reported, and what the card is now.
func escalationComment(issueNumber, parks24h int, reason, note, target string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "🧑 Foreman review — the swarm parked this card %d times in 24h without a new human reply; the cycle is broken.\n\n", parks24h)
	if r := fenceSummary(reason); r != "" {
		b.WriteString("Last swarm report: " + r + "\n\n")
	}
	if n := fenceSummary(note); n != "" {
		b.WriteString("Where things stand: " + n + "\n\n")
	}
	b.WriteString("Card #" + strconv.Itoa(issueNumber) + " moves to " + target + " and is assigned to the foreman as their work. ")
	b.WriteString("The swarm will not act on it again — agents never work a card a human owns. ")
	b.WriteString("(Automated review, on the foreman's standing delegation.)")
	return b.String()
}

// resumeComment renders the disclosure for a resumed card.
func resumeComment(issueNumber int) string {
	return fmt.Sprintf("🧑 Foreman review — a human replied on card #%d since the swarm parked it; the swarm resumes with the reply in its context. (Automated review, on the foreman's standing delegation.)", issueNumber)
}

// pauseWithBreakerTx is the pause choke point with the cycle breaker:
// every escalation the system can produce (LLM pause, advance-to-Human
// Review, quiet-guard trip) parks through pauseIssueTx — unless the
// park would put the issue at the threshold within the guard window,
// in which case it escalates the card to the human foreman instead
// (reviewEscalate below, the same transaction). It reports
// tripped=true either way: the issue is no longer actionable for the
// agent (parked or handed over), so the worker moves on.
func (a *API) pauseWithBreakerTx(ctx context.Context, tx pgx.Tx, workspaceID string, row issueRow, p *Principal, reason, note string) (recs []syncActionRecord, tripped bool, outReason string, err error) {
	facts, err := a.reviewFactsTx(ctx, tx, row.ID)
	if err != nil {
		return nil, false, "", err
	}
	if cycleEscalates(facts.ParkCount24h+1, facts.LastParkAt, facts.RepliesSincePark) {
		if human, herr := a.foremanHumanIDTx(ctx, tx, workspaceID); herr == nil && human != "" {
			recs, err := a.escalateIssueTx(ctx, tx, workspaceID, row, p, human, reason, note, facts)
			if err != nil {
				return nil, false, "", err
			}
			return recs, true,
				fmt.Sprintf("escalated to the human foreman (park %d within 24h)", facts.ParkCount24h+1), nil
		}
		// No human in the workspace to hand to: park as usual (an
		// agents-only deployment has no escalation target; the quiet
		// guards and the flag keep it out of the swarm's queue).
	}
	recs, err = a.pauseIssueTx(ctx, tx, workspaceID, row, p, reason, note)
	if err != nil {
		return nil, false, "", err
	}
	return recs, true, reason, nil
}

// escalateIssueTx hands a cycled card to its target, in the caller's
// transaction under the team lock: the swarm's escalation is closed
// (the pause flag clears), the card is reassigned to the target (the
// human foreman) and moved to In Progress when the team has one, and
// the timeline gets the disclosure (a history row for each changed
// field, a comment, an audit event, the outbox). actor is the trace's
// author: the agent that hit the breaker in the work cycle, or the
// human foreman when the standing tick acts. The caller broadcasts the
// records after commit.
func (a *API) escalateIssueTx(ctx context.Context, tx pgx.Tx, workspaceID string, row issueRow, actor *Principal, targetID, reason, note string, facts reviewFacts) (recs []syncActionRecord, err error) {
	target, err := a.inProgressStatusIDTx(ctx, tx, row.TeamID)
	if err != nil {
		return nil, err
	}
	targetName := "the current state"
	if target != "" {
		targetName = "In Progress"
	}

	updateSQL := "update issues set agent_paused = false, assignee_id = $2, version = version + 1, updated_at = now()"
	args := []any{row.ID, targetID}
	if target != "" && target != strval(row.StatusID) {
		updateSQL += ", status_id = $3"
		args = append(args, target)
	}
	updateSQL += " where id = $1"
	if _, err = tx.Exec(ctx, updateSQL, args...); err != nil {
		return nil, err
	}

	disclosure := escalationComment(row.Number, facts.ParkCount24h+1, reason, note, targetName)
	recs = make([]syncActionRecord, 0, 6)
	if from, to := strval(row.AssigneeID), targetID; from != to {
		rec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID, actor.AccountID,
			"updated", "assignee", from, to, reason)
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	rec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID, actor.AccountID,
		"resumed", "agent_paused", "true", "false", reason)
	if err != nil {
		return nil, err
	}
	recs = append(recs, rec)
	if target != "" && target != strval(row.StatusID) {
		rec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID, actor.AccountID,
			"updated", "status", strval(row.StatusID), target, "")
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	if err = a.auditTx(ctx, tx, workspaceID, actor.AccountID, "issue.escalated", "Issue", row.ID); err != nil {
		return nil, err
	}
	commentRec, err := a.applyCommentTx(ctx, tx, workspaceID, row.ID, actor.AccountID, disclosure)
	if err != nil {
		return nil, err
	}
	recs = append(recs, commentRec)
	fresh, err := a.issueByIDTx(ctx, tx, row.ID)
	if err != nil {
		return nil, err
	}
	issueRec, err := a.emitChange(ctx, tx, workspaceID, "Issue", row.ID, "UPDATE", a.issueData(fresh))
	if err != nil {
		return nil, err
	}
	recs = append(recs, issueRec)
	return recs, a.refreshOutboxTx(ctx, tx, workspaceID, &issueRec, a.issueData(fresh))
}

// resumeIssueTx resumes a parked card the human has answered, in the
// caller's transaction under the team lock: the pause flag clears, the
// card moves to In Progress when the team has one (a parked card in
// the Human Review column is out of the swarm's queue by name — the
// move is what puts it back in it), and the timeline gets the
// breadcrumb. The caller broadcasts after commit and wakes the
// assignee (the reply is new input; the swarm re-decides with it).
func (a *API) resumeIssueTx(ctx context.Context, tx pgx.Tx, workspaceID string, row issueRow, p *Principal) (recs []syncActionRecord, err error) {
	target, err := a.inProgressStatusIDTx(ctx, tx, row.TeamID)
	if err != nil {
		return nil, err
	}
	updateSQL := "update issues set agent_paused = false, version = version + 1, updated_at = now()"
	args := []any{row.ID}
	if target != "" && target != strval(row.StatusID) {
		updateSQL += ", status_id = $2"
		args = append(args, target)
	}
	updateSQL += " where id = $1"
	if _, err = tx.Exec(ctx, updateSQL, args...); err != nil {
		return nil, err
	}

	recs = make([]syncActionRecord, 0, 5)
	rec, err := a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID, p.AccountID,
		"resumed", "agent_paused", "true", "false", "a human replied; the swarm resumes")
	if err != nil {
		return nil, err
	}
	recs = append(recs, rec)
	if target != "" && target != strval(row.StatusID) {
		rec, err = a.writeHistoryTx(ctx, tx, workspaceID, row.TeamID, row.ID, p.AccountID,
			"updated", "status", strval(row.StatusID), target, "")
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	if err = a.auditTx(ctx, tx, workspaceID, p.AccountID, "issue.resumed", "Issue", row.ID); err != nil {
		return nil, err
	}
	commentRec, err := a.applyCommentTx(ctx, tx, workspaceID, row.ID, p.AccountID, resumeComment(row.Number))
	if err != nil {
		return nil, err
	}
	recs = append(recs, commentRec)
	fresh, err := a.issueByIDTx(ctx, tx, row.ID)
	if err != nil {
		return nil, err
	}
	issueRec, err := a.emitChange(ctx, tx, workspaceID, "Issue", row.ID, "UPDATE", a.issueData(fresh))
	if err != nil {
		return nil, err
	}
	recs = append(recs, issueRec)
	return recs, a.refreshOutboxTx(ctx, tx, workspaceID, &issueRec, a.issueData(fresh))
}

// reviewIssue resolves one swarm-parked card through the protocol: one
// transaction, the locked core (reviewIssueLocked), a commit, the
// broadcast — and, for a resume, the wake that puts the reply in front
// of the swarm. It returns the verdict that committed (reviewNone when
// the card moved on in between: a concurrent human or worker mutation
// wins, and the review commits nothing).
func (rt *AgentRuntime) reviewIssue(ctx context.Context, workspaceID, issueID, teamID string) (reviewVerdict, error) {
	a := rt.a
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return reviewNone, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	verdict, recs, err := a.reviewIssueLocked(ctx, tx, workspaceID, issueID, teamID)
	if err != nil {
		return reviewNone, err
	}
	if err := tx.Commit(ctx); err != nil {
		return reviewNone, err
	}
	for i := range recs {
		a.broadcastRecord(recs[i])
	}
	if verdict == reviewResume {
		a.wakeIssueOwner(ctx, workspaceID, issueID)
	}
	return verdict, nil
}

// reviewIssueLocked is the protocol's locked core: the team lock, the
// row re-read under it (it must still be the paused card the scan saw),
// the facts under the same lock, the verdict applied. The caller holds
// the open transaction and commits after (an unapplied verdict commits
// nothing but an empty transaction).
func (a *API) reviewIssueLocked(ctx context.Context, tx pgx.Tx, workspaceID, issueID, teamID string) (reviewVerdict, []syncActionRecord, error) {
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_issue_"+teamID); err != nil {
		return reviewNone, nil, err
	}
	var row issueRow
	if err := a.loadIssueRowTx(ctx, tx, issueID, &row); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Deleted between the scan and the lock: it moved on.
			return reviewNone, nil, nil
		}
		return reviewNone, nil, err
	}
	if row.Status != "active" || !row.AgentPaused {
		// It un-parked between the scan and the lock (a human acted):
		// nothing to do, and acting would race them.
		return reviewNone, nil, nil
	}
	facts, err := a.reviewFactsTx(ctx, tx, issueID)
	if err != nil {
		return reviewNone, nil, err
	}
	verdict := decideReview(facts)
	if verdict == reviewNone {
		return reviewNone, nil, nil
	}
	// The protocol acts as the human foreman: the standing delegation
	// is the human's, and their account is the trace's actor.
	foreman, err := a.foremanHumanIDTx(ctx, tx, workspaceID)
	if err != nil {
		return reviewNone, nil, err
	}
	if foreman == "" {
		// No human to resume with or hand to: the card keeps waiting.
		return reviewNone, nil, nil
	}
	p := &Principal{AccountID: foreman, Kind: auth.AccountKindHuman}
	switch verdict {
	case reviewResume:
		recs, err := a.resumeIssueTx(ctx, tx, workspaceID, row, p)
		if err != nil {
			return reviewNone, nil, err
		}
		return verdict, recs, nil
	case reviewEscalate:
		recs, err := a.escalateIssueTx(ctx, tx, workspaceID, row, p, foreman,
			"parked repeatedly without a human reply", "", facts)
		if err != nil {
			return reviewNone, nil, err
		}
		return verdict, recs, nil
	default:
		return reviewNone, nil, nil
	}
}

// reviewPass reviews every card the swarm has paused in the workspace
// and records the pass for the swarm plane's indicator. Cards are
// handled sequentially: the parked set is small (it is the queue a
// human is pointed at, not the whole board), and each one takes the
// team lock anyway.
func (rt *AgentRuntime) reviewPass(ctx context.Context) {
	workspaces, err := rt.workspacesWithAgents(ctx)
	if err != nil {
		rt.log.Warn("foreman review: workspace scan failed", "error", err)
		return
	}
	resumed, escalated := 0, 0
	for _, workspaceID := range workspaces {
		rows, err := rt.a.pool.Query(ctx, `
			select i.id, i.team_id
			from issues i
			join teams t on t.id = i.team_id
			where t.workspace_id = $1 and i.status = 'active' and i.agent_paused
			order by i.created_at`, workspaceID)
		if err != nil {
			rt.log.Warn("foreman review: parked scan failed", "workspace", workspaceID, "error", err)
			continue
		}
		type card struct{ id, teamID string }
		var cards []card
		for rows.Next() {
			var c card
			if err := rows.Scan(&c.id, &c.teamID); err != nil {
				rows.Close()
				rt.log.Warn("foreman review: parked scan failed", "workspace", workspaceID, "error", err)
				break
			}
			cards = append(cards, c)
		}
		rows.Close()
		for _, c := range cards {
			verdict, err := rt.reviewIssue(ctx, workspaceID, c.id, c.teamID)
			if err != nil {
				rt.log.Warn("foreman review: issue review failed", "workspace", workspaceID,
					"issue", c.id, "error", err)
				continue
			}
			switch verdict {
			case reviewResume:
				resumed++
			case reviewEscalate:
				escalated++
			}
		}
	}
	note := "clean"
	if resumed > 0 || escalated > 0 {
		note = fmt.Sprintf("%d resumed, %d escalated", resumed, escalated)
	}
	rt.recordReview(note)
	if resumed > 0 || escalated > 0 {
		rt.log.Info("foreman review pass", "resumed", resumed, "escalated", escalated)
	}
}

// reviewLoop is the standing duty: one immediate pass on start (the
// queue is reviewed as soon as the process is up — the same boot
// contract as the dispatcher), then a pass on every interval. It stops
// on the process context or Stop.
func (rt *AgentRuntime) reviewLoop(ctx context.Context) {
	interval := rt.a.cfg.SwarmReviewInterval
	rt.reviewPass(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-rt.done:
			return
		case <-ticker.C:
			rt.reviewPass(ctx)
		}
	}
}

// recordReview stores the last pass's outcome for the swarm plane's
// indicator (the same pattern as the brain: mutex-guarded fields,
// served by the status endpoint).
func (rt *AgentRuntime) recordReview(note string) {
	rt.reviewMu.Lock()
	rt.reviewAt = time.Now()
	rt.reviewNote = note
	rt.reviewMu.Unlock()
}

// swarmReview is the swarm plane's standing-duty indicator: the review
// interval in effect, when the last pass ran, and what it did.
type swarmReview struct {
	Interval string `json:"interval"` // "30m" | "off"
	// Pointer so omitempty drops it before the first pass.
	LastRunAt *time.Time `json:"lastRunAt,omitempty"`
	LastNote  string     `json:"lastNote,omitempty"`
}

// reviewIntervalLabel renders the interval for the indicator: whole
// hours/minutes get product strings ("1h", "30m"); everything else the
// duration's own spelling ("45s"). Zero is "off".
func reviewIntervalLabel(d time.Duration) string {
	if d == 0 {
		return "off"
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return d.String()
}

// reviewView assembles the indicator from the in-memory state and the
// deploy config.
func (rt *AgentRuntime) reviewView() swarmReview {
	rt.reviewMu.Lock()
	note, at := rt.reviewNote, rt.reviewAt
	rt.reviewMu.Unlock()
	r := swarmReview{Interval: reviewIntervalLabel(rt.a.cfg.SwarmReviewInterval)}
	if !at.IsZero() {
		t := at
		r.LastRunAt = &t
	}
	r.LastNote = note
	return r
}
