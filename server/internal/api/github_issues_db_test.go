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
