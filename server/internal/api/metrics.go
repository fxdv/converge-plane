// metrics.go — the metrics plane (docs/spec/product-spec, ch. 6,
// §Metrics): a separate read-only surface for everything a foreman
// watches while the product, the codebase, and the swarm are alive.
// spec cs:swarm:metrics
//
// Four sections, one endpoint:
//
//   - Product  — the tenant's own usage: what exists, what moved.
//   - Codebase — the running artifact and its live process: version,
//     uptime, goroutines, pool, realtime subscribers, feed depth.
//   - Swarm    — the fleet: the panel's roster plus the swarm-level
//     rates (token spend, handoff volume, pause rate, resume time,
//     agent share of completed work).
//   - Proxy    — the LLM fleet router: per-node requests, latency,
//     errors, tokens (the nodes that decide), plus each account's
//     request-rate usage (the D2 burn proxy).
//
// Access: any active workspace member may read (the same rule as the
// swarm status — every datum is an aggregate of rows the member can
// already see, or of in-memory slots the plane exists to expose).
//
// Cost: a handful of bounded indexed reads and in-memory snapshots per
// poll. No new tables, no per-request writes (the proxy registry is the
// one in-memory writer, and it writes on decision, not on poll). The
// plane scales with the roster, not with the work.
package api

import (
	"context"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"converge/internal/auth"
)

// guardWindowLiteral is the metrics plane's 24h window as a SQL literal
// (the runtime's guardWindowSQL is a parameter value, not a literal —
// a constant pair to keep, like the runtime's own window pair).
const guardWindowLiteral = "interval '24 hours'"

// completedStatusIDsSQL is the tenant's completed status ids, as text.
// issue_history.to_value is an opaque text column (an id for status
// transitions, a title for title changes), so the plane compares its
// lower-cased form against the lower-cased id text instead of casting
// it to uuid: a non-id history row must never 500 a dashboard, and the
// client may send upper-case ids (the database renders uuids lower).
const completedStatusIDsSQL = `
	select lower(ws.id::text)
	from workflow_statuses ws
	join teams t on t.id = ws.team_id
	where t.workspace_id = $1 and ws.category = 'COMPLETED'`

// buildinfo is overridable at link time, e.g.
//
//	go build -ldflags "-X converge/internal/api.buildGitSHA=$(git rev-parse --short HEAD) ..."
//
// The defaults keep dev builds honest ("dev" is a visible state, not a
// blank one).
var (
	buildVersion  = "dev"
	buildGitSHA   = "none"
	buildTime     = "unknown"
	buildPlatform = runtime.GOOS + "/" + runtime.GOARCH
)

// ---- wire shapes (the client's metrics page renders exactly these) ----

// stateCount is one workflow state's share of the workspace's live
// issues (the product section's distribution row).
type stateCount struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// productMetrics is the tenant's usage: what exists now, and what moved
// in the bounded windows (24h / 7d).
type productMetrics struct {
	Members       int          `json:"members"`
	MembersActive int          `json:"membersActive"`
	Teams         int          `json:"teams"`
	Workflows     int          `json:"workflows"`
	Labels        int          `json:"labels"`
	Projects      int          `json:"projects"`
	Views         int          `json:"views"`
	Issues        int          `json:"issues"`
	States        []stateCount `json:"states"`
	Comments      int          `json:"comments"`
	HistoryRows   int          `json:"historyRows"`
	// Bounded-window flows.
	IssuesCreated24h int `json:"issuesCreated24h"`
	IssuesCreated7d  int `json:"issuesCreated7d"`
	IssuesDone24h    int `json:"issuesDone24h"`
	IssuesDone7d     int `json:"issuesDone7d"`
	Comments24h      int `json:"comments24h"`
	Comments7d       int `json:"comments7d"`
	Handoffs24h      int `json:"handoffs24h"`
	Handoffs7d       int `json:"handoffs7d"`
}

// poolGauges is the database pool's live gauges.
type poolGauges struct {
	Acquired int `json:"acquired"`
	Idle     int `json:"idle"`
	Max      int `json:"max"`
}

