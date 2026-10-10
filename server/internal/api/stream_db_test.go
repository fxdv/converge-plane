// stream_db_test.go — an open realtime stream held by a web session
// ends once the session is revoked or the member is suspended, instead
// of delivering the workspace feed until the client disconnects. Gated
// on CONVERGE_TEST_DATABASE_URL.
package api

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// sessionAccessToken inserts a sessions row for the account and mints
// its access token in the session wire format (auth/session.go), signed
// with the secret tokenRouter configures.
func (f *workFixture) sessionAccessToken(accountID string) (sessionID, token string) {
	f.t.Helper()
	sessionID = testUUID()
	f.exec(`insert into sessions (id, account_id, token_hash, expires_at) values ($1, $2, $3, now() + interval '1 day')`,
		sessionID, accountID, "stream-test-"+sessionID)
	ate := time.Now().Add(time.Hour).UnixMilli()
	mac := hmac.New(sha256.New, []byte("test-secret"))
	fmt.Fprintf(mac, "access|%s|%s||%d", accountID, sessionID, ate)
	body, _ := json.Marshal(map[string]any{
		"r": 1, "ate": ate, "up": accountID, "sid": sessionID, "hs": hex.EncodeToString(mac.Sum(nil)),
	})
	return sessionID, base64.StdEncoding.EncodeToString(body)
}

// openSessionStream opens the workspace stream as the fixture owner's
// web session, waits for live heartbeats, then applies cut and expects
// the stream to end.
func openSessionStream(t *testing.T, cut func(f *workFixture, sessionID string)) {
	f := newWorkFixture(t)
	prev := ssePingInterval
	ssePingInterval = 50 * time.Millisecond
	t.Cleanup(func() { ssePingInterval = prev })
	srv := httptest.NewServer(f.tokenRouter())
	t.Cleanup(srv.Close)
	sid, token := f.sessionAccessToken(f.owner)

	req, err := http.NewRequest("GET", srv.URL+"/api/v1/sync_actions/stream?workspaceId="+f.ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
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
				t.Fatal("the stream ended while its session was live")
			}
			if l == "event: head" {
				heads++
			}
		case <-deadline:
			t.Fatal("no heartbeats on a live session's stream")
		}
	}

	cut(f, sid)
	for {
		select {
		case _, ok := <-lines:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the stream outlived its session or membership")
		}
	}
}

func TestSessionStreamEndsWhenMemberSuspended(t *testing.T) {
	openSessionStream(t, func(f *workFixture, _ string) {
		f.exec(`update workspace_members set status = 'suspended' where workspace_id = $1 and account_id = $2`, f.ws, f.owner)
	})
}

func TestSessionStreamEndsWhenSessionRevoked(t *testing.T) {
	openSessionStream(t, func(f *workFixture, sessionID string) {
		f.exec(`update sessions set revoked_at = now() where id = $1`, sessionID)
	})
}
