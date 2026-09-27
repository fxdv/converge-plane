// github_poll.go — the pull request poller (Phase 2).
// spec cs:agents:prlinks
//
// Linked pull requests (pullrequests.go) are checked against the GitHub
// REST API while GitHub can still change them: pending and open ones
// every CONVERGE_GITHUB_POLL_INTERVAL, with the last ETag, so an
// unchanged PR costs a 304. A merged or closed PR is no longer checked;
// reporting a closed one again re-arms it.
//
// Rows are leased before the request (next_check_at moves past the
// batch's lifetime under skip locked), so any number of API processes
// poll without duplicating work or holding a transaction across the
// network call.
//
// A renamed or moved repository is followed through GitHub's redirect on
// the API host, and the link takes the name GitHub now files the PR
// under, if the allowlist covers it; one moved elsewhere is not followed.
//
// A failing row backs off (doubling from the interval, at most an hour)
// and is given up after githubMaxFailures in a row. A rate limit or a
// rejected token pauses the whole poller instead: every row would fail
// the same way.
//
// When a PR merges and no PR on the issue is still pending or open, the
// issue moves to its team's first Done state through the ordinary patch
// path, as the system (CONVERGE_GITHUB_AUTO_DONE=false turns this off).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	githubAPIBase        = "https://api.github.com"
	githubPollTick       = 5 * time.Second
	githubPollBatch      = 10
	githubRequestTimeout = 10 * time.Second
	githubLease          = 2 * time.Minute
	githubMaxBackoff     = time.Hour
	githubMaxFailures    = 48
	githubMaxBody        = 2 << 20
	githubMaxPause       = time.Hour
	githubTokenPause     = 15 * time.Minute
)

// systemPrincipal acts for the server itself: its history rows carry no
// account (actor_type system) and its notifications name Converge.
func systemPrincipal() *Principal {
	return &Principal{Fullname: "Converge", Kind: "system"}
}

type githubPoller struct {
	a        *API
	base     string
	token    string
	interval time.Duration
	client   *http.Client

	mu          sync.Mutex
	pausedUntil time.Time
}

func newGitHubPoller(a *API) *githubPoller {
	g := &githubPoller{a: a, base: githubAPIBase, token: a.cfg.GitHubToken, interval: a.cfg.GitHubPollInterval}
	if g.interval <= 0 {
		g.interval = time.Minute
	}
	g.client = &http.Client{
		Timeout: githubRequestTimeout,
		// GitHub redirects a renamed repository within the API host.
		// Anything else is not followed: the token goes to GitHub only.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			base, err := url.Parse(g.base)
			if err != nil || len(via) >= 3 || req.URL.Scheme != base.Scheme || req.URL.Host != base.Host {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	return g
}

func (g *githubPoller) run(ctx context.Context, done <-chan struct{}) {
	t := time.NewTicker(githubPollTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-t.C:
			if _, err := g.pollOnce(ctx); err != nil && ctx.Err() == nil {
				g.a.log.Warn("pull request poll failed", "error", err)
			}
		}
	}
}

func (g *githubPoller) paused() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Now().Before(g.pausedUntil)
}

func (g *githubPoller) pause(d time.Duration) {
	d = min(max(d, time.Minute), githubMaxPause)
	g.mu.Lock()
	defer g.mu.Unlock()
	if until := time.Now().Add(d); until.After(g.pausedUntil) {
		g.pausedUntil = until
	}
}

type leasedPull struct {
	id, issueID, etag, state string
	failures                 int
}

