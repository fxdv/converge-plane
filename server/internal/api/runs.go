// runs.go — the run ledger for external agents (Phase 2).
// spec cs:agents:runs
//
// A run is one claim's worth of work: it opens with the claim (same id)
// and ends when the claim ends, with the claim's end reason. The agent
// reports against it on POST /issues/{id}/claim/report, and may carry
// the same report on a heartbeat or on release:
//
//   - model and usage totals (input/output tokens, cost in micro-USD).
//     Totals are the run's running totals, never increments, so a retried
//     report cannot double-count; they may only grow.
//   - trace events (step, tool, note, error), appended and numbered, at
//     most 1000 per run; the excess is dropped and counted in the answer
//     so usage is never refused because the trace is full.
//   - evidence links (pull request, commit, CI run, deployment, link),
//     deduplicated by URL, at most 20.
//   - an outcome (done, failed, blocked, partial) and a summary.
//
// Everything in a report is agent-asserted: the ledger records it, it
// does not verify it. A run accepts reports while open and for an hour
// after it ends (the final numbers of a lease that lapsed still count),
// then it is closed.
//
// Runs reach the client as AgentRun sync records. Reports do not touch
// the issue row, so reporting never bumps the issue version an agent
// writes against.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

const (
	modelAgentRun = "AgentRun"

	runEventCap         = 1000
	runEventsPerReport  = 50
	runEventMaxRunes    = 1000
	runSummaryMaxRunes  = 2000
	runModelMaxRunes    = 100
	runEvidenceCap      = 20
	runEvidenceURLMax   = 2048
	runEvidenceTitleMax = 200
	runTotalMax         = 1_000_000_000_000
	runEventsPageMax    = 200
	packetRunCap        = 5
)

// runReportGraceSQL is how long an ended run still accepts reports.
const runReportGraceSQL = `interval '1 hour'`

var (
	runOutcomes   = map[string]bool{"done": true, "failed": true, "blocked": true, "partial": true}
	runEventKinds = map[string]bool{"step": true, "tool": true, "note": true, "error": true}
	evidenceKinds = map[string]bool{"pull_request": true, "commit": true, "ci_run": true, "deployment": true, "link": true}
)

type runEvidence struct {
	Kind  string  `json:"kind"`
	URL   string  `json:"url"`
	Title *string `json:"title"`
}

type runTotals struct {
	InputTokens  *int64 `json:"inputTokens"`
	OutputTokens *int64 `json:"outputTokens"`
	CostMicros   *int64 `json:"costMicros"`
}

type runEventIn struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// runReport is the body of claim/report, and the optional report part of
// a heartbeat or release body.
type runReport struct {
	ClaimID  string        `json:"claimId"`
	Model    *string       `json:"model"`
	Totals   *runTotals    `json:"totals"`
	Events   []runEventIn  `json:"events"`
	Evidence []runEvidence `json:"evidence"`
	Outcome  *string       `json:"outcome"`
	Summary  *string       `json:"summary"`
}

func (rp *runReport) empty() bool {
	return rp.Model == nil && rp.Totals == nil && len(rp.Events) == 0 &&
		len(rp.Evidence) == 0 && rp.Outcome == nil && rp.Summary == nil
}

