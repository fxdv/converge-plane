// Package seed creates a deterministic demo workspace so a fresh
// deployment has something to look at. It is idempotent: if the demo
// workspace already exists, nothing is written.
package seed

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const demoEmail = "demo@circle.dev"
const demoSlug = "acme"

type seedUser struct {
	email, name, role string
}

// Run seeds the demo workspace and returns the seeded login email.
func Run(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) (string, error) {
	var exists bool
	err := pool.QueryRow(ctx, "select exists(select 1 from workspaces where slug = $1)", demoSlug).Scan(&exists)
	if err != nil {
		return "", fmt.Errorf("check existing workspace: %w", err)
	}
	if exists {
		return demoEmail, fmt.Errorf("demo workspace %q already exists; nothing to do", demoSlug)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := func(sql string, args ...any) pgx.Row { return tx.QueryRow(ctx, sql, args...) }
	exec := func(what, sql string, args ...any) error {
		_, err := tx.Exec(ctx, sql, args...)
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		return nil
	}

	now := time.Now().UTC()
	ago := func(days float64) time.Time {
		return now.Add(-time.Duration(days * float64(24*time.Hour)))
	}
	ids := map[string]string{}
	mk := func(label string) string {
		if v, ok := ids[label]; ok {
			return v
		}
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		v := uuidString(b)
		ids[label] = v
		return v
	}

	// ---- accounts -----------------------------------------------------
	users := []seedUser{
		{demoEmail, "Alex Demo", "owner"},
		{"maya@circle.dev", "Maya Chen", "admin"},
		{"leo@circle.dev", "Leo Park", "member"},
	}
	for _, u := range users {
		if err := exec("seed account "+u.email, `
			insert into accounts (id, email, name)
			values ($1, $2, $3)
			on conflict (email) do nothing`, mk("acc:"+u.email), u.email, u.name); err != nil {
			return "", err
		}
	}

	// ---- workspace + memberships --------------------------------------
	wsID := mk("workspace")
	if err := exec("seed workspace", `
		insert into workspaces (id, name, slug, created_by)
		values ($1, 'Acme Engineering', $2, $3)`,
		wsID, demoSlug, ids["acc:"+demoEmail]); err != nil {
		return "", err
	}
	for _, u := range users {
		if err := exec("seed membership "+u.email, `
			insert into workspace_members (id, workspace_id, account_id, role, status, joined_at)
			values ($1, $2, $3, $4, 'active', $5)`,
			mk("wm:"+u.email), wsID, ids["acc:"+u.email], u.role, ago(30)); err != nil {
			return "", err
		}
	}

	// ---- teams ---------------------------------------------------------
	teams := []struct{ label, name, identifier string }{
		{"team-eng", "Engineering", "ENG"},
		{"team-plat", "Platform", "PLAT"},
	}
	for i, t := range teams {
		if err := exec("seed team "+t.name, `
			insert into teams (id, workspace_id, name, identifier, position)
			values ($1, $2, $3, $4, $5)`,
			mk(t.label), wsID, t.name, t.identifier, i); err != nil {
			return "", err
		}
	}
	memberRoles := map[string]map[string]string{
		"team-eng":  {"acc:" + demoEmail: "manager", "acc:maya@circle.dev": "member", "acc:leo@circle.dev": "member"},
		"team-plat": {"acc:" + demoEmail: "manager", "acc:leo@circle.dev": "member"},
	}
	for teamLabel, roles := range memberRoles {
		for accLabel, role := range roles {
			if err := exec("seed team member "+accLabel, `
				insert into team_members (id, team_id, account_id, role)
				values ($1, $2, $3, $4)`,
				mk(teamLabel+":"+accLabel), ids[teamLabel], ids[accLabel], role); err != nil {
				return "", err
			}
		}
	}

	// ---- workflow statuses ---------------------------------------------
	statusDefs := map[string][]struct{ name, category, color string }{
		"team-eng": {
			{"Backlog", "BACKLOG", "#8d8d8d"},
			{"To Do", "UNSTARTED", "#8884d8"},
			{"In Progress", "STARTED", "#4484d5"},
			{"Done", "COMPLETED", "#36b37e"},
			{"Canceled", "CANCELED", "#cf222e"},
		},
		"team-plat": {
			{"Backlog", "BACKLOG", "#8d8d8d"},
			{"To Do", "UNSTARTED", "#8884d8"},
			{"In Progress", "STARTED", "#4484d5"},
			{"Done", "COMPLETED", "#36b37e"},
		},
	}
	for teamLabel, defs := range statusDefs {
		for i, s := range defs {
			if err := exec("seed status "+s.name, `
				insert into workflow_statuses (id, team_id, name, category, color, position)
				values ($1, $2, $3, $4, $5, $6)`,
				mk(teamLabel+":status:"+s.name), ids[teamLabel], s.name, s.category, s.color, i); err != nil {
				return "", err
			}
		}
	}

	// ---- labels ----------------------------------------------------------
	for _, l := range []struct{ name, color string }{
		{"Bug", "#cf222e"},
		{"Feature", "#8884d8"},
		{"Improvement", "#4484d5"},
		{"Technical Debt", "#bf8700"},
		{"Documentation", "#36b37e"},
	} {
		if err := exec("seed label "+l.name, `
			insert into labels (id, workspace_id, name, color)
			values ($1, $2, $3, $4)`,
			mk("label:"+l.name), wsID, l.name, l.color); err != nil {
			return "", err
		}
	}

	// ---- issues ---------------------------------------------------------
	type issueDef struct {
		team     string
		title    string
		desc     string
		priority int
		status   string // status def name within the team
		assignee string // account label or ""
		parent   string // issue id or ""
		daysAgo  float64
	}
	engIssues := []issueDef{
		{"team-eng", "Migrate billing service to Go", "The billing service is on EOL Node 14. Plan the migration incrementally: payment gateway first, then invoicing.", 1, "In Progress", "acc:maya@circle.dev", "", 21},
		{"team-eng", "Fix race condition in webhook retry loop", "Under load the retry loop can double-process webhooks. Add a per-payload lock and a regression test.", 1, "In Progress", "acc:demo@circle.dev", "", 18},
		{"team-eng", "Upgrade Kubernetes cluster to 1.31", "CIS audit requires the new minor. Schedule a maintenance window and test the ingress controller upgrade.", 2, "To Do", "acc:leo@circle.dev", "", 15},
		{"team-eng", "Reduce p95 API latency on issue lists", "The /issues endpoint serializes 2k rows in one pass. Batch the join and add a covering index.", 2, "In Progress", "", "", 12},
		{"team-eng", "Document self-hosted deployment", "Write the operator guide: TLS, backups, upgrades, and the env reference.", 3, "To Do", "acc:maya@circle.dev", "", 10},
		{"team-eng", "Add dark mode contrast pass", "Several muted-foreground colors fail AA in dark mode. Bump them per the design tokens.", 3, "Backlog", "acc:leo@circle.dev", "", 9},
		{"team-eng", "Keyboard shortcut for creating issues", "Global C key opens the create dialog, matching the design spec.", 2, "Backlog", "", "", 8},
		{"team-eng", "Tighten session revocation on suspension", "Suspended members keep their session cookie until expiry. Revoke on state change.", 1, "Done", "acc:demo@circle.dev", "", 7},
		{"team-eng", "Instrument search latency", "Add structured logs and a histogram for the full-text search path.", 3, "Done", "acc:maya@circle.dev", "", 6},
		{"team-eng", "Deduplicate label names on import", "Imported labels can collide case-insensitively. Normalize on write.", 3, "Done", "", "", 5},
		{"team-eng", "Kanban: allow dropping into any column", "The current dnd handler restricts by category. Relax per the interaction spec.", 2, "Done", "acc:leo@circle.dev", "", 4},
		{"team-eng", "Archive legacy integration definitions", "The deprecated action integrations clutter settings. Move them to an archive state.", 4, "Done", "acc:maya@circle.dev", "", 3},
		{"team-eng", "Evaluate object storage for attachments", "Compare S3-compatible providers for the upcoming attachments feature.", 3, "Backlog", "", "", 2},
		{"team-eng", "Investigate flaky e2e auth test", "The magic-link e2e test is flaky under CI load. Likely a timing assumption.", 2, "Canceled", "acc:demo@circle.dev", "", 1},
	}
	platIssues := []issueDef{
		{"team-plat", "Provision staging Postgres with pgvector", "Needed for the (future) similarity experiments; provision and harden.", 2, "In Progress", "acc:leo@circle.dev", "", 11},
		{"team-plat", "Upgrade CI to GitHub Actions runners v2", "Standard runners are being deprecated. Migrate the workflow files.", 2, "To Do", "acc:demo@circle.dev", "", 6},
		{"team-plat", "Cost review of managed Kubernetes", "Usage jumped 18% last month. Identify the offenders and right-size.", 1, "Done", "", "", 4},
	}

	for _, teamLabel := range []string{"team-eng", "team-plat"} {
		if err := exec("seed counter "+teamLabel, `
			insert into issue_counters (team_id) values ($1)
			on conflict (team_id) do nothing`, ids[teamLabel]); err != nil {
			return "", err
		}
	}

	createIssue := func(def issueDef, number int) (string, error) {
		is := mk("issue:" + def.team + ":" + def.title)
		var statusID string
		if err := q(`select id from workflow_statuses where id = $1`,
			ids[def.team+":status:"+def.status]).Scan(&statusID); err != nil {
			return "", fmt.Errorf("status lookup for %q: %w", def.title, err)
		}
		descJSON, _ := json.Marshal(def.desc)
		if def.assignee != "" {
			err := exec("seed issue "+def.title, `
				insert into issues (id, team_id, number, title, description, status_id,
				                            priority, sort_order, assignee_id, updated_at)
				values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				is, ids[def.team], number, def.title, string(descJSON), statusID,
				def.priority, 0, ids[def.assignee], ago(def.daysAgo))
			if err != nil {
				return "", err
			}
			return is, nil
		}
		err := exec("seed issue "+def.title, `
			insert into issues (id, team_id, number, title, description, status_id,
			                            priority, sort_order, updated_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			is, ids[def.team], number, def.title, string(descJSON), statusID,
			def.priority, 0, ago(def.daysAgo))
		if err != nil {
			return "", err
		}
		return is, nil
	}

	seedIssues := func(definitions []issueDef) error {
		for i, def := range definitions {
			created, err := createIssue(def, i+1)
			if err != nil {
				return err
			}
			if def.parent != "" {
				if err := exec("reparent issue "+def.title,
					`update issues set parent_id = $2 where id = $1`, created, def.parent); err != nil {
					return err
				}
			}
			// Labels by simple rules so the demo board shows color.
			var labelName string
			switch {
			case def.priority == 1:
				labelName = "Bug"
			case def.title == "Add dark mode contrast pass":
				labelName = "Improvement"
			case def.title == "Document self-hosted deployment":
				labelName = "Documentation"
			case def.title == "Migrate billing service to Go":
				labelName = "Technical Debt"
			}
			if labelName != "" {
				if err := exec("seed issue label "+def.title, `
					insert into issue_labels (issue_id, label_id) values ($1, $2)`,
					created, ids["label:"+labelName]); err != nil {
					return err
				}
			}
			// One status-change history entry per issue. to_value carries the
			// status id; the client resolves it against team workflows.
			if err := exec("seed issue history "+def.title, `
				insert into issue_history (workspace_id, team_id, issue_id, actor_id, action, field, from_value, to_value)
				values ($1, $2, $3, $4, 'status_changed', 'status', null, $5)`,
				wsID, ids[def.team], created, ids["acc:"+demoEmail], ids[def.team+":status:"+def.status]); err != nil {
				return err
			}
		}
		return nil
	}
	if err := seedIssues(engIssues); err != nil {
		return "", err
	}
	if err := seedIssues(platIssues); err != nil {
		return "", err
	}

	// Sub-issues under the billing migration.
	parentID := ids["issue:team-eng:"+engIssues[0].title]
	children := []issueDef{
		{"team-eng", "Billing: cut over payment gateway", "Switch Stripe calls to the new Go client behind a flag.", 2, "In Progress", "acc:maya@circle.dev", parentID, 14},
		{"team-eng", "Billing: deprecate Node invoicing", "After the gateway cutover, stop the Node invoicing workers.", 2, "Backlog", "", parentID, 12},
	}
	for i, def := range children {
		created, err := createIssue(def, len(engIssues)+1+i)
		if err != nil {
			return "", err
		}
		if def.parent != "" {
			if err := exec("reparent issue "+def.title,
				`update issues set parent_id = $2 where id = $1`, created, def.parent); err != nil {
				return "", err
			}
		}
	}

	// Comments on the first two engineering issues.
	comment := func(issueTitle, authorLabel, body string, daysAgo float64) error {
		return exec("seed comment "+issueTitle, `
			insert into comments (id, issue_id, author_id, body, created_at, updated_at)
			values ($1, $2, $3, $4, $5, $6)`,
			mk("comment:"+issueTitle+":"+body),
			ids["issue:team-eng:"+issueTitle],
			ids[authorLabel],
			mustJSON(body),
			ago(daysAgo), ago(daysAgo))
	}
	commentCalls := []struct {
		issue, author, body string
		days                float64
	}{
		{engIssues[0].title, "acc:maya@circle.dev", "Gateway cutover is behind a feature flag, safe to test on staging.", 16},
		{engIssues[0].title, "acc:demo@circle.dev", "Agreed. Let's keep the flag for one release cycle before removing the old path.", 15},
		{engIssues[1].title, "acc:leo@circle.dev", "Repro: send 200 identical payloads concurrently against the dev env.", 17},
		{engIssues[1].title, "acc:demo@circle.dev", "Confirmed the double-process in the logs. Draft fix in the linked PR.", 16},
	}
	for _, c := range commentCalls {
		if err := comment(c.issue, c.author, c.body, c.days); err != nil {
			return "", err
		}
	}

	// Sync watermark for the workspace.
	if err := exec("seed sync watermark",
		"insert into sync_sequences (workspace_id, last_sequence) values ($1, 1000)",
		wsID); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit seed: %w", err)
	}
	log.Info("demo workspace seeded", "workspace", "Acme Engineering", "login_email", demoEmail)
	return demoEmail, nil
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// uuidString renders 16 random bytes as a canonical UUID string.
func uuidString(b []byte) string {
	// Canonical 8-4-4-4-12 form; Postgres accepts any 128-bit UUID.
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
