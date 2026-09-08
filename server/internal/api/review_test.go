// review_test.go — the foreman review protocol pins (cs:swarm:review).
//
// The decision table is pure and pinned directly; the transactional
// surface runs through the same in-memory pgx fakes as the work-cycle
// tests (fakeTx replays canned results per query fragment and fails
// loudly on anything unmatched), with the Execs recorded for
// assertion — the writes are the contract.

package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"converge/internal/config"
)

// TestDecideReview pins the protocol's decision table: a human reply
// since the last park wins over the cycle counter (the reply is new
// input; the swarm may try once more), the counter escalates at the
// threshold, and below it the card waits.
func TestDecideReview(t *testing.T) {
	now := time.Now()
	recent, stale := now.Add(-time.Hour), now.Add(-5*time.Hour)
	cases := []struct {
		name string
		f    reviewFacts
		want reviewVerdict
	}{
		{"fresh park, no reply: wait", reviewFacts{ParkCount24h: 1, LastParkAt: recent, RepliesSincePark: 0}, reviewNone},
		{"second park, no reply: wait", reviewFacts{ParkCount24h: 2, LastParkAt: recent, RepliesSincePark: 0}, reviewNone},
		{"third park, no reply: escalate (counter)", reviewFacts{ParkCount24h: 3, LastParkAt: recent, RepliesSincePark: 0}, reviewEscalate},
		{"seventh park, no reply: escalate (counter)", reviewFacts{ParkCount24h: 7, LastParkAt: recent, RepliesSincePark: 0}, reviewEscalate},
		{"unanswered for a session: escalate (stale)", reviewFacts{ParkCount24h: 1, LastParkAt: stale, RepliesSincePark: 0}, reviewEscalate},
		{"never parked is never stale: wait", reviewFacts{ParkCount24h: 1, LastParkAt: time.Time{}, RepliesSincePark: 0}, reviewNone},
		{"replied, one park: resume", reviewFacts{ParkCount24h: 1, LastParkAt: recent, RepliesSincePark: 1}, reviewResume},
		{"replied beats the counter: resume", reviewFacts{ParkCount24h: 5, LastParkAt: stale, RepliesSincePark: 1}, reviewResume},
	}
	for _, c := range cases {
		if got := decideReview(c.f); got != c.want {
			t.Errorf("%s: decideReview(%+v) = %v, want %v", c.name, c.f, got, c.want)
		}
	}
}

// reviewRow builds the issue row the protocol reads: an active card,
// agent-assigned, in statusID. The 17 values are issueColumns' scan
// shape (issueByIDTx); loadIssueRowTx appends the version.
func reviewRow(paused bool, statusID string) []any {
	now := time.Now()
	return []any{
		"iss1", "team1", 7, nil, 0, "Test card", "", "active", now, now,
		"ag1", "ag1", nil, statusID, paused, nil, nil,
	}
}

// reviewFixture is the full canned result set of the review's queries.
// Rule order matters: the version-carrying load (18 values) before the
// plain issueByID (17), since both share the "from issues i" tail.
// statusID is the row's current state (the parked column for the tick
// tests, In Progress for the work-cycle breaker test).
func reviewFixture(t *testing.T, statusID string, parkCount, replies int, foremanErr error, paused bool) *fakeTx {
	now := time.Now()
	lastPark := now.Add(-time.Hour)
	row := reviewRow(paused, statusID)
	return &fakeTx{
		t: t,
		rules: []fakeRule{
			{frag: "i.version from issues i where i.id", rowVals: append(append([]any{}, row...), 3)},
			{frag: "with park as", rowVals: []any{parkCount, lastPark, replies}},
			{frag: "from workspace_members", rowVals: []any{"human1"}, rowErr: foremanErr},
			{frag: "category = 'STARTED'", rowVals: []any{"st-ip"}},
			{frag: "and lower(name) = $2", rowVals: []any{"st-hr"}},
			{frag: "from issues i where i.id", rowVals: row},
			{frag: "insert into issue_history", rowVals: []any{"hist1"}},
			{frag: "select created_at from issue_history where id", rowVals: []any{now}},
			{frag: "insert into sync_sequences", rowVals: []any{int64(7)}},
			{frag: "insert into comments", rowVals: []any{"com1"}},
			{frag: "from comments cm where cm.id", rowVals: []any{"com1", `{"type":"doc"}`, "human1", "iss1", nil, now, now}},
		},
	}
}

