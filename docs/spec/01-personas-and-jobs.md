# 01 — Personas and jobs-to-be-done

## Persona hierarchy

The product is optimized for the first four personas. Later personas influence contracts but do not justify MVP surface area.

| ID | Persona | Primary need | Release relevance |
| --- | --- | --- | --- |
| P-01 | Contributor | Capture, understand, update, and discuss assigned work with minimal navigation | MVP |
| P-02 | Team lead / triage owner | Keep incoming work actionable, prioritized, assigned, and visible | MVP |
| P-03 | Workspace owner/admin | Establish teams and access, configure shared structure, and govern data safely | MVP |
| P-04 | Self-host operator | Install, configure, monitor, back up, restore, and upgrade reliably | MVP |
| P-05 | Planner / project lead | Coordinate multi-issue outcomes and time-boxed work without spreadsheet duplication | NEXT |
| P-06 | Integration admin | Connect external systems with understood scopes and observable failure handling | LATER |
| P-07 | API consumer | Automate bounded workflows through a documented, stable contract | LATER |

Customer-support agents, CRM administrators, arbitrary action developers, and AI prompt administrators are explicitly not target personas.

## P-01 — Contributor

**Context:** participates in one or more teams, often returns from a link or notification, and spends most time in an issue list, board, or detail panel.

| Job | Desired outcome | Failure to avoid |
| --- | --- | --- |
| When I notice work, help me capture it quickly | A correctly team-scoped issue exists with minimal required input | Losing context in a long form or creating it in the wrong team |
| When I start my day, show what needs my attention | Assigned, active work is visible and ordered | Maintaining personal duplicate lists |
| When work changes, let me update it in place | Status, priority, assignee, and labels change without page churn | Hidden saves, stale values, accidental bulk changes |
| When I need context, keep discussion and history together | Description, comments, and meaningful changes explain current state | Chat fragments becoming the only record |
| When I remember a term or identifier, find the issue | Authorized matching issues return quickly with clear ranking | Cross-team leakage or opaque semantic guesses |

Success means the contributor can complete common work with pointer, keyboard, or assistive technology and can recover from conflicts or network errors without losing authored text.

## P-02 — Team lead / triage owner

**Context:** manages team workflow quality, reviews new requests, adjusts priority/ownership, and needs a reliable overview rather than a heavyweight reporting suite.

| Job | Desired outcome | Failure to avoid |
| --- | --- | --- |
| When requests arrive, help me review and route them | Triage items can be clarified, assigned, prioritized, moved to a working state, or closed | A second incompatible support workflow |
| When the team’s workload changes, show bottlenecks | Grouped list/Kanban exposes state and ownership concentration | Building dashboards before core data is trustworthy |
| When our process evolves, let me update workflows safely | Status changes preserve issue meaning and history | Orphaning issues or changing category semantics silently |
| When a recurring slice matters, preserve it | NEXT shared views reproduce filters and display settings | Personal, unshareable filter state |
| When coordinating an outcome, group related work | NEXT projects/cycles show scope and progress | Turning every issue into project bureaucracy |

## P-03 — Workspace owner/admin

**Context:** responsible for tenant structure, membership, security, and lifecycle. The person may not be a system operator.

| Job | Desired outcome | Failure to avoid |
| --- | --- | --- |
| When the workspace starts, establish a usable default | First team, workflow, and initial member are created atomically | Half-created onboarding state |
| When people join or leave, control access confidently | Invites, roles, team memberships, suspension, and removal have clear effects | Authentication without object authorization |
| When shared structure changes, keep it coherent | Teams, labels, and workflows enforce invariants and show impact | Destructive edits with hidden consequences |
| When data must be inspected or removed, make it accountable | Audited export/deletion follows retention and permission rules | Silent bulk access or irreversible mistakes |
| When integrations arrive, understand their reach | LATER scopes, data fields, installer, health, and revocation are visible | Provider tokens becoming ambient server authority |

## P-04 — Self-host operator

**Context:** deploys for a team or organization, often with limited time and no desire to assemble many infrastructure services.

| Job | Desired outcome | Failure to avoid |
| --- | --- | --- |
| When I deploy, validate configuration before serving traffic | One application artifact and PostgreSQL reach healthy state predictably | Runtime discovery of missing secrets or unsafe defaults |
| When I upgrade, know schema and compatibility impact | Preflight, migration, rollback/restore instructions, and version notes exist | Irreversible migrations without backup guidance |
| When something breaks, see actionable evidence | Structured logs, health/readiness, metrics hooks, and job visibility isolate failure | Public admin endpoints or content-heavy logs |
| When protecting data, verify recovery | Backups are documented and restore is exercised | Backups that have never been restored |
| When external features are disabled, retain the core product | Search/realtime/email/provider failures degrade independently | Integration outage preventing issue work |

## P-05 — Planner / project lead

**NEXT context:** coordinates an outcome spanning teams and optionally a time-box.

Jobs are to define project intent, attach existing issues without duplicating them, inspect derived progress, and manage a cycle’s scope and rollover. The system must not require projects or cycles for ordinary issues.

## P-06 — Integration admin

**LATER context:** installs GitHub/Slack or configures outbound webhooks.

Jobs are to preview requested scopes and fields, restrict accessible teams, complete provider authorization, test the connection, inspect deliveries/retries, rotate credentials, and revoke access cleanly.

## P-07 — API consumer

**LATER context:** builds scripts or services against stable documented resources.

Jobs are to create a narrowly scoped token, discover pagination and rate limits, make idempotent mutations, handle versioned errors/events, and revoke access without affecting interactive sessions.

## Cross-persona jobs

| Job | Product response |
| --- | --- |
| Trust what I can see | Navigation, search, counts, events, and exports all use the same permission policy |
| Understand what changed | User-facing activity and security audit distinguish domain changes from administrative access |
| Recover from mistakes | Confirm destructive scope, preserve drafts, support archive/restore where defined, and explain conflicts |
| Work efficiently without memorization | Visible controls, contextual shortcuts, command discovery, and consistent object pickers |
| Know whether the system is working | Honest loading/error/empty states for users and health/metrics/runbooks for operators |

## Prioritization rule

When persona needs conflict, protect tenant security and core contributor correctness first, then team coordination, then administrative convenience, then planning, and finally integrations. Operator safety is never traded away as “non-user-facing.”
