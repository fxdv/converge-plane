// Optimistic concurrency for HTTP issue writes (docs/spec 08, "Mutation
// concurrency").
//
// An agent acts on what it last read. If-Match names the issue version
// that read returned (the Issue payload's "version", or the ETag of a
// previous write), and the write lands only if nothing changed since;
// otherwise 412 with the current version, so the agent re-reads and
// decides again instead of overwriting a human's edit.
//
// Agents must send it (428 without). The forked web client does not send
// versions yet, so for humans the check applies only when they do.
package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"converge/internal/auth"
	"converge/internal/metrics"
)

// preconditionFailures counts refused issue writes. stale is the
// mechanism working (an agent lost a race to a human); missing is a
// client that does not send versions.
var preconditionFailures = metrics.Default.NewCounterVec("converge_issue_precondition_failures_total",
	"Issue writes refused by If-Match: missing (428) or stale (412).", "reason")

// ifMatchVersion parses If-Match as an issue version: a bare or quoted
// integer, weak or strong. present is false when the header is absent.
func ifMatchVersion(r *http.Request) (version int, present, valid bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return 0, false, false
	}
	raw = strings.Trim(strings.TrimPrefix(raw, "W/"), `"`)
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return 0, true, false
	}
	return v, true, true
}

func issueETag(version int) string {
	return `"` + strconv.Itoa(version) + `"`
}

// issuePreconditionTx row-locks the issue and checks If-Match against its
// version, writing the 400/412/428 itself when the write must not
// proceed. current is the locked row's version.
func (a *API) issuePreconditionTx(ctx context.Context, tx pgx.Tx, w http.ResponseWriter, r *http.Request, p *Principal, issueID string) (current int, ok bool) {
	want, present, valid := ifMatchVersion(r)
	switch {
	case !present && p.Kind == auth.AccountKindAgent:
		preconditionFailures.With("missing").Inc()
		writeError(w, http.StatusPreconditionRequired, "If-Match with the issue version is required for API-token writes")
		return 0, false
	case present && !valid:
		writeError(w, http.StatusBadRequest, "If-Match must be an issue version")
		return 0, false
	}
	err := tx.QueryRow(ctx, `select version from issues where id = $1 for update`, issueID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return 0, false
	}
	if err != nil {
		a.internalError(w, err)
		return 0, false
	}
	if present && want != current {
		preconditionFailures.With("stale").Inc()
		w.Header().Set("ETag", issueETag(current))
		writeJSON(w, http.StatusPreconditionFailed, map[string]any{
			"error":   "the issue changed since it was read; re-read and retry",
			"version": current,
		})
		return 0, false
	}
	return current, true
}
