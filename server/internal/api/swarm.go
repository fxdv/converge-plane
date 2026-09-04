// swarm.go — D2: the swarm panel (docs/spec/12).
// spec cs:swarm:panel
// spec cs:swarm:metrics
//
// GET /api/v1/workspaces/{id}/swarm serves the issues board's swarm
// panel: a live fleet roster (who owns what, busy/idle, last handoff,
// 24h burn) plus the issues currently paused for a human. The shape is
// topology-agnostic by design: foreman and flat swarms produce the
// identical payload; the panel renders the trace, and topology stays a
// policy layer elsewhere (the D4 selector, when it arrives).
//
// Every statistic is a read-only aggregate over the existing trace
// tables (issues, issue_history, issue_handoffs — all tenant-indexed
// since the D1 migration) plus the in-memory request counter kept by
// the rate limiter. No new tables, no per-request writes: the panel
// costs a handful of bounded indexed reads per poll, so it scales with
// the roster, not with the work.
//
// requests24h counts authenticated API requests (the D2 burn proxy —
// still live for agents that act over HTTP, like the tools/swarm demo
// process). tokens24h is the D3 runtime's slot: real model tokens the
// agent spent inside the in-process runtime (zero for the
// deterministic policy; an LLM policy reports its usage here).
package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"converge/internal/auth"
)

// swarmSummaryTrim bounds the handoff summary the panel displays; the
// full summary lives on the issue's timeline (D1) and on issue_handoffs.
const swarmSummaryTrim = 200

// swarmSummaryCap is the SQL form of the cap, shared by the panel's
// queries (all alias the summary column h.summary). var, not const:
// the single source of truth for the number stays swarmSummaryTrim.
var swarmSummaryCap = fmt.Sprintf("left(h.summary, %d)", swarmSummaryTrim)

// swarmHandoff is the panel's per-agent last-handoff line: one atomic
// work transition, the direction from this agent's point of view.
type swarmHandoff struct {
	IssueID         string    `json:"issueId"`
	IssueNumber     int       `json:"issueNumber"`
	IssueTitle      string    `json:"issueTitle"`
	Direction       string    `json:"direction"` // "in": the agent received the issue; "out": it handed it off
	CounterpartID   string    `json:"counterpartId"`
	CounterpartName string    `json:"counterpartName"`
	Summary         string    `json:"summary"`
	CreatedAt       time.Time `json:"createdAt"`
}

// swarmAgent is one agent member's fleet row.
type swarmAgent struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Email   string   `json:"email"`
	Status  string   `json:"status"` // ACTIVE | SUSPENDED
	TeamIDs []string `json:"teamIds"`
	// Busy: the agent has work it can actually act on — an active
	// membership and at least one open issue that is not paused. A
	// paused issue counts toward openIssueCount but not toward busy:
	// it is waiting on a human, and the agent is idle.
	Busy             bool          `json:"busy"`
	OpenIssueCount   int           `json:"openIssueCount"`
	PausedIssueCount int           `json:"pausedIssueCount"`
	LastActivityAt   *time.Time    `json:"lastActivityAt"`
	LastHandoff      *swarmHandoff `json:"lastHandoff"`
	// The quiet-guard signals, per agent, over the shared 24h window:
	// handoffs received (the loop guard) and authored operations (the
	// op budget's per-agent share), plus API calls (the burn proxy).
	Handoffs24h int   `json:"handoffs24h"`
	Ops24h      int   `json:"ops24h"`
	Requests24h int64 `json:"requests24h"`
	// Tokens24h is the D3 spend slot: model tokens the in-process
	// runtime spent for this agent over the 24h window (zero until an
	// LLM policy is attached; the deterministic policy spends none).
	Tokens24h int64     `json:"tokens24h"`
	CreatedAt time.Time `json:"createdAt"`
}

// swarmPausedIssue is a D1 escalation currently open: the issue is
// paused, no agent may act on it, and a human is being pointed at it.
// Reason is the guard's own words from the pause history row.
type swarmPausedIssue struct {
	ID           string    `json:"id"`
	Number       int       `json:"number"`
	Title        string    `json:"title"`
	TeamID       string    `json:"teamId"`
	StateID      string    `json:"stateId"`
	AssigneeID   *string   `json:"assigneeId"`
	AssigneeName *string   `json:"assigneeName"`
	Reason       string    `json:"reason"`
	PausedAt     time.Time `json:"pausedAt"`
}

// swarmStatus is the GET /api/v1/workspaces/{id}/swarm response: the
// fleet roster (busy first) plus the issues that need a human.
type swarmStatus struct {
	Agents       []swarmAgent       `json:"agents"`
	PausedIssues []swarmPausedIssue `json:"pausedIssues"`
}