func reviewAPI() *API {
	return &API{log: discardLogger(), cfg: config.Config{}}
}

// findCall returns the first recorded write (Exec or QueryRow) whose
// SQL contains frag.
func findCall(t *testing.T, tx *fakeTx, frag string) fakeExecCall {
	t.Helper()
	for i := range tx.execs {
		if strings.Contains(tx.execs[i].sql, frag) {
			return tx.execs[i]
		}
	}
	for i := range tx.rows {
		if strings.Contains(tx.rows[i].sql, frag) {
			return tx.rows[i]
		}
	}
	t.Fatalf("no recorded write containing %q (execs=%d rows=%d)", frag, len(tx.execs), len(tx.rows))
	return fakeExecCall{}
}

// TestReviewFactsTx pins the facts read: the windowed pause count, the
// last park's timestamp, and the human replies after it.
func TestReviewFactsTx(t *testing.T) {
	tx := &fakeTx{
		t:     t,
		rules: []fakeRule{{frag: "with park as", rowVals: []any{4, time.Now().Add(-time.Hour), 2}}},
	}
	f, err := reviewAPI().reviewFactsTx(context.Background(), tx, "iss1")
	if err != nil {
		t.Fatal(err)
	}
	if f.ParkCount24h != 4 || f.RepliesSincePark != 2 || f.LastParkAt.IsZero() {
		t.Fatalf("facts = %+v", f)
	}
}

// TestForemanHumanIDTx pins the escalation target resolution: the owner
// wins, the oldest admin is the fallback, and an agents-only workspace
// has no foreman (the protocol then parks as usual).
func TestForemanHumanIDTx(t *testing.T) {
	tx := &fakeTx{t: t, rules: []fakeRule{{frag: "from workspace_members", rowVals: []any{"owner1"}}}}
	if got, err := reviewAPI().foremanHumanIDTx(context.Background(), tx, "ws1"); err != nil || got != "owner1" {
		t.Fatalf("foreman = %q, %v — want owner1", got, err)
	}
	tx = &fakeTx{t: t, rules: []fakeRule{{frag: "from workspace_members", rowErr: pgx.ErrNoRows}}}
	if got, err := reviewAPI().foremanHumanIDTx(context.Background(), tx, "ws1"); err != nil || got != "" {
		t.Fatalf("foreman = %q, %v — want empty for a human-less workspace", got, err)
	}
}

// TestBreakerEscalatesAtThreshold: a pause that would put the card at
// the threshold within the window escalates it to the human foreman
// instead of re-parking — the reassignment and the flag clear are the
// writes that end the cycle.
func TestBreakerEscalatesAtThreshold(t *testing.T) {
	tx := reviewFixture(t, "st-ip", 2, 0, nil, false) // 2 parks + this one = 3
	a := reviewAPI()
	row := issueRow{ID: "iss1", TeamID: "team1", Number: 7, Status: "active"}
	row.StatusID = strvalptr("st-ip")
	row.AssigneeID = strvalptr("ag1")
	recs, tripped, reason, err := a.pauseWithBreakerTx(context.Background(), tx, "ws1", row,
		&Principal{AccountID: "ag1", Kind: "agent"}, "blocked on foreman", "a note")
	if err != nil {
		t.Fatal(err)
	}
	if !tripped || len(recs) == 0 {
		t.Fatalf("breaker = tripped %v, %d records — the worker must move on either way", tripped, len(recs))
	}
	if !strings.Contains(reason, "escalated") {
		t.Fatalf("reason = %q — want the escalation named", reason)
	}
	upd := findCall(t, tx, "agent_paused = false, assignee_id")
	if len(upd.args) < 2 || upd.args[1] != "human1" {
		t.Fatalf("escalation update args = %v — the card must go to the human foreman", upd.args)
	}
	// The disclosure comment is written on the card (a QueryRow — the
	// insert reads back its id), with the disclosure in its body.
	ins := findCall(t, tx, "insert into comments")
	if len(ins.args) < 3 || !strings.Contains(ins.args[2].(string), "Foreman review") {
		t.Fatalf("comment body = %v — want the foreman review disclosure", ins.args)
	}
}

