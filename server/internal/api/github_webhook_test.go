// github_webhook_test.go — the webhook receiver: only a delivery signed
// with the shared secret counts, a pull_request event makes that PR's
// live links due without writing their state, and a check leased before
// the event does not record over the one the event asked for. The
// database cases are gated on CONVERGE_TEST_DATABASE_URL.
package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func githubSignature(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestValidGitHubSignature(t *testing.T) {
	secret := strings.Repeat("k", 32)
	body := []byte(`{"zen":"Keep it logically awesome."}`)
	good := githubSignature(secret, string(body))
	if !validGitHubSignature(secret, body, good) {
		t.Fatal("a correct signature was refused")
	}
	for name, header := range map[string]string{
		"missing":        "",
		"sha1":           "sha1=" + strings.TrimPrefix(good, "sha256="),
		"not hex":        "sha256=zz",
		"truncated":      good[:len(good)-2],
		"other secret":   githubSignature(secret+"x", string(body)),
		"other body":     githubSignature(secret, string(body)+" "),
		"upper-case tag": "SHA256=" + strings.TrimPrefix(good, "sha256="),
	} {
		if validGitHubSignature(secret, body, header) {
			t.Errorf("%s: accepted %q", name, header)
		}
	}
	if validGitHubSignature("", body, githubSignature("", string(body))) {
		t.Fatal("an empty secret must accept nothing")
	}
}

type webhookFixture struct {
	*prFixture
	secret string
	router chi.Router
}

func newWebhookFixture(t *testing.T) *webhookFixture {
	f := newPRFixture(t)
	secret := strings.Repeat("w", 32)
	f.a.cfg.GitHubWebhookSecret = secret
	r := chi.NewRouter()
	f.a.Mount(r)
	return &webhookFixture{prFixture: f, secret: secret, router: r}
}

func (f *webhookFixture) deliverAs(contentType, event, body, signature string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://x/api/github/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-GitHub-Event", event)
	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *webhookFixture) deliver(event, body string) *httptest.ResponseRecorder {
	return f.deliverAs("application/json", event, body, githubSignature(f.secret, body))
}

func pullRequestEvent(repo string, number int) string {
	return fmt.Sprintf(`{"action":"closed","number":%d,"pull_request":{"number":%d},"repository":{"full_name":%q}}`,
		number, number, repo)
}

// dueNow reports whether the poller would take the link on its next pass.
func (f *prFixture) dueNow(issueID string, number int) bool {
	f.t.Helper()
	var due bool
	if err := f.pool.QueryRow(context.Background(), `
		select coalesce(next_check_at <= now(), false)
		from issue_pull_requests where issue_id = $1 and number = $2`, issueID, number).Scan(&due); err != nil {
		f.t.Fatalf("read link %d: %v", number, err)
	}
	return due
}

func wantDue(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	checkStatus(t, rec, http.StatusAccepted)
	var v struct{ Due int }
	decodeBody(t, rec, &v)
	if v.Due != want {
		t.Fatalf("due = %d, want %d", v.Due, want)
	}
}

func TestPullRequestWebhook(t *testing.T) {
	f := newWebhookFixture(t)

	t.Run("the receiver is mounted only with a secret", func(t *testing.T) {
		a := &API{pool: &fakePool{}, log: discardLogger(), limiter: newAccountRateLimiter(0, 0)}
		a.cfg.GitHubRepos = []string{"acme/app"}
		r := chi.NewRouter()
		a.Mount(r)
		body := pullRequestEvent("acme/app", 1)
		req := httptest.NewRequest(http.MethodPost, "http://x/api/github/webhook", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hub-Signature-256", githubSignature("", body))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		checkStatus(t, rec, http.StatusNotFound)
	})

	t.Run("only a delivery signed with the secret counts", func(t *testing.T) {
		body := pullRequestEvent("acme/app", 90)
		wantError(t, f.deliverAs("application/json", "pull_request", body, ""), http.StatusUnauthorized, "invalid signature")
		wantError(t, f.deliverAs("application/json", "pull_request", body, githubSignature(f.secret+"x", body)),
			http.StatusUnauthorized, "invalid signature")
		wantError(t, f.deliverAs("application/json", "pull_request", body+" ", githubSignature(f.secret, body)),
			http.StatusUnauthorized, "invalid signature")
		wantError(t, f.deliverAs("application/x-www-form-urlencoded", "pull_request", "payload="+body, githubSignature(f.secret, "payload="+body)),
			http.StatusUnsupportedMediaType, "set the webhook's content type to application/json")
		big := `{"pad":"` + strings.Repeat("x", githubWebhookMaxBody) + `"}`
		wantError(t, f.deliver("pull_request", big), http.StatusRequestEntityTooLarge, "payload too large")

		checkStatus(t, f.deliver("ping", `{"zen":"Design for failure."}`), http.StatusOK)
		wantDue(t, f.deliver("push", `{"ref":"refs/heads/main"}`), 0)
		wantError(t, f.deliver("pull_request", `{"number":"seven"}`), http.StatusBadRequest, "invalid pull_request payload")
	})

	t.Run("a pull_request event makes the PR's live links due", func(t *testing.T) {
		first, second, unlinked, merged := f.issue(f.t1, f.doing, f.ext1), f.issue(f.t1, f.doing, f.ext1),
			f.issue(f.t1, f.doing, f.ext1), f.issue(f.t1, f.doing, f.ext1)
		for _, issue := range []string{first, second, unlinked} {
			checkStatus(t, f.linkByHand(issue, "https://github.com/acme/app/pull/91"), http.StatusCreated)
		}
		checkStatus(t, f.linkByHand(merged, "https://github.com/acme/app/pull/92"), http.StatusCreated)
		checkStatus(t, f.unlink(unlinked, f.linkID(unlinked, "acme/app", 91)), http.StatusOK)
		// first is scheduled for later; second was closed and is no
		// longer checked; merged is final.
		f.exec(`update issue_pull_requests set next_check_at = now() + interval '1 hour' where issue_id = $1`, first)
		f.exec(`update issue_pull_requests set state = 'closed', next_check_at = null, failures = 3 where issue_id = $1`, second)
		f.exec(`update issue_pull_requests set state = 'merged', next_check_at = null where issue_id = $1`, merged)

		wantDue(t, f.deliver("pull_request", pullRequestEvent("Acme/App", 91)), 2)
		if !f.dueNow(first, 91) || !f.dueNow(second, 91) || f.link(second, 91).failures != 0 {
			t.Fatal("the event must make every live link to the PR due, closed ones included")
		}
		if f.link(first, 91).state != "pending" || f.link(second, 91).state != "closed" {
			t.Fatal("the event must leave the state to the poller")
		}
		if f.dueNow(unlinked, 91) {
			t.Fatal("the event revived an unlinked PR")
		}
		wantDue(t, f.deliver("pull_request", pullRequestEvent("acme/app", 92)), 0)
		if f.link(merged, 92).due {
			t.Fatal("the event re-armed a merged PR")
		}
		wantDue(t, f.deliver("pull_request", pullRequestEvent("elsewhere/app", 91)), 0)
	})

	t.Run("a check leased before the event does not record its answer", func(t *testing.T) {
		issue := f.issue(f.t1, f.doing, f.ext1)
		checkStatus(t, f.linkByHand(issue, "https://github.com/acme/app/pull/93"), http.StatusCreated)
		f.exec(`update issue_pull_requests set next_check_at = now() - interval '100 years' where issue_id = $1`, issue)
		g := f.poller()
		ref := pullRef{Repo: "acme/app", Number: 93}
		_, leased, err := g.lease(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var l leasedPull
		for _, c := range leased[ref] {
			if c.issueID == issue {
				l = c
			}
		}
		if l.id == "" {
			t.Fatal("the pass did not lease the link")
		}
		if f.dueNow(issue, 93) {
			t.Fatal("a leased link must not be due")
		}

		wantDue(t, f.deliver("pull_request", pullRequestEvent("acme/app", 93)), 1)
		for _, res := range []githubResult{
			{outcome: githubFetched, pull: githubPull{State: "open", Title: "Before the event"}},
			{outcome: githubNotModified},
			{outcome: githubFailed},
		} {
			if err := g.apply(context.Background(), ref, l, res); err != nil {
				t.Fatal(err)
			}
		}
		if s := f.link(issue, 93); s.state != "pending" || s.title != "" || s.failures != 0 || !f.dueNow(issue, 93) {
			t.Fatalf("a superseded check recorded its answer: %+v", s)
		}

		f.gh.queue("/repos/acme/app/pulls/93", pullJSON("closed", true, "After the event"))
		f.poll(g, issue)
		if s := f.link(issue, 93); s.state != "merged" || s.title != "After the event" {
			t.Fatalf("the check the event asked for = %+v", s)
		}
	})
}
