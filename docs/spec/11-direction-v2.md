# 11 — Direction v2: the Converge revival path

**Status:** Authoritative addendum to product-spec v1
**Effective date:** 2026-08-30
**Product:** Converge — open-source issue tracker, Go server + Tegon-derived web frontend

## Why this document exists

Specification v1 (documents 00–10) described a **clean-room** product: original
code, original design system, Tegon as behavioral reference only. On 2026-08-30
the project owner redirected the effort to a **revival path**:

> Take Tegon's UI as a basis, refine it, and reframe it as a new product —
> Circle — with an original Go backend.

On 2026-08-31 the project owner renamed the product **Converge** to avoid any
brand adjacency with CircleCI/Circle Payments (see R-12); all references in
this document and the repository now use the current name.

Tegon is effectively unmaintained (last release v0.3.11-alpha, ~1 year of
inactivity, 53 open issues at time of forking), which makes forking its
frontend a stable, legally clear base rather than a moving one.

Where this document conflicts with 00–10, **this document controls**.
Everything else in the v1 specification (domain model, permission matrix,
API contracts, scope matrix, roadmap gates, security invariants) **continues
to apply unchanged** and remains the execution contract for the product.

## Rulings (v1 superseded where noted)

| # | Topic | v1 position | v2 ruling |
| --- | --- | --- | --- |
| R-1 | Frontend provenance (doc 00 principle 5, greenfield-spec) | original code and assets only | The web frontend (`web/`, `packages/`) is a **derivative of Tegon v0.3.11-alpha** (commit `e9d07e4d`) and **remains AGPL-3.0**. Tegon's name, logo, copy, and visual identity must be removed; workflow-level similarity is acceptable, Tegon/Linear trademark expression is not. |
| R-2 | License (OD-01) | open (AGPL vs Apache) | **Closed: AGPL-3.0-or-later for the entire repository.** The AGPL frontend forces the decision; one license keeps the boundary simple. Dual-licensing may be revisited later with a clean-room UI. |
| R-3 | Product name (OD-02) | open | **Closed: Circle (2026-08-30), renamed Converge (2026-08-31, R-12).** Final trademark/domain validation is a release-gate task (v1 launch checklist), not a blocker for development. Provenance is documented in `NOTICE` and `README`. |
| R-4 | Implementation stack (OD-03) | "choose from team expertise" | **Closed: Go.** `server/` is a Go modular monolith (chi + pgx + PostgreSQL) shipped as one static binary; `web/` is the forked Next.js client. The web stays TypeScript/Next.js — it is the forked asset. |
| R-5 | Authentication (OD-04) | maintained OIDC/magic-link component | **Closed: our own Go sessions.** Email magic-link over SMTP plus httpOnly, SameSite=Lax session cookies with server-side session records, rotation, and revocation. No Supertokens service. OIDC as an operator-configurable provider is a later hardening item. The web app's supertokens client is replaced by a small custom auth client. |
| R-6 | Rich text (OD-05) | small versioned JSON schema | **Resolved by inheritance:** keep the Tegon web app's TipTap-based document format (versioned JSON, sanitized renderer, plain-text projection) as-is; the Go backend persists and validates the same document schema. |
| R-7 | Deployment topology (OD-09) | one application artifact | **Adapted:** v1 ships the familiar **two-service layout** (web + api) plus PostgreSQL — the topology Tegon self-hosters already know. Consolidation into one artifact (Go serving built web assets, or static export) is a v1.1 simplification, not a v1 gate. |
| R-8 | Realtime (OD-16 / PLAT-16) | SSE | **Unchanged, now load-bearing:** the web's socket.io client is replaced by an SSE client; the Go server implements the event vocabulary. Refetch-after-gap remains authoritative. |
| R-9 | Client state (PLAT-02) | bounded cache, no IDB replica | **Unchanged, now the main web workstream:** the forked IndexedDB + sequence-delta sync layer is removed; client state becomes server-authoritative REST + SSE with bounded caching and optimistic updates. The store's public API is preserved so screens keep working during the transition. |
| R-10 | Scope matrix | 03/09 govern delivery | **Unchanged, with a trim rule:** any capability the forked web app ships but the matrix does not include in the active release (AI, actions, integrations, support/CRM, prompts) **must be removed from the web app during the trim milestone** — no dead navigation. |
| R-11 | v1 feature scope | MVP per 09 | **Confirmed with one addition:** saved views ship in v1 (core to the product feel); projects/cycles remain v1.1+ per the matrix. |
| R-12 | Product rename (2026-08-31) | Circle | **Closed: Converge.** The full repository (module path, env prefix, compose, docs, demo identities) is renamed `circle`→`converge`. Circle remains a distinct established brand in CI/payments; the rename removes any adjacency doubt without waiting on the launch-gate validation. |

## Decision records (abridged template)

```text
Decision ID: OD-01
Date: 2026-08-30
Owner/approvers: project owner
Chosen option: AGPL-3.0-or-later, whole repository
Rationale: AGPL frontend fork forces open source; uniform license minimizes boundary risk
Security/privacy: unchanged
Operational: none
Alternatives rejected: Apache-2.0 (would require clean-room UI first); dual license (later, post clean-room)
Documents updated: LICENSE, NOTICE, README, this document
```