// TestBreakerParksUnderThreshold: below the threshold the pause is a
// plain park (flag set, the card moves to Human Review) — the protocol
// only intervenes at the cycle, never on the swarm's first question.
func TestBreakerParksUnderThreshold(t *testing.T) {
	tx := reviewFixture(t, "st-ip", 1, 0, nil, false) // 1 park + this one = 2: below
	a := reviewAPI()
	row := issueRow{ID: "iss1", TeamID: "team1", Number: 7, Status: "active"}
	row.StatusID = strvalptr("st-ip")
	row.AssigneeID = strvalptr("ag1")
	_, tripped, reason, err := a.pauseWithBreakerTx(context.Background(), tx, "ws1", row,
		&Principal{AccountID: "ag1", Kind: "agent"}, "blocked on foreman", "")
	if err != nil || !tripped {
		t.Fatalf("pause: tripped = %v, err = %v", tripped, err)
	}
	if strings.Contains(reason, "escalated") {
		t.Fatalf("reason = %q — below the threshold it must be a plain park", reason)
	}
	upd := findCall(t, tx, "agent_paused = true")
	if !strings.Contains(upd.sql, "status_id") {
		t.Fatalf("park must move the card to the parking column: %q", upd.sql)
	}
}

// TestBreakerWithoutHumanParks: a human-less workspace has no
// escalation target — the breaker degrades to the plain park (the flag
// and the column keep the card out of the swarm's queue either way).
func TestBreakerWithoutHumanParks(t *testing.T) {
	tx := reviewFixture(t, "st-ip", 5, 0, pgx.ErrNoRows, false)
	a := reviewAPI()
	row := issueRow{ID: "iss1", TeamID: "team1", Number: 7, Status: "active"}
	row.StatusID = strvalptr("st-ip")
	row.AssigneeID = strvalptr("ag1")
	_, _, reason, err := a.pauseWithBreakerTx(context.Background(), tx, "ws1", row,
		&Principal{AccountID: "ag1", Kind: "agent"}, "blocked", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reason, "escalated") {
		t.Fatalf("reason = %q — no human to escalate to; it must park", reason)
	}
}

// TestReviewIssueResume: a parked card the human has answered resumes —
// the flag clears, the card moves to In Progress (out of the parking
// column and back into the swarm's queue), with the breadcrumb.
func TestReviewIssueResume(t *testing.T) {
	tx := reviewFixture(t, "st-hr", 1, 1, nil, true) // parked in the column, one reply since
	a := reviewAPI()
	verdict, recs, err := a.reviewIssueLocked(context.Background(), tx, "ws1", "iss1", "team1")
	if err != nil {
		t.Fatal(err)
	}
	if verdict != reviewResume || len(recs) == 0 {
		t.Fatalf("verdict = %v (%d records) — want resume", verdict, len(recs))
	}
	// The move back into the workflow is the resume (a card sitting in
	// the Human Review column is out of the swarm's queue by name).
	upd := findCall(t, tx, "update issues set agent_paused = false")
	if !strings.Contains(upd.sql, "status_id") || upd.args[len(upd.args)-1] != "st-ip" {
		t.Fatalf("resume must return the card to In Progress: %q %v", upd.sql, upd.args)
	}
}

// TestReviewIssueEscalate: a parked card at the threshold without a
// reply is handed to the human foreman as their work.
func TestReviewIssueEscalate(t *testing.T) {
	tx := reviewFixture(t, "st-hr", 3, 0, nil, true) // parked, at the threshold
	a := reviewAPI()
	verdict, _, err := a.reviewIssueLocked(context.Background(), tx, "ws1", "iss1", "team1")
	if err != nil {
		t.Fatal(err)
	}
	if verdict != reviewEscalate {
		t.Fatalf("verdict = %v — want escalate", verdict)
	}
	upd := findCall(t, tx, "agent_paused = false, assignee_id")
	if upd.args[1] != "human1" {
		t.Fatalf("escalation assignee = %v — want the human foreman", upd.args[1])
	}
}

