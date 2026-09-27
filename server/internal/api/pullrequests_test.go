package api

import (
	"net/http"
	"testing"
	"time"

	"converge/internal/config"
)

func TestParsePullRequestURL(t *testing.T) {
	ok := map[string]pullRef{
		"https://github.com/acme/app/pull/12":                    {"acme/app", 12},
		"https://github.com/Acme/App/pull/12/files":              {"acme/app", 12},
		"http://www.github.com/acme/app.site/pull/7?w=1#diff-1":  {"acme/app.site", 7},
		"https://GitHub.com/acme-labs/my_repo/pull/2147483647":   {"acme-labs/my_repo", 2147483647},
		"https://github.com/acme/app/pull/3/commits/abc123":      {"acme/app", 3},
		"https://github.com/a/b/pull/1#issuecomment-1234567":     {"a/b", 1},
		"https://github.com/acme/app/pull/12/":                   {"acme/app", 12},
		"https://github.com/0rg/-dash-in-name-is-fine/pull/5":    {"0rg/-dash-in-name-is-fine", 5},
		"https://github.com/acme/app/pull/99/checks?check_run=1": {"acme/app", 99},
	}
	for raw, want := range ok {
		got, parsed := parsePullRequestURL(raw)
		if !parsed || got != want {
			t.Errorf("parse %q = %v %v, want %v", raw, got, parsed, want)
		}
	}
	for _, raw := range []string{
		"",
		"github.com/acme/app/pull/12",
		"ftp://github.com/acme/app/pull/12",
		"https://gitlab.com/acme/app/pull/12",
		"https://github.com.evil.example/acme/app/pull/12",
		"https://api.github.com/repos/acme/app/pulls/12",
		"https://user:pw@github.com/acme/app/pull/12",
		"https://github.com:8443/acme/app/pull/12",
		"https://github.com/acme/app/pulls/12",
		"https://github.com/acme/app/issues/12",
		"https://github.com/acme/app/pull/",
		"https://github.com/acme/app/pull/0",
		"https://github.com/acme/app/pull/012",
		"https://github.com/acme/app/pull/+12",
		"https://github.com/acme/app/pull/12abc",
		"https://github.com/acme/app/pull/2147483648",
		"https://github.com/acme/../pull/1",
		"https://github.com/acme/./pull/1",
		"https://github.com/acme/%2e%2e/pull/1",
		"https://github.com/acme%2Fapp/x/pull/1",
		"https://github.com/-acme/app/pull/1",
		"https://github.com/ac_me/app/pull/1",
		"https://github.com//app/pull/1",
		"https://github.com/acme/app",
	} {
		if got, parsed := parsePullRequestURL(raw); parsed {
			t.Errorf("parse %q = %v, want refused", raw, got)
		}
	}
	if u := (pullRef{"acme/app", 12}).url(); u != "https://github.com/acme/app/pull/12" {
		t.Fatalf("url = %q", u)
	}
}

func TestGitHubTracks(t *testing.T) {
	a := &API{cfg: config.Config{GitHubRepos: []string{"acme/app", "labs/*"}}}
	for repo, want := range map[string]bool{
		"acme/app": true, "acme/web": false, "labs/anything": true, "labsx/app": false, "other/app": false,
	} {
		if got := a.githubTracks(repo); got != want {
			t.Errorf("tracks %q = %v, want %v", repo, got, want)
		}
	}
}

func TestRateLimitPause(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	h := http.Header{}
	if _, limited := rateLimitPause(h, now); limited {
		t.Fatal("no headers must not read as a rate limit")
	}
	h.Set("X-RateLimit-Remaining", "0")
	h.Set("X-RateLimit-Reset", "1000300")
	if d, limited := rateLimitPause(h, now); !limited || d != 300*time.Second {
		t.Fatalf("primary limit = %v %v", d, limited)
	}
	h = http.Header{}
	h.Set("Retry-After", "42")
	if d, limited := rateLimitPause(h, now); !limited || d != 42*time.Second {
		t.Fatalf("retry-after = %v %v", d, limited)
	}
	h = http.Header{}
	h.Set("X-RateLimit-Remaining", "17")
	if _, limited := rateLimitPause(h, now); limited {
		t.Fatal("remaining budget must not read as a rate limit")
	}
}

// The client's IssuePullRequest model (web/src/store/issue-pull-requests)
// rejects a missing key: every optional field must be present as null.
func TestPullRequestDataShape(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	d := pullRequestData(pullRow{ID: "p1", IssueID: "i1", Repo: "acme/app", Number: 12, State: "pending",
		CreatedAt: now, UpdatedAt: now})
	want := []string{"id", "createdAt", "updatedAt", "issueId", "repo", "number", "url", "state", "draft",
		"title", "mergedAt", "linkedById", "runId"}
	if len(d) != len(want) {
		t.Fatalf("wire has %d keys, want exactly %d: %v", len(d), len(want), keys(d))
	}
	for _, k := range want {
		if _, ok := d[k]; !ok {
			t.Fatalf("wire lacks %q", k)
		}
	}
	if d["url"] != "https://github.com/acme/app/pull/12" || d["title"] != nil || d["mergedAt"] != nil ||
		d["linkedById"] != nil || d["runId"] != nil || d["draft"] != false {
		t.Fatalf("unset fields must be null: %v", d)
	}
	title, run := "Fix it", "r1"
	d = pullRequestData(pullRow{ID: "p1", IssueID: "i1", Repo: "acme/app", Number: 12, State: "merged",
		Title: &title, MergedAt: &now, RunID: &run, CreatedAt: now, UpdatedAt: now})
	if d["title"] != "Fix it" || d["mergedAt"] != "2026-09-27T10:00:00Z" || d["runId"] != "r1" {
		t.Fatalf("set fields = %v", d)
	}
}

func TestGitHubBackoff(t *testing.T) {
	g := &githubPoller{interval: time.Minute}
	for failures, want := range map[int]time.Duration{
		1: 2 * time.Minute, 2: 4 * time.Minute, 5: 32 * time.Minute, 6: time.Hour, 40: time.Hour,
	} {
		if got := g.backoff(failures); got != want {
			t.Errorf("backoff(%d) = %v, want %v", failures, got, want)
		}
	}
}
