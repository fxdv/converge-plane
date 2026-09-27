// pullrequests_db_test.go — GitHub pull request links against a real
// database and a fake GitHub: only tracked github.com PRs link, the
// poller follows a PR from open through an ETag-revalidated check to
// merged, the merge moves the issue to Done as the system once no PR is
// still open, and failures back off or pause without leaking the token.
// Members link and unlink by hand, and an unlinked PR stays unlinked.
// Gated on CONVERGE_TEST_DATABASE_URL.
package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeReply struct {
	status int
	header map[string]string
	body   string
}

type fakeRequest struct {
	path   string
	header http.Header
}

// fakeGitHub answers each path from its queue; the last reply repeats.
type fakeGitHub struct {
	mu      sync.Mutex
	replies map[string][]fakeReply
	seen    []fakeRequest
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seen = append(g.seen, fakeRequest{path: r.URL.Path, header: r.Header.Clone()})
	q := g.replies[r.URL.Path]
	if len(q) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	reply := q[0]
	if len(q) > 1 {
		g.replies[r.URL.Path] = q[1:]
	}
	for k, v := range reply.header {
		w.Header().Set(k, v)
	}
	w.WriteHeader(reply.status)
	_, _ = w.Write([]byte(reply.body))
}

func (g *fakeGitHub) queue(path string, replies ...fakeReply) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.replies[path] = replies
}

func (g *fakeGitHub) requests(path string) []fakeRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []fakeRequest
	for _, r := range g.seen {
		if r.path == path {
			out = append(out, r)
		}
	}
	return out
}

func pullJSON(state string, merged bool, title string) fakeReply {
	mergedAt := "null"
	if merged {
		mergedAt = `"2026-09-27T10:00:00Z"`
	}
	return fakeReply{status: http.StatusOK, header: map[string]string{"ETag": `W/"` + state + fmt.Sprint(merged) + `"`},
		body: fmt.Sprintf(`{"number":1,"state":%q,"draft":false,"title":%q,"merged":%v,"merged_at":%s,"body":"%s"}`,
			state, title, merged, mergedAt, strings.Repeat("x", 100))}
}

// pullJSONAt answers for a PR GitHub files under repo, as it does once
// the repository the link names was renamed or moved.
func pullJSONAt(repo string, number int, state, title string) fakeReply {
	return fakeReply{status: http.StatusOK, header: map[string]string{"ETag": `W/"at"`},
		body: fmt.Sprintf(`{"number":%d,"state":%q,"draft":false,"title":%q,"merged":false,"merged_at":null,"html_url":"https://github.com/%s/pull/%d"}`,
			number, state, title, repo, number)}
}

const testGitHubToken = "github_pat_TEST_do_not_log_1234567890"

type prFixture struct {
	*workFixture
	gh  *fakeGitHub
	srv *httptest.Server
}

func newPRFixture(t *testing.T) *prFixture {
	f := newWorkFixture(t)
	f.a.cfg.GitHubRepos = []string{"acme/app", "labs/*"}
	f.a.cfg.GitHubPollInterval = time.Minute
	f.a.cfg.GitHubAutoDone = true
	f.a.cfg.Version = "test"
	gh := &fakeGitHub{replies: map[string][]fakeReply{}}
	srv := httptest.NewServer(gh)
	t.Cleanup(srv.Close)
	return &prFixture{workFixture: f, gh: gh, srv: srv}
}

func (f *prFixture) poller() *githubPoller {
	g := newGitHubPoller(f.a)
	g.base = f.srv.URL
	g.token = testGitHubToken
	return g
}

// poll makes the issue's links the oldest due ones, so a pass leases
// them ahead of any other test's leftovers, and runs one pass.
func (f *prFixture) poll(g *githubPoller, issueID string) {
	f.t.Helper()
	f.exec(`update issue_pull_requests set next_check_at = now() - interval '1 day'
		where issue_id = $1 and next_check_at is not null`, issueID)
	if _, err := g.pollOnce(context.Background()); err != nil {
		f.t.Fatalf("poll: %v", err)
	}
}

