// pullrequests.go — GitHub pull request links (Phase 2).
// spec cs:agents:prlinks
//
// An external agent's pull_request evidence for a repository on the
// operator's allowlist (CONVERGE_GITHUB_REPOS) links that PR to the
// issue: a row in issue_pull_requests which the poller (github_poll.go)
// keeps in step with GitHub. The evidence is what the agent says; the
// row is what GitHub says, and it is what the card shows.
//
// Only github.com pull request URLs link. Owner, name and number are
// parsed and validated; the poller builds its own API path from them and
// never requests the reported URL.
//
// Rows reach the client as IssuePullRequest sync records.
package api

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"converge/internal/config"
)

const (
	modelPullRequest     = "IssuePullRequest"
	pullRequestsPerIssue = 20
	pullRequestTitleMax  = 200
)

// pullRef names one pull request: a lower-cased "owner/name" and a number.
type pullRef struct {
	Repo   string
	Number int
}

func (p pullRef) url() string {
	return "https://github.com/" + p.Repo + "/pull/" + strconv.Itoa(p.Number)
}

func (p pullRef) String() string {
	return p.Repo + "#" + strconv.Itoa(p.Number)
}

// parsePullRequestURL accepts https://github.com/{owner}/{name}/pull/{n},
// optionally followed by a sub-page (/files, /commits), a query or a
// fragment. Anything escaped is refused: real owner and repository
// names never need it, and decoding could smuggle a separator.
func parsePullRequestURL(raw string) (pullRef, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" {
		return pullRef{}, false
	}
	if host := strings.ToLower(u.Hostname()); host != "github.com" && host != "www.github.com" {
		return pullRef{}, false
	}
	path := u.EscapedPath()
	if strings.Contains(path, "%") {
		return pullRef{}, false
	}
	seg := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(seg) < 4 || seg[2] != "pull" {
		return pullRef{}, false
	}
	owner, name := strings.ToLower(seg[0]), strings.ToLower(seg[1])
	if !config.ValidGitHubOwner(owner) || !config.ValidGitHubName(name) {
		return pullRef{}, false
	}
	n, ok := parsePullNumber(seg[3])
	if !ok {
		return pullRef{}, false
	}
	return pullRef{Repo: owner + "/" + name, Number: n}, true
}

// parsePullNumber reads a PR number: 1 to 2^31-1, digits only, no
// leading zero.
func parsePullNumber(s string) (int, bool) {
	if s == "" || len(s) > 10 || s[0] == '0' {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, false
	}
	return int(n), true
}

// githubTracks reports whether the allowlist covers the repository.
func (a *API) githubTracks(repo string) bool {
	owner, _, _ := strings.Cut(repo, "/")
	for _, entry := range a.cfg.GitHubRepos {
		if entry == repo || entry == owner+"/*" {
			return true
		}
	}
	return false
}

// pullRow is one issue_pull_requests row.
type pullRow struct {
	ID, WorkspaceID, IssueID, Repo string
	Number                         int
	LinkedBy, RunID                *string
	State                          string
	Draft                          bool
	Title                          *string
	MergedAt, CheckedAt            *time.Time
	CreatedAt, UpdatedAt           time.Time
}

const pullColumns = `p.id, p.workspace_id, p.issue_id, p.repo, p.number, p.linked_by, p.run_id,
	p.state, p.draft, p.title, p.merged_at, p.checked_at, p.created_at, p.updated_at`

func (p *pullRow) scanDest() []any {
	return []any{&p.ID, &p.WorkspaceID, &p.IssueID, &p.Repo, &p.Number, &p.LinkedBy, &p.RunID,
		&p.State, &p.Draft, &p.Title, &p.MergedAt, &p.CheckedAt, &p.CreatedAt, &p.UpdatedAt}
}

func isoOrNull(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(iso)
}

