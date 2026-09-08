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

On budget exhaustion, loop detection, or an agent's own judgment that it
cannot proceed, the issue **pauses** (no agent may act on it) and a human
is notified. Confirmed default (owner, 2026-09-02): pause + notify, not
flag-and-continue — a stuck swarm must stop spending tokens, not decorate
the board.

### The Human Review protocol (as shipped)

The pause is `issues.agent_paused`, and every pause writes **three
disclosures** in one transaction, through one choke point (`pauseIssueTx`):

1. **A column.** When the team's workflow has a state named
   `Human Review` (a reserved name — teams without it are unaffected,
   the pause flags in place), the issue moves there. The board shows
   *where* the card waits instead of leaving the signal scattered.
2. **A comment.** The card receives a human-handoff comment authored by
   the agent that parked it: *why* the swarm stopped (the guard's own
   words, the model's reason); the *where things stand* note (D4 — when
   the pausing side supplied one: what was done, what was found, what
   is blocked, what the human must decide or do, written for a reader
   with zero context); plus the exact path — reply with your decision,
   move the card back into the workflow to resume, or take the card
   over / close it. The signal (badge/panel) says where; the comment
   says what to do.
3. **The badge and timeline.** As before: the board's "needs human"
   badge, a `paused` timeline event carrying the reason, an audit
   event, the broadcast.

**Queue exclusion.** No agent works a card sitting in `Human Review`:
the dispatcher scan, the worker queue, and the fleet's workload counts
all exclude that state by its reserved name, and agents get a 422 on
every mutation of a card in it (update, comment, handoff, delete).
Parked means parked, whatever the pause flag says — including a card a
*human* moved there: a human-parked card stays parked until the human
moves it back, reassigns it, or closes it.

**Resume.** A human mutation on a paused issue resumes the swarm (D1)
and wakes the assignee agent; the agent re-decides with the issue's
recent discussion in its prompt context (the last few comments, fenced
like every other authored field) — a human's answer in a comment
*reaches* the swarm on the next decision. The swarm panel's needs-human
list is the pause flag **or** the column: a card shows for as long as
it waits on a human by either signal, with the reason (swarm's own
words for a swarm pause; "parked" for a human-parked card).

As shipped (D1), agents get a 422 on every issue mutation of a paused
issue (handoff, update, move, delete, comment); humans act freely, and
any human mutation on the paused issue resumes it — the human's fix is
the unblock.

### The foreman review (as built)

`spec cs:swarm:review`

A pause is the swarm asking a human a question. The question it asks
most, unasked, is what happens to a card the human resumed that the
swarm still cannot progress on — left alone, the card cycles through
Human Review (park, resume, park) decorating the queue with noise the
reply never changed. The review protocol breaks that cycle with **one
rule, two places**:

| Mechanism | Fires when | Effect |
| --- | --- | --- |
| Cycle breaker (work cycle) | a pause would put the card at ≥ 3 parks within the shared 24h guard window, **or** the last park went unanswered for a work session | the pause does **not** park: the card is **escalated** to the human foreman — reassigned, In Progress, pause cleared, full disclosure |
| Standing review (tick) | a swarm-parked card has a human reply since the last park | the swarm **resumes** — flag cleared, back to In Progress, the reply in its prompt context |
| Standing review (tick) | no reply since the last park, and the card is at ≥ 3 parks in 24h | **escalated**, the same shape as the breaker |
| Standing review (tick) | no reply since the last park, and the last park is a work session (4h) old | **escalated** — a parked card may not wait forever: the counter can age out of its 24h window, but the duty cannot |

The breaker sits in the pause choke point every escalation flows
through, so no pause surface (LLM pause, advance-into-Human-Review,
quiet-guard trip) can re-park a cycled card; below the threshold and
fresh, a pause parks exactly as before (one park is a question, two a
pattern, three a cycle). The escalation condition is one shared rule
— the cycle counter or staleness (a full work session without an
answer) — applied at both places. The tick is a runtime goroutine on an interval
(`CONVERGE_SWARM_REVIEW_INTERVAL`, default 30m, `0` disables the tick
while the breaker remains) that runs an immediate pass on boot, then
reviews every swarm-parked card in every workspace. It acts **as the
human foreman** (the workspace owner, else the oldest admin): the
standing delegation is the human's, and every action it takes is a
history row, a comment, an audit event, and the outbox — the same trace
shape as any mutation, so the duty is visible in the activity feed, and
a brain-indicator-shaped slot on the swarm plane reports the interval,
the last pass, and what it did.