// normalize cleans the report's text in place and returns a validation
// message, or "" when the report is acceptable.
func (rp *runReport) normalize() string {
	if rp.Model != nil {
		m := cleanRunText(*rp.Model, runModelMaxRunes, false)
		if m == "" {
			return "model must be 1-100 characters"
		}
		rp.Model = &m
	}
	if t := rp.Totals; t != nil {
		for _, v := range []*int64{t.InputTokens, t.OutputTokens, t.CostMicros} {
			if v != nil && (*v < 0 || *v > runTotalMax) {
				return "totals must be between 0 and 1000000000000"
			}
		}
	}
	if len(rp.Events) > runEventsPerReport {
		return "a report carries at most 50 events"
	}
	for i := range rp.Events {
		e := &rp.Events[i]
		if !runEventKinds[e.Kind] {
			return "event kind must be step, tool, note, or error"
		}
		e.Message = cleanRunText(e.Message, runEventMaxRunes, true)
		if e.Message == "" {
			return "event message must not be empty"
		}
	}
	if len(rp.Evidence) > runEvidenceCap {
		return "a run keeps at most 20 evidence links"
	}
	for i := range rp.Evidence {
		ev := &rp.Evidence[i]
		if !evidenceKinds[ev.Kind] {
			return "evidence kind must be pull_request, commit, ci_run, deployment, or link"
		}
		if !validEvidenceURL(ev.URL) {
			return "evidence url must be an http(s) URL without credentials, at most 2048 characters"
		}
		if ev.Title != nil {
			if t := cleanRunText(*ev.Title, runEvidenceTitleMax, false); t != "" {
				ev.Title = &t
			} else {
				ev.Title = nil
			}
		}
	}
	if rp.Outcome != nil && !runOutcomes[*rp.Outcome] {
		return "outcome must be done, failed, blocked, or partial"
	}
	if rp.Summary != nil {
		if s := cleanRunText(*rp.Summary, runSummaryMaxRunes, true); s != "" {
			rp.Summary = &s
		} else {
			rp.Summary = nil
		}
	}
	return ""
}

// cleanRunText makes agent text safe to store and show: valid UTF-8, no
// control characters (newlines and tabs survive when multiline), no
// bidirectional overrides that could disguise what a human reads,
// trimmed, and at most max runes (an over-long text ends in "…").
func cleanRunText(s string, max int, multiline bool) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	s = strings.Map(func(r rune) rune {
		switch {
		case multiline && (r == '\n' || r == '\t'):
			return r
		case r == '\r':
			return -1
		case r < 0x20 || r == 0x7f:
			if multiline {
				return -1
			}
			return ' '
		case (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069):
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max-1]) + "…"
	}
	return s
}

