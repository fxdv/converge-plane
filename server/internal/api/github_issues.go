// Open GitHub issues on an allowlisted repository are copied into one
// team's queue. The copy is not written back, and a close on GitHub
// does not move the Converge issue.
// spec cs:agents:prlinks

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
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type githubListedIssue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	PullRequest *struct{} `json:"pull_request"`
}

// importOnce lists open issues for one concrete allowlisted repository
// and copies any that this workspace has not already copied. owner/*
// entries are skipped: listing them would mean discovering every
// repository under the owner.
func (g *githubPoller) importOnce(ctx context.Context) error {
	if g.paused() || g.a.cfg.GitHubIssueTeam == "" {
		return nil
	}
	var repos []string
	for _, entry := range g.a.cfg.GitHubRepos {
		if !strings.HasSuffix(entry, "/*") {
			repos = append(repos, entry)
		}
	}
	if len(repos) == 0 {
		return nil
	}
	g.mu.Lock()
	repo := repos[g.issueCursor%len(repos)]
	g.issueCursor++
	g.mu.Unlock()

	owner, name, _ := strings.Cut(repo, "/")
	target := g.base + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/issues?state=open&per_page=20"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil
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
	res, err := g.client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			g.a.log.Warn("GitHub issue list failed", "repo", repo, "error", err)
		}
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusUnauthorized {
		if d, limited := rateLimitPause(res.Header, time.Now()); limited {
			g.pause(d)
			return nil
		}
		g.pause(time.Minute)
		return nil
	}
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4<<10))
		g.a.log.Warn("GitHub issue list was refused", "repo", repo, "status", res.StatusCode)
		return nil
	}
	var listed []githubListedIssue
	if err := json.NewDecoder(io.LimitReader(res.Body, githubMaxBody)).Decode(&listed); err != nil {
		g.a.log.Warn("GitHub issue list was unreadable", "repo", repo, "error", err)
		return nil
	}
	for _, item := range listed {
		if item.PullRequest != nil || item.State != "open" || item.Number < 1 {
			continue
		}
		if err := g.copyIssue(ctx, repo, item); err != nil {
			return err
		}
	}
	return nil
}

func (g *githubPoller) copyIssue(ctx context.Context, repo string, item githubListedIssue) error {
	var workspaceID, teamID, stateID string
	err := g.a.pool.QueryRow(ctx, `
		select t.workspace_id::text, t.id::text,
		       coalesce((select s.id::text from workflow_statuses s
		                 where s.team_id = t.id and s.category = 'UNSTARTED' and s.status = 'active'
		                 order by s.position, s.created_at limit 1), '')
		from teams t where t.id = $1`, g.a.cfg.GitHubIssueTeam).Scan(&workspaceID, &teamID, &stateID)
	if errors.Is(err, pgx.ErrNoRows) || stateID == "" {
		g.a.log.Warn("github issue team has no unstarted status", "team", g.a.cfg.GitHubIssueTeam)
		return nil
	}
	if err != nil {
		return err
	}
	var copied bool
	if err := g.a.pool.QueryRow(ctx, `
		select exists(select 1 from github_issue_links
		              where workspace_id = $1 and repo = $2 and number = $3)`,
		workspaceID, repo, item.Number).Scan(&copied); err != nil {
		return err
	}
	if copied {
		return nil
	}

	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = repo + "#" + strconv.Itoa(item.Number)
	}
	title = trimRunes(title, 255)
	body := trimRunes(strings.TrimSpace(item.Body), 4000)
	description := "Imported from https://github.com/" + repo + "/issues/" + strconv.Itoa(item.Number)
	if body != "" {
		description += "\n\n" + body
	}

	tx, err := g.a.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "conv_issue_"+teamID); err != nil {
		return err
	}
	var number int
	if err := tx.QueryRow(ctx, `select coalesce(max(number), 0) + 1 from issues where team_id = $1`, teamID).Scan(&number); err != nil {
		return err
	}
	var id string
	err = tx.QueryRow(ctx, `
		insert into issues (team_id, number, title, description, status_id, sort_order)
		values ($1, $2, $3, $4::jsonb, $5, 0)
		returning id`,
		teamID, number, title, toJSONB(description), stateID).Scan(&id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		insert into github_issue_links (workspace_id, issue_id, repo, number)
		values ($1, $2, $3, $4)`, workspaceID, id, repo, item.Number)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return nil
		}
		return err
	}
	rec, err := g.a.emitChange(ctx, tx, workspaceID, "Issue", id, "CREATE", nil)
	if err != nil {
		return err
	}
	row, err := g.a.issueByIDTx(ctx, tx, id)
	if err != nil {
		return err
	}
	hist, err := g.a.writeHistoryTx(ctx, tx, workspaceID, teamID, id, "", "created", "status", "", stateID,
		"imported from "+repo+"#"+strconv.Itoa(item.Number))
	if err != nil {
		return err
	}
	if err := g.a.refreshOutboxTx(ctx, tx, workspaceID, &rec, g.a.issueData(row)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	g.a.broadcastRecord(rec)
	g.a.broadcastRecord(hist)
	return nil
}

func trimRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for k := range s {
		if i == n {
			return s[:k]
		}
		i++
	}
	return s
}