// handleSwarmStatus implements GET /api/v1/workspaces/{id}/swarm.
//
// Any active workspace member may read the fleet: every datum is either
// already visible in the workspace (the agents, the issues, the trace)
// or a count of it — the panel leaks no information a member could not
// already assemble from the sync feed.
func (a *API) handleSwarmStatus(w http.ResponseWriter, r *http.Request) {
	p := PrincipalFromContext(r.Context())
	if p == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	workspaceID := r.PathValue("id")
	if !isUUID(workspaceID) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if _, ok := a.workspaceRole(ctx, p, workspaceID); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	agents, err := a.swarmRoster(ctx, workspaceID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	paused, err := a.swarmPausedIssues(ctx, workspaceID)
	if err != nil {
		a.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, swarmStatus{Agents: agents, PausedIssues: paused})
}

// swarmRoster builds the per-agent entries. The membership base is one
// query; every statistic is one aggregate query keyed by account id and
// joined in Go — five bounded indexed reads instead of a cartesian
// per-agent join, so the cost grows with the roster.
func (a *API) swarmRoster(ctx context.Context, workspaceID string) ([]swarmAgent, error) {
	rows, err := a.pool.Query(ctx, `
		select a.id, a.name, a.email, wm.status,
		       coalesce((select array_agg(tm.team_id) from team_members tm
		               where tm.account_id = a.id), '{}'),
		       wm.created_at
		from accounts a
		join workspace_members wm on wm.workspace_id = $1 and wm.account_id = a.id
		where a.kind = $2
		order by wm.created_at, a.name`, workspaceID, auth.AccountKindAgent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	agents := []swarmAgent{}
	ids := []string{}
	for rows.Next() {
		var ag swarmAgent
		var status string
		if err := rows.Scan(&ag.ID, &ag.Name, &ag.Email, &status, &ag.TeamIDs, &ag.CreatedAt); err != nil {
			return nil, err
		}
		ag.Status = strings.ToUpper(status)
		agents = append(agents, ag)
		ids = append(ids, ag.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(agents) == 0 {
		return agents, nil
	}

	// Open and paused work per agent. Terminal = a completed or
	// canceled workflow category; everything else a human or an agent
	// can still move is open.
	workloads := map[string][2]int{}
	wrows, err := a.pool.Query(ctx, `
		select i.assignee_id,
		       count(*) filter (where coalesce(ws.category, 'UNSTARTED') not in ('COMPLETED', 'CANCELED'))::int,
		       count(*) filter (where coalesce(ws.category, 'UNSTARTED') not in ('COMPLETED', 'CANCELED')
		                          and i.agent_paused)::int
		from issues i
		join teams t on t.id = i.team_id
		left join workflow_statuses ws on ws.id = i.status_id
		where t.workspace_id = $1 and i.status = 'active' and i.assignee_id = any($2::uuid[])
		group by 1`, workspaceID, ids)
	if err != nil {
		return nil, err
	}
	for wrows.Next() {
		var accountID string
		var open, paused int
		if err := wrows.Scan(&accountID, &open, &paused); err != nil {
			wrows.Close()
			return nil, err
		}
		workloads[accountID] = [2]int{open, paused}
	}
	err = wrows.Err()
	wrows.Close()
	if err != nil {
		return nil, err
	}

	// Activity: the agent's newest authored history row (any action)
	// and its 24h operation share of the swarm's output.
	type swarmActivity struct {
		last   time.Time
		ops24h int
	}
	activity := map[string]swarmActivity{}
	hrows, err := a.pool.Query(ctx, `
		select h.actor_id, max(h.created_at),
		       count(*) filter (where h.created_at > now() - $3::interval)::int
		from issue_history h
		join accounts a on a.id = h.actor_id and a.kind = $4
		where h.workspace_id = $1 and h.actor_id = any($2::uuid[])
		group by 1`, workspaceID, ids, guardWindowSQL, auth.AccountKindAgent)
	if err != nil {
		return nil, err
	}
	for hrows.Next() {
		var accountID string
		var act swarmActivity
		if err := hrows.Scan(&accountID, &act.last, &act.ops24h); err != nil {
			hrows.Close()
			return nil, err
		}
		activity[accountID] = act
	}
	err = hrows.Err()
	hrows.Close()
	if err != nil {
		return nil, err
	}

	// The last handoff the agent was part of, in either direction.
	// Newest wins per agent: the set is ordered by (agent, created_at
	// desc) and the first row per agent is kept.
	lastHandoffs := map[string]swarmHandoff{}
	lhrows, err := a.pool.Query(ctx, `
		select agent, issue_id, issue_number, issue_title, direction,
		       counterpart_id, counterpart_name, summary, created_at
		from (
			select h.to_account_id as agent,
			       i.id as issue_id, i.number as issue_number, i.title as issue_title,
			       'in' as direction,
			       h.from_account_id as counterpart_id, fa.name as counterpart_name,
			       `+swarmSummaryCap+` as summary,
			       h.created_at
			from issue_handoffs h
			join issues i on i.id = h.issue_id
			join accounts fa on fa.id = h.from_account_id
			where h.workspace_id = $1 and h.to_account_id = any($2::uuid[])
			union all
			select h.from_account_id, i.id, i.number, i.title, 'out',
			       h.to_account_id, ta.name, `+swarmSummaryCap+`,
			       h.created_at
			from issue_handoffs h
			join issues i on i.id = h.issue_id
			join accounts ta on ta.id = h.to_account_id
			where h.workspace_id = $1 and h.from_account_id = any($2::uuid[])
		) t
		order by agent, created_at desc`, workspaceID, ids)
	if err != nil {
		return nil, err
	}
	for lhrows.Next() {
		var agentID string
		var h swarmHandoff
		if err := lhrows.Scan(&agentID, &h.IssueID, &h.IssueNumber, &h.IssueTitle, &h.Direction,
			&h.CounterpartID, &h.CounterpartName, &h.Summary, &h.CreatedAt); err != nil {
			lhrows.Close()
			return nil, err
		}
		if _, seen := lastHandoffs[agentID]; !seen {
			lastHandoffs[agentID] = h
		}
	}
	err = lhrows.Err()
	lhrows.Close()
	if err != nil {
		return nil, err
	}

	// The loop-guard signal: handoffs each agent RECEIVED in the window
	// (the D1 guard counts per target; this is the same count per agent).
	handoffs24 := map[string]int{}
	srows, err := a.pool.Query(ctx, `
		select to_account_id, count(*)::int
		from issue_handoffs
		where workspace_id = $1 and created_at > now() - $2::interval
		  and to_account_id = any($3::uuid[])
		group by 1`, workspaceID, guardWindowSQL, ids)
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var accountID string
		var n int
		if err := srows.Scan(&accountID, &n); err != nil {
			srows.Close()
			return nil, err
		}
		handoffs24[accountID] = n
	}
	err = srows.Err()
	srows.Close()
	if err != nil {
		return nil, err
	}

	for i := range agents {
		ag := &agents[i]
		if wl, ok := workloads[ag.ID]; ok {
			ag.OpenIssueCount, ag.PausedIssueCount = wl[0], wl[1]
		}
		if act, ok := activity[ag.ID]; ok {
			last := act.last
			ag.LastActivityAt = &last
			ag.Ops24h = act.ops24h
		}
		if h, ok := lastHandoffs[ag.ID]; ok {
			ag.LastHandoff = &h
		}
		ag.Handoffs24h = handoffs24[ag.ID]
		ag.Requests24h = a.limiter.usageCount(ag.ID)
		ag.Tokens24h = a.runtime.SpendCount(ag.ID)
		ag.Busy = ag.Status == "ACTIVE" && ag.OpenIssueCount > ag.PausedIssueCount
	}

	// The panel's reading order: working agents first, then idle,
	// suspended last; within a group, the busiest then the name.
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Busy != agents[j].Busy {
			return agents[i].Busy
		}
		if agents[i].Status != agents[j].Status {
			return agents[i].Status == "ACTIVE"
		}
		if agents[i].OpenIssueCount != agents[j].OpenIssueCount {
			return agents[i].OpenIssueCount > agents[j].OpenIssueCount
		}
		return agents[i].Name < agents[j].Name
	})
	return agents, nil
}

// swarmPausedIssues lists the D1 escalations currently open in the
// workspace, newest first. Reason is the guard's own words (the pause
// history row's summary) so a human sees why the swarm stopped before
// opening the issue.
func (a *API) swarmPausedIssues(ctx context.Context, workspaceID string) ([]swarmPausedIssue, error) {
	rows, err := a.pool.Query(ctx, `
		select i.id, i.number, i.title, i.team_id, i.status_id, i.assignee_id,
		       i.updated_at,
		       coalesce((select `+swarmSummaryCap+`
		                 from issue_history h
		                 where h.issue_id = i.id and h.action = 'paused'
		                 order by h.created_at desc limit 1), ''),
		       coalesce((select an.name from accounts an where an.id = i.assignee_id), '')
		from issues i
		join teams t on t.id = i.team_id
		where t.workspace_id = $1 and i.status = 'active' and i.agent_paused
		order by i.updated_at desc`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []swarmPausedIssue{}
	for rows.Next() {
		var pi swarmPausedIssue
		var assigneeID, assigneeName string
		if err := rows.Scan(&pi.ID, &pi.Number, &pi.Title, &pi.TeamID, &pi.StateID,
			&assigneeID, &pi.PausedAt, &pi.Reason, &assigneeName); err != nil {
			return nil, err
		}
		if assigneeID != "" {
			id := assigneeID
			pi.AssigneeID = &id
		}
		if assigneeName != "" {
			name := assigneeName
			pi.AssigneeName = &name
		}
		out = append(out, pi)
	}
	return out, rows.Err()
}
