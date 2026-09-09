# 03 — Screen and capability inventory

This inventory is the authoritative classification. Repository evidence comes from the Tegon route tree, modules, stores, Prisma schema, actions, and documentation summarized in the superseded research census. Classification reflects the intended greenfield product, not Tegon maturity.

## Screen inventory

| Screen ID | Screen | Decision | Release | Contract location |
| --- | --- | --- | --- | --- |
| SCR-AUTH-SIGNIN | Sign in | ADAPT | MVP | SCR-01 |
| SCR-AUTH-VERIFY | Email/provider verification and callback | ADAPT | MVP | SCR-01 |
| SCR-INVITE | Invitation review/acceptance | ADOPT | MVP | SCR-02 |
| SCR-ONBOARD | First workspace/team setup | ADAPT | MVP | SCR-03 |
| SCR-MY-ISSUES | Personal assigned-issue collection | ADOPT | MVP | SCR-04 |
| SCR-TEAM-ISSUES | Team issue collection, list/Kanban | ADOPT | MVP | SCR-04 |
| SCR-TEAM-TRIAGE | Team triage collection | ADAPT | MVP | SCR-04 |
| SCR-ISSUE-CREATE | Contextual create-issue dialog/sheet | ADAPT | MVP | SCR-05 |
| SCR-ISSUE | Issue full page and detail panel | ADOPT | MVP | SCR-06 |
| SCR-SEARCH | Search overlay/results | ADAPT | MVP | SCR-07 |
| SCR-ACCOUNT | Profile, security/sessions, appearance | ADAPT | MVP | SCR-08 |
| SCR-WORKSPACE-SETTINGS | General, members, teams, labels | ADAPT | MVP | SCR-09 |
| SCR-TEAM-SETTINGS | General, members, workflow | ADAPT | MVP | SCR-10 |
| SCR-INBOX | Notification list and issue context | ADAPT | NEXT | SCR-11 |
| SCR-VIEWS | Shared/custom view directory and editor | ADAPT | NEXT | SCR-12 |
| SCR-VIEW | Saved view issue collection | ADOPT | NEXT | SCR-12 |
| SCR-PROJECTS | Project directory/create | ADAPT | NEXT | SCR-13 |
| SCR-PROJECT | Project overview and issue collection | ADAPT | NEXT | SCR-13 |
| SCR-CYCLES | Team cycle directory/create | ADAPT | NEXT | SCR-14 |
| SCR-CYCLE | Cycle overview and issue collection | ADAPT | NEXT | SCR-14 |
| SCR-INTEGRATIONS | Integration catalog and health | ADAPT | LATER | SCR-15 |
| SCR-INTEGRATION | Provider install/configuration/delivery detail | ADAPT | LATER | SCR-15 |
| SCR-TEMPLATES | Issue template management | ADAPT | LATER | SCR-16 |
| SCR-DEVELOPER | API tokens and outbound webhooks | ADAPT | LATER | SCR-17 |
| SCR-ACTIONS | Executable Action catalog/configuration | DROP | EXCLUDED | No screen may be created |
| SCR-AI | Conversational assistant/prompt management | DROP | EXCLUDED | No screen may be created |
| SCR-SUPPORT | Support inbox/people/company views | DROP | EXCLUDED | No screen may be created |
| SCR-IMPERSONATE | Hidden admin impersonation | DROP | EXCLUDED | No screen may be created |

`SCR-xx` contract references point into [05 — Screen specifications](05-screen-specifications.md).

## Organization, identity, and administration capabilities

| ID | Capability | Decision | Release | Greenfield boundary |
| --- | --- | --- | --- | --- |
| CAP-ORG-01 | Account sign-in/session/logout | ADAPT | MVP | Maintained auth boundary, secure cookies, session list/revoke |
| CAP-ORG-02 | Workspace create/switch/update | ADAPT | MVP | Tenant switch clears state; slug and ownership invariants |
| CAP-ORG-03 | First-run onboarding | ADAPT | MVP | Atomic account/workspace/team/default-workflow setup |
| CAP-ORG-04 | Workspace invitations | ADOPT | MVP | Expiring, revocable, role/team-scoped invites |
| CAP-ORG-05 | Workspace roles | ADAPT | MVP | Owner, Admin, Member with centralized policy |
| CAP-ORG-06 | Team create/update/archive | ADOPT | MVP | Workspace-owned visibility boundary |
| CAP-ORG-07 | Team membership/manager role | ADAPT | MVP | Explicit membership; owner/admin implicit access |
| CAP-ORG-08 | Member suspend/remove | ADAPT | MVP | Immediate session/realtime revocation and audit |
| CAP-ORG-09 | Profile/avatar/appearance | ADAPT | MVP | Light/dark/system; safe avatar handling |
| CAP-ORG-10 | Hidden impersonation | DROP | EXCLUDED | No equivalent |
| CAP-ORG-11 | Anonymous telemetry opt-in | DEFER | EXCLUDED | Requires privacy and operator RFC |

## Issue and workflow capabilities