type linkState struct {
	state, title, etag string
	failures           int
	due                bool
	mergedAt           bool
}

func (f *prFixture) link(issueID string, number int) linkState {
	f.t.Helper()
	var s linkState
	var title, etag *string
	if err := f.pool.QueryRow(context.Background(), `
		select state, title, etag, failures, next_check_at is not null, merged_at is not null
		from issue_pull_requests where issue_id = $1 and number = $2`, issueID, number).
		Scan(&s.state, &title, &etag, &s.failures, &s.due, &s.mergedAt); err != nil {
		f.t.Fatalf("read link %d: %v", number, err)
	}
	s.title, s.etag = strval(title), strval(etag)
	return s
}

// repos lists the repositories the issue's links to PR number name.
func (f *prFixture) repos(issueID string, number int) []string {
	f.t.Helper()
	rows, err := f.pool.Query(context.Background(),
		`select repo from issue_pull_requests where issue_id = $1 and number = $2 order by repo`, issueID, number)
	if err != nil {
		f.t.Fatalf("read repos: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var repo string
		if err := rows.Scan(&repo); err != nil {
			f.t.Fatalf("read repos: %v", err)
		}
		out = append(out, repo)
	}
	return out
}

func (f *prFixture) linkID(issueID, repo string, number int) string {
	f.t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(),
		`select id from issue_pull_requests where issue_id = $1 and repo = $2 and number = $3`,
		issueID, repo, number).Scan(&id); err != nil {
		f.t.Fatalf("read link %s#%d: %v", repo, number, err)
	}
	return id
}

func (f *prFixture) lastAction(id string) string {
	f.t.Helper()
	var action string
	if err := f.pool.QueryRow(context.Background(), `
		select action from sync_outbox where workspace_id = $1 and model_name = $2 and model_id = $3
		order by sequence_id desc limit 1`, f.ws, modelPullRequest, id).Scan(&action); err != nil {
		f.t.Fatalf("read outbox for %s: %v", id, err)
	}
	return action
}

func (f *prFixture) linkCount(issueID string) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`select count(*) from issue_pull_requests where issue_id = $1`, issueID).Scan(&n); err != nil {
		f.t.Fatalf("count links: %v", err)
	}
	return n
}

func (f *prFixture) statusOf(issueID string) string {
	f.t.Helper()
	row, err := f.a.issueByID(context.Background(), issueID)
	if err != nil {
		f.t.Fatalf("read issue: %v", err)
	}
	return strval(row.StatusID)
}

// claimAndReport claims the issue for ext1 and reports the PR URLs as
// pull_request evidence.
func (f *prFixture) claimAndReport(issueID string, urls ...string) string {
	f.t.Helper()
	claimID := f.claim(f.ext1, issueID, http.StatusOK).Claim.ID
	f.reportPRs(issueID, claimID, urls...)
	return claimID
}

func (f *prFixture) reportPRs(issueID, claimID string, urls ...string) {
	f.t.Helper()
	ev := make([]string, len(urls))
	for i, u := range urls {
		ev[i] = fmt.Sprintf(`{"kind":"pull_request","url":%q}`, u)
	}
	f.report(f.ext1, issueID, fmt.Sprintf(`{"claimId":%q,"evidence":[%s]}`, claimID, strings.Join(ev, ",")), http.StatusOK)
}