Escalation is **terminal for the swarm**: the card is assigned to a
human, and the swarm's queue is agent-assigned (a worker only picks a
card assigned to itself), so no worker can touch it again — the cycle
ends by construction, not by promise. A human who wants the swarm back
reassigns the card to an agent; the 24h window then clears and the
swarm gets its full threshold of attempts again. The verdict is
decided under the team lock against the row re-read under it (the work
cycle's snapshot/apply shape), anchored by the row's version: a
concurrent human or worker mutation wins, and the review commits
nothing.

**Constants (shipped):** park threshold = 3 within the shared 24h
window (the quiet-guard family); staleness = 4h (one work session
without an answer); review interval = `CONVERGE_SWARM_REVIEW_INTERVAL`
default 30m (`0` disables the tick, the breaker remains). To confirm
against usage data before they harden into product constants.

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
- **Flat (D4, selectable).** Any agent may hand to any agent (N×(N−1)
  edges); guards per edge; combinatorial noise and token spread.
  Selectable on the Swarm page; the foreman default stands until usage
  data favors flat.
- **Selector (D4, shipped).** The Swarm page's fleet settings: the human
  picks the topology (foreman / flat) and designates the foreman, per
  workspace; the decision takes effect on the swarm's next decision —
  fleet-level, consequences spelled out on the page, never a board or
  issue control. A suspended or removed foreman designation degrades to
  the tenure rule; the setting never blocks the swarm.
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
| D3 | Agent runtime: in-process dispatcher + per-agent workers; deterministic policy (advance / complete / hand-off / pause); fenced untrusted summaries; per-agent spend slot | **Shipped** — the moat. The LLM backend is the next build on the same policy slot |
| D4 | Swarm plane: the dedicated Swarm page (fleet settings: topology + foreman), rich handoff notes, aligned needs-human chips | **Shipped** — per-workspace topology/foreman settings (the deploy-time env stays the default for workspaces without a row; effective on the swarm's next decision, no restart; owner/admin only, agents 422); the pause's human-handoff comment carries a task-level "where things stand" note (LLM pause: model note, fenced, 1600-char cap; guards and dead-ends: server templates); the client chip mirrors the server's needs-human predicate (flag OR column) |

## D3 as-built (agent runtime)

The runtime runs in-process inside the API artifact (one Go binary,
in-process workers) and adds **no tables and no dependencies**: the D1/D2
trace tables plus in-memory state (per-agent worker map, 24h spend
window, one coalesced wakeup) — the same seam discipline as the rate
limiter, so a multi-instance deployment moves it to the shared broker.

- **Inbox.** The issue table is the queue: open, unpaused, non-terminal,
  assigned to the agent. A dispatcher reconciles the fleet on a tick
  (`CONVERGE_RUNTIME_TICK`, default 5s); the mutation paths that create
  agent work (handoff applied, reassignment, resume-from-pause) wake it
  after commit. A lost or dropped wake costs at most one tick — the scan
  is the truth, the wake is a hint.
- **Workers.** At most one per agent at a time, self-terminating when the
  queue drains, the account is retired, or it is cancelled. An agent is
  serialized against itself by construction; concurrency is bounded by
  the fleet, never by the issue count. The work cycle's optional reads
  (queue pick, issue reload, current status, incoming handoff summary)
  map *no rows* to *empty*, never to failure: a drained queue or a
  freshly assigned issue with no handoff is a quiet state, not an error
  (the class of bug the live demo caught on first run).

  The work cycle is **three phases** (D3+): *snapshot* — one short
  transaction under the issue's team advisory lock: the row is read
  under the lock, the cycle is gated on it still being actionable, the
  policy's input is assembled, and the row's version is captured;
  *decide* — outside any transaction: a pure decision costs
  microseconds, the LLM decision is one model call bounded by its own
  timeout, and no database connection, transaction, or lock is held
  across it (model latency must never serialize the team or freeze
  transaction-time timestamps); *apply* — one short transaction under
  the same lock: the row is re-read under the lock and must still be the
  one the decision was made on (same owner, still actionable, same
  version — every issue mutation bumps it). A mutation during the model
  call invalidates the decision: it is discarded (its tokens are counted
  as spend — the call happened) and the worker re-picks and re-decides
  from fresh facts.
- **Policy slot.** `Act(DecisionInput) → (Action, tokens)`, with the
  input split into trusted server facts and the fenced untrusted summary.
  Shipped: the deterministic policy — advance one workflow step (comment
  the step), complete at the terminal state, a dead-end state escapes
  through the handoff protocol (the loop guard is the circuit breaker),
  and a dead-end with no available agent pauses for a human. The LLM
  backend (D3+) plugs into the same slot: one OpenAI-compatible model
  call per decision — a self-hosted fleet of Qwen3.8-27B (UD-Q4_K_M
  GGUF) on four Tesla V100-SXM3 32GB via llama.cpp servers, one instance
  per GPU, bound to loopback only (the model server has no auth; the box
  firewall allows SSH alone), the fleet provided by the deployment via
  `CONVERGE_LLM` — with the fenced title/description/summary as marked
  data, a strict JSON reply contract, and a token cap. Every proposal is
  validated against the trusted input (state names must exist, forward-
  only, never canceled, never self; handoff targets must satisfy the
  active topology; comments capped), and any failure — endpoint down,
  timeout, malformed or invalid output — falls back to the deterministic
  policy per decision. Default **off**: the deterministic policy is the
  shipped brain; the fleet is an operator opt-in. The fleet is addressed
  by a stable per-agent hash: an agent's context stays on one instance
  (warm weights, warm KV cache across that agent's repeated decisions)
  while the swarm's work spreads across the fleet — with one llama.cpp
  instance per GPU, an N-agent swarm draws on up to N GPUs (each
  instance runs n_slots=4), so the parallelism a five-agent swarm sees
  is real, not a shared queue.
- **Topology.** The workspace's saved swarm settings (D4, the Swarm
  page) — topology and foreman designation — override the environment
  switch (`CONVERGE_RUNTIME_TOPOLOGY=foreman|flat`, the default for
  workspaces without a saved row); the foreman is the human's
  designation while that agent is active, else the fleet's oldest active
  agent (the tenure rule). Effective on the swarm's next decision; no
  restart.
- **Config.** `CONVERGE_RUNTIME` (default on), `CONVERGE_RUNTIME_TOPOLOGY`
  (foreman), `CONVERGE_RUNTIME_TICK` (5s), `CONVERGE_LLM` (default off),
  `CONVERGE_LLM_URLS` (comma-separated OpenAI-compatible endpoints, one
  per GPU), `CONVERGE_LLM_MODEL` (qwen3.8-27b), `CONVERGE_LLM_TIMEOUT`
  (60s), `CONVERGE_LLM_MAX_TOKENS` (2048).
- **Trace and spend.** Every runtime action writes the same history /
  outbox / broadcast trail as the API; the panel's token slot reports
  per-agent spend over the 24h window.
- **Live signal.** Each worker publishes its in-flight state as a
  `SwarmActivity` sync record (spec `cs:swarm:activity`): one record per
  agent carrying `{agent, issue, phase, since}`. `working` covers the
  transactional phases (milliseconds); `deciding` is emitted only when the
  active policy is the LLM brain (a deterministic decision is
  microseconds — the phase would only flicker). Emission is best-effort on
  a fresh 5s context (a wedged database logs and drops the signal; it
  never fails the work) and the record is deleted on every worker exit.
  Ephemeral by contract: no new table (the outbox row is the record,
  trimmed with the window), the bootstrap collector replays the runtime's
  live in-memory state, and the client TTLs entries at 2 minutes — longer
  than the LLM decision bound, shorter than human patience. A crashed
  process simply stops emitting.

## Live swarm surfaces (as-built, D3+)

The trace says what *has been* done; the signal says what *is happening*
now. One signal, three surfaces; the board stays signal-only (the
anti-crowding rule — a swarm of any size must not turn the board into its
own news feed):

1. **Board chip** (the card): while a fresh signal is present, a pulsing
   emerald dot + `{agentName} deciding|working` beside the "needs human"
   badge, plus the latest handoff summary within 7 days as one muted,
   clamped line (≤200 chars). The card shows where the swarm left off, not
   what it said.
2. **Activity feed** (workspace): a header button opens a right panel
   deriving every entry client-side from stores the client already syncs in
   full — handoffs (summary), status moves (destination named), pauses
   (guard reason), assignments, comments (doc-JSON → text preview), and the
   live signals on top. Bounded to the 50 most recent entries; an
   All/Agents/Humans filter keeps the swarm's output from drowning out the
   team's. **There is deliberately no `/activity` endpoint**: the outbox
   already delivers the full workspace trace (history, comments) and the
   signal rides the same seam; a server endpoint would be a parallel source
   of truth.
3. **Swarm panel** (fleet): the in-flight issue + phase as a live row,
   status dot pulsing; the 24h counters remain poll-driven (history, not
   signal).

The client's `phase` is a plain string in the MST model on purpose: a new
server phase must degrade, never crash model validation (the project has a
history of exactly that crash class).
