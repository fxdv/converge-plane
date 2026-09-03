# 12 — Agent coordination: the handoff protocol

**Status:** Authoritative for the D workstream (agent runtime)
**Effective date:** 2026-09-02
**Depends on:** doc 07 §Agent principals (M6), doc 08 (sync contract), doc 11 (direction v2)

## Why this document exists

M6 made agents first-class actors: machine accounts, API tokens, assignability,
per-agent suspension, per-account rate limiting. The demo swarm proved the
plumbing with a poll-and-advance heuristic — but it has no coordination. Five
agents sharing one board, uncoordinated, is a zerg: ping-pong reassignments,
comment stampedes, un-attributable noise, and a board humans stop trusting.

This document is the coordination contract the agent runtime builds on. It is
the spine of the D workstream and is deliberately topology-agnostic: the
protocol is small (one table, one endpoint) and the topology is a policy layer
over it (see below).

## The three rules

1. **Mediated channel only.** All agent-to-agent communication is a
   server-mediated **handoff** on an issue. No direct agent-to-agent channels,
   no chat/DM system (a noise generator with no board semantics — if two agents
   need shared context, the artifact is a sub-issue or a note attached to work).
   Every handoff is a request from an authenticated principal; the server
   stamps the actor. Trust comes from mediation, attribution, and auditability
   — not from the agents.

2. **A handoff is an atomic work transition, not a message.** One operation,
   one board-visible event: *new assignee + state move + bounded summary*.
   The summary is structured: what was done / what was found / what remains /
   why that agent. Size-capped at the server (~4 KB).

3. **Every issue has a quiet budget and a loop guard.** Agent operations on an
   issue are budgeted per time window. When handoffs cycle back to the same
   agent K times within a window, the issue freezes into `needs-human` (rule 4
   escalation). Ping-pong is the zerg signature; this is its circuit breaker.

## Escalation

On budget exhaustion or loop detection the issue **pauses** (no agent may act
on it) and a human is notified. Confirmed default (owner, 2026-09-02):
pause + notify, not flag-and-continue — a stuck swarm must stop spending
tokens, not decorate the board.

As shipped (D1), the pause is `issues.agent_paused`: agents get a
422 on every issue mutation (handoff, update, move, delete, comment);
humans act freely, and any human mutation on the paused issue resumes
it — the human's fix is the unblock. The notification surfaces as the
board's "needs human" badge and a timeline event carrying the guard's
reason.

## Data and API

- `issue_handoffs(id, workspace_id, issue_id, from_account_id,
  to_account_id, state_id, summary, created_at)` + a per-issue handoff counter
  for the loop guard.

  **Quiet-guard constants (shipped, D1):** loop = the same account is the
  handoff **target** ≥3 times in 24 h; budget = ≥50 agent-authored
  `issue_history` rows on the issue in 24 h; summary cap = 4096 bytes.
  Both guards read under the issue's per-team advisory lock, in the same
  transaction that applies the handoff, so two concurrent handoffs cannot
  race past each other.
- A handoff is also an `issue_history` row (`action = 'handoff'`) and an
  audit event, so the trace exists in every existing surface.
- `POST /api/v1/issues/{id}/handoff {toAccountId, stateId, summary}` — the
  proven shape: validate (requester is the current assignee or the foreman;
  target is an active member agent; budget/loop pass) → one transaction
  (assignee + state + history + outbox) → broadcast. Same contract as every
  other mutation.
- Wire: the handoff rides the existing sync feed; the client renders it as a
  first-class timeline item and board chip (see board semantics).

## Trust and security

- **Identity.** Per-account tokens (M6); the server derives the actor from the
  token — an agent cannot impersonate or forge another agent's work; humans and
  agents are distinguishable in every record.
- **Content.** A summary authored by agent A becomes part of agent B's context:
  a prompt-injection vector. The server caps and sanitizes size; the runtime
  **must** treat received summaries as untrusted data, never instructions.
- **Cadence.** Two independent caps: the per-account rate limit (M6) and the
  per-issue operation budget (this document). One agent cannot flood; a swarm
  cannot flood one issue.

## Topology is a policy layer, not a protocol

A handoff is always `from → to + summary`. A topology only changes *policy* on
the same primitive: who may dispatch, the allowed handoff graph, and where
budgets/escalation sit.

- **Foreman (default).** One lead agent dispatches work, accepts returns, and
  is the sole handoff target for workers. Structurally no zerg; loop-detection
  in one place. The dispatch point is a single point of failure and carries its
  own budget + escalation (foreman-stalled → human).
- **Flat.** Any agent may hand to any agent (N×(N−1) edges); guards per edge;
  combinatorial noise and token spread. Shipped only after usage data favors
  it — never before foreman has real production time.
- **Selector.** A user-facing topology selector is **deferred** until both
  modes have usage data. When it arrives it is a fleet-level setting
  (Settings → Agents) with the consequences spelled out on the settings
  screen — never a board or issue control.
- **Demo/experimentation.** An env/seed-level mode on the swarm runner
  (`topology=foreman|flat`) is acceptable; it is experiment infrastructure,
  not product UI.

## Board semantics (noise → signal)

- Issue chip: current owner (badged) + the *latest* handoff summary, collapsed.
- Agents' intermediate work (comments, diffs, experiments) lives in a
  per-issue activity log — hidden by default, drillable. Humans get signal;
  engineers get the trace.
- **The trace is the product.** Full per-issue timeline — who, why, when, with
  what result — so a human can always reconstruct what the swarm did.
- **Swarm panel** (D2, shipped): `GET /api/v1/workspaces/{id}/swarm` feeds
  the Swarm button on the issues board. Per agent: busy/open/paused work,
  last handoff in either direction (summary trimmed to 200 chars — the full
  trace is on the issue timeline), 24h handoffs-received and operation
  counts (the quiet-guard signals per agent), and its API request count —
  the token-burn proxy until D3's LLM runtime reports real usage into the
  same slot. Paused issues (D1 escalations) sit at the top with the guard's
  reason. Topology-agnostic: identical for foreman or flat.

  Implementation notes: every statistic is a read-only aggregate over the
  existing trace tables (tenant-indexed since 0014) — five bounded indexed
  reads per poll, no per-request writes. The request counter rides the rate
  limiter's in-memory seam on the shared 24h guard window. Any workspace
  member may read it: all data is already visible in the workspace.

## Build sequence (D workstream)

| Phase | Content | Notes |
| --- | --- | --- |
| D1 | Handoff protocol: table + endpoint + budget/loop guards + client flow (picker, summary, timeline item, board chip) | **Shipped** — `POST /issues/{id}/handoff`, pause + "needs human" badge, handoff/pause timeline items, all history rows ride the sync feed with `action` + `summary` |
| D2 | Swarm panel: roster + trace + token accounting UI | **Shipped** — `GET /workspaces/{id}/swarm` + board Swarm button: fleet roster (busy/idle, last handoff, 24h ops/handoffs/calls) with paused issues on top; requests24h is the token-burn proxy D3 upgrades to real LLM usage |
| D3 | LLM agent runtime: inbox = assignments + incoming handoffs; summaries as untrusted data; act → hand back; per-agent spend | The moat; consumes D1/D2 |
| D4 | Topology selector as a fleet setting | Only after D3 produces usage data for both modes (doc 11 table) |
