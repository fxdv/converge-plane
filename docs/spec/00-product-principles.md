# 00 — Product principles and specification authority

**Status:** Authoritative greenfield product specification v1
**Effective date:** 2026-08-03
**Addendum (2026-08-30):** [11-direction-v2.md](11-direction-v2.md) records the pivot to the Converge revival path (Tegon-derived UI, AGPL, Go backend). Where the two conflict, 11 controls for UI provenance, license, name, stack, auth, and topology; all other v1 contracts remain binding.
**Research source:** Tegon repository at the current checkout
**Product target:** a lightweight, self-hostable, open-source issue tracker for software teams

## Authority and document map

This specification defines the product to build. Tegon is evidence of product behavior, not an implementation base or a completeness checklist. Where Tegon behavior conflicts with this specification, this specification wins. Where documents inside this directory conflict, the lower-numbered document defines intent and the more specific contract defines execution.

| Document | Authority |
| --- | --- |
| [00 — Product principles](00-product-principles.md) | Vision, scope vocabulary, non-goals, change control |
| [01 — Personas and jobs](01-personas-and-jobs.md) | Intended users and outcomes |
| [02 — Information architecture](02-information-architecture.md) | Product map, navigation, route ownership |
| [03 — Feature inventory](03-feature-inventory.md) | Screen and capability classification |
| [04 — User journeys](04-user-journeys.md) | End-to-end behavior and exceptional paths |
| [05 — Screen specifications](05-screen-specifications.md) | Per-screen states, actions, permissions, acceptance |
| [06 — Interaction and design system](06-interaction-and-design-system.md) | Interaction rules, visual tokens, responsive and accessible behavior |
| [07 — Domain and permissions](07-domain-and-permissions.md) | Entity invariants, tenant ownership, operation-level authorization |
| [08 — API and event contracts](08-api-and-event-contracts.md) | Service, realtime, job, and integration boundaries |
| [09 — Delivery roadmap](09-mvp-roadmap.md) | Release commitments and exit gates |
| [10 — Open decisions](10-open-decisions.md) | Unresolved choices, defaults, owners, decision gates |

[product-spec.tex](product-spec.tex) is the **integrated product specification**: the
post-pivot product (statement, personas, features, domain, API, swarm,
architecture, roadmap, moat) as one typeset document, compiled to
`product-spec.pdf`. It integrates 00–12 in post-pivot terms; the numbered
documents remain the detailed contracts. It is traceable to the codebase:
every feature carries a `cs:domain:token` tag, code references the same
token with a `spec cs:domain:token` comment, and the specsync checker
(`server/tools/specsync`, run in the Go test gate) fails on drift in either
direction. Where product-spec.tex and a numbered document conflict, the
numbered document defines its detailed contract and product-spec.tex
defines product intent; a conflict is a bug to fix in one of them.

The earlier `greenfield-spec/` census remains historical research. It is superseded wherever its classifications or delivery bands differ from this directory.

## Product statement

For small and medium software teams that need a fast, understandable way to coordinate work, the product is a focused issue tracker that combines excellent list and Kanban workflows with team-scoped organization, keyboard efficiency, and credible self-hosting. Unlike broad project-management suites, support CRMs, or automation platforms, it makes the common issue loop excellent before adding planning and integrations.

## Outcomes

The product succeeds when a new team can:

1. deploy or join a workspace without specialist product training;
2. capture, prioritize, assign, discuss, find, and complete work quickly;
3. understand who may see and change each object;
4. operate the system with PostgreSQL and a small, documented service footprint;
5. add collaboration and integrations later without weakening tenancy or core usability.

## Scope vocabulary

| Decision | Binding meaning |
| --- | --- |
| **ADOPT** | Preserve the capability’s essential user outcome. Code, assets, copy, and design expression remain original. |
| **ADAPT** | Preserve the value while simplifying or redesigning behavior, UX, data, or architecture. |
| **DEFER** | Not committed. Requires evidence, an RFC, and a specification revision before implementation. |
| **DROP** | Deliberate non-goal. No schema, service, setting, placeholder, or extension point may be added for it without reversing the decision. |

