# 10 — Open decisions and decision log

Open decisions do not authorize broader scope. Each row has a recommended default that governs prototypes and discussion, but a blocking row must be explicitly accepted before its gate.

| ID | Decision | Recommended default | Gate | Status |
| --- | --- | --- | --- | --- |
| OD-01 | Open-source license and contribution model | Choose AGPL-3.0-or-later for network copyleft, or Apache-2.0 if ecosystem adoption outweighs hosted-service reciprocity; obtain legal review | Before public repository/code contribution | OPEN — blocking |
| OD-02 | Product name/trademark and visual identity | New name, original iconography/copy/layout; document Tegon only as research provenance | Before public UI/repository | OPEN — blocking |
| OD-03 | Implementation stack | Modular monolith and one deployable artifact; PostgreSQL; typed web client; choose languages/frameworks from team expertise and maintenance quality | Before schema/application code | OPEN — blocking |
| OD-04 | Authentication boundary | Maintained OIDC/email-magic-link capable component with secure server sessions; no custom password crypto | Before auth implementation | OPEN — blocking |
| OD-05 | Rich-text document format/editor | Small versioned JSON schema with sanitized renderer and plain-text projection; evaluate maintained editors without importing their whole product model | Before issue schema freeze | OPEN — blocking |
| OD-06 | Workspace admin access to all team content | Baseline: Owner/Admin has disclosed implicit access to all teams; evaluate private-team demand before changing the policy graph | Before permission fixtures/schema | OPEN — blocking |
| OD-07 | Team identifier changes and issue move keys | Team identifier immutable after first issue; move allocates target-team key and records old key alias/activity | Before issue numbering code | OPEN — blocking |
| OD-08 | Archive, deletion, and retention periods | Recoverable archive; 30-day deletion window as starting proposal; backup disclosure; legal/operator configuration bounded by safe minimums | Before destructive UI and production beta | OPEN — blocking for beta |
| OD-09 | MVP deployment topology | One application artifact with web/API and optional worker process mode plus PostgreSQL; reverse proxy/TLS outside app; no Redis | Before deployment skeleton | OPEN — blocking |
| OD-10 | PostgreSQL search strategy | Built-in full-text/trigram indexes with exact key boost; no Elasticsearch/vector DB | Before search implementation | OPEN — default strong |
| OD-11 | MVP job execution | PostgreSQL transactional outbox and leased worker, runnable in same artifact; no Trigger.dev/external scheduler | Before outbox implementation | OPEN — default strong |
| OD-12 | NEXT attachment storage/scanning | S3-compatible interface plus local-development adapter; short-lived URLs; pluggable scanning hook and quarantine policy | Before attachment schema/API | OPEN — NEXT blocking |
| OD-13 | NEXT project/cycle rules | Projects cross teams without granting access; cycles single-team/non-overlapping; explicit incomplete-item rollover | Before planning schema | OPEN — NEXT blocking |
| OD-14 | NEXT notification triggers/grouping | Assignment, mention, comment, relation, watched issue; deterministic burst grouping; in-app only; user mute controls | Before notification projector | OPEN — NEXT blocking |
| OD-15 | Performance budgets and representative scale | Define p95 API/UI budgets using 10k issues/workspace, 1k/team active collection, 100 concurrent sessions as initial fixture—not claimed capacity | Before MVP UI acceptance | OPEN — blocking for beta |
| OD-16 | Realtime transport | SSE in NEXT; refetch/gap recovery authoritative; WebSocket only for measured bidirectional need | Before NEXT realtime implementation | OPEN — NEXT default strong |
| OD-17 | Public API versioning/support | Resource-oriented `/v1`, cursor pagination, idempotency, scoped credentials, published deprecation window and ownership | Before LATER public API | OPEN — LATER blocking |
| OD-18 | GitHub/Slack exact workflows | GitHub linking before sync; Slack bounded notification/intake; validate with users and provider policy | Before provider installation work | OPEN — LATER blocking |
| OD-19 | Telemetry and crash reporting | Off by default for self-host; explicit operator opt-in; publish fields/retention; no issue content | Before any analytics dependency | OPEN — blocking if telemetry proposed |
| OD-20 | Localization and timezone baseline | English-first externalized copy; UTC instants, account display timezone, date-only due/cycle dates | Before date/presentation schema | OPEN — default strong |
| OD-21 | Browser support | Current and previous major Chrome/Edge/Firefox/Safari; responsive web, no native app/offline mutation | Before test matrix freeze | OPEN — default strong |
| OD-22 | Accessibility conformance process | WCAG 2.2 AA, automated CI plus manual keyboard/screen-reader audits per release | Before design-system acceptance | OPEN — default strong |

## Decisions already fixed by this specification

These are not open implementation choices:

- Tegon architecture is rejected as a greenfield base.
- The product is workspace-tenant and team-restricted.
- Authentication alone is insufficient; object authorization uses the explicit matrix.
- MVP contains no custom views, projects, cycles, Inbox, attachments, integrations, API, or generic automation.
- NEXT contains no provider integrations or public extension compatibility promise.
- Arbitrary remote Actions, in-process third-party code, token injection, full client database replication, WAL dependence, support/CRM, AI assistants, and semantic/vector search are excluded.
- Public/shared URLs, projects, assignments, notifications, and integrations never grant permission implicitly.
- Security, accessibility, audit, backup/restore, upgrade safety, and tests are delivery requirements, not optional hardening.

## Decision-record template

When closing an open decision, append a record using:

```text
Decision ID:
Date:
Owner/approvers:
Chosen option:
User/product rationale:
Security and privacy impact:
Operational and dependency impact:
Migration/rollback impact:
Alternatives rejected:
Documents/contracts updated:
```

## Immediate pre-code decision sequence

1. OD-01 and OD-02 establish legal and product identity boundaries.
2. OD-03, OD-09, and OD-11 establish deployable architecture without importing Tegon complexity.
3. OD-04 establishes authentication/session ownership.
4. OD-06 and OD-07 freeze permission and issue identity semantics.
5. OD-05 and OD-20 freeze content/date representation.
6. Convert the accepted decisions into an architecture decision record set and an executable MVP vertical-slice backlog.

Until those blocking decisions close, repository work should remain specification, prototype, or throwaway-spike work—not production greenfield implementation.
