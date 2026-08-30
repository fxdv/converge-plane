# 04 — User journeys

## Journey contract

Each journey specifies actor, preconditions, happy path, recoverable branches, permission/audit effects, and acceptance. Screen IDs refer to [03 — Feature inventory](03-feature-inventory.md); operation rules are defined in [07 — Domain and permissions](07-domain-and-permissions.md).

## JRN-01 — Sign in and create the first workspace

**Release:** MVP
**Actor:** P-03 Workspace owner, also acting as P-01 Contributor
**Screens:** SCR-AUTH-SIGNIN → SCR-AUTH-VERIFY → SCR-ONBOARD → SCR-TEAM-ISSUES

### Preconditions

- The deployment is healthy and has a configured authentication method.
- The email/account is not suspended.
- The account has no accessible workspace, or explicitly chooses “Create workspace.”

### Happy path

1. User enters an email or chooses the configured OIDC provider.
2. System rate-limits the attempt, creates a one-time challenge, and returns the same non-enumerating response for known and unknown email addresses.
3. User verifies the challenge. The service rotates the session identifier and establishes an HttpOnly, Secure, SameSite session.
4. Onboarding asks for display name, workspace name, first team name, and short team identifier.
5. A preview shows the resulting workspace URL and sample issue key.
6. One transaction creates the workspace, Owner membership, team, team-manager membership, default workflow statuses, and initial label set.
7. User lands on the team’s empty issue list with a primary “Create first issue” action and keyboard hint.

### Branches and recovery

- Expired/used challenge: verification explains expiry without disclosing account existence and offers a new challenge.
- Workspace slug collision: suggest available slugs; preserve other fields.
- Team identifier collision/invalid format: validate before submission and preserve the draft.
- Transaction failure: create no partial tenant resources; show retry with correlation ID.
- Existing account with workspaces: land in the most recently used accessible workspace, never onboarding by accident.

### Permissions and audit

- Only the authenticated account can complete its onboarding challenge.
- Audit workspace creation, initial Owner assignment, team creation, and default workflow creation.
- Do not audit authentication secrets or magic-link contents.

### Acceptance

- Refresh/back cannot repeat resource creation.
- A failed final request leaves zero partial workspace/team rows.
- The new Owner can create an issue; another account cannot discover the workspace from its slug alone.
- Keyboard and screen-reader users can complete every field and understand validation.

## JRN-02 — Accept an invitation

**Release:** MVP
**Actor:** P-01 Contributor
**Screens:** SCR-INVITE → optional SCR-AUTH-SIGNIN/VERIFY → SCR-TEAM-ISSUES

1. Invitee opens an opaque, expiring, single-use token.
2. Screen shows inviting workspace, inviter, proposed role, and team access without exposing workspace data.
3. If unauthenticated, the invite context survives authentication.
4. Service verifies token, normalized account email where applicable, workspace status, inviter authority at acceptance time, and seat/policy constraints.
5. One transaction creates/reactivates membership, assigns teams, consumes the invite, and audits acceptance.
6. User lands in the first accessible team or My issues.

**Branches:** revoked/expired/consumed invite; signed in as a different email; suspended existing membership; no longer authorized inviter. Each explains a safe resolution without revealing member lists or issue content.

**Acceptance:** replay cannot create or elevate membership; team access exactly matches the current invite; workspace state is not loaded before acceptance succeeds.

## JRN-03 — Create and complete an issue

**Release:** MVP
**Actors:** P-01 Contributor, P-02 Team lead
**Screens:** SCR-TEAM-ISSUES or SCR-MY-ISSUES → SCR-ISSUE-CREATE → SCR-ISSUE

### Happy path

1. User presses `C` or chooses Create issue.
2. Dialog inherits team from context; outside a team it requires explicit team confirmation.
3. User enters title. Description, status, priority, assignee, and labels are optional and progressively disclosed.
4. Client validates known constraints; server derives workspace, checks team create access and referenced-object access, assigns the next team issue number, persists issue and activity atomically, then returns canonical key/URL.
5. UI confirms creation and opens or preserves context according to user preference.
6. Authorized members edit status/priority/assignee/labels inline. Each change shows pending → saved or reverts with an actionable error.
7. Members add comments; authors may edit/delete their own comments. Activity distinguishes content comments from state changes.
8. Assignee moves the issue to a Completed-category status. The collection updates on the next authoritative refetch; NEXT realtime makes that prompt across clients.
9. An authorized member archives the issue through a confirmation describing visibility and recovery.