```text
Decision ID: OD-02
Date: 2026-08-30 (amended 2026-08-31)
Owner/approvers: project owner
Chosen option: Circle, amended to Converge on 2026-08-31
Rationale: short, memorable, no known issue-tracker name collision. Amended to Converge proactively because CircleCI/Circle Payments are established adjacent brands; final validation remains a release-gate task
Security/privacy: none
Operational: github org/repo naming pending
Alternatives rejected: Cairn, Sift, Tally, Strata, Waypoint
Documents updated: package names, README, NOTICE, this document
```

```text
Decision ID: OD-03
Date: 2026-08-30
Owner/approvers: project owner
Chosen option: Go modular monolith (chi, pgx, embedded migrations) + forked Next.js web; PostgreSQL only
Rationale: owner preference for Go; single static binary matches the small-footprint principle; web stays the forked TypeScript asset
Security/privacy: none directly; Go backend is original code (no AGPL-tainted code)
Operational: one binary + postgres (+ web container in v1)
Alternatives rejected: NestJS reuse (rejected by spec), Node rewrite (abandons the fork asset)
Documents updated: server/, this document
```

```text
Decision ID: OD-04
Date: 2026-08-30
Owner/approvers: project owner
Chosen option: original Go auth — email magic link + server-side sessions; OIDC later
Rationale: removes the Supertokens service dependency; full control over session revocation and workspace membership claims
Security/privacy: sessions stored hashed, rotated, revocable; no password crypto
Operational: SMTP configuration (dev fallback: logged links)
Alternatives rejected: Supertokens (extra service), embedded better-auth in Go (no maintained equivalent)
Documents updated: web auth client (M1), this document
```

```text
Decision ID: OD-05
Date: 2026-08-30
Owner/approvers: project owner
Chosen option: inherit Tegon's TipTap document format (versioned JSON + sanitized render + plain-text projection)
Rationale: the web editor is the forked asset; its document schema becomes the canonical format
Security/privacy: sanitization on write, defensive render (existing)
Operational: none
Alternatives rejected: new editor (abandons fork asset)
Documents updated: this document
```

```text
Decision ID: OD-09
Date: 2026-08-30
Owner/approvers: project owner
Chosen option: v1 = web + api + postgres (three containers); one-artifact consolidation deferred to v1.1
Rationale: matches the topology self-hosters of Tegon-class apps expect; de-risks v1
Security/privacy: unchanged
Operational: docker-compose at repo root
Alternatives rejected: single artifact in v1 (blocking risk)
Documents updated: docker-compose.yaml, this document
```

## Open items and work plan (v1 → v1.1)

| ID | Item | Gate |
| --- | --- | --- |
| OD-15 | Concrete p95 performance budgets at 10k issues / 1k team / 100 sessions | v1 acceptance |
| OD-19 | Telemetry: removed from the forked web in the M3 trim (PostHog + Sentry); if analytics ever returns: off by default, operator opt-in, no issue content | **Resolved (M3)** |
| OD-21/22 | Browser matrix, WCAG 2.2 AA process | v1 acceptance |
| A1 | Crash-class sweep: audit client MST models and value-lookups against the Go wire contract (role, DELETE records, and priority found organically so far — all fixed; the rest must be audited, not discovered) | v1 acceptance |
| A2 | Wire-contract tests: server Go tests for every model's I/U/D payload + a client MST replay harness — makes A1 a permanent guarantee (CI currently runs `go test ./...` against zero test files) | v1 acceptance |
| A3 | Real invite email (SMTP) + production transport flags in the deploy guide (dev magic-link only today) | v1 acceptance |
| C | Multi-instance: shared broker (NATS/Redis) behind the `Broadcaster` seam + distributed rate limit — the delta endpoint keeps such a deployment *correct* until then | before horizontal scale |
| D1 | Handoff protocol (doc 12): `issue_handoffs` + `POST /issues/{id}/handoff` + quiet budget/loop guard + client handoff flow, timeline item, board chip — server-first | **Done** — shipped with pause + "needs human" badge; next is D3 |
| D2 | Swarm panel: live roster, handoff trail, token burn — topology-agnostic watchability | **Done** — Swarm button on the issues board: fleet roster (busy/idle, last handoff, 24h accounting) with paused issues on top; request-count burn proxy until D3 |
| D3 | LLM agent runtime: inbox = assignments + incoming handoffs, summaries as untrusted data, act → hand back, per-agent spend | post-v1 |
| D4 | Topology selector as a fleet setting (Settings → Agents) | v1.1+ — only after D3 yields usage data for both modes (doc 12) |
| B | R-9 client-state rewrite (drop IndexedDB + sequence-delta for server-authoritative + bounded cache) — explicitly **v1.1, not pre-release**: the sync layer is load-bearing and was just stabilized (M6 sync fix); rewriting it is churn with no user-visible return | v1.1 |
| NEW | Final trademark/domain validation for "Converge" | public launch |
| NEW | Go module path on first public release | public launch |
| NEW | Web standalone Docker layout verification | M1 — **resolved (M4)** |

## Consequences accepted

1. **Converge is open source, AGPL, permanently** under this path. A future
   closed-source variant would require the clean-room UI work the v1 spec
   describes — the v1 documents are retained in `docs/spec` precisely to
   keep that door defined.
2. The forked web app carries Tegon's accumulated technical debt (IndexedDB
   sync, dead modules, Supertokens wiring). The roadmap now has an explicit
   **trim + data-layer workstream** to pay it down.
3. Attribution to Tegon is permanent (NOTICE, README, AGPL notices).
