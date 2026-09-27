// runs_db_test.go — the run ledger against a real database: a run opens
// and ends with its claim, reports accumulate usage (totals only grow),
// trace (capped, paged) and evidence (deduplicated), a release carries
// the final report, an ended run accepts reports for the grace hour
// only, and reports never move the issue version. Gated on
// CONVERGE_TEST_DATABASE_URL.
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type runResponse struct {
	Run struct {
		ID           string        `json:"id"`
		ClaimID      *string       `json:"claimId"`
		EndedAt      *string       `json:"endedAt"`
		EndReason    *string       `json:"endReason"`
		Outcome      *string       `json:"outcome"`
		Summary      *string       `json:"summary"`
		Model        *string       `json:"model"`
		InputTokens  int64         `json:"inputTokens"`
		OutputTokens int64         `json:"outputTokens"`
		CostMicros   int64         `json:"costMicros"`
		EventCount   int           `json:"eventCount"`
		Evidence     []runEvidence `json:"evidence"`
	} `json:"run"`
	DroppedEvents int    `json:"droppedEvents"`
	Released      bool   `json:"released"`
	EndReason     string `json:"endReason"`
}

func (f *workFixture) report(agent, issueID, body string, want int) runResponse {
	f.t.Helper()
	rec := f.call(externalAgentPrincipal(agent), (*API).handleClaimReport, "POST",
		"/api/v1/issues/"+issueID+"/claim/report", body, "id", issueID)
	checkStatus(f.t, rec, want)
	var out runResponse
	if want == http.StatusOK {
		decodeBody(f.t, rec, &out)
	}
	return out
}

func (f *workFixture) issueVersion(issueID string) int {
	f.t.Helper()
	row, err := f.a.issueByID(context.Background(), issueID)
	if err != nil {
		f.t.Fatalf("read issue: %v", err)
	}
	return row.Version
}

func (f *workFixture) outboxCount(model, id string) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`select count(*) from sync_outbox where workspace_id = $1 and model_name = $2 and model_id = $3`,
		f.ws, model, id).Scan(&n); err != nil {
		f.t.Fatalf("count outbox: %v", err)
	}
	return n
}

func eventsBody(claimID string, n int, kind string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"claimId":%q,"events":[`, claimID)
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"kind":%q,"message":"line %d"}`, kind, i)
	}
	b.WriteString("]}")
	return b.String()
}