// validEvidenceURL admits absolute http(s) URLs with a host and without
// credentials or whitespace: the client renders them as links.
func validEvidenceURL(raw string) bool {
	if raw == "" || len(raw) > runEvidenceURLMax {
		return false
	}
	for _, r := range raw {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}

// runRow is one agent_runs row.
type runRow struct {
	ID, WorkspaceID, IssueID, AgentID  string
	ClaimID                            *string
	StartedAt, UpdatedAt               time.Time
	EndedAt                            *time.Time
	EndReason, Outcome, Summary, Model *string
	InputTokens, OutputTokens          int64
	CostMicros                         int64
	EventCount                         int
	Evidence                           []byte
}

const runColumns = `r.id, r.workspace_id, r.issue_id, r.agent_id, r.claim_id, r.started_at, r.updated_at,
	r.ended_at, r.end_reason, r.outcome, r.summary, r.model,
	r.input_tokens, r.output_tokens, r.cost_micros, r.event_count, r.evidence`

func (r *runRow) scanDest() []any {
	return []any{&r.ID, &r.WorkspaceID, &r.IssueID, &r.AgentID, &r.ClaimID, &r.StartedAt, &r.UpdatedAt,
		&r.EndedAt, &r.EndReason, &r.Outcome, &r.Summary, &r.Model,
		&r.InputTokens, &r.OutputTokens, &r.CostMicros, &r.EventCount, &r.Evidence}
}

func (r *runRow) evidence() []runEvidence {
	out := []runEvidence{}
	if len(r.Evidence) > 0 {
		_ = json.Unmarshal(r.Evidence, &out)
	}
	return out
}

// runData serializes a run in the exact shape of the client's AgentRun
// model. Every optional field is present (null when unset).
func runData(r runRow) map[string]any {
	var endedAt any
	if r.EndedAt != nil {
		endedAt = r.EndedAt.Format(iso)
	}
	return map[string]any{
		"id":           r.ID,
		"createdAt":    r.StartedAt.Format(iso),
		"updatedAt":    r.UpdatedAt.Format(iso),
		"issueId":      r.IssueID,
		"agentId":      r.AgentID,
		"claimId":      nullOrEmpty(strval(r.ClaimID)),
		"startedAt":    r.StartedAt.Format(iso),
		"endedAt":      endedAt,
		"endReason":    nullOrEmpty(strval(r.EndReason)),
		"outcome":      nullOrEmpty(strval(r.Outcome)),
		"summary":      nullOrEmpty(strval(r.Summary)),
		"model":        nullOrEmpty(strval(r.Model)),
		"inputTokens":  r.InputTokens,
		"outputTokens": r.OutputTokens,
		"costMicros":   r.CostMicros,
		"eventCount":   r.EventCount,
		"evidence":     r.evidence(),
	}
}

func (a *API) runByIDTx(ctx context.Context, tx pgx.Tx, id string) (runRow, error) {
	var r runRow
	err := tx.QueryRow(ctx, "select "+runColumns+" from agent_runs r where r.id = $1", id).Scan(r.scanDest()...)
	return r, err
}

// emitRunTx emits the run's current state to the sync feed.
func (a *API) emitRunTx(ctx context.Context, tx pgx.Tx, runID, action string) (syncActionRecord, runRow, error) {
	r, err := a.runByIDTx(ctx, tx, runID)
	if err != nil {
		return syncActionRecord{}, r, err
	}
	rec, err := a.emitChange(ctx, tx, r.WorkspaceID, modelAgentRun, r.ID, action, runData(r))
	return rec, r, err
}

// openRunTx opens the run of a claim just taken.
func (a *API) openRunTx(ctx context.Context, tx pgx.Tx, claimID, workspaceID, issueID, agentID string) (syncActionRecord, error) {
	if _, err := tx.Exec(ctx, `
		insert into agent_runs (id, workspace_id, issue_id, agent_id, claim_id)
		values ($1, $2, $3, $4, $1)`, claimID, workspaceID, issueID, agentID); err != nil {
		return syncActionRecord{}, err
	}
	rec, _, err := a.emitRunTx(ctx, tx, claimID, "CREATE")
	return rec, err
}

// endRunTx ends a claim's run with the claim's end reason. It returns no
// records when the run had already ended.
func (a *API) endRunTx(ctx context.Context, tx pgx.Tx, claimID, reason string) ([]syncActionRecord, error) {
	tag, err := tx.Exec(ctx, `
		update agent_runs set ended_at = now(), end_reason = $2, updated_at = now()
		where claim_id = $1 and ended_at is null`, claimID, reason)
	if err != nil || tag.RowsAffected() == 0 {
		return nil, err
	}
	rec, _, err := a.emitRunTx(ctx, tx, claimID, "UPDATE")
	if err != nil {
		return nil, err
	}
	return []syncActionRecord{rec}, nil
}

// reportRunTx locks the agent's run on the issue and applies a report to
// it. On refusal it returns the HTTP status and body to answer with; the
// caller emits the run afterwards, and broadcasts linked: the pull
// requests the report's evidence linked to the issue.
func (a *API) reportRunTx(ctx context.Context, tx pgx.Tx, runID, issueID, agentID string, rp runReport) (dropped, status int, body map[string]any, linked []syncActionRecord, err error) {
	var (
		run    runRow
		closed bool
	)
	err = tx.QueryRow(ctx, "select "+runColumns+`,
		       r.ended_at is not null and r.ended_at < now() - `+runReportGraceSQL+`
		from agent_runs r where r.id = $1 and r.issue_id = $2
		for update`, runID, issueID).Scan(append(run.scanDest(), &closed)...)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && run.AgentID != agentID) {
		return 0, http.StatusNotFound, map[string]any{"error": "run not found"}, nil, nil
	}
	if err != nil {
		return 0, 0, nil, nil, err
	}
	if closed {
		return 0, http.StatusConflict, map[string]any{
			"error": "the run is closed to reports", "endReason": strval(run.EndReason)}, nil, nil
	}
	if rp.empty() {
		return 0, 0, nil, nil, nil
	}
	if t := rp.Totals; t != nil {
		if (t.InputTokens != nil && *t.InputTokens < run.InputTokens) ||
			(t.OutputTokens != nil && *t.OutputTokens < run.OutputTokens) ||
			(t.CostMicros != nil && *t.CostMicros < run.CostMicros) {
			return 0, http.StatusUnprocessableEntity, map[string]any{
				"error": "usage totals only grow: report the run's totals so far"}, nil, nil
		}
	}
	evidence := run.evidence()
	for _, ev := range rp.Evidence {
		replaced := false
		for i := range evidence {
			if evidence[i].URL == ev.URL {
				evidence[i], replaced = ev, true
				break
			}
		}
		if !replaced {
			evidence = append(evidence, ev)
		}
	}
	if len(evidence) > runEvidenceCap {
		return 0, http.StatusUnprocessableEntity, map[string]any{"error": "a run keeps at most 20 evidence links"}, nil, nil
	}
	events := rp.Events
	if room := runEventCap - run.EventCount; len(events) > room {
		events = events[:max(room, 0)]
	}
	dropped = len(rp.Events) - len(events)
	if len(events) > 0 {
		kinds := make([]string, len(events))
		messages := make([]string, len(events))
		for i, e := range events {
			kinds[i], messages[i] = e.Kind, e.Message
		}
		if _, err := tx.Exec(ctx, `
			insert into agent_run_events (run_id, seq, kind, message)
			select $1, $2::int + t.ord::int, t.kind, t.message
			from unnest($3::text[], $4::text[]) with ordinality as t(kind, message, ord)`,
			runID, run.EventCount, kinds, messages); err != nil {
			return 0, 0, nil, nil, err
		}
	}
	evRaw, err := json.Marshal(evidence)
	if err != nil {
		return 0, 0, nil, nil, err
	}
	var in, out, cost *int64
	if t := rp.Totals; t != nil {
		in, out, cost = t.InputTokens, t.OutputTokens, t.CostMicros
	}
	if cost != nil {
		var teamID string
		if err := tx.QueryRow(ctx, `select team_id from issues where id = $1`, issueID).Scan(&teamID); err != nil {
			return 0, 0, nil, nil, err
		}
		if err := a.spendAllowsTx(ctx, tx, teamID, runID, *cost); err != nil {
			if errors.Is(err, errSpendBudget) {
				return 0, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()}, nil, nil
			}
			return 0, 0, nil, nil, err
		}
	}
	if _, err := tx.Exec(ctx, `
		update agent_runs set
			model = coalesce($2, model),
			input_tokens = coalesce($3, input_tokens),
			output_tokens = coalesce($4, output_tokens),
			cost_micros = coalesce($5, cost_micros),
			event_count = event_count + $6::int,
			evidence = $7::jsonb,
			outcome = coalesce($8, outcome),
			summary = coalesce($9, summary),
			updated_at = now()
		where id = $1`,
		runID, rp.Model, in, out, cost, len(events), string(evRaw), rp.Outcome, rp.Summary); err != nil {
		return 0, 0, nil, nil, err
	}
	linked, err = a.linkPullRequestsTx(ctx, tx, run, rp.Evidence)
	if err != nil {
		return 0, 0, nil, nil, err
	}
	if err := a.enqueueWebhookTx(ctx, tx, run.WorkspaceID, "run.reported", map[string]any{
		"runId": run.ID, "issueId": run.IssueID, "agentId": run.AgentID,
	}); err != nil {
		return 0, 0, nil, nil, err
	}
	return dropped, 0, nil, linked, nil
}