// pollOnce checks one batch of due links and returns how many it
// checked. Only database errors are returned; GitHub's answers, good or
// bad, are recorded on the rows.
func (g *githubPoller) pollOnce(ctx context.Context) (int, error) {
	if g.paused() {
		return 0, nil
	}
	rows, err := g.a.pool.Query(ctx, `
		with due as (
			select id from issue_pull_requests
			where next_check_at <= now()
			order by next_check_at
			limit $1
			for update skip locked
		)
		update issue_pull_requests p set next_check_at = now() + $2::int * interval '1 second'
		from due where p.id = due.id
		returning p.id, p.issue_id, p.repo, p.number, coalesce(p.etag, ''), p.state, p.failures`,
		githubPollBatch, int(githubLease.Seconds()))
	if err != nil {
		return 0, err
	}
	var order []pullRef
	leased := map[pullRef][]leasedPull{}
	for rows.Next() {
		var (
			l   leasedPull
			ref pullRef
		)
		if err := rows.Scan(&l.id, &l.issueID, &ref.Repo, &ref.Number, &l.etag, &l.state, &l.failures); err != nil {
			rows.Close()
			return 0, err
		}
		if leased[ref] == nil {
			order = append(order, ref)
		}
		leased[ref] = append(leased[ref], l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	checked := 0
	for _, ref := range order {
		if ctx.Err() != nil || g.paused() {
			break
		}
		group := leased[ref]
		// One request serves every issue linking the PR; the ETag is
		// only sent when they all saw the same answer.
		etag := group[0].etag
		for _, l := range group[1:] {
			if l.etag != etag {
				etag = ""
			}
		}
		res := g.fetch(ctx, ref, etag)
		for _, l := range group {
			if err := g.apply(ctx, ref, l, res); err != nil {
				return checked, err
			}
			checked++
		}
	}
	return checked, nil
}

type githubPull struct {
	State    string     `json:"state"`
	Draft    bool       `json:"draft"`
	Title    string     `json:"title"`
	Merged   bool       `json:"merged"`
	MergedAt *time.Time `json:"merged_at"`
	HTMLURL  string     `json:"html_url"`
}

type githubOutcome int

const (
	githubFetched githubOutcome = iota
	githubNotModified
	githubGone
	githubFailed
	githubPaused
)

type githubResult struct {
	outcome githubOutcome
	pull    githubPull
	etag    string
	// movedTo is the repository GitHub now files the PR under, when it
	// is not the one requested: the repository was renamed or moved.
	movedTo string
}

func (g *githubPoller) fetch(ctx context.Context, ref pullRef, etag string) githubResult {
	owner, name, _ := strings.Cut(ref.Repo, "/")
	target := g.base + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/pulls/" + strconv.Itoa(ref.Number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return githubResult{outcome: githubFailed}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	version := g.a.cfg.Version
	if version == "" {
		version = "dev"
	}
	req.Header.Set("User-Agent", "converge/"+version)
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	res, err := g.client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			g.a.log.Warn("GitHub request failed", "pull_request", ref.String(), "error", err)
		}
		return githubResult{outcome: githubFailed}
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		var pull githubPull
		dec := json.NewDecoder(io.LimitReader(res.Body, githubMaxBody))
		if err := dec.Decode(&pull); err != nil || (pull.State != "open" && pull.State != "closed") {
			g.a.log.Warn("GitHub answered with an unreadable pull request", "pull_request", ref.String())
			return githubResult{outcome: githubFailed}
		}
		tag := res.Header.Get("ETag")
		if len(tag) > 200 {
			tag = ""
		}
		out := githubResult{outcome: githubFetched, pull: pull, etag: tag}
		// A renamed repository answers through a redirect to its numeric
		// id, so the new name is only in the PR's own html_url.
		if at, ok := parsePullRequestURL(pull.HTMLURL); ok && at.Number == ref.Number && at.Repo != ref.Repo {
			out.movedTo = at.Repo
		}
		return out
	case http.StatusNotModified:
		return githubResult{outcome: githubNotModified}
	case http.StatusNotFound, http.StatusGone, http.StatusUnavailableForLegalReasons:
		return githubResult{outcome: githubGone}
	case http.StatusUnauthorized:
		g.a.log.Warn("GitHub rejected CONVERGE_GITHUB_TOKEN; pull request polling paused", "pause", githubTokenPause)
		g.pause(githubTokenPause)
		return githubResult{outcome: githubPaused}
	case http.StatusForbidden, http.StatusTooManyRequests:
		if d, limited := rateLimitPause(res.Header, time.Now()); limited {
			g.a.log.Warn("GitHub rate limit reached; pull request polling paused", "pause", d)
			g.pause(d)
			return githubResult{outcome: githubPaused}
		}
		// A secondary limit may come without headers; GitHub then asks
		// for at least a minute. Its message is the only tell on a 403.
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		if res.StatusCode == http.StatusTooManyRequests ||
			strings.Contains(strings.ToLower(string(body)), "rate limit") {
			g.a.log.Warn("GitHub rate limit reached; pull request polling paused", "pause", time.Minute)
			g.pause(time.Minute)
			return githubResult{outcome: githubPaused}
		}
		// A 403 without rate-limit signals: the token cannot see it.
		return githubResult{outcome: githubGone}
	default:
		g.a.log.Warn("GitHub answered with an unexpected status", "pull_request", ref.String(), "status", res.StatusCode)
		return githubResult{outcome: githubFailed}
	}
}

// rateLimitPause reads GitHub's rate-limit answer: Retry-After (the
// secondary limits), or an exhausted primary limit with its reset time.
func rateLimitPause(h http.Header, now time.Time) (time.Duration, bool) {
	if v := h.Get("Retry-After"); v != "" {
		if s, err := strconv.Atoi(v); err == nil && s >= 0 {
			return time.Duration(s) * time.Second, true
		}
	}
	if h.Get("X-RateLimit-Remaining") == "0" {
		if s, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return time.Unix(s, 0).Sub(now), true
		}
		return time.Minute, true
	}
	return 0, false
}