| ID | Capability | Decision | Release | Greenfield boundary |
| --- | --- | --- | --- | --- |
| CAP-ISS-01 | Team-sequential issue key | ADOPT | MVP | Immutable key, unique inside deployment/workspace rules |
| CAP-ISS-02 | Create/read/update/archive issue | ADOPT | MVP | Title required; server-authorized team scope |
| CAP-ISS-03 | Rich description | ADAPT | MVP | Small sanitized versioned document schema |
| CAP-ISS-04 | Workflow categories/statuses | ADOPT | MVP | Triage, backlog, unstarted, started, completed, canceled |
| CAP-ISS-05 | Priority | ADOPT | MVP | One documented scale, directly editable |
| CAP-ISS-06 | Assignee | ADOPT | MVP | Zero/one workspace member with team access |
| CAP-ISS-07 | Workspace labels | ADAPT | MVP | Shared labels; avoid duplicate team/workspace identity |
| CAP-ISS-08 | Comments | ADOPT | MVP | Create/edit/delete own; moderation by authorized managers |
| CAP-ISS-09 | Replies | ADAPT | MVP | One visible reply level; preserve thread identity |
| CAP-ISS-10 | Reactions | DEFER | EXCLUDED | Reconsider as collaboration polish |
| CAP-ISS-11 | Issue activity | ADOPT | MVP | Transactional actor/change history for meaningful fields |
| CAP-ISS-12 | Archive/restore/retention | ADAPT | MVP | Recoverable policy; hard-delete only through lifecycle job |
| CAP-ISS-13 | Due date | ADAPT | NEXT | Optional; timezone/date-only semantics specified before build |
| CAP-ISS-14 | Estimate/manual ordering | DEFER | EXCLUDED | Requires planning evidence |
| CAP-ISS-15 | Parent/sub-issues | ADAPT | NEXT | One parent, ordered children, no cycles |
| CAP-ISS-16 | Blocks/related/duplicate relations | ADAPT | SHIPPED (v1.1) | Inverses derived server-side; duplicate behavior explicit; PARENT/SUB_ISSUE ride on `parent_id`; UI = timeline entry + board indicator + related section with removal |
| CAP-ISS-17 | Attachments | ADAPT | NEXT | Authorized object storage, quotas, validation, scanning hook |
| CAP-ISS-18 | Watchers/subscribers | ADAPT | NEXT | Notification participation plus mute controls |
| CAP-ISS-19 | Bulk edit | ADAPT | NEXT | Preview scope, partial-failure contract, per-object authorization |
| CAP-ISS-20 | External linked issues/comments | DEFER | EXCLUDED | Wait for connector model |
| CAP-ISS-21 | Similar/suggested issues | DROP | EXCLUDED | No semantic/AI infrastructure |

## Retrieval and interaction capabilities

| ID | Capability | Decision | Release | Greenfield boundary |
| --- | --- | --- | --- | --- |
| CAP-VIEW-01 | Team issue list | ADOPT | MVP | Virtualized grouped list, direct property editing |
| CAP-VIEW-02 | Team Kanban | ADOPT | MVP | Status columns, pointer/keyboard movement |
| CAP-VIEW-03 | My issues | ADOPT | MVP | Predefined assignee filter across authorized teams |
| CAP-VIEW-04 | Triage | ADAPT | MVP | Predefined Triage-category collection |
| CAP-VIEW-05 | Structured filters | ADAPT | MVP | Status, assignee, priority, label, team; explicit AND/OR |
| CAP-VIEW-06 | Group/order/display options | ADAPT | MVP | Persist per user/context; no hidden AI parsing |
| CAP-VIEW-07 | Text/identifier search | ADAPT | MVP | PostgreSQL-backed, permission-scoped, deterministic fallback |
| CAP-VIEW-08 | Keyboard shortcuts | ADAPT | MVP | Discoverable and accessible; avoid form conflicts |
| CAP-VIEW-09 | Command palette | ADAPT | MVP | Navigation and permitted actions only |
| CAP-VIEW-10 | Issue side panel/full page | ADOPT | MVP | Canonical URL, focus/history preservation |
| CAP-VIEW-11 | Inline editing | ADOPT | MVP | Explicit pending/saved/error/conflict feedback |
| CAP-VIEW-12 | Custom/shared views | ADAPT | NEXT | Filter + layout + grouping + ordering + visibility |
| CAP-VIEW-13 | Personal bookmarks/favorites | ADAPT | NEXT | Member-owned presentation state |
| CAP-VIEW-14 | Sheet/table layout | DEFER | EXCLUDED | Needs proven bulk-edit problem |
| CAP-VIEW-15 | Natural-language filters | DROP | EXCLUDED | Structured UI is the supported contract |

## Collaboration and planning capabilities