func TestPullRequestLinks(t *testing.T) {
	f := newPRFixture(t)

	t.Run("only tracked github.com pull requests link", func(t *testing.T) {
		issue := f.issue(f.t1, f.todo, f.ext1)
		claimID := f.claim(f.ext1, issue, http.StatusOK).Claim.ID
		f.report(f.ext1, issue, `{"claimId":"`+claimID+`","evidence":[
			{"kind":"pull_request","url":"https://github.com/Acme/App/pull/12/files"},
			{"kind":"pull_request","url":"https://github.com/labs/tool/pull/3"},
			{"kind":"pull_request","url":"https://github.com/other/repo/pull/1"},
			{"kind":"link","url":"https://github.com/acme/app/pull/13"},
			{"kind":"pull_request","url":"https://github.example/acme/app/pull/14"}]}`, http.StatusOK)
		if n := f.linkCount(issue); n != 2 {
			t.Fatalf("links = %d, want acme/app#12 and labs/tool#3", n)
		}
		if s := f.link(issue, 12); s.state != "pending" || !s.due {
			t.Fatalf("new link = %+v, want pending and due", s)
		}
		var id string
		if err := f.pool.QueryRow(context.Background(),
			`select id from issue_pull_requests where issue_id = $1 and number = 12`, issue).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if n := f.outboxCount(modelPullRequest, id); n != 1 {
			t.Fatalf("outbox records for the link = %d, want 1", n)
		}
		// Heartbeats repeat evidence: the same link neither duplicates
		// nor re-emits.
		f.reportPRs(issue, claimID, "https://github.com/acme/app/pull/12")
		if n := f.linkCount(issue); n != 2 {
			t.Fatalf("links after a repeat = %d", n)
		}
		if n := f.outboxCount(modelPullRequest, id); n != 1 {
			t.Fatalf("outbox records after a repeat = %d, want 1", n)
		}
		recs, err := f.a.collectModel(context.Background(), modelPullRequest, f.ws, f.owner)
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, rec := range recs {
			if rec.ModelID == id {
				found = true
				body := string(rec.Data)
				for _, want := range []string{`"url":"https://github.com/acme/app/pull/12"`, `"state":"pending"`,
					`"repo":"acme/app"`, `"number":12`, `"title":null`, `"mergedAt":null`, `"linkedById":"` + f.ext1 + `"`} {
					if !strings.Contains(body, want) {
						t.Fatalf("bootstrap record %s lacks %s", body, want)
					}
				}
			}
		}
		if !found {
			t.Fatal("bootstrap did not carry the link")
		}
	})

	t.Run("tracking off links nothing", func(t *testing.T) {
		repos := f.a.cfg.GitHubRepos
		f.a.cfg.GitHubRepos = nil
		defer func() { f.a.cfg.GitHubRepos = repos }()
		issue := f.issue(f.t1, f.todo, f.ext1)
		f.claimAndReport(issue, "https://github.com/acme/app/pull/12")
		if n := f.linkCount(issue); n != 0 {
			t.Fatalf("links with tracking off = %d", n)
		}
	})

	t.Run("an issue keeps at most 20 links", func(t *testing.T) {
		issue := f.issue(f.t1, f.todo, f.ext1)
		f.exec(`insert into issue_pull_requests (workspace_id, issue_id, repo, number)
			select $1, $2, 'acme/app', n from generate_series(100, 119) n`, f.ws, issue)
		f.claimAndReport(issue, "https://github.com/acme/app/pull/12")
		if n := f.linkCount(issue); n != 20 {
			t.Fatalf("links = %d, want the cap of 20", n)
		}
	})

	t.Run("open, revalidated, merged: the issue moves to Done as the system", func(t *testing.T) {
		issue := f.issue(f.t1, f.doing, f.ext1)
		f.claimAndReport(issue, "https://github.com/acme/app/pull/21")
		path := "/repos/acme/app/pulls/21"
		f.gh.queue(path,
			pullJSON("open", false, "Fix the \u202egnilbbub\u202c title"),
			fakeReply{status: http.StatusNotModified},
			pullJSON("closed", true, "Fix the bubbling title"))
		g := f.poller()

		f.poll(g, issue)
		s := f.link(issue, 21)
		if s.state != "open" || s.title != "Fix the gnilbbub title" || s.etag != `W/"openfalse"` || !s.due {
			t.Fatalf("after the first check = %+v", s)
		}
		reqs := f.gh.requests(path)
		if len(reqs) != 1 {
			t.Fatalf("requests = %d", len(reqs))
		}
		h := reqs[0].header
		if h.Get("Authorization") != "Bearer "+testGitHubToken || h.Get("Accept") != "application/vnd.github+json" ||
			h.Get("X-GitHub-Api-Version") != "2022-11-28" || h.Get("User-Agent") != "converge/test" || h.Get("If-None-Match") != "" {
			t.Fatalf("first request headers = %v", h)
		}

		version := f.issueVersion(issue)
		f.poll(g, issue)
		if reqs = f.gh.requests(path); len(reqs) != 2 || reqs[1].header.Get("If-None-Match") != `W/"openfalse"` {
			t.Fatalf("revalidation did not send the ETag: %d requests", len(reqs))
		}
		if s := f.link(issue, 21); s.state != "open" || !s.due {
			t.Fatalf("after a 304 = %+v", s)
		}
		if f.statusOf(issue) != f.doing || f.issueVersion(issue) != version {
			t.Fatal("a 304 must not touch the issue")
		}

		f.poll(g, issue)
		if s := f.link(issue, 21); s.state != "merged" || !s.mergedAt || s.due {
			t.Fatalf("after the merge = %+v, want merged and no longer polled", s)
		}
		if f.statusOf(issue) != f.done {
			t.Fatalf("status = %s, want Done", f.statusOf(issue))
		}
		if f.issueVersion(issue) <= version {
			t.Fatal("the move must bump the issue version")
		}
		var (
			actor           *string
			actorType, note string
		)
		if err := f.pool.QueryRow(context.Background(), `
			select actor_id::text, actor_type, summary from issue_history
			where issue_id = $1 and field = 'status' order by created_at desc limit 1`, issue).
			Scan(&actor, &actorType, &note); err != nil {
			t.Fatal(err)
		}
		if actor != nil || actorType != "system" || note != "acme/app#21 merged on GitHub" {
			t.Fatalf("history = %v %q %q", actor, actorType, note)
		}
		var (
			ntype     string
			nActor    *string
			actorName string
		)
		if err := f.pool.QueryRow(context.Background(), `
			select type, actor_id::text, actor_name from notifications
			where issue_id = $1 and account_id = $2`, issue, f.owner).Scan(&ntype, &nActor, &actorName); err != nil {
			t.Fatal(err)
		}
		if ntype != notifClosed || nActor != nil || actorName != "Converge" {
			t.Fatalf("notification = %q %v %q", ntype, nActor, actorName)
		}
		// Merged is final: no further request, and a re-report does not
		// re-arm it.
		f.poll(g, issue)
		if n := len(f.gh.requests(path)); n != 3 {
			t.Fatalf("requests after the merge = %d, want 3", n)
		}
	})

	t.Run("the move waits until no linked PR is open", func(t *testing.T) {
		issue := f.issue(f.t1, f.doing, f.ext1)
		f.claimAndReport(issue,
			"https://github.com/acme/app/pull/31",
			"https://github.com/acme/app/pull/32",
			"https://github.com/acme/app/pull/33")
		f.gh.queue("/repos/acme/app/pulls/31", pullJSON("closed", true, "Part one"))
		f.gh.queue("/repos/acme/app/pulls/32", pullJSON("open", false, "Part two"), pullJSON("closed", true, "Part two"))
		f.gh.queue("/repos/acme/app/pulls/33", pullJSON("closed", false, "Abandoned"))
		g := f.poller()
		f.poll(g, issue)
		if f.link(issue, 33).state != "closed" || f.link(issue, 33).due {
			t.Fatalf("closed PR = %+v, want closed and no longer polled", f.link(issue, 33))
		}
		if f.statusOf(issue) != f.doing {
			t.Fatal("the issue moved while #32 was still open")
		}
		f.poll(g, issue)
		if f.statusOf(issue) != f.done {
			t.Fatal("the issue did not move once every open PR merged")
		}
	})

	t.Run("a finished, reopened, or opted-out issue stays put", func(t *testing.T) {
		canceled := testUUID()
		f.exec(`insert into workflow_statuses (id, team_id, name, category, position) values ($1, $2, 'Canceled', 'CANCELED', 5)`,
			canceled, f.t1)
		issue := f.issue(f.t1, f.doing, f.ext1)
		f.claimAndReport(issue, "https://github.com/acme/app/pull/41")
		f.exec(`update issues set status_id = $2 where id = $1`, issue, canceled)
		f.gh.queue("/repos/acme/app/pulls/41", pullJSON("closed", true, "Late merge"))
		f.poll(f.poller(), issue)
		if f.statusOf(issue) != canceled {
			t.Fatal("a canceled issue was moved")
		}

		f.a.cfg.GitHubAutoDone = false
		defer func() { f.a.cfg.GitHubAutoDone = true }()
		other := f.issue(f.t1, f.doing, f.ext1)
		f.claimAndReport(other, "https://github.com/acme/app/pull/42")
		f.gh.queue("/repos/acme/app/pulls/42", pullJSON("closed", true, "Merged"))
		f.poll(f.poller(), other)
		if f.link(other, 42).state != "merged" || f.statusOf(other) != f.doing {
			t.Fatal("with auto-done off the PR is tracked but the issue stays put")
		}
	})

	t.Run("a closed PR stops polling until it is reported again", func(t *testing.T) {
		issue := f.issue(f.t1, f.doing, f.ext1)
		claimID := f.claimAndReport(issue, "https://github.com/acme/app/pull/51")
		f.gh.queue("/repos/acme/app/pulls/51", pullJSON("closed", false, "Closed"), pullJSON("open", false, "Reopened"))
		g := f.poller()
		f.poll(g, issue)
		if s := f.link(issue, 51); s.state != "closed" || s.due {
			t.Fatalf("closed = %+v", s)
		}
		f.reportPRs(issue, claimID, "https://github.com/acme/app/pull/51")
		if !f.link(issue, 51).due {
			t.Fatal("reporting a closed PR again must re-arm it")
		}
		f.poll(g, issue)
		if f.link(issue, 51).state != "open" {
			t.Fatal("the reopened PR was not picked up")
		}
	})

	t.Run("a renamed repository: the link takes the new name", func(t *testing.T) {
		issue := f.issue(f.t1, f.doing, f.ext1)
		f.claimAndReport(issue, "https://github.com/labs/tool/pull/71")
		id := f.linkID(issue, "labs/tool", 71)
		f.gh.queue("/repos/labs/tool/pulls/71", fakeReply{status: http.StatusMovedPermanently,
			header: map[string]string{"Location": f.srv.URL + "/repositories/4242/pulls/71"}})
		f.gh.queue("/repositories/4242/pulls/71", pullJSONAt("labs/tool-next", 71, "open", "Renamed repo"))
		f.poll(f.poller(), issue)
		if got := f.repos(issue, 71); len(got) != 1 || got[0] != "labs/tool-next" {
			t.Fatalf("repos = %v, want the new name", got)
		}
		if s := f.link(issue, 71); s.state != "open" || !s.due {
			t.Fatalf("after the rename = %+v", s)
		}
		recs, err := f.a.collectModel(context.Background(), modelPullRequest, f.ws, f.owner)
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range recs {
			if rec.ModelID == id && !strings.Contains(string(rec.Data), `"url":"https://github.com/labs/tool-next/pull/71"`) {
				t.Fatalf("the record still names the old repository: %s", rec.Data)
			}
		}
	})

	t.Run("a renamed repository the issue already links under the new name keeps one link", func(t *testing.T) {
		issue := f.issue(f.t1, f.doing, f.ext1)
		f.claimAndReport(issue, "https://github.com/labs/old/pull/72", "https://github.com/labs/new/pull/72")
		oldID := f.linkID(issue, "labs/old", 72)
		f.gh.queue("/repos/labs/old/pulls/72", pullJSONAt("labs/new", 72, "open", "Same PR"))
		f.gh.queue("/repos/labs/new/pulls/72", pullJSONAt("labs/new", 72, "open", "Same PR"))
		f.poll(f.poller(), issue)
		if got := f.repos(issue, 72); len(got) != 1 || got[0] != "labs/new" {
			t.Fatalf("repos = %v, want only labs/new", got)
		}
		if a := f.lastAction(oldID); a != "DELETE" {
			t.Fatalf("the merged-away link's last record = %s, want DELETE", a)
		}
	})

	t.Run("a repository moved off the allowlist is not followed", func(t *testing.T) {
		issue := f.issue(f.t1, f.doing, f.ext1)
		f.claimAndReport(issue, "https://github.com/labs/tool/pull/73")
		f.gh.queue("/repos/labs/tool/pulls/73", pullJSONAt("elsewhere/tool", 73, "open", "Moved away"))
		f.poll(f.poller(), issue)
		if got := f.repos(issue, 73); len(got) != 1 || got[0] != "labs/tool" {
			t.Fatalf("repos = %v, want the tracked name kept", got)
		}
		if s := f.link(issue, 73); s.state != "unavailable" || s.due || s.title != "" {
			t.Fatalf("after a move off the allowlist = %+v, want unavailable and no longer checked", s)
		}
	})

	t.Run("failures back off, limits pause, and the token stays out of the logs", func(t *testing.T) {
		var logs bytes.Buffer
		log := f.a.log
		f.a.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		defer func() { f.a.log = log }()

		issue := f.issue(f.t1, f.doing, f.ext1)
		f.claimAndReport(issue, "https://github.com/acme/app/pull/61")
		path := "/repos/acme/app/pulls/61"
		g := f.poller()

		f.gh.queue(path, fakeReply{status: http.StatusNotFound})
		f.poll(g, issue)
		if s := f.link(issue, 61); s.state != "unavailable" || s.failures != 1 || !s.due {
			t.Fatalf("after a 404 = %+v", s)
		}
		var wait float64
		if err := f.pool.QueryRow(context.Background(), `
			select extract(epoch from next_check_at - now()) from issue_pull_requests
			where issue_id = $1`, issue).Scan(&wait); err != nil {
			t.Fatal(err)
		}
		if wait < 110 || wait > 130 {
			t.Fatalf("first backoff = %.0fs, want about two intervals", wait)
		}

		f.gh.queue(path, fakeReply{status: http.StatusBadGateway})
		f.poll(g, issue)
		if s := f.link(issue, 61); s.state != "unavailable" || s.failures != 2 {
			t.Fatalf("after a 502 = %+v", s)
		}

		// A redirect off the API host is not followed.
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("the poller followed a redirect off the API host, Authorization=%q", r.Header.Get("Authorization"))
		}))
		defer other.Close()
		f.gh.queue(path, fakeReply{status: http.StatusFound, header: map[string]string{"Location": other.URL + path}})
		f.poll(g, issue)
		if s := f.link(issue, 61); s.failures != 3 {
			t.Fatalf("after an off-host redirect = %+v", s)
		}

		reset := fmt.Sprint(time.Now().Add(10 * time.Minute).Unix())
		f.gh.queue(path, fakeReply{status: http.StatusForbidden,
			header: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": reset}})
		f.poll(g, issue)
		if !g.paused() {
			t.Fatal("an exhausted rate limit must pause the poller")
		}
		before := len(f.gh.requests(path))
		f.poll(g, issue)
		if len(f.gh.requests(path)) != before {
			t.Fatal("a paused poller still called GitHub")
		}
		if s := f.link(issue, 61); s.failures != 3 {
			t.Fatalf("a rate limit must not count as the PR's failure: %+v", s)
		}

		g = f.poller()
		f.gh.queue(path, fakeReply{status: http.StatusForbidden,
			body: `{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`})
		f.poll(g, issue)
		if !g.paused() {
			t.Fatal("a secondary rate limit without headers must pause the poller")
		}
		if s := f.link(issue, 61); s.failures != 3 || s.state != "unavailable" {
			t.Fatalf("a secondary rate limit must not touch the PR: %+v", s)
		}

		g = f.poller()
		f.gh.queue(path, fakeReply{status: http.StatusUnauthorized, body: `{"message":"Bad credentials"}`})
		f.poll(g, issue)
		if !g.paused() {
			t.Fatal("a rejected token must pause the poller")
		}
		if strings.Contains(logs.String(), testGitHubToken) || strings.Contains(logs.String(), "github_pat_") {
			t.Fatalf("the token reached the log:\n%s", logs.String())
		}
		if !strings.Contains(logs.String(), "GitHub rejected CONVERGE_GITHUB_TOKEN") {
			t.Fatalf("the rejected token was not reported:\n%s", logs.String())
		}
	})
}

