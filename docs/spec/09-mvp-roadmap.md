# 09 — Delivery roadmap

The roadmap is gated by outcomes, not calendar promises. A release begins only after its predecessor meets exit criteria. Future contracts guide compatibility discussions but do not authorize speculative implementation.

## Stage 0 — Specification and engineering foundation

**Goal:** make the product boundary and security model implementable before feature code.

### Required decisions

- resolve all “before code” rows in [10 — Open decisions](10-open-decisions.md);
- select product name/license and confirm no Tegon source/assets are being copied;
- approve workspace/team role semantics and issue identifier behavior;
- choose implementation stack, auth boundary, rich-text format, and deployment topology;
- convert permission operations into policy names and executable authorization test fixtures;
- define performance/accessibility/security test harnesses and release workflow.

### Exit gate

- The 11 product-spec documents are accepted and contradictions resolved.
- Domain schema proposal maps only MVP ADOPT/ADAPT capabilities.
- Threat model covers tenant isolation, auth/session, rich text, uploads future boundary, jobs, and self-host defaults.
- A thin deployment skeleton can start, migrate an empty database, report health/readiness, log safely, and shut down gracefully.

## Release 1 — MVP: minimal core

**Promise:** a team can securely operate the complete issue loop using list or Kanban.

### Workstream A — Identity, tenancy, and operation

- sessions/authentication and invitation flow;
- workspace/team/member/role policy layer;
- atomic onboarding/default workflow;
- PostgreSQL schema/migrations and tenant-scoped repositories;
- health/readiness/version, structured logs, metrics hooks, audit, outbox worker;
- self-host configuration validation, TLS/proxy guidance, backup/restore and upgrade runbook.

**Gate:** cross-workspace and cross-team authorization tests pass for every implemented endpoint; last-Owner, suspension, invite replay, session revocation, and state-switch cache clearing are verified.

### Workstream B — Issue domain

- team issue keys and concurrency-safe numbering;
- title, sanitized rich description, workflow status, priority, assignee, workspace labels;
- comments and one-level replies;
- activity/audit, archive/restore/retention behavior;
- optimistic concurrency and idempotent create.

**Gate:** create/edit/comment/complete/archive journeys pass under concurrency, retries, stale references, permission revocation, and malformed rich content.

### Workstream C — Primary product experience

- original responsive application shell and settings IA;
- team Issues, Triage, My issues;
- grouped virtualized list and status Kanban;
- issue create dialog, full page, side panel, direct property editing;
- structured filters, grouping/order/display preferences;
- PostgreSQL-backed key/title/description search;
- command palette, shortcuts, focus/history behavior, light/dark/system themes.

**Gate:** JRN-01 through JRN-05 and JRN-09 pass with pointer, keyboard, and representative screen reader; no future-release navigation appears.

### Workstream D — Release hardening

- representative workspace data and performance budgets;
- browser/API/domain/policy/migration tests;
- dependency and container scanning, threat-model review, rate/abuse limits;
- accessibility manual audit and automated CI checks;
- fresh install, backup, restore, and upgrade drills;
- operator and user documentation.

### MVP exit criteria

- Clean documented deployment reaches first issue without manual database changes.
- All MVP screen contracts and journeys meet acceptance criteria.
- Authorization matrix has positive/negative tests for every MVP operation and tenant boundary.
- No critical/high security issue remains open; secret and rich-content redaction tests pass.
- Issue list, Kanban, detail, and search meet OD-15 budgets at representative size.
- Backup is restored into a clean environment and application integrity checks pass.
- Accessibility review covers sign-in/onboarding, issue create/detail, list/Kanban, filters/search, and settings.
- Operational failure of optional email/search enhancement/background retry does not corrupt core issue state.

## Release 2 — NEXT: collaboration and planning

**Promise:** teams can preserve useful perspectives, coordinate larger outcomes, receive in-product awareness, and share richer issue context.

### Scope

- custom/shared views and personal bookmarks;
- projects and derived progress;
- team cycles with current/upcoming/past and explicit rollover;
- issue hierarchy and blocks/related/duplicate relations;
- attachments with safe object-storage lifecycle;
- watchers/mute controls;
- in-app Inbox and notifications;
- authorized realtime invalidation/updates;
- bounded bulk edit and due dates where approved.

### Recommended sequence

1. Saved-view structured query versioning and permission behavior.
2. Issue relations/hierarchy and activity model.
3. Projects, then cycles, reusing issue collection/progress primitives.
4. Attachment storage/security/retention.
5. Notification participation rules and Inbox projection.
6. Realtime transport after notification/event authorization is proven.
7. Bulk operations after single-object policy/concurrency behavior is mature.

### NEXT entry gate