// pullRequestData serializes a link in the exact shape of the client's
// IssuePullRequest model. Every optional field is present (null when
// unset). title is GitHub's, not the agent's. The last check time is
// not on the wire: an unchanged check emits nothing.
func pullRequestData(p pullRow) map[string]any {
	ref := pullRef{Repo: p.Repo, Number: p.Number}
	return map[string]any{
		"id":         p.ID,
		"createdAt":  p.CreatedAt.UTC().Format(iso),
		"updatedAt":  p.UpdatedAt.UTC().Format(iso),
		"issueId":    p.IssueID,
		"repo":       p.Repo,
		"number":     p.Number,
		"url":        ref.url(),
		"state":      p.State,
		"draft":      p.Draft,
		"title":      nullOrEmpty(strval(p.Title)),
		"mergedAt":   isoOrNull(p.MergedAt),
		"linkedById": nullOrEmpty(strval(p.LinkedBy)),
		"runId":      nullOrEmpty(strval(p.RunID)),
	}
}

func (a *API) pullByIDTx(ctx context.Context, tx pgx.Tx, id string) (pullRow, error) {
	var p pullRow
	err := tx.QueryRow(ctx, "select "+pullColumns+" from issue_pull_requests p where p.id = $1", id).Scan(p.scanDest()...)
	return p, err
}

// emitPullRequestTx emits the link's current state to the sync feed.
func (a *API) emitPullRequestTx(ctx context.Context, tx pgx.Tx, id, action string) (syncActionRecord, pullRow, error) {
	p, err := a.pullByIDTx(ctx, tx, id)
	if err != nil {
		return syncActionRecord{}, p, err
	}
	rec, err := a.emitChange(ctx, tx, p.WorkspaceID, modelPullRequest, p.ID, action, pullRequestData(p))
	return rec, p, err
}

// linkPullRequestsTx links the tracked pull requests among a report's
// evidence to the run's issue. A new link starts pending, due at once.
// Reporting a known link again re-arms a poller that had stopped
// looking (a closed PR may have been reopened), except after a merge,
// and leaves a scheduled one alone: agents repeat their evidence on
// every heartbeat. An issue keeps at most 20 links; past that, reported
// PRs stay plain evidence.
func (a *API) linkPullRequestsTx(ctx context.Context, tx pgx.Tx, run runRow, evidence []runEvidence) ([]syncActionRecord, error) {
	if len(a.cfg.GitHubRepos) == 0 {
		return nil, nil
	}
	var recs []syncActionRecord
	seen := map[pullRef]bool{}
	for _, ev := range evidence {
		if ev.Kind != "pull_request" {
			continue
		}
		ref, ok := parsePullRequestURL(ev.URL)
		if !ok || seen[ref] || !a.githubTracks(ref.Repo) {
			continue
		}
		seen[ref] = true
		var id string
		err := tx.QueryRow(ctx, `
			insert into issue_pull_requests (workspace_id, issue_id, repo, number, linked_by, run_id)
			select $1, $2, $3, $4, $5, $6
			where (select count(*) from issue_pull_requests where issue_id = $2) < $7
			on conflict (issue_id, repo, number) do nothing
			returning id`,
			run.WorkspaceID, run.IssueID, ref.Repo, ref.Number, run.AgentID, run.ID, pullRequestsPerIssue).Scan(&id)
		switch {
		case err == nil:
			rec, _, err := a.emitPullRequestTx(ctx, tx, id, "CREATE")
			if err != nil {
				return nil, err
			}
			recs = append(recs, rec)
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := tx.Exec(ctx, `
				update issue_pull_requests set next_check_at = now(), failures = 0, updated_at = now()
				where issue_id = $1 and repo = $2 and number = $3
				  and next_check_at is null and state <> 'merged'`,
				run.IssueID, ref.Repo, ref.Number); err != nil {
				return nil, err
			}
		default:
			return nil, err
		}
	}
	return recs, nil
}

// collectPullRequests maps the workspace's links to the client's
// IssuePullRequest shape.
func (a *API) collectPullRequests(ctx context.Context, workspaceID string, emit emitFn) ([]syncActionRecord, error) {
	rows, err := a.pool.Query(ctx, "select "+pullColumns+`
		from issue_pull_requests p where p.workspace_id = $1
		order by p.created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []syncActionRecord
	for rows.Next() {
		var p pullRow
		if err := rows.Scan(p.scanDest()...); err != nil {
			return nil, err
		}
		rec, err := emit(p.ID, pullRequestData(p))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
