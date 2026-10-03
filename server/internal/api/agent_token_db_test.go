// agent_token_db_test.go — token rotation and revocation against a real
// database: a rotation keeps the grant and never widens it, the old
// token dies on time and never later, and an open realtime stream ends
// once its token dies. Gated on CONVERGE_TEST_DATABASE_URL.
package api

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"converge/internal/auth"
	"converge/internal/config"
)

type rotation struct {
	TokenID        string    `json:"tokenId"`
	Token          string    `json:"token"`
	TokenExpiresAt time.Time `json:"tokenExpiresAt"`
	Name           string    `json:"name"`
	Scopes         []string  `json:"scopes"`
	TeamIDs        []string  `json:"teamIds"`
	OldTokenID     string    `json:"oldTokenId"`
	OldTokenEndsAt time.Time `json:"oldTokenEndsAt"`
}

// tokenRouter mounts the real router behind the real session middleware,
// so bearer tokens are resolved the way production resolves them.
func (f *workFixture) tokenRouter() http.Handler {
	f.a.auth = auth.NewService(f.pool, config.Config{SessionSecret: "test-secret", AccessTokenTTL: time.Hour}, slog.New(slog.DiscardHandler), nil)
	f.a.limiter = newAccountRateLimiter(0, 0)
	r := chi.NewRouter()
	f.a.Mount(r)
	return r
}

func (f *workFixture) createAgent(body string) agentResponse {
	f.t.Helper()
	rec := f.call(humanPrincipal(f.owner), (*API).handleCreateAgent, "POST", "/x", body, "id", f.ws)
	checkStatus(f.t, rec, 201)
	var created agentResponse
	decodeBody(f.t, rec, &created)
	f.accounts = append(f.accounts, created.ID)
	return created
}