// TestReviewIssueBelowThresholdNoop: below the counter and no reply,
// the review touches nothing — the card waits for a human.
func TestReviewIssueBelowThresholdNoop(t *testing.T) {
	tx := reviewFixture(t, "st-hr", 1, 0, nil, true)
	a := reviewAPI()
	verdict, recs, err := a.reviewIssueLocked(context.Background(), tx, "ws1", "iss1", "team1")
	if err != nil || verdict != reviewNone || recs != nil {
		t.Fatalf("verdict = %v, %d records — want none (a fresh park waits)", verdict, len(recs))
	}
	if len(tx.execs) != 1 || !strings.Contains(tx.execs[0].sql, "pg_advisory_xact_lock") {
		t.Fatalf("the no-op must write nothing (only the lock): %+v", tx.execs)
	}
}

// TestReviewIssueConcurrentResume: a card a human un-parks between the
// scan and the lock is left alone — the review re-reads under the lock
// and finds it already moved.
func TestReviewIssueConcurrentResume(t *testing.T) {
	tx := reviewFixture(t, "st-hr", 5, 0, nil, false) // at threshold, but NO LONGER paused
	a := reviewAPI()
	verdict, _, err := a.reviewIssueLocked(context.Background(), tx, "ws1", "iss1", "team1")
	if err != nil {
		t.Fatal(err)
	}
	if verdict != reviewNone {
		t.Fatalf("verdict = %v — a concurrently resumed card must be left alone", verdict)
	}
}

// TestReviewIssueMissingRow: a card deleted between the scan and the
// lock is not an error — it moved on.
func TestReviewIssueMissingRow(t *testing.T) {
	tx := &fakeTx{
		t: t,
		rules: []fakeRule{
			{frag: "i.version from issues i where i.id", rowErr: pgx.ErrNoRows},
		},
	}
	a := reviewAPI()
	verdict, _, err := a.reviewIssueLocked(context.Background(), tx, "ws1", "iss1", "team1")
	if err != nil || verdict != reviewNone {
		t.Fatalf("missing row: verdict = %v, err = %v — want none, nil", verdict, err)
	}
}

// TestReviewView pins the swarm plane's standing-duty indicator: the
// interval in effect, the "off" spelling, and the last pass's note.
func TestReviewView(t *testing.T) {
	rt := runtimeForTests(true)
	labels := map[time.Duration]string{30 * time.Minute: "30m", time.Hour: "1h", 45 * time.Second: "45s", 0: "off"}
	for d, want := range labels {
		if got := reviewIntervalLabel(d); got != want {
			t.Fatalf("label(%v) = %q, want %q", d, got, want)
		}
	}
	rt.a.cfg.SwarmReviewInterval = 30 * time.Minute
	if got := rt.reviewView().Interval; got != "30m" {
		t.Fatalf("interval = %q, want 30m", got)
	}
	rt.a.cfg.SwarmReviewInterval = 0
	if got := rt.reviewView().Interval; got != "off" {
		t.Fatalf("interval = %q, want off", got)
	}
	rt.recordReview("2 resumed, 1 escalated")
	view := rt.reviewView()
	if view.LastNote != "2 resumed, 1 escalated" || view.LastRunAt == nil {
		t.Fatalf("view = %+v — the last pass must be visible", view)
	}
}

// TestEscalationCommentDiscloses pins the disclosure contract: a human
// with zero context reads the cycle, the swarm's last words (fenced),
// and where the card went.
func TestEscalationCommentDiscloses(t *testing.T) {
	c := escalationComment(8, 3, "blocked on foreman\x00[injection]", "we reached the end", "In Progress")
	for _, want := range []string{"parked this card 3 times", "Last swarm report:", "Where things stand:", "In Progress", "agents never work a card a human owns"} {
		if !strings.Contains(c, want) {
			t.Fatalf("disclosure missing %q:\n%s", want, c)
		}
	}
	// The fenced reason must not carry control characters.
	// The fenced reason is one line of the composed comment: no control
	// character inside it (the line breaks between blocks are the
	// composition's own — each line becomes one paragraph).
	if i := strings.Index(c, "Last swarm report:"); i >= 0 {
		line := c[i:]
		if j := strings.IndexByte(line, '\n'); j > 0 {
			line = line[:j]
		}
		for _, r := range line {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("control character %q survived the disclosure fence: %q", r, line)
			}
		}
	}
}
