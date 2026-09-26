package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestIfMatchVersion(t *testing.T) {
	cases := []struct {
		header         string
		version        int
		present, valid bool
	}{
		{"", 0, false, false},
		{"7", 7, true, true},
		{`"7"`, 7, true, true},
		{`W/"7"`, 7, true, true},
		{" 12 ", 12, true, true},
		{"*", 0, true, false},
		{`"abc"`, 0, true, false},
		{"-1", 0, true, false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/", nil)
		if c.header != "" {
			r.Header.Set("If-Match", c.header)
		}
		v, present, valid := ifMatchVersion(r)
		if v != c.version || present != c.present || valid != c.valid {
			t.Errorf("If-Match %q = (%d, %v, %v), want (%d, %v, %v)",
				c.header, v, present, valid, c.version, c.present, c.valid)
		}
	}
}

// TestIssueWritePreconditions pins optimistic concurrency on the update
// path: agents must name the version they read; anyone who names one is
// held to it; humans without one keep last-write-wins.
func TestIssueWritePreconditions(t *testing.T) {
	const current = 5
	run := func(t *testing.T, p *Principal, ifMatch string) (*httptest.ResponseRecorder, *fakeTx) {
		t.Helper()
		row := issueRowFixture(7, "Test", "st2", int(2))
		row = append(row, current)
		reload := append(issueRowFixture(7, "New title", "st2", int(2)), current+1)
		pool := updateIssuePool(t, row, fakeRule{frag: "lower(ws.name) = $3", rowVals: []any{false}})
		tx := updateIssueTx(t, reload)
		tx.rules[0] = lockedVersionRule(current)
		pool.txs = []*fakeTx{tx}
		a := apiForTests(t, pool)
		req := requestFor(t, p, "POST", "http://x/api/v1/issues/i1", `{"title":"New title"}`, "id", "i1")
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		return record(t, a, req, a.handleUpdateIssue), tx
	}

	t.Run("an agent without If-Match is refused", func(t *testing.T) {
		rec, tx := run(t, agentPrincipal("ag1"), "")
		wantError(t, rec, 428, "If-Match with the issue version is required for API-token writes")
		if hasSQL(tx, "update issues") {
			t.Fatal("a refused write reached the table")
		}
	})
	t.Run("an agent on a stale version gets 412 and the current one", func(t *testing.T) {
		rec, tx := run(t, agentPrincipal("ag1"), `"4"`)
		checkStatus(t, rec, 412)
		var body struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Version != current {
			t.Fatalf("412 body = %s, want version %d", rec.Body.String(), current)
		}
		if got := rec.Header().Get("ETag"); got != `"5"` {
			t.Fatalf("ETag = %q, want the current version", got)
		}
		if hasSQL(tx, "update issues") {
			t.Fatal("a stale write reached the table")
		}
	})
	t.Run("an agent on the current version writes and learns the next", func(t *testing.T) {
		rec, tx := run(t, agentPrincipal("ag1"), `"5"`)
		checkStatus(t, rec, 200)
		if !hasSQL(tx, "update issues set title") {
			t.Fatal("the write did not land")
		}
		if got := rec.Header().Get("ETag"); got != `"6"` {
			t.Fatalf("ETag = %q, want the post-write version", got)
		}
		var body struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Version != current+1 {
			t.Fatalf("body version = %d, want %d", body.Version, current+1)
		}
	})
	t.Run("a human without If-Match keeps last-write-wins", func(t *testing.T) {
		rec, tx := run(t, humanPrincipal("u1"), "")
		checkStatus(t, rec, 200)
		if !hasSQL(tx, "update issues set title") {
			t.Fatal("the human write did not land")
		}
	})
	t.Run("a human who sends If-Match is held to it", func(t *testing.T) {
		rec, _ := run(t, humanPrincipal("u1"), "4")
		checkStatus(t, rec, 412)
	})
	t.Run("a malformed If-Match is a 400", func(t *testing.T) {
		rec, _ := run(t, agentPrincipal("ag1"), "*")
		wantError(t, rec, 400, "If-Match must be an issue version")
	})
}

func TestDeleteRequiresIfMatchForAgents(t *testing.T) {
	row := issueRowFixture(7, "Test", "st2", int(2))
	tx := &fakeTx{t: t, rules: []fakeRule{lockedVersionRule(0)}}
	pool := updateIssuePool(t, row, fakeRule{frag: "lower(ws.name) = $3", rowVals: []any{false}})
	pool.txs = []*fakeTx{tx}
	a := apiForTests(t, pool)
	rec := record(t, a, requestFor(t, agentPrincipal("ag1"), "DELETE", "http://x/api/v1/issues/i1", "", "id", "i1"), a.handleDeleteIssue)
	checkStatus(t, rec, 428)
	if hasSQL(tx, "set status = 'deleted'") {
		t.Fatal("an unconditioned agent delete reached the table")
	}
}