- MVP exit criteria remain green for the latest release.
- Product evidence shows repeated needs for each selected NEXT slice; slices may ship independently behind no user-visible placeholder.
- OD-12 through OD-14 and attachment/notification/privacy decisions are resolved before their schemas begin.

### NEXT exit criteria

- JRN-06 through JRN-08 pass, plus extended JRN-03/JRN-05 behavior.
- View/project/cycle counts and content never reveal hidden teams/issues.
- Attachment threat tests cover quota, spoofed type/size, active content, unauthorized download, deletion, and scanning failures.
- Notification/realtime revocation and reconnect/gap behavior pass multi-session tests.
- Cycle close/rollover and bulk edits are idempotent and recover cleanly from worker/process failure.
- MVP performance and operating footprint remain within agreed regression budgets.

## Release 3 — LATER: integrations and developer surface

**Promise:** approved external systems can exchange bounded issue data through observable, revocable, versioned contracts.

### Scope

- GitHub integration: identity/installation, team grants, PR/issue linking first; sync only by separate acceptance;
- Slack integration: narrowly selected intake/notification workflows, not default full-thread mirroring;
- issue templates;
- stable public API and generated/supported SDK;
- personal/service tokens;
- outbound webhooks and delivery logs;
- authorized data/CSV export.

### Recommended sequence

1. Public resource/scope/versioning contract and service-token model.
2. Outbound webhooks as the generic extension primitive.
3. GitHub link/reference integration.
4. Slack bounded notification/intake.
5. SDK generated from conformance-tested API.
6. Templates and export may ship independently when demand and security contracts are ready.

### LATER entry gate

- NEXT data/event models are stable enough to support a compatibility promise.
- Integration admin research validates desired workflows and acceptable scopes.
- Secret storage/rotation, OAuth, webhook egress, rate limits, delivery observability, and incident procedures are implemented and reviewed.
- Public API ownership and deprecation/support policy are staffed.

### LATER exit criteria

- JRN-10 and SCR-15 through SCR-17 acceptance pass.
- Provider and service principals cannot exceed selected scopes/team grants.
- OAuth/webhook tests cover CSRF/state, replay, signature, DNS rebinding/SSRF, redirect, payload, rate, retry, idempotency, and token redaction.
- Disabling/uninstalling immediately stops effective access and leaves an audit trail.
- Provider outage/rate limit cannot block core issue mutations or exhaust workers.
- Public API/SDK conformance, versioning, pagination, rate-limit, and deprecation documentation are published.

## Optional advanced work

Optional does not mean pre-approved.

| Capability | Current decision | Re-entry evidence required |
| --- | --- | --- |
| Declarative automation | DEFER | Repeated webhook/manual workflow, fixed safe vocabulary, loop/dry-run/audit design |
| Scheduled automation | DEFER | Approved declarative engine plus durable scheduling use case |
| Email-to-triage or email notifications | DEFER | Sender abuse/threading/attachment or batching/unsubscribe/bounce design |
| Estimates and project milestones | DEFER | Planning adoption and observed decision problem |
| Sheet/table layout | DEFER | Proven bulk comparison/edit limitation in list |
| Sentry/provider-specific links | DEFER | Generic link/connector model and demand |
| Reactions | DEFER | Collaboration research showing value over comments |
| AI/semantic search | DROP under v1 | New product strategy, privacy/evaluation model, operator/provider boundary, and explicit reversal |
| Remote executable Actions | DROP | Not eligible for in-process re-entry; only external API/webhook clients are acceptable |
| Support/CRM | DROP | Separate product decision; must not enter this domain model |

## Release traceability

| Release | Personas | Journeys | Screens | Capability families |
| --- | --- | --- | --- | --- |
| MVP | P-01–P-04 | JRN-01–05, JRN-09 | SCR-01–10 | CAP-ORG MVP, CAP-ISS MVP, CAP-VIEW MVP, CAP-PLAT MVP |
| NEXT | P-01, P-02, P-05 | JRN-06–08 plus extensions | SCR-11–14 | CAP-COL, CAP-PLAN, NEXT CAP-ISS/VIEW |
| LATER | P-06, P-07 plus admins | JRN-10 | SCR-15–17 | CAP-EXT approved rows |

## Roadmap governance

- A release label is not permission to build every row at once; implement vertical slices that meet their own entry/exit criteria.
- Feature flags are for controlled rollout/rollback, not indefinite half-shipped navigation.
- Each slice includes schema migration, policy tests, accessibility, metrics, operator impact, docs, and rollback.
- Scope pressure first removes lower-value capability; it never removes tenant isolation, audit, tests, accessibility, backup/restore, or safe upgrade work.
- No calendar estimate is credible until Stage 0 stack/hosting/open decisions and executable slice breakdown are accepted.