// backoff is the wait after the failures-th failure in a row.
func (g *githubPoller) backoff(failures int) time.Duration {
	d := g.interval << min(failures, 12)
	return min(d, githubMaxBackoff)
}

// apply records one answer on one leased link.
func (g *githubPoller) apply(ctx context.Context, ref pullRef, l leasedPull, res githubResult) error {
	a := g.a
	switch res.outcome {
	case githubPaused:
		// The lease stands; the row comes due again after the pause.
		return nil
	case githubNotModified:
		_, err := a.pool.Exec(ctx, `
			update issue_pull_requests
			set checked_at = now(), failures = 0, next_check_at = now() + $2::int * interval '1 second'
			where id = $1`, l.id, int(g.interval.Seconds()))
		return err
	case githubFailed:
		_, err := a.pool.Exec(ctx, `
			update issue_pull_requests
			set failures = failures + 1,
			    next_check_at = case when failures + 1 >= $2::int then null
			                         else now() + $3::int * interval '1 second' end
			where id = $1`, l.id, githubMaxFailures, int(g.backoff(l.failures+1).Seconds()))
		return err
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Lock order: the issue's team, then the link (the claim paths take
	// the team first too).
	var teamID string
	if err := tx.QueryRow(ctx, `select team_id from issues where id = $1`, l.issueID).Scan(&teamID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_issue_"+teamID); err != nil {
		return err
	}
	var (
		before   pullRow
		failures int
	)
	err = tx.QueryRow(ctx, "select "+pullColumns+`, p.failures
		from issue_pull_requests p where p.id = $1 for update`, l.id).Scan(append(before.scanDest(), &failures)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	var recs []syncActionRecord
	if res.outcome == githubGone {
		failures++
		if _, err := tx.Exec(ctx, `
			update issue_pull_requests
			set state = 'unavailable', etag = null, checked_at = now(), failures = $2::int,
			    next_check_at = case when $2::int >= $3::int then null else now() + $4::int * interval '1 second' end,
			    updated_at = case when state = 'unavailable' then updated_at else now() end
			where id = $1`, l.id, failures, githubMaxFailures, int(g.backoff(failures).Seconds())); err != nil {
			return err
		}
		if before.State != "unavailable" {
			rec, _, err := a.emitPullRequestTx(ctx, tx, l.id, "UPDATE")
			if err != nil {
				return err
			}
			recs = append(recs, rec)
		}
	} else {
		kept := true
		if res.movedTo != "" {
			var moveRecs []syncActionRecord
			kept, moveRecs, err = g.moveTx(ctx, tx, before, ref, res.movedTo)
			if err != nil {
				return err
			}
			recs = append(recs, moveRecs...)
		}
		if kept {
			fetchRecs, err := g.recordFetchTx(ctx, tx, before, res)
			if err != nil {
				return err
			}
			recs = append(recs, fetchRecs...)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for i := range recs {
		a.broadcastRecord(recs[i])
	}
	return nil
}

// moveTx follows a PR whose repository was renamed or moved. A link to a
// repository the allowlist still covers takes the new name, unless the
// issue already links the PR under it: then this row goes and the other
// is checked now. A repository moved off the allowlist is not followed:
// the link turns unavailable and is no longer checked. kept reports
// whether the row still stands, now under the new name.
func (g *githubPoller) moveTx(ctx context.Context, tx pgx.Tx, before pullRow, ref pullRef, to string) (kept bool, recs []syncActionRecord, err error) {
	a := g.a
	if !a.githubTracks(to) {
		a.log.Info("a linked pull request moved to a repository CONVERGE_GITHUB_REPOS does not cover; it is no longer checked",
			"pull_request", ref.String(), "moved_to", to)
		if _, err := tx.Exec(ctx, `
			update issue_pull_requests
			set state = 'unavailable', etag = null, checked_at = now(), failures = 0, next_check_at = null,
			    updated_at = case when state = 'unavailable' then updated_at else now() end
			where id = $1`, before.ID); err != nil {
			return false, nil, err
		}
		if before.State != "unavailable" {
			rec, _, err := a.emitPullRequestTx(ctx, tx, before.ID, "UPDATE")
			if err != nil {
				return false, nil, err
			}
			recs = append(recs, rec)
		}
		return false, recs, nil
	}
	var other string
	err = tx.QueryRow(ctx, `
		select id from issue_pull_requests
		where issue_id = $1 and repo = $2 and number = $3 and id <> $4
		for update`, before.IssueID, to, ref.Number, before.ID).Scan(&other)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		_, err = tx.Exec(ctx, `update issue_pull_requests set repo = $2 where id = $1`, before.ID, to)
		return err == nil, nil, err
	case err != nil:
		return false, nil, err
	}
	if _, err := tx.Exec(ctx, `delete from issue_pull_requests where id = $1`, before.ID); err != nil {
		return false, nil, err
	}
	rec, err := a.emitChange(ctx, tx, before.WorkspaceID, modelPullRequest, before.ID, "DELETE", map[string]any{"id": before.ID})
	if err != nil {
		return false, nil, err
	}
	if _, err := tx.Exec(ctx, `
		update issue_pull_requests set next_check_at = now()
		where id = $1 and state <> 'merged'`, other); err != nil {
		return false, nil, err
	}
	return false, []syncActionRecord{rec}, nil
}

// recordFetchTx records GitHub's answer on a link: its state, and the
// issue's move to Done when this answer is the merge.
func (g *githubPoller) recordFetchTx(ctx context.Context, tx pgx.Tx, before pullRow, res githubResult) ([]syncActionRecord, error) {
	a := g.a
	after, err := a.pullByIDTx(ctx, tx, before.ID)
	if err != nil {
		return nil, err
	}
	pull := res.pull
	state := "open"
	switch {
	case pull.Merged || pull.MergedAt != nil:
		state = "merged"
	case pull.State == "closed":
		state = "closed"
	}
	title := cleanRunText(pull.Title, pullRequestTitleMax, false)
	mergedAt := pull.MergedAt
	if state == "merged" && mergedAt == nil {
		now := time.Now()
		mergedAt = &now
	}
	changed := state != before.State || pull.Draft != before.Draft || title != strval(before.Title) ||
		(mergedAt == nil) != (before.MergedAt == nil) || after.Repo != before.Repo
	settled := state == "merged" || state == "closed"
	if _, err := tx.Exec(ctx, `
		update issue_pull_requests
		set state = $2, draft = $3, title = nullif($4::text, ''), merged_at = $5::timestamptz,
		    etag = nullif($6::text, ''), checked_at = now(), failures = 0,
		    next_check_at = case when $7::bool then null else now() + $8::int * interval '1 second' end,
		    updated_at = case when $9::bool then now() else updated_at end
		where id = $1`,
		before.ID, state, pull.Draft, title, mergedAt, res.etag, settled, int(g.interval.Seconds()), changed); err != nil {
		return nil, err
	}
	var recs []syncActionRecord
	if changed {
		rec, _, err := a.emitPullRequestTx(ctx, tx, before.ID, "UPDATE")
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	if state == "merged" && before.State != "merged" && a.cfg.GitHubAutoDone {
		done, err := a.completeOnMergeTx(ctx, tx, before, pullRef{Repo: after.Repo, Number: after.Number})
		if err != nil {
			return nil, err
		}
		recs = append(recs, done...)
	}
	return recs, nil
}

// completeOnMergeTx moves the link's issue to its team's first Done state
// once no PR on it is still pending or open, unless the issue is already
// done or canceled, or no longer active. The caller holds the team lock.
func (a *API) completeOnMergeTx(ctx context.Context, tx pgx.Tx, link pullRow, ref pullRef) ([]syncActionRecord, error) {
	row, err := a.issueByIDTx(ctx, tx, link.IssueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.Status != "active" {
		return nil, nil
	}
	var (
		category string
		waiting  bool
		target   string
	)
	if err := tx.QueryRow(ctx, `
		select coalesce((select category from workflow_statuses where id = $1), 'UNSTARTED'),
		       exists(select 1 from issue_pull_requests where issue_id = $2 and state in ('pending', 'open'))`,
		row.StatusID, row.ID).Scan(&category, &waiting); err != nil {
		return nil, err
	}
	if waiting || category == "COMPLETED" || category == "CANCELED" {
		return nil, nil
	}
	err = tx.QueryRow(ctx, `
		select id from workflow_statuses
		where team_id = $1 and category = 'COMPLETED' and status = 'active'
		order by position, created_at limit 1`, row.TeamID).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	req := issueRequest{StateID: &target, statusNote: ref.String() + " merged on GitHub"}
	changed, recs, err := a.applyIssuePatchTx(ctx, tx, systemPrincipal(), link.WorkspaceID, row, req)
	if err != nil || !changed {
		return recs, err
	}
	rec, err := a.emitChange(ctx, tx, link.WorkspaceID, "Issue", row.ID, "UPDATE", nil)
	if err != nil {
		return nil, err
	}
	fresh, err := a.issueByIDTx(ctx, tx, row.ID)
	if err != nil {
		return nil, err
	}
	if err := a.refreshOutboxTx(ctx, tx, link.WorkspaceID, &rec, a.issueData(fresh)); err != nil {
		return nil, err
	}
	return append([]syncActionRecord{rec}, recs...), nil
}