// handleClaimReport implements POST /api/v1/issues/{id}/claim/report.
func (a *API) handleClaimReport(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !externalAgentGuard(w, p) {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var rp runReport
	if err := decodeOptional(r, &rp); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if msg := rp.normalize(); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if !isUUID(id) || !isUUID(rp.ClaimID) {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if _, _, ok := a.issueAccess(ctx, p, id); !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	dropped, status, body, linked, err := a.reportRunTx(ctx, tx, rp.ClaimID, id, p.AccountID, rp)
	if err != nil {
		a.internalError(w, err)
		return
	}
	if status != 0 {
		writeJSON(w, status, body)
		return
	}
	if rp.empty() {
		run, err := a.runByIDTx(ctx, tx, rp.ClaimID)
		if err != nil {
			a.internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run": runData(run), "droppedEvents": 0})
		return
	}
	rec, run, err := a.emitRunTx(ctx, tx, rp.ClaimID, "UPDATE")
	if err != nil {
		a.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.internalError(w, err)
		return
	}
	a.broadcastRecord(rec)
	for i := range linked {
		a.broadcastRecord(linked[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": runData(run), "droppedEvents": dropped})
}

type runEventOut struct {
	Seq     int       `json:"seq"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
}

// handleRunEvents implements GET /api/v1/issues/{id}/runs/{runId}/events:
// one page of a run's trace, oldest first, for anyone who can see the
// issue. ?after=<seq> continues from nextAfter.
func (a *API) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx := r.Context()
	id, runID := chi.URLParam(r, "id"), chi.URLParam(r, "runId")
	if !isUUID(id) || !isUUID(runID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	after, limit := 0, runEventsPageMax
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "after must be a non-negative integer")
			return
		}
		after = n
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > runEventsPageMax {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 200")
			return
		}
		limit = n
	}
	if _, _, ok := a.issueAccess(ctx, p, id); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var found bool
	if err := a.pool.QueryRow(ctx,
		"select exists(select 1 from agent_runs where id = $1 and issue_id = $2)", runID, id).Scan(&found); err != nil {
		a.internalError(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	rows, err := a.pool.Query(ctx, `
		select seq, at, kind, message from agent_run_events
		where run_id = $1 and seq > $2
		order by seq limit $3`, runID, after, limit)
	if err != nil {
		a.internalError(w, err)
		return
	}
	defer rows.Close()
	events := []runEventOut{}
	for rows.Next() {
		var e runEventOut
		if err := rows.Scan(&e.Seq, &e.At, &e.Kind, &e.Message); err != nil {
			a.internalError(w, err)
			return
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		a.internalError(w, err)
		return
	}
	var next any
	if len(events) == limit {
		next = events[len(events)-1].Seq
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "nextAfter": next})
}

// collectAgentRuns maps the workspace's runs to the client's AgentRun
// shape.
func (a *API) collectAgentRuns(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, "select "+runColumns+`
		from agent_runs r where r.workspace_id = $1
		order by r.started_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []syncActionRecord
	for rows.Next() {
		var run runRow
		if err := rows.Scan(run.scanDest()...); err != nil {
			return nil, err
		}
		rec, err := emit(run.ID, runData(run))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// packetRun is an earlier attempt at the issue, so an agent picking it
// up sees how previous runs ended. Summary and evidence are agent-authored.
type packetRun struct {
	ID        string        `json:"id"`
	AgentID   string        `json:"agentId"`
	StartedAt time.Time     `json:"startedAt"`
	EndedAt   *time.Time    `json:"endedAt"`
	EndReason *string       `json:"endReason"`
	Outcome   *string       `json:"outcome"`
	Summary   *string       `json:"summary"`
	Evidence  []runEvidence `json:"evidence"`
}

// previousRunsTx lists the issue's ended runs, newest first.
func (a *API) previousRunsTx(ctx context.Context, tx pgx.Tx, issueID string) ([]packetRun, error) {
	rows, err := tx.Query(ctx, "select "+runColumns+`
		from agent_runs r where r.issue_id = $1 and r.ended_at is not null
		order by r.ended_at desc limit `+strconv.Itoa(packetRunCap), issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []packetRun{}
	for rows.Next() {
		var run runRow
		if err := rows.Scan(run.scanDest()...); err != nil {
			return nil, err
		}
		out = append(out, packetRun{
			ID: run.ID, AgentID: run.AgentID, StartedAt: run.StartedAt, EndedAt: run.EndedAt,
			EndReason: run.EndReason, Outcome: run.Outcome, Summary: run.Summary, Evidence: run.evidence(),
		})
	}
	return out, rows.Err()
}