func bearer(r http.Handler, token, path string) int {
	req := httptest.NewRequest("GET", "http://x"+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

func near(got, want time.Time) bool {
	d := got.Sub(want)
	return d > -time.Minute && d < time.Minute
}

func TestAgentTokenRotation(t *testing.T) {
	f := newWorkFixture(t)
	r := f.tokenRouter()
	ag := f.createAgent(`{"name":"rotor-` + f.ws[:6] + `","teamIds":["` + f.t1 + `","` + f.t2 + `"],"driver":"external",` +
		`"token":{"scopes":["work","issues:read"],"teamIds":["` + f.t1 + `","` + f.t2 + `"],"ttlHours":720}}`)
	rotate := func(p *Principal, accountID, body string, want int) rotation {
		t.Helper()
		rec := f.call(p, (*API).handleRotateAgentToken, "POST", "/x", body, "id", f.ws, "accountId", accountID)
		checkStatus(t, rec, want)
		var out rotation
		if want == 201 {
			decodeBody(t, rec, &out)
			if strings.Contains(out.Token, " ") || out.Token == "" {
				t.Fatalf("rotation token = %q", out.Token)
			}
		}
		return out
	}
	owner := humanPrincipal(f.owner)
	body := func(tokenID string, grace int) string {
		return `{"tokenId":"` + tokenID + `","graceHours":` + strconv.Itoa(grace) + `}`
	}
	const queue = "/api/v1/agent/queue"

	t.Run("refusals", func(t *testing.T) {
		rotate(externalAgentPrincipal(ag.ID), ag.ID, body(ag.TokenID, 0), 404) // not an admin
		rotate(owner, ag.ID, body(testUUID(), 0), 404)
		rotate(owner, ag.ID, `{"tokenId":"nope"}`, 404)
		rotate(owner, ag.ID, body(ag.TokenID, -1), 422)
		rotate(owner, ag.ID, body(ag.TokenID, 169), 422)
		rotate(owner, f.ext1, body(ag.TokenID, 0), 404) // another agent's token
		if got := bearer(r, ag.Token, queue); got != 200 {
			t.Fatalf("a refused rotation touched the token: queue = %d", got)
		}
	})

	// A grace keeps the old token working for at most that long.
	second := rotate(owner, ag.ID, body(ag.TokenID, 1), 201)
	now := time.Now()
	if second.Name != "default" || second.OldTokenID != ag.TokenID {
		t.Fatalf("rotation = name %q old %q, want default replacing %s", second.Name, second.OldTokenID, ag.TokenID)
	}
	if strings.Join(second.Scopes, ",") != "work,issues:read" || len(second.TeamIDs) != 2 {
		t.Fatalf("grant = %v %v, want the old one", second.Scopes, second.TeamIDs)
	}
	if !near(second.TokenExpiresAt, now.Add(720*time.Hour)) || !near(second.OldTokenEndsAt, now.Add(time.Hour)) {
		t.Fatalf("expiry = %v, old ends %v", second.TokenExpiresAt, second.OldTokenEndsAt)
	}
	if bearer(r, ag.Token, queue) != 200 || bearer(r, second.Token, queue) != 200 {
		t.Fatal("both tokens should work during the grace")
	}
	// A longer grace on the same token never extends it.
	third := rotate(owner, ag.ID, body(ag.TokenID, 24), 201)
	if !near(third.OldTokenEndsAt, second.OldTokenEndsAt) {
		t.Fatalf("second grace moved the end from %v to %v", second.OldTokenEndsAt, third.OldTokenEndsAt)
	}

	// Leaving a team narrows the next rotation; grace 0 kills the old at once.
	f.exec(`delete from team_members where account_id = $1 and team_id = $2`, ag.ID, f.t2)
	fourth := rotate(owner, ag.ID, body(second.TokenID, 0), 201)
	if strings.Join(fourth.TeamIDs, ",") != f.t1 {
		t.Fatalf("teams after leaving %s = %v, want only %s", f.t2, fourth.TeamIDs, f.t1)
	}
	if got := bearer(r, second.Token, queue); got != 401 {
		t.Fatalf("rotated-out token = %d, want 401", got)
	}
	if got := bearer(r, fourth.Token, queue); got != 200 {
		t.Fatalf("replacement = %d, want 200", got)
	}
	rotate(owner, ag.ID, body(second.TokenID, 0), 404) // already revoked

	// With no granted team left, widening to every team is refused.
	f.exec(`delete from team_members where account_id = $1`, ag.ID)
	rotate(owner, ag.ID, body(fourth.TokenID, 0), 422)
	var revoked bool
	if err := f.pool.QueryRow(context.Background(),
		`select revoked_at is not null from api_tokens where id = $1`, fourth.TokenID).Scan(&revoked); err != nil || revoked {
		t.Fatalf("a refused rotation revoked the token (revoked=%v, err=%v)", revoked, err)
	}

	// A new token must name scopes. A token already stored with none
	// keeps that grant when it is rotated.
	rec := f.call(owner, (*API).handleIssueAgentToken, "POST", "/x", `{"name":"wide"}`, "id", f.ws, "accountId", ag.ID)
	checkStatus(t, rec, 422)
	_, hash, err := auth.IssueAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	wideID := testUUID()
	f.exec(`insert into api_tokens
		(id, account_id, name, token_hash, token_prefix, created_at, expires_at, created_by)
		values ($1, $2, 'wide', $3, $4, now(), now() + interval '90 days', $5)`,
		wideID, ag.ID, hash, auth.APITokenPrefix, f.owner)
	full := rotate(owner, ag.ID, body(wideID, 0), 201)
	if full.Name != "wide" || full.Scopes != nil || full.TeamIDs != nil {
		t.Fatalf("full-access rotation = %+v", full)
	}
	if !near(full.TokenExpiresAt, time.Now().Add(auth.APITokenTTL)) {
		t.Fatalf("full-access expiry = %v", full.TokenExpiresAt)
	}

	var audits int
	if err := f.pool.QueryRow(context.Background(),
		`select count(*) from audit_events where workspace_id = $1 and action = 'agent.token.rotated'`, f.ws).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 4 {
		t.Fatalf("rotation audits = %d, want 4", audits)
	}
}

// A realtime stream authenticated by a token ends once the token dies,
// instead of delivering the workspace feed until the client disconnects.
func TestTokenStreamEndsWhenTokenDies(t *testing.T) {
	f := newWorkFixture(t)
	prev := ssePingInterval
	ssePingInterval = 50 * time.Millisecond
	t.Cleanup(func() { ssePingInterval = prev })
	srv := httptest.NewServer(f.tokenRouter())
	t.Cleanup(srv.Close)
	ag := f.createAgent(`{"name":"watcher-` + f.ws[:6] + `","teamIds":["` + f.t1 + `"],"driver":"external"}`)

	req, err := http.NewRequest("GET", srv.URL+"/api/v1/sync_actions/stream?workspaceId="+f.ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ag.Token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stream = %d, want 200", resp.StatusCode)
	}
	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	heads := 0
	deadline := time.After(5 * time.Second)
	for heads < 3 {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("the stream ended while its token was live")
			}
			if l == "event: head" {
				heads++
			}
		case <-deadline:
			t.Fatal("no heartbeats on a live token's stream")
		}
	}

	f.exec(`update api_tokens set revoked_at = now() where id = $1`, ag.TokenID)
	for {
		select {
		case _, ok := <-lines:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the stream outlived its revoked token")
		}
	}
}