// codebaseMetrics is the running artifact (what is deployed) plus the
// live process (how it is doing right now).
type codebaseMetrics struct {
	Version        string        `json:"version"`
	GitSHA         string        `json:"gitSha"`
	BuildTime      string        `json:"buildTime"`
	Platform       string        `json:"platform"`
	Uptime         time.Duration `json:"uptime"` // nanoseconds on the wire
	Goroutines     int           `json:"goroutines"`
	Pool           poolGauges    `json:"pool"`
	SSESubscribers int           `json:"sseSubscribers"`
	FeedDepth      int           `json:"feedDepth"`    // outbox rows retained for the workspace
	FeedSequence   string        `json:"feedSequence"` // the current watermark
}

// swarmMetrics is the fleet: the roster (identical to the panel's) plus
// the swarm-level rates over the guard windows.
type swarmMetrics struct {
	Agents       int     `json:"agents"` // agent members, any status
	ActiveAgents int     `json:"activeAgents"`
	Busy         int     `json:"busy"`
	OpenIssues   int     `json:"openIssues"` // open, not paused (all assignees)
	PausedIssues int     `json:"pausedIssues"`
	Tokens24h    int64   `json:"tokens24h"`
	Ops24h       int     `json:"ops24h"`
	Handoffs24h  int     `json:"handoffs24h"`
	Pauses24h    int     `json:"pauses24h"`
	PauseRate24h float64 `json:"pauseRate24h"` // pauses / (pauses + completions), 24h
	MeanResumeMs int64   `json:"meanResumeMs"` // 0 when nothing resumed in the window
	// The approved statistic (the mean is kept alongside): 0 when nothing
	// resumed in the window; the two diverge when a few long parks skew
	// the distribution.
	MedianResumeMs int64 `json:"medianResumeMs"`
	Completions24h int   `json:"completions24h"`
	// Cost per completed issue over the guard window: the per-issue model
	// tokens for the issues that reached a completed state, with spend.
	// In-memory attribution (a restart starts fresh; a spend that crossed
	// the window boundary counts what the window holds).
	CostMedianTokens24h int64 `json:"costMedianTokens24h"`
	CostMeanTokens24h   int64 `json:"costMeanTokens24h"`
	// Completed issues that had spend data in the window (the sample the
	// two medians above are over; 0 makes them "no data", not "free").
	CostedIssues24h int `json:"costedIssues24h"`
	// The floor's share of the runtime's decisions in the window (the
	// fallback rate): 1.0 means the model never decided, 0 means it
	// never fell back. 0 also when nothing reported (a floor-only fleet).
	FallbackRate24h float64 `json:"fallbackRate24h"`
	// The deepest handoff chain on the board in the window (hops of the
	// most-chased issue) — the crowding signal the handoff graph asks for.
	LongestHandoffChain24h int          `json:"longestHandoffChain24h"`
	CompletedByAgents7d    int          `json:"completedByAgents7d"`
	CompletedByHumans7d    int          `json:"completedByHumans7d"`
	AgentShare7d           float64      `json:"agentShare7d"` // agent completions / all completions, 7d
	Roster                 []swarmAgent `json:"roster"`
}

// agentBurnRow is one account's rate-limiter usage (the D2 burn proxy,
// per account, over its window).
type agentBurnRow struct {
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Usage24h  int64  `json:"usage24h"`
}

// proxyMetrics is the LLM fleet router: the nodes (where decisions are
// made) and the accounts' request usage (where the flood guard sits).
type proxyMetrics struct {
	Mode      string          `json:"mode"` // "llm" | "floor" | "off"
	Model     string          `json:"model,omitempty"`
	Timeout   time.Duration   `json:"timeout,omitempty"` // configured decision bound
	Nodes     []FleetNodeStat `json:"nodes"`             // empty when no fleet is configured
	AgentBurn []agentBurnRow  `json:"agentBurn"`
	RateRPS   float64         `json:"rateRps"`
	RateBurst int64           `json:"rateBurst"`
}