func (f *prFixture) linkByHand(issueID, url string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.call(humanPrincipal(f.owner), (*API).handleLinkPullRequest, "POST",
		"/api/v1/issues/"+issueID+"/pull_requests", fmt.Sprintf(`{"url":%q}`, url), "id", issueID)
}

func (f *prFixture) unlink(issueID, linkID string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.call(humanPrincipal(f.owner), (*API).handleUnlinkPullRequest, "DELETE",
		"/api/v1/issues/"+issueID+"/pull_requests/"+linkID, "", "id", issueID, "linkId", linkID)
}

func (f *prFixture) unlinked(linkID string) bool {
	f.t.Helper()
	var gone bool
	if err := f.pool.QueryRow(context.Background(),
		`select unlinked_at is not null from issue_pull_requests where id = $1`, linkID).Scan(&gone); err != nil {
		f.t.Fatalf("read link %s: %v", linkID, err)
	}
	return gone
}

func TestPullRequestAllowlistChange(t *testing.T) {
	f := newPRFixture(t)
	issue := f.issue(f.t1, f.doing, f.ext1)
	checkStatus(t, f.linkByHand(issue, "https://github.com/acme/app/pull/96"), http.StatusCreated)
	checkStatus(t, f.linkByHand(issue, "https://github.com/labs/tool/pull/97"), http.StatusCreated)
	f.gh.queue("/repos/acme/app/pulls/96", pullJSON("open", false, "Still read"))
	f.gh.queue("/repos/labs/tool/pulls/97", pullJSON("open", false, "Not read"))

	f.a.cfg.GitHubRepos = []string{"acme/app"}
	f.poll(f.poller(), issue)
	if len(f.gh.requests("/repos/labs/tool/pulls/97")) != 0 {
		t.Fatal("the poller read a repository taken off the allowlist")
	}
	if s := f.link(issue, 97); s.state != "pending" || !f.dueNow(issue, 97) {
		t.Fatalf("a link to a repository off the allowlist changed: %+v", s)
	}
	if f.link(issue, 96).title != "Still read" {
		t.Fatal("the repository still on the allowlist was not read")
	}

	f.a.cfg.GitHubRepos = []string{"acme/app", "labs/*"}
	f.poll(f.poller(), issue)
	if f.link(issue, 97).title != "Not read" {
		t.Fatal("checks must resume when the repository comes back")
	}
}