| Release | Commitment |
| --- | --- |
| **MVP — Minimal core** | Workspaces, teams, issues, workflows, labels, comments, list/Kanban, filters, search, and keyboard navigation, plus the identity and operational foundation they require. |
| **NEXT — Collaboration** | Custom views, projects, cycles, Inbox, notifications, issue relations, attachments, and authorized realtime collaboration. |
| **LATER — Integrations** | GitHub and Slack, templates, public API/SDK, outbound webhooks, and carefully bounded extension surfaces. |
| **EXCLUDED — Optional RFC or never** | AI assistants, remote Actions, support/CRM, semantic-search infrastructure, and other rows marked DEFER or DROP. |

Only ADOPT or ADAPT rows assigned to the active release authorize implementation. Future rows must not receive speculative tables, services, navigation, or configuration during earlier releases.

## Product principles

1. **Issue tracking first.** Every release must improve the capture-to-completion loop or make it safer to operate.
2. **Progressive complexity.** A first issue requires only a title and team. Planning, relations, attachments, notifications, and integrations appear only when shipped and relevant.
3. **Fast by interaction, simple by architecture.** Dense views, direct editing, shortcuts, and optimistic feedback are desirable; database replication into the browser is not.
4. **Server-authoritative tenancy.** The authenticated principal and server-side object relationships determine access. A client-supplied `::WORKSPACE_ID::` is routing context, never authorization evidence.
5. **Original product identity.** Reuse generic workflow lessons, not Tegon/Linear source code, assets, exact layout, trade dress, or copy. Review AGPL obligations before reusing any implementation.
6. **Accessible efficiency.** Keyboard operation accelerates visible actions but never replaces pointer or assistive-technology access. WCAG 2.2 AA is a release criterion.
7. **Small operational footprint.** MVP requires the application and PostgreSQL. New stateful dependencies need measured justification and failure/restore procedures.
8. **Safe extension boundaries.** Integrations are scoped principals using versioned APIs and events. The trusted server never imports arbitrary remote modules or injects secrets into user code.
9. **Auditable mutation.** Security-relevant and user-visible changes produce actor, tenant, object, timestamp, and before/after audit data where appropriate.
10. **Predictable degradation.** Search, background work, realtime transport, email, or external providers may fail without corrupting the core issue transaction.
11. **Privacy by minimization.** Collect only fields required for the active release, expose integration scopes before consent, and document export and deletion behavior.
12. **No feature theater.** Navigation and settings contain only working capabilities with defined empty, loading, error, and permission states.

## Explicit non-goals

- customer support inbox, CRM people/companies, or support-team product mode;
- arbitrary remote TypeScript/JavaScript Actions or an executable marketplace;
- built-in conversational AI, prompt administration, AI issue writing, or LLM bug enrichment;
- vector database or semantic-search infrastructure without a future approved use case;
- first-party Discord or WhatsApp synchronization;
- full offline-first database replication, PostgreSQL WAL consumption, or a generic client model-delta protocol;
- hidden administrator impersonation;
- public API stability commitments during MVP;
- mobile-native applications during the initial roadmap.

## Product-level security invariants

- Every persisted domain object has one authoritative `::WORKSPACE_ID::`, directly or through an immutable parent relation.
- Team-restricted objects require both workspace access and team access unless an explicit workspace owner/admin policy grants administrative visibility.
- Object lookup and mutation occur through tenant-scoped queries; “load by ID, then hope” is prohibited.
- Realtime subscription authorization uses the same policy layer as HTTP reads and is rechecked after membership changes.
- Integration principals receive explicit resource/action scopes and cannot silently inherit the installer’s full access.
- Export and deletion are asynchronous, audited, authorization-checked jobs with documented retention and recovery semantics.
- Logs, events, errors, analytics, and URLs must not disclose secrets or content across workspaces.

## Product success measures

Targets are finalized in [10 — Open decisions](10-open-decisions.md), but every release must measure:

- onboarding completion and time to first issue;
- issue-create latency and error rate;
- list/Kanban/search responsiveness at representative workspace size;
- weekly teams completing the full create → assign → discuss → complete loop;
- permission-denial correctness and cross-tenant security test coverage;
- deployment success, backup completion, restore verification, and upgrade failure rate;
- keyboard and accessibility task completion for primary journeys.

## Change control

Any scope change updates the feature inventory, affected screen/journey contracts, permissions, technical boundary, roadmap, and open-decision record in the same change. The proposal must state user evidence, security impact, operating cost, migration/rollback behavior, and testable acceptance criteria.