// metricsResponse is the GET /api/v1/workspaces/{id}/metrics payload.
type metricsResponse struct {
	WorkspaceID string          `json:"workspaceId"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Product     productMetrics  `json:"product"`
	Codebase    codebaseMetrics `json:"codebase"`
	Swarm       swarmMetrics    `json:"swarm"`
	Proxy       proxyMetrics    `json:"proxy"`
}

// ---- the handler ----

// handleMetrics implements GET /api/v1/workspaces/{id}/metrics.
func (a *API) handleMetrics(w http.ResponseWriter, r *http.Request) {
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

	out := metricsResponse{
		WorkspaceID: workspaceID,
		GeneratedAt: time.Now(),
		Codebase: codebaseMetrics{
			Version:        buildVersion,
			GitSHA:         buildGitSHA,
			BuildTime:      buildTime,
			Platform:       buildPlatform,
			Uptime:         time.Since(a.startedAt),
			Goroutines:     runtime.NumGoroutine(),
			Pool:           a.poolGauges(),
			SSESubscribers: a.bcast.SubscriberCount(workspaceID),
		},
	}
	if err := a.productSection(ctx, workspaceID, &out.Product); err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.feedSection(ctx, workspaceID, &out.Codebase); err != nil {
		a.internalError(w, err)
		return
	}
	if err := a.swarmSection(ctx, workspaceID, &out.Swarm); err != nil {
		a.internalError(w, err)
		return
	}
	a.proxySection(ctx, workspaceID, &out.Proxy)
	writeJSON(w, http.StatusOK, out)
}

// poolGauges snapshots the database pool (zero-safe on a missing or
// non-pool db — the tests' fake has no gauges).
func (a *API) poolGauges() poolGauges {
	p, ok := a.pool.(*pgxpool.Pool)
	if !ok || p == nil {
		return poolGauges{}
	}
	s := p.Stat()
	return poolGauges{
		Acquired: int(s.AcquiredConns()),
		Idle:     int(s.IdleConns()),
		Max:      int(s.MaxConns()),
	}
}

// productSection fills the tenant's usage section.
func (a *API) productSection(ctx context.Context, ws string, m *productMetrics) error {
	if err := a.pool.QueryRow(ctx, `
		select count(*), count(*) filter (where status = 'active')
		from workspace_members where workspace_id = $1`, ws).Scan(&m.Members, &m.MembersActive); err != nil {
		return err
	}
	counts := map[string]int{}
	var name string
	rows, err := a.pool.Query(ctx, `
		select 'teams', count(*) from teams where workspace_id = $1 and status = 'active'
		union all
		select 'workflows', count(*) from workflow_statuses ws
			join teams t on t.id = ws.team_id
			where t.workspace_id = $1 and ws.status = 'active'
		union all
		select 'labels', count(*) from labels where workspace_id = $1 and status = 'active'
		union all
		select 'projects', count(*) from projects where workspace_id = $1 and deleted_at is null
		union all
		select 'views', count(*) from saved_views where workspace_id = $1 and status = 'active'
		union all
		select 'issues', count(*) from issues i
			join teams t on t.id = i.team_id
			where t.workspace_id = $1 and i.status = 'active'
		union all
		select 'comments', count(*) from comments c
			join issues i on i.id = c.issue_id
			join teams t on t.id = i.team_id
			where t.workspace_id = $1 and c.status = 'active'
		union all
		select 'history', count(*) from issue_history where workspace_id = $1`, ws)
	if err != nil {
		return err
	}
	for rows.Next() {
		var n int
		if err = rows.Scan(&name, &n); err != nil {
			rows.Close()
			return err
		}
		counts[name] = n
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	m.Teams = counts["teams"]
	m.Workflows = counts["workflows"]
	m.Labels = counts["labels"]
	m.Projects = counts["projects"]
	m.Views = counts["views"]
	m.Issues = counts["issues"]
	m.Comments = counts["comments"]
	m.HistoryRows = counts["history"]

	m.States = []stateCount{}
	srows, err := a.pool.Query(ctx, `
		select ws.name, coalesce(ws.category, 'UNSTARTED'), count(*)
		from issues i
		join teams t on t.id = i.team_id
		left join workflow_statuses ws on ws.id = i.status_id
		where t.workspace_id = $1 and i.status = 'active'
		group by 1, 2 order by 3 desc`, ws)
	if err != nil {
		return err
	}
	for srows.Next() {
		var sc stateCount
		if err = srows.Scan(&sc.Name, &sc.Category, &sc.Count); err != nil {
			srows.Close()
			return err
		}
		m.States = append(m.States, sc)
	}
	if err = srows.Err(); err != nil {
		srows.Close()
		return err
	}
	srows.Close()

	// Bounded-window flows: one indexed count per (object, window). Each
	// where carries its own window clause — a joined query has several
	// created_at columns, and only the object's own counts its creations.
	sevenDay := "interval '7 days'"
	countIn := func(where string) (int, error) {
		var n int
		if err := a.pool.QueryRow(ctx, where, ws).Scan(&n); err != nil {
			return 0, err
		}
		return n, nil
	}
	issueWhere := func(win string) string {
		return `select count(*) from issues i join teams t on t.id = i.team_id where t.workspace_id = $1 and i.status = 'active' and i.created_at > now() - ` + win
	}
	commentWhere := func(win string) string {
		return `select count(*) from comments c join issues i on i.id = c.issue_id join teams t on t.id = i.team_id where t.workspace_id = $1 and c.status = 'active' and c.created_at > now() - ` + win
	}
	offWhere := func(win string) string {
		return `select count(*) from issue_handoffs h where h.workspace_id = $1 and h.created_at > now() - ` + win
	}
	var n int
	if n, err = countIn(issueWhere(guardWindowLiteral)); err != nil {
		return err
	}
	m.IssuesCreated24h = n
	if n, err = countIn(issueWhere(sevenDay)); err != nil {
		return err
	}
	m.IssuesCreated7d = n
	if n, err = countIn(commentWhere(guardWindowLiteral)); err != nil {
		return err
	}
	m.Comments24h = n
	if n, err = countIn(commentWhere(sevenDay)); err != nil {
		return err
	}
	m.Comments7d = n
	if n, err = countIn(offWhere(guardWindowLiteral)); err != nil {
		return err
	}
	m.Handoffs24h = n
	if n, err = countIn(offWhere(sevenDay)); err != nil {
		return err
	}
	m.Handoffs7d = n
	// A completion is a status transition into a completed-category state.
	doneWhere := func(win string) string {
		return `
		select count(*) from issue_history h
		where h.workspace_id = $1 and h.action = 'updated' and h.field = 'status'
		  and h.created_at > now() - ` + win + `
		  and lower(h.to_value) in (` + completedStatusIDsSQL + `)`
	}
	if n, err = countIn(doneWhere(guardWindowLiteral)); err != nil {
		return err
	}
	m.IssuesDone24h = n
	if n, err = countIn(doneWhere(sevenDay)); err != nil {
		return err
	}
	m.IssuesDone7d = n
	return nil
}

// feedSection fills the realtime-feed gauges (the sync engine's live
// state for this workspace).
func (a *API) feedSection(ctx context.Context, ws string, m *codebaseMetrics) error {
	var depth int
	if err := a.pool.QueryRow(ctx,
		"select count(*) from sync_outbox where workspace_id = $1", ws).Scan(&depth); err != nil {
		return err
	}
	m.FeedDepth = depth
	// The coalesce subselect mirrors sync.go: a workspace with no
	// watermark yet still answers ("0"), never a 500.
	var seq int64
	if err := a.pool.QueryRow(ctx,
		"select coalesce((select last_sequence from sync_sequences where workspace_id = $1), 0)", ws).Scan(&seq); err != nil {
		return err
	}
	m.FeedSequence = strconv.FormatInt(seq, 10)
	return nil
}

// medianDuration is the median of a window's samples (even count: the
// mean of the two middle values). The plane's statistics are gauges,
// and a median keeps one long park from dominating the read.
func medianDuration(ds []time.Duration) time.Duration {
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	n := len(ds)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return ds[n/2]
	}
	return (ds[n/2-1] + ds[n/2]) / 2
}

// medianInt64 is the same statistic for the cost samples (tokens per
// completed issue).
func medianInt64(vs []int64) int64 {
	sort.Slice(vs, func(i, j int) bool { return vs[i] < vs[j] })
	n := len(vs)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return vs[n/2]
	}
	return (vs[n/2-1] + vs[n/2]) / 2
}

// swarmSection fills the fleet section: the panel's roster (the shared
// swarmRoster — the panel and the plane never diverge) plus the
// swarm-level rates.
func (a *API) swarmSection(ctx context.Context, ws string, m *swarmMetrics) error {
	roster, err := a.swarmRoster(ctx, ws)
	if err != nil {
		return err
	}
	m.Roster = roster
	for _, ag := range roster {
		if ag.Status == "ACTIVE" {
			m.ActiveAgents++
			if ag.Busy {
				m.Busy++
			}
			m.Tokens24h += ag.Tokens24h
			m.Ops24h += ag.Ops24h
		}
		m.Handoffs24h += ag.Handoffs24h
	}
	m.Agents = len(roster)

	// Work counts for the whole board (all assignees, agent or human) —
	// the same filter pair the roster uses, without the agent join.
	var open, paused int
	err = a.pool.QueryRow(ctx, `
		select count(*) filter (where coalesce(ws.category, 'UNSTARTED') not in ('COMPLETED', 'CANCELED')
		                          and not i.agent_paused and `+notHumanReviewSQL+`),
		       count(*) filter (where coalesce(ws.category, 'UNSTARTED') not in ('COMPLETED', 'CANCELED')
		                          and `+needsHumanSQL+`)
		from issues i
		join teams t on t.id = i.team_id
		left join workflow_statuses ws on ws.id = i.status_id
		where t.workspace_id = $1 and i.status = 'active'`, ws).Scan(&open, &paused)
	if err != nil {
		return err
	}
	m.OpenIssues = open
	m.PausedIssues = paused

	// Pause and completion events over the guard window: the pause rate
	// is pauses against all terminal-ish outcomes (a swarm that neither
	// pauses nor completes reads as zero — the honest reading).
	err = a.pool.QueryRow(ctx, `
		select count(*) filter (where h.action = 'paused'),
		       count(*) filter (where h.action = 'updated' and h.field = 'status'
		                          and lower(h.to_value) in (`+completedStatusIDsSQL+`))
		from issue_history h
		where h.workspace_id = $1 and h.created_at > now() - `+guardWindowLiteral, ws).Scan(&m.Pauses24h, &m.Completions24h)
	if err != nil {
		return err
	}
	if denom := m.Pauses24h + m.Completions24h; denom > 0 {
		m.PauseRate24h = float64(m.Pauses24h) / float64(denom)
	}

	// Mean time-to-human-resume: pair each pause with the next resume of
	// the same issue inside the window (a few rows; paired in Go).
	type parkRow struct {
		issue string
		kind  string
		at    time.Time
	}
	erows, err := a.pool.Query(ctx, `
		select h.issue_id, h.action, h.created_at
		from issue_history h
		where h.workspace_id = $1 and h.action in ('paused', 'resumed')
		  and h.created_at > now() - `+guardWindowLiteral+`
		order by h.issue_id, h.created_at`, ws)
	if err != nil {
		return err
	}
	openPark := map[string]time.Time{}
	var total time.Duration
	var resumes []time.Duration
	var paired int
	for erows.Next() {
		var ev parkRow
		if err = erows.Scan(&ev.issue, &ev.kind, &ev.at); err != nil {
			erows.Close()
			return err
		}
		switch ev.kind {
		case "paused":
			openPark[ev.issue] = ev.at
		case "resumed":
			if started, ok := openPark[ev.issue]; ok {
				d := ev.at.Sub(started)
				total += d
				resumes = append(resumes, d)
				paired++
				delete(openPark, ev.issue)
			}
		}
	}
	if err = erows.Err(); err != nil {
		erows.Close()
		return err
	}
	erows.Close()
	if paired > 0 {
		m.MeanResumeMs = int64(total / time.Duration(paired) / time.Millisecond)
		m.MedianResumeMs = int64(medianDuration(resumes) / time.Millisecond)
	}

	// Cost per completed issue: the issues that reached a completed state
	// in the window, then their per-issue spend from the runtime's
	// attribution. In-memory: a completed issue the process did not watch
	// spend has no row here (the plane's sample says so — 0 spend reads
	// as "no data" through CostedIssues24h, not as "free").
	cids, err := a.pool.Query(ctx, `
		select distinct h.issue_id
		from issue_history h
		where h.workspace_id = $1 and h.action = 'updated' and h.field = 'status'
		  and h.created_at > now() - `+guardWindowLiteral+`
		  and lower(h.to_value) in (`+completedStatusIDsSQL+`)
	`, ws)
	if err != nil {
		return err
	}
	var spends []int64
	for cids.Next() {
		var id string
		if err = cids.Scan(&id); err != nil {
			cids.Close()
			return err
		}
		if tok := a.runtime.IssueSpendCount(ws, id); tok > 0 {
			spends = append(spends, tok)
		}
	}
	if err = cids.Err(); err != nil {
		cids.Close()
		return err
	}
	cids.Close()
	if n := len(spends); n > 0 {
		m.CostedIssues24h = n
		m.CostMedianTokens24h = medianInt64(spends)
		var sum int64
		for _, s := range spends {
			sum += s
		}
		m.CostMeanTokens24h = sum / int64(n)
	}

	// Fallback rate: the floor's share of the runtime's decisions in the
	// window (the in-memory decision counter; 0 when nothing reported).
	if decisions, fallbacks := a.runtime.DecisionStats24h(); decisions > 0 {
		m.FallbackRate24h = float64(fallbacks) / float64(decisions)
	}

	// Longest handoff chain in the window: the most-chased issue's hop
	// count (the crowding signal behind the handoff graph). One bounded
	// aggregate over the tenant-indexed trace table.
	err = a.pool.QueryRow(ctx, `
		select coalesce(max(cnt), 0) from (
			select count(*) cnt
			from issue_handoffs
			where workspace_id = $1 and created_at > now() - `+guardWindowLiteral+`
			group by issue_id
		) chains`, ws).Scan(&m.LongestHandoffChain24h)
	if err != nil {
		return err
	}

	// Agent share of completed work, 7d: the actor's kind on the
	// completion row (an agent and a human both write the same
	// transition; the kind is what splits the credit).
	err = a.pool.QueryRow(ctx, `
		select count(*) filter (where a.kind = $2),
		       count(*) filter (where a.kind <> $2)
		from issue_history h
		join accounts a on a.id = h.actor_id
		where h.workspace_id = $1 and h.action = 'updated' and h.field = 'status'
		  and h.created_at > now() - interval '7 days'
		  and lower(h.to_value) in (`+completedStatusIDsSQL+`)`,
		ws, auth.AccountKindAgent).Scan(&m.CompletedByAgents7d, &m.CompletedByHumans7d)
	if err != nil {
		return err
	}
	if denom := m.CompletedByAgents7d + m.CompletedByHumans7d; denom > 0 {
		m.AgentShare7d = float64(m.CompletedByAgents7d) / float64(denom)
	}
	return nil
}

// proxySection fills the LLM-fleet section from the in-memory registry.
// It never fails the request: a missing runtime or fleet degrades to a
// disabled row (a dashboard must show its own dead state, not 500).
func (a *API) proxySection(ctx context.Context, ws string, m *proxyMetrics) {
	m.Nodes = []FleetNodeStat{}
	m.AgentBurn = []agentBurnRow{}
	m.RateRPS = a.cfg.RateLimitRPS
	m.RateBurst = a.cfg.RateLimitBurst
	if a.runtime == nil {
		m.Mode = "off"
		return
	}
	m.Mode = a.runtime.brainView().Mode
	m.Model = a.cfg.LLMModel
	m.Timeout = a.cfg.LLMTimeout
	if llm := a.runtime.llm; llm != nil {
		m.Nodes = llm.fleetView()
	}
	rows, err := a.pool.Query(ctx, `
		select a.id, a.name, wm.status
		from accounts a
		join workspace_members wm on wm.workspace_id = $1 and wm.account_id = a.id
		where a.kind = $2 order by a.name`, ws, auth.AccountKindAgent)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		row := agentBurnRow{}
		var status string
		if err = rows.Scan(&row.AccountID, &row.Name, &status); err != nil {
			return
		}
		row.Status = strings.ToUpper(status)
		if a.limiter != nil {
			row.Usage24h = a.limiter.usageCount(row.AccountID)
		}
		m.AgentBurn = append(m.AgentBurn, row)
	}
}