func TestPullRequestManualLinks(t *testing.T) {
	f := newPRFixture(t)

	t.Run("a member links by hand under the evidence rules", func(t *testing.T) {
		me := f.call(humanPrincipal(f.owner), (*API).handleGetUser, "GET", "/api/v1/users", "")
		checkStatus(t, me, http.StatusOK)
		var u struct {
			Features struct{ GitHubPullRequests bool }
		}
		decodeBody(t, me, &u)
		if !u.Features.GitHubPullRequests {
			t.Fatal("GET /users must offer hand-made links on a server that follows GitHub repositories")
		}

		issue := f.issue(f.t1, f.doing, f.ext1)
		rec := f.linkByHand(issue, "https://github.com/Acme/App/pull/81/files")
		checkStatus(t, rec, http.StatusCreated)
		var v struct {
			ID, Repo, State, LinkedByID string
			Number                      int
		}
		decodeBody(t, rec, &v)
		if v.Repo != "acme/app" || v.Number != 81 || v.State != "pending" || v.LinkedByID != f.owner {
			t.Fatalf("link = %+v", v)
		}
		if !f.link(issue, 81).due || f.lastAction(v.ID) != "CREATE" {
			t.Fatal("a hand-made link must be due at once and on the feed")
		}
		rec = f.linkByHand(issue, "https://github.com/acme/app/pull/81")
		checkStatus(t, rec, http.StatusOK)
		if n := f.linkCount(issue); n != 1 {
			t.Fatalf("links after linking again = %d", n)
		}
		wantError(t, f.linkByHand(issue, "https://github.com/other/repo/pull/1"), http.StatusUnprocessableEntity,
			"Converge does not follow other/repo; the repositories it follows are set by the server operator")
		wantError(t, f.linkByHand(issue, "https://gitlab.com/acme/app/-/merge_requests/1"), http.StatusUnprocessableEntity,
			"url must be a github.com pull request, like https://github.com/owner/repo/pull/123")
	})

	t.Run("an unlinked PR stays unlinked until a member links it again", func(t *testing.T) {
		issue := f.issue(f.t1, f.doing, f.ext1)
		claimID := f.claimAndReport(issue, "https://github.com/acme/app/pull/82")
		id := f.linkID(issue, "acme/app", 82)
		other := f.issue(f.t1, f.doing, f.ext1)
		wantError(t, f.unlink(other, id), http.StatusNotFound, "not found")

		checkStatus(t, f.unlink(issue, id), http.StatusOK)
		if !f.unlinked(id) || f.link(issue, 82).due || f.lastAction(id) != "DELETE" {
			t.Fatal("unlinking must leave a tombstone that is not checked and is off the feed")
		}
		wantError(t, f.unlink(issue, id), http.StatusNotFound, "not found")

		f.reportPRs(issue, claimID, "https://github.com/acme/app/pull/82")
		if !f.unlinked(id) || f.lastAction(id) != "DELETE" {
			t.Fatal("the agent's next heartbeat revived an unlinked PR")
		}
		recs, err := f.a.collectModel(context.Background(), modelPullRequest, f.ws, f.owner)
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range recs {
			if rec.ModelID == id {
				t.Fatal("bootstrap carried an unlinked PR")
			}
		}

		// A check leased before the unlink lands after it: it must not
		// bring the link back.
		g := f.poller()
		ref := pullRef{Repo: "acme/app", Number: 82}
		if err := g.apply(context.Background(), ref, leasedPull{id: id, issueID: issue},
			githubResult{outcome: githubFetched, pull: githubPull{State: "open", Title: "Late answer"}}); err != nil {
			t.Fatal(err)
		}
		if err := g.apply(context.Background(), ref, leasedPull{id: id, issueID: issue},
			githubResult{outcome: githubNotModified}); err != nil {
			t.Fatal(err)
		}
		if s := f.link(issue, 82); s.due || s.title != "" || f.lastAction(id) != "DELETE" {
			t.Fatalf("a late check touched the tombstone: %+v", s)
		}

		checkStatus(t, f.linkByHand(issue, "https://github.com/acme/app/pull/82"), http.StatusCreated)
		if f.unlinked(id) || !f.link(issue, 82).due || f.link(issue, 82).state != "pending" || f.lastAction(id) != "CREATE" {
			t.Fatal("linking by hand must bring the PR back, pending and due")
		}
	})

	t.Run("an unlinked PR does not hold the issue back from Done", func(t *testing.T) {
		issue := f.issue(f.t1, f.doing, f.ext1)
		f.claimAndReport(issue, "https://github.com/acme/app/pull/83", "https://github.com/acme/app/pull/84")
		checkStatus(t, f.unlink(issue, f.linkID(issue, "acme/app", 83)), http.StatusOK)
		f.gh.queue("/repos/acme/app/pulls/84", pullJSON("closed", true, "The real fix"))
		f.poll(f.poller(), issue)
		if f.statusOf(issue) != f.done {
			t.Fatal("a pending but unlinked PR kept the issue from moving to Done")
		}
	})
}