### Branches and recovery

- Network failure during create: keep draft locally for the current account/workspace and retry with an idempotency key.
- Duplicate submit: same idempotency key returns the first issue.
- Assignee/label removed concurrently: server rejects invalid reference; preserve form and request a replacement.
- Edit conflict: non-overlapping property updates may succeed; description/title conflict returns current version and offers reload/copy, never silent last-write data loss.
- Permission removed while screen is open: mutation fails; refetch hides inaccessible content and clears cached tenant data.
- Issue archived/moved concurrently: explain new location/state if still authorized; otherwise show non-leaking not-found.

### Permissions and audit

- Issue inherits `::WORKSPACE_ID::` from team; the client does not choose tenant ownership independently.
- Create/update/comment/read require team access. Assignment requires the target member also has team access.
- Audit create, field changes, archive/restore, and comment moderation. Keep ordinary comment body out of administrative audit payloads.

### Acceptance

- Required MVP issue operations work in list, Kanban, detail panel, and full page without inconsistent policy.
- Issue number allocation is concurrency-safe.
- Failed edits never display a saved state or discard authored text.
- Archived issues disappear from default collections and remain recoverable according to retention policy.

## JRN-04 — Triage incoming work

**Release:** MVP
**Actor:** P-02 Team lead / triage owner
**Screens:** SCR-TEAM-TRIAGE → SCR-ISSUE → SCR-TEAM-ISSUES

1. User opens Triage, a predefined filter for the team’s Triage workflow category.
2. Empty state explains how manually created issues enter triage; it does not advertise unshipped integrations.
3. User opens an issue in the side panel, clarifies title/description, sets priority and assignee, adds labels, and comments if needed.
4. User chooses a non-Triage status to accept work, a Canceled-category status to decline it, or leaves it in triage.
5. Accepted/declined issue leaves the current filtered collection with undo-friendly feedback; full activity records the decision.

**Branches:** team has no Triage-category status (team manager is prompted to repair workflow); user may comment but lacks triage mutation permission; issue changed by another user. The screen must never invent a support-specific state machine.

**Acceptance:** Triage count and contents use the same query/policy as ordinary issues; status change is enough to enter/leave; browser back restores collection position and filters.

## JRN-05 — Filter, navigate, and search

**Release:** MVP
**Actors:** P-01, P-02
**Screens:** SCR-MY-ISSUES / SCR-TEAM-ISSUES / SCR-SEARCH / SCR-ISSUE

1. User opens filters with `F` or visible control.
2. User chooses status, assignee, priority, label, or team clauses. Values within a field are OR; different fields are AND unless the explicit advanced grouping UI says otherwise.
3. Collection updates URL-safe query state, count, and empty-state explanation.
4. User changes grouping/order or list/Kanban; preference persists for the current context.
5. User presses `/`, enters text or an issue key, and receives permission-filtered results with team/key/status context.
6. Arrow keys or pointer choose a result; detail opens canonically and Escape returns focus to the prior control.

**Branches:** zero results distinguishes no match from inaccessible content without revealing which; malformed/stale URL filters are ignored individually with a repair message; search timeout retains query and offers retry; unavailable search enhancement falls back to identifier/title matching.

**Acceptance:** no search/filter count includes inaccessible issues; filter state is shareable within its permission context; 10,000-issue representative data meets the performance budget set in OD-15.

## JRN-06 — Create and use a custom view

**Release:** NEXT
**Actor:** P-02 Team lead
**Screens:** SCR-TEAM-ISSUES → SCR-VIEWS → SCR-VIEW

1. User applies filters, layout, grouping, ordering, and completed visibility.
2. “Save as view” requests name, description, workspace/team scope, and visibility.
3. Server validates every referenced team/label/status and creator’s access; it stores a versioned query definition, not raw executable query text.
4. Authorized members open the stable view URL; results are re-authorized at query time.
5. Creator or authorized manager edits metadata/query; members may bookmark it personally.

**Branches:** creator loses access to one team; referenced label/status is archived; another user edits concurrently; view produces zero results. The view becomes partially invalid with repair guidance, never a policy bypass.

