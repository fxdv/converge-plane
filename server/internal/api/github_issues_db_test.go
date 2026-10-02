package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGitHubIssuesEnterTheQueueOnce(t *testing.T) {
	f := newWorkFixture(t)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet {
			t.Errorf("method %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"number":4,"title":"Fix the gate","body":"the check is wrong","state":"open"},
			{"number":5,"title":"A pull request","state":"open","pull_request":{}},
			{"number":6,"title":"Already closed","state":"closed"}
		]`))
	}))
	defer srv.Close()

	f.a.cfg.GitHubRepos = []string{"acme/app", "labs/*"}
	f.a.cfg.GitHubIssueTeam = f.t1
	g := &githubPoller{a: f.a, base: srv.URL, client: srv.Client(), interval: time.Minute}
	if err := g.importOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := g.importOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	var n int
	var title, category string
	if err := f.pool.QueryRow(context.Background(), `
		select count(*), coalesce(min(i.title), ''), coalesce(min(s.category), '')
		from github_issue_links l
		join issues i on i.id = l.issue_id
		join workflow_statuses s on s.id = i.status_id
		where l.workspace_id = $1 and l.repo = 'acme/app'`, f.ws).Scan(&n, &title, &category); err != nil {
		t.Fatal(err)
	}
	if n != 1 || title != "Fix the gate" || category != "UNSTARTED" {
		t.Fatalf("imported = %d %q %s", n, title, category)
	}
	if calls < 1 {
		t.Fatal("the list was not read")
	}
}

// TestImportedIssueClosesWhenThePollerReadsAMerge is the BRAVO-1 path:
// an open GitHub issue is copied into the queue, an agent claims it and
// reports a cost, a trace step, and a pull request, and the poller
// reading that pull request as merged moves the issue to Done. The
// pull request is already merged when it is linked. A second copy is
// not created, and the test server is only read.
func TestImportedIssueClosesWhenThePollerReadsAMerge(t *testing.T) {
	f := newWorkFixture(t)
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet {
			http.Error(w, "read only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/repos/acme/app/issues":
			_, _ = w.Write([]byte(`[{"number":141,"title":"Delete keys from a map","body":"want a delete","state":"open"}]`))
		case r.URL.Path == "/repos/acme/app/pulls/22":
			_, _ = w.Write([]byte(`{"state":"closed","merged":true,"title":"Bump the dependency","html_url":"https://github.com/acme/app/pull/22","merged_at":"2026-09-28T00:00:00Z"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	f.a.cfg.GitHubRepos = []string{"acme/app"}
	f.a.cfg.GitHubIssueTeam = f.t1
	f.a.cfg.GitHubAutoDone = true
	g := &githubPoller{a: f.a, base: srv.URL, client: srv.Client(), interval: time.Minute}
	ctx := context.Background()
	if err := g.importOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var issue string
	if err := f.pool.QueryRow(ctx, `
		select issue_id::text from github_issue_links
		where workspace_id = $1 and repo = 'acme/app' and number = 141`, f.ws).Scan(&issue); err != nil {
		t.Fatal(err)
	}
	work := f.claim(f.ext1, issue, http.StatusOK)
	f.report(f.ext1, issue, `{"claimId":"`+work.Claim.ID+`","totals":{"costMicros":2500},"events":[{"kind":"note","message":"linked the merged pull request"}],"evidence":[{"kind":"pull_request","url":"https://github.com/acme/app/pull/22"}]}`, http.StatusOK)
	if _, err := g.pollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.importOnce(ctx); err != nil {
		t.Fatal(err)
	}

	var category, prState string
	var cost, steps, copies int
	if err := f.pool.QueryRow(ctx, `
		select s.category,
		       coalesce((select state from issue_pull_requests where issue_id = $1 and number = 22), ''),
		       coalesce((select sum(cost_micros) from agent_runs where issue_id = $1), 0),
		       coalesce((select count(*) from agent_run_events e join agent_runs r on r.id = e.run_id where r.issue_id = $1), 0),
		       (select count(*) from github_issue_links where workspace_id = $2 and repo = 'acme/app')
		from issues i
		join workflow_statuses s on s.id = i.status_id
		where i.id = $1`, issue, f.ws).Scan(&category, &prState, &cost, &steps, &copies); err != nil {
		t.Fatal(err)
	}
	if category != "COMPLETED" || prState != "merged" || cost != 2500 || steps != 1 || copies != 1 {
		t.Fatalf("path = %s pr %s cost %d steps %d copies %d", category, prState, cost, steps, copies)
	}
	var notes int
	if err := f.pool.QueryRow(ctx, `
		select count(*) from notifications
		where issue_id = $1 and account_id = $2 and type = 'closed'`, issue, f.owner).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if notes != 1 {
		t.Fatalf("owner notifications = %d, want 1", notes)
	}
	for _, m := range methods {
		if len(m) < 4 || m[:4] != "GET " {
			t.Fatalf("GitHub was not only read: %v", methods)
		}
	}
}
