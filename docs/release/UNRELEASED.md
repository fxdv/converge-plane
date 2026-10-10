# Converge — unreleased (since v1.0.0)

Draft notes for the next tag. `v1.0.0` (2026-09-07) is the only tag; this
file covers `git log v1.0.0..HEAD` (139 commits). Row ids are doc 11's
(`docs/spec/11-direction-v2.md`); contracts are in doc 08 "As built".

## The pivot: external agents first

v1.0 was a tracker with a built-in swarm: in-process agent members, a
deterministic policy, and an optional self-hosted LLM proposing decisions.
Since then the primary path is **coding agents the operator already runs**
(Claude Code, Cursor, Codex, …) working the board over MCP and a work API,
with cost, trace, and pull-request proof on the card. The in-process
runtime remains as the floor that triages (`CONVERGE_RUNTIME`, on by
default); the LLM backend is optional and off by default (`CONVERGE_LLM`).

## What shipped

| Row | Scope |
|---|---|
| v1.1 | Issue relations; projects (workspace-scoped, one per issue, board rail with drag assign/clear) |
| SWR-56, SWR-13, SWR-14 | Issue artifacts (the swarm's document channel); the in-app inbox; swarm metrics on the metrics page |
| H | Phase 0/1 hardening: fail-closed config, single-use magic links, trusted-proxy client IP and auth rate limits, comment/relation authorization, rotated sessions with reuse detection, cookie Origin check, `If-Match` for agents, gap-free sync cursor, Prometheus metrics |
| P2-1 | Scoped agent tokens (scopes + team grants, deny by default), per-agent driver (`runtime` / `external`), claim/heartbeat/release work API |
| P2-2 | Run ledger: reported model, tokens, cost, capped trace, evidence, outcome per claim |
| P2-3 | MCP server: stateless streamable HTTP at `POST /api/v1/mcp`, tools re-dispatched to REST under the caller's token |
| P2-4, P2-5 | GitHub pull-request links (allowlisted repos), ETag poller, signed GitHub webhook receiver, manual link/unlink, repo renames |
| P2-6 | Agent token rotation and revocation that never widens a grant; token streams close on revocation |
| P2-7, P3-1 | Signed outbound webhooks; evidence-gated Done (merged PR or human approval), team spend caps, signed JSONL trace export |
| P5 | GitHub combined status as Done evidence; web client sends `If-Match`; webhook delivery list/retry |
| P6 | Multiple API processes: `LISTEN/NOTIFY` fan-out, shared `rate_buckets`, one advisory-lock leader for runtime, sweeper, poller, and webhook delivery |
| P7 | Live check on a deployed board: an external agent's claimed issue closed by a merged PR |
| P8, P9 | Shared sign-in limits; one-way import of open GitHub issues into one team's queue |
| P10 | Agent economy on the swarm page: per-team 24h spend and remainder, spend by agent, refusal counts |
| Web | Spec 25–28 frontend phases: sync banner and conflicts, R-9 client-state slices, TanStack Query v5, CSP nonces, board virtualization, TipTap 3, Next.js 16.3.8; R-10 AI-writing trim (doc 11) |

## Migrations 0016–0027

Forward-only, applied at boot unless `CONVERGE_AUTO_MIGRATE=false`.

| File | Adds |
|---|---|
| 0016_issue_relations | Directed issue relations |
| 0017_projects | `projects`; `issues.project_ids` (at most one) |
| 0018_issue_artifacts | Documents posted to an issue |
| 0019_notifications | In-app inbox records |
| 0020_session_rotation | Sessions become the refresh authority (rotation, reuse detection) |
| 0021_agent_work_api | Token scopes and team grants; `agent_driver`; `issue_claims` for the work API |
| 0022_agent_runs | Run ledger (backfills one run per existing claim) |
| 0023_issue_pull_requests | Tracked PR links |
| 0024_issue_pull_request_unlink | Manual link and unlink (tombstones) |
| 0025_governance | Done approval columns for the evidence gate; outbound webhook endpoints and events |
| 0026_phase5_phase6 | Stored commit status; shared `rate_buckets` |
| 0027_phase8_phase9 | `auth_rate_buckets`; `github_issue_links` for imported issues |

## Operator-facing changes

- New settings (see `deploy/README.md`): `CONVERGE_TRUSTED_PROXIES`,
  `CONVERGE_WEB_ORIGIN_EXTRA`, `CONVERGE_MAIL_LOG_BODIES`, SMTP
  (`CONVERGE_SMTP_*`), `CONVERGE_METRICS_ADDR`, `CONVERGE_TRACE_SIGNING_KEY`,
  and GitHub (`CONVERGE_GITHUB_REPOS`, `_TOKEN`, `_WEBHOOK_SECRET`,
  `_POLL_INTERVAL`, `_AUTO_DONE`, `_ISSUE_TEAM`).
- Config fails closed: invalid values stop the server at boot.
- A second API process is supported against the same Postgres; no broker.
- External agents need an agent member with the `external` driver and a
  scoped token from the Add agent dialog, then an MCP client pointed at
  `/api/v1/mcp`.
- The self-hosted LLM fleet is no longer part of the default stack; the
  runbook treats it as optional.
- Tunnel helper scripts (`scripts/*-tunnel.sh`) now require
  `CONVERGE_TUNNEL_HOST` and `CONVERGE_TUNNEL_KEY`; they have no defaults.