**Acceptance:** saving/reopening reproduces query and presentation; sharing a URL does not share authority; deleted/hidden teams cannot leak through counts or filter options.

## JRN-07 — Plan with projects and cycles

**Release:** NEXT
**Actors:** P-05 Planner, P-02 Team lead
**Screens:** SCR-PROJECTS / SCR-PROJECT / SCR-CYCLES / SCR-CYCLE / SCR-ISSUE

### Project path

1. Authorized workspace member creates a project with name, summary, lead, participating teams, status, and optional dates.
2. Planner adds existing authorized issues or creates an issue in a participating team.
3. Project displays issue collection and progress derived from workflow semantic categories.
4. Removing an issue unlinks it; it does not delete the issue.

### Cycle path

1. Team manager creates a non-overlapping date range and name/sequence.
2. Authorized members add team issues. An issue belongs to at most one cycle at a time.
3. Current cycle view shows scope and derived completion.
4. On close, incomplete items require an explicit choice: move to a selected future cycle or return to no cycle. Every move is audited.

**Branches:** cross-team issue not in project team set; inaccessible issue; overlapping cycle; archived status/workflow; planner removed mid-edit; issue moved concurrently.

**Acceptance:** project/cycle deletion unlinks issues and preserves issue history; progress is defined and reproducible; no separate planning-history table duplicates the common audit stream.

## JRN-08 — Review notifications and issue context

**Release:** NEXT
**Actor:** P-01 Contributor
**Screens:** SCR-INBOX → SCR-ISSUE

1. Authorized events create deduplicated notification records for assignment, mention, comment, relation, or explicitly watched changes.
2. User sees unread count scoped to current account/workspace.
3. Opening an item rechecks issue access before returning preview content, marks read after successful display, and retains a link to canonical issue.
4. User can mark read/unread, mute issue, or clear the item without changing domain history.

**Branches:** issue archived/deleted, access revoked, actor removed, notification target malformed, realtime disconnected. The item becomes unavailable/removed without revealing stale content.

**Acceptance:** unread count converges after reconnect; revocation prevents body/title preview; duplicate high-frequency updates group predictably.

## JRN-09 — Manage settings and membership

**Release:** MVP with NEXT/LATER additions
**Actor:** P-03 Workspace owner/admin, team manager, P-01 self-service user
**Screens:** SCR-ACCOUNT / SCR-WORKSPACE-SETTINGS / SCR-TEAM-SETTINGS

1. Any user updates own profile, appearance, and sessions.
2. Owner/admin invites a member with workspace role and team set.
3. Owner/admin changes role/team access after a confirmation summarizing effective access.
4. Suspending a member revokes sessions, realtime connections, tokens (LATER policy), and future access; authored history remains attributed.
5. Team manager configures team name, workflow, and team members; workspace Owner/Admin manages shared workspace labels.
6. Destructive changes show dependent object counts and replacement/migration choices.

**Acceptance:** the last Owner cannot demote/remove self; a manager cannot grant workspace admin; workflow/label deletion cannot orphan required issue fields; all security-relevant changes are audited.

## JRN-10 — Install and operate an integration

**Release:** LATER
**Actor:** P-06 Integration admin
**Screens:** SCR-INTEGRATIONS → SCR-INTEGRATION; provider authorization; optional SCR-DEVELOPER

1. Admin chooses GitHub or Slack and sees exact requested permissions, fields, event types, accessible teams, data destination, and revocation effect.
2. Admin selects allowed teams before provider authorization.
3. Server creates short-lived signed OAuth state bound to workspace, installer, provider, redirect target, and PKCE where supported.
4. Callback validates state, stores encrypted credentials, creates an integration principal with narrow scopes, and performs a safe connection test.
5. Integration detail shows health, scopes, installer, allowed teams, last success/error, deliveries, retries, credential rotation, and uninstall.
6. Provider events verify signature/replay window, normalize to idempotent internal commands, and operate only through the same domain policy layer.

**Branches:** installer loses admin role; callback replay; provider denies scopes; team removed; rate limit; credential revoked; webhook payload references inaccessible object. Core issue operations remain available.

**Acceptance:** uninstall revokes provider access when supported and deletes/encrypts credentials per retention; logs never expose tokens/payload secrets; integration cannot access teams outside its grant; retries are bounded and idempotent.