func TestRunLedger(t *testing.T) {
	f := newWorkFixture(t)
	work := f.issue(f.t1, f.todo, f.ext1)
	c := f.claim(f.ext1, work, http.StatusOK)
	runID := c.Claim.ID
	var openRun string

	t.Run("the claim opens its run", func(t *testing.T) {
		if f.outboxCount(modelAgentRun, runID) != 1 {
			t.Fatal("claiming must emit the run's CREATE record")
		}
		recs, err := f.a.collectAgentRuns(context.Background(), f.ws, func(id string, data any) (syncActionRecord, error) {
			return syncActionRecord{ModelID: id}, nil
		})
		if err != nil || len(recs) != 1 || recs[0].ModelID != runID {
			t.Fatalf("bootstrap runs = %v, %v; want the one run", recs, err)
		}
	})

	t.Run("reports accumulate without moving the issue version", func(t *testing.T) {
		before := f.issueVersion(work)
		out := f.report(f.ext1, work, `{"claimId":"`+runID+`","model":"claude-x",
			"totals":{"inputTokens":1000,"outputTokens":200,"costMicros":4200},
			"events":[{"kind":"step","message":"read the issue"},{"kind":"tool","message":"go test ./..."}],
			"evidence":[{"kind":"pull_request","url":"https://github.com/o/r/pull/7","title":"draft"}]}`, 200)
		if out.Run.InputTokens != 1000 || out.Run.CostMicros != 4200 || strval(out.Run.Model) != "claude-x" ||
			out.Run.EventCount != 2 || len(out.Run.Evidence) != 1 {
			t.Fatalf("run after report = %+v", out.Run)
		}
		// A retried report with the same totals changes nothing; the same
		// URL updates its evidence entry instead of adding one.
		out = f.report(f.ext1, work, `{"claimId":"`+runID+`",
			"totals":{"inputTokens":1000,"outputTokens":200,"costMicros":4200},
			"evidence":[{"kind":"pull_request","url":"https://github.com/o/r/pull/7","title":"ready"}]}`, 200)
		if out.Run.InputTokens != 1000 || len(out.Run.Evidence) != 1 || strval(out.Run.Evidence[0].Title) != "ready" {
			t.Fatalf("retried report = %+v", out.Run)
		}
		f.report(f.ext1, work, `{"claimId":"`+runID+`","totals":{"costMicros":10}}`, http.StatusUnprocessableEntity)
		if f.issueVersion(work) != before {
			t.Fatalf("issue version moved %d -> %d: reports must not fence the agent's own writes", before, f.issueVersion(work))
		}
	})

	t.Run("doors", func(t *testing.T) {
		f.report(f.ext2, work, `{"claimId":"`+runID+`"}`, http.StatusNotFound)
		f.report(f.ext1, work, `{"claimId":"`+testUUID()+`"}`, http.StatusNotFound)
		checkStatus(t, f.call(agentPrincipal(f.rt1), (*API).handleClaimReport, "POST", "/x",
			`{"claimId":"`+runID+`"}`, "id", work), http.StatusForbidden)
		other := f.issue(f.t1, f.todo, f.ext2)
		f.report(f.ext1, other, `{"claimId":"`+runID+`"}`, http.StatusNotFound) // the run is not on that issue
	})

	t.Run("an empty report reads the run without writing", func(t *testing.T) {
		before := f.outboxCount(modelAgentRun, runID)
		out := f.report(f.ext1, work, `{"claimId":"`+runID+`","summary":"   "}`, 200)
		if out.Run.ID != runID || out.Run.CostMicros != 4200 {
			t.Fatalf("empty report = %+v; want the current run", out.Run)
		}
		if n := f.outboxCount(modelAgentRun, runID); n != before {
			t.Fatalf("outbox %d -> %d: an empty report must not emit", before, n)
		}
	})

	t.Run("the trace is capped and paged", func(t *testing.T) {
		f.exec(`update agent_runs set event_count = $2 where id = $1`, runID, runEventCap-3)
		// Pretend the gap is filled so the numbering stays contiguous.
		f.exec(`insert into agent_run_events (run_id, seq, kind, message)
			select $1, s, 'note', 'filler' from generate_series(3, $2::int) s`, runID, runEventCap-3)
		out := f.report(f.ext1, work, eventsBody(runID, 5, "note"), 200)
		if out.Run.EventCount != runEventCap || out.DroppedEvents != 2 {
			t.Fatalf("event cap: count %d dropped %d, want %d and 2", out.Run.EventCount, out.DroppedEvents, runEventCap)
		}
		type page struct {
			Events []struct {
				Seq     int    `json:"seq"`
				Kind    string `json:"kind"`
				Message string `json:"message"`
			} `json:"events"`
			NextAfter *int `json:"nextAfter"`
		}
		read := func(p *Principal, query string, want int) page {
			rec := f.call(p, (*API).handleRunEvents, "GET",
				"/api/v1/issues/"+work+"/runs/"+runID+"/events"+query, "", "id", work, "runId", runID)
			checkStatus(t, rec, want)
			var pg page
			if want == 200 {
				decodeBody(t, rec, &pg)
			}
			return pg
		}
		first := read(humanPrincipal(f.owner), "?limit=2", 200)
		if len(first.Events) != 2 || first.Events[0].Message != "read the issue" || first.Events[1].Kind != "tool" ||
			first.NextAfter == nil || *first.NextAfter != 2 {
			t.Fatalf("first page = %+v", first)
		}
		last := read(humanPrincipal(f.owner), fmt.Sprintf("?after=%d", runEventCap-2), 200)
		if len(last.Events) != 2 || last.Events[1].Seq != runEventCap || last.NextAfter != nil {
			t.Fatalf("last page = %+v", last)
		}
		read(humanPrincipal(testUUID()), "", http.StatusNotFound) // not a member
	})

	t.Run("a heartbeat may carry a report", func(t *testing.T) {
		rec := f.transitionBody(f.ext1, work, false, `{"claimId":"`+runID+`","totals":{"outputTokens":300}}`)
		checkStatus(t, rec, 200)
		var out struct {
			Claim claimView `json:"claim"`
			runResponse
		}
		decodeBody(t, rec, &out)
		if out.Claim.ID != runID || out.Run.OutputTokens != 300 {
			t.Fatalf("heartbeat with report = claim %q output %d", out.Claim.ID, out.Run.OutputTokens)
		}
	})

	t.Run("release carries the final report and ends the run", func(t *testing.T) {
		rec := f.transitionBody(f.ext1, work, true, `{"claimId":"`+runID+`","outcome":"done",
			"summary":"Fixed the flaky test.\nPR is up.","totals":{"costMicros":9000}}`)
		checkStatus(t, rec, 200)
		var out runResponse
		decodeBody(t, rec, &out)
		if !out.Released || out.Run.EndedAt == nil || strval(out.Run.EndReason) != claimEndReleased ||
			strval(out.Run.Outcome) != "done" || out.Run.CostMicros != 9000 ||
			strval(out.Run.Summary) != "Fixed the flaky test.\nPR is up." {
			t.Fatalf("released run = %+v", out)
		}
	})

	t.Run("an ended run takes reports for the grace hour only", func(t *testing.T) {
		out := f.report(f.ext1, work, `{"claimId":"`+runID+`","totals":{"costMicros":9500}}`, 200)
		if out.Run.CostMicros != 9500 {
			t.Fatalf("late report = %+v", out.Run)
		}
		f.exec(`update agent_runs set ended_at = now() - interval '2 hours' where id = $1`, runID)
		f.report(f.ext1, work, `{"claimId":"`+runID+`","totals":{"costMicros":9600}}`, http.StatusConflict)
		// Releasing again stays idempotent, but a report on it is refused.
		rec := f.transitionBody(f.ext1, work, true, `{"claimId":"`+runID+`"}`)
		checkStatus(t, rec, 200)
		checkStatus(t, f.transitionBody(f.ext1, work, true, `{"claimId":"`+runID+`","outcome":"failed"}`), http.StatusConflict)
	})

	t.Run("a lapsed claim's run ends with it; the next claim sees it", func(t *testing.T) {
		c2 := f.claim(f.ext1, work, http.StatusOK)
		f.exec(`update issue_claims set expires_at = now() - interval '1 second' where id = $1`, c2.Claim.ID)
		if _, err := f.a.SweepClaims(context.Background()); err != nil {
			t.Fatal(err)
		}
		var reason string
		if err := f.pool.QueryRow(context.Background(),
			`select end_reason from agent_runs where id = $1`, c2.Claim.ID).Scan(&reason); err != nil || reason != claimEndExpired {
			t.Fatalf("run end reason = %q (%v), want expired", reason, err)
		}
		rec := f.call(externalAgentPrincipal(f.ext1), (*API).handleClaimIssue, "POST",
			"/api/v1/issues/"+work+"/claim", "", "id", work)
		checkStatus(t, rec, 200)
		var out struct {
			Claim  claimView `json:"claim"`
			Packet struct {
				PreviousRuns []packetRun `json:"previousRuns"`
			} `json:"packet"`
		}
		decodeBody(t, rec, &out)
		if len(out.Packet.PreviousRuns) != 2 || out.Packet.PreviousRuns[0].ID != c2.Claim.ID ||
			strval(out.Packet.PreviousRuns[1].Outcome) != "done" {
			t.Fatalf("previousRuns = %+v, want the lapsed run then the released one", out.Packet.PreviousRuns)
		}
		openRun = out.Claim.ID
	})

	t.Run("supersede and revoke end runs too", func(t *testing.T) {
		c3 := f.claim(f.ext1, work, http.StatusOK) // same agent again: supersedes the open claim
		var reason string
		if err := f.pool.QueryRow(context.Background(),
			`select end_reason from agent_runs where id = $1`, openRun).Scan(&reason); err != nil || reason != claimEndSuperseded {
			t.Fatalf("superseded run = %q (%v)", reason, err)
		}
		var n int
		if err := f.pool.QueryRow(context.Background(),
			`select count(*) from agent_runs where issue_id = $1 and ended_at is null`, work).Scan(&n); err != nil || n != 1 {
			t.Fatalf("open runs on the issue = %d (%v), want exactly one", n, err)
		}
		f.exec(`update accounts set agent_driver = 'runtime' where id = $1`, f.ext1)
		defer f.exec(`update accounts set agent_driver = 'external' where id = $1`, f.ext1)
		tx, err := f.pool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.a.endAgentClaimsTx(context.Background(), tx, f.ext1, claimEndRevoked); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := f.pool.QueryRow(context.Background(),
			`select end_reason from agent_runs where id = $1`, c3.Claim.ID).Scan(&reason); err != nil || reason != claimEndRevoked {
			t.Fatalf("revoked run = %q (%v)", reason, err)
		}
	})
}