| ID | Capability | Decision | Release | Greenfield boundary |
| --- | --- | --- | --- | --- |
| CAP-COL-01 | In-app Inbox | ADAPT | NEXT | Authorized at read time, grouped events, unread/mute controls |
| CAP-COL-02 | Issue notifications | ADAPT | NEXT | Assignment, mention, comment, relation, watched changes |
| CAP-COL-03 | Email notification delivery | DEFER | EXCLUDED | Requires preferences, batching, unsubscribe, bounce handling |
| CAP-COL-04 | Authorized realtime updates | ADAPT | NEXT | Server-scoped events; reconnect/refetch correctness |
| CAP-PLAN-01 | Projects | ADAPT | SHIPPED (v1.1) | Workspace-scoped meaning label; one project per issue; board rail (create/rename/delete, drag assign + clear, derived done/total rollup); the other board groupings take the rail in v2 |
| CAP-PLAN-02 | Derived project progress | ADAPT | NEXT | Computed from issue workflow categories |
| CAP-PLAN-03 | Project milestones | DEFER | EXCLUDED | Add only after project adoption evidence |
| CAP-PLAN-04 | Team cycles | ADAPT | NEXT | Time-boxed team issue collection with explicit rollover |
| CAP-PLAN-05 | Current/upcoming/past cycles | ADAPT | NEXT | No overlapping active cycles for a team |
| CAP-PLAN-06 | Planning-specific history tables | DROP | EXCLUDED | Use common audit/activity events |

## Integration, automation, and distribution capabilities

| ID | Capability | Decision | Release | Greenfield boundary |
| --- | --- | --- | --- | --- |
| CAP-EXT-01 | GitHub connection | ADAPT | LATER | Scoped app/OAuth principal; links first, sync separately |
| CAP-EXT-02 | Slack connection | ADAPT | LATER | Bounded notifications/intake; no default full-thread mirroring |
| CAP-EXT-03 | Issue templates | ADAPT | LATER | Issue-only, versioned defaults; no document/project templates |
| CAP-EXT-04 | Public API | ADAPT | LATER | Versioning, scopes, pagination, rate limits, idempotency |
| CAP-EXT-05 | SDK | ADAPT | LATER | Generated/supported from stable public API |
| CAP-EXT-06 | Personal/service tokens | ADAPT | LATER | Hashed, scoped, expiring, revocable, last-used visibility |
| CAP-EXT-07 | Outbound webhooks | ADAPT | LATER | Signed, replay-safe, SSRF-resistant, observable retries |
| CAP-EXT-08 | CSV/data export | ADAPT | LATER | Authorized async job, formula-injection defense, audit |
| CAP-EXT-09 | Declarative automation | DEFER | EXCLUDED | Future RFC after webhooks; fixed vocabulary only |
| CAP-EXT-10 | Scheduled automation | DEFER | EXCLUDED | Depends on declarative engine and demand |
| CAP-EXT-11 | Remote executable Actions | DROP | EXCLUDED | Prohibited in trusted server |
| CAP-EXT-12 | Discord first-party connector | DROP | EXCLUDED | Generic future connector may use public contract |
| CAP-EXT-13 | WhatsApp first-party connector | DROP | EXCLUDED | Support-domain feature |
| CAP-EXT-14 | Sentry-specific UI | DEFER | EXCLUDED | Generic external links first |

## AI, support, and platform capabilities

| ID | Capability | Decision | Release | Greenfield boundary |
| --- | --- | --- | --- | --- |
| CAP-AI-01 | AI writing/title/sub-issue help | DROP | EXCLUDED | No model/provider surface |
| CAP-AI-02 | AI summaries | DROP | EXCLUDED | No launch data disclosure path |
| CAP-AI-03 | Conversational assistant/history | DROP | EXCLUDED | Not part of focused issue tracker |
| CAP-AI-04 | Prompt/model administration | DROP | EXCLUDED | No equivalent |
| CAP-AI-05 | Semantic/vector search | DROP | EXCLUDED | PostgreSQL text search only until future evidence/RFC |
| CAP-SUP-01 | Engineering/support team modes | DROP | EXCLUDED | One issue experience |
| CAP-SUP-02 | Support conversations/chat | DROP | EXCLUDED | No support inbox |
| CAP-SUP-03 | People/companies CRM | DROP | EXCLUDED | External system links only |
| CAP-SUP-04 | Email-to-triage | DEFER | EXCLUDED | May return through integration RFC |
| CAP-PLAT-01 | First-class self-hosting | ADOPT | MVP | Application + PostgreSQL, validated config, documented lifecycle |
| CAP-PLAT-02 | Internal typed HTTP API | ADAPT | MVP | Central authorization/domain services; not public commitment |
| CAP-PLAT-03 | Essential background jobs | ADAPT | MVP | Transactional outbox/in-process worker before new infrastructure |
| CAP-PLAT-04 | Health/logs/metrics/audit | ADAPT | MVP | Launch requirement |
| CAP-PLAT-05 | Browser query cache | ADAPT | MVP | Bounded cache and optimistic updates |
| CAP-PLAT-06 | Full IndexedDB/database replica | DROP | EXCLUDED | No generic delta protocol or WAL dependency |

## Classification guardrails

- **MVP:** may be designed and implemented now.
- **NEXT/LATER:** contracts may be refined, but implementation artifacts are not created until the preceding release passes its exit gate.
- **DEFER:** no committed release and no anticipatory schema.
- **DROP:** no product surface or architectural accommodation.
- A capability omitted from this inventory is out of scope until added through the change-control process in [00 — Product principles](00-product-principles.md).
