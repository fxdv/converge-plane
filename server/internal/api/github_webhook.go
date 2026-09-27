// github_webhook.go — GitHub's webhook receiver (Phase 2).
// spec cs:agents:prlinks
//
// POST /api/github/webhook is mounted only when
// CONVERGE_GITHUB_WEBHOOK_SECRET is set. A delivery carries no session
// and names no account: its X-Hub-Signature-256, the HMAC-SHA256 of the
// raw body under the shared secret, is its only credential.
//
// A pull_request event does not write the PR's state. It makes the links
// to that PR due, and the poller reads the PR from the API on its next
// tick, so GitHub reaches a link by one path only, deliveries may arrive
// in any order, and a replayed one costs at most a check. Polling stays
// on as the safety net for deliveries that never arrive.
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
)

// githubWebhookMaxBody bounds what an unauthenticated caller can make the
// server read and hash. A pull_request payload is far smaller; a larger
// one is refused and its PR waits for the poll.
const githubWebhookMaxBody = 1 << 20

func (a *API) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "set the webhook's content type to application/json")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, githubWebhookMaxBody+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body) > githubWebhookMaxBody {
		writeError(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}
	if !validGitHubSignature(a.cfg.GitHubWebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		a.log.Warn("github webhook refused: bad signature", "delivery", r.Header.Get("X-GitHub-Delivery"))
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	switch r.Header.Get("X-GitHub-Event") {
	case "ping":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	case "pull_request":
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"due": 0})
		return
	}
	var ev struct {
		Number     int `json:"number"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &ev); err != nil || ev.Number <= 0 {
		writeError(w, http.StatusBadRequest, "invalid pull_request payload")
		return
	}
	repo := strings.ToLower(ev.Repository.FullName)
	if !a.githubTracks(repo) {
		writeJSON(w, http.StatusAccepted, map[string]any{"due": 0})
		return
	}
	// A closed PR may have been reopened, and one given up on may answer
	// again, so links that stopped being checked are re-armed too; only a
	// merge is final.
	tag, err := a.pool.Exec(r.Context(), `
		update issue_pull_requests set next_check_at = now(), failures = 0
		where repo = $1 and number = $2 and unlinked_at is null and state <> 'merged'`, repo, ev.Number)
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"due": tag.RowsAffected()})
}

// validGitHubSignature checks an X-Hub-Signature-256 header,
// "sha256=<hex HMAC-SHA256 of body>", in constant time.
func validGitHubSignature(secret string, body []byte, header string) bool {
	hexSum, ok := strings.CutPrefix(header, "sha256=")
	if secret == "" || !ok {
		return false
	}
	got, err := hex.DecodeString(hexSum)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