// Concurrent reports on one run serialize on the run row: every event
// lands, numbered 1..n without gaps or collisions.
func TestRunReportsConcurrent(t *testing.T) {
	f := newWorkFixture(t)
	work := f.issue(f.t1, f.todo, f.ext1)
	runID := f.claim(f.ext1, work, http.StatusOK).Claim.ID
	const reporters, each = 6, 4
	var wg sync.WaitGroup
	codes := make([]int, reporters)
	for i := range reporters {
		wg.Go(func() {
			rec := httptest.NewRecorder()
			req := requestFor(t, externalAgentPrincipal(f.ext1), "POST", "http://x/report", eventsBody(runID, each, "step"), "id", work)
			f.a.handleClaimReport(rec, req)
			codes[i] = rec.Code
		})
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("reporter %d got %d", i, c)
		}
	}
	var count, maxSeq, rows int
	if err := f.pool.QueryRow(context.Background(), `
		select r.event_count, coalesce(max(e.seq), 0), count(e.seq)
		from agent_runs r left join agent_run_events e on e.run_id = r.id
		where r.id = $1 group by r.event_count`, runID).Scan(&count, &maxSeq, &rows); err != nil {
		t.Fatal(err)
	}
	if count != reporters*each || maxSeq != count || rows != count {
		t.Fatalf("event_count %d, max seq %d, rows %d; want %d each", count, maxSeq, rows, reporters*each)
	}
}

// transitionBody posts a heartbeat or release with a full body.
func (f *workFixture) transitionBody(agent, issueID string, release bool, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	h, verb := (*API).handleClaimHeartbeat, "heartbeat"
	if release {
		h, verb = (*API).handleClaimRelease, "release"
	}
	return f.call(externalAgentPrincipal(agent), h, "POST", "/api/v1/issues/"+issueID+"/claim/"+verb, body, "id", issueID)
}
