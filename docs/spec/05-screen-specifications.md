# 05 — Screen contracts

## Universal screen-state contract

Every screen and significant panel implements these states explicitly.

| State | Required behavior |
| --- | --- |
| Initial/loading | Preserve shell and context; show structure-matching skeleton after a short delay; never flash another tenant’s cached content |
| Success | Render server-authorized data with last mutation/realtime state resolved |
| First-use empty | Explain the object and show one permitted primary action |
| Filtered empty | Show active query/filter reason, clear/reset action, and no misleading create prompt |
| Permission empty | Do not imply whether a secret object exists; provide workspace/team access guidance only when safe |
| Recoverable error | Preserve input/query/scroll, explain impact, provide retry, and attach a non-sensitive correlation ID |
| Partial/stale | Mark stale section, allow safe refetch, and prevent actions that require unknown current state |
| Conflict | Show current server value/version and preserve the user’s attempted text for copy/reapply |
| Offline/disconnected | Distinguish network from permission/server errors; queue no mutation unless its idempotency/retry contract is explicit |
| Destructive confirmation | Name object, effect, dependent count, retention/recovery, and required replacement; destructive button is visually and semantically distinct |

All screens provide a title, canonical URL where applicable, landmark structure, programmatic focus destination, document title, and accessible announcement for asynchronous success/error.

## SCR-01 — Sign in and verification

**Covers:** SCR-AUTH-SIGNIN, SCR-AUTH-VERIFY
**Release:** MVP
**Permission:** public challenge endpoints; authenticated users redirect safely

- **States:** email/provider selection, challenge sent, verifying, expired/invalid, rate limited, provider unavailable, authenticated redirect.
- **Actions:** request link/challenge, choose OIDC, resend after cooldown, cancel, sign out current session.
- **Empty/loading/error:** never reveal whether an email exists; provider failures name the provider and offer configured alternative; pending callback blocks duplicate submission.
- **Acceptance:** secure return URL allowlist; challenge single-use/expiry; session rotation; no secret in logs/referrer; full keyboard/autofill/password-manager compatibility.

## SCR-02 — Invitation

**Covers:** SCR-INVITE
**Release:** MVP
**Permission:** holder of valid opaque token; acceptance bound to authenticated account policy

- **States:** valid preview, authentication required, accepting, accepted, expired, revoked, consumed, wrong account, workspace unavailable.
- **Actions:** sign in, accept, decline, switch account, request a new invite through safe contact guidance.
- **Empty/loading/error:** preview exposes only workspace name, inviter display name, proposed role/teams; invalid token shows no tenant details.
- **Acceptance:** replay-safe; current inviter authority and membership state checked at acceptance; success creates exactly one membership and audit event.

## SCR-03 — Onboarding

**Covers:** SCR-ONBOARD
**Release:** MVP
**Permission:** authenticated account allowed to create a workspace

- **States:** editable form, validating slug/identifier, submitting transaction, recoverable conflict/error, completed redirect.
- **Actions:** set profile/workspace/team names and identifier, preview URL/key, submit, cancel to existing workspace if available.
- **Empty/loading/error:** inline validation does not erase values; server collision suggestions are accessible; submission retry uses one idempotency key.
- **Acceptance:** atomic resources/default workflow; no duplicate workspace on refresh; completed users cannot accidentally re-enter first-run onboarding.

## SCR-04 — Issue collections

**Covers:** SCR-MY-ISSUES, SCR-TEAM-ISSUES, SCR-TEAM-TRIAGE
**Release:** MVP
**Permission:** workspace member plus team access; owner/admin implicit team access is disclosed

- **States:** list/Kanban, grouped/collapsed, loading more, first-use empty, filtered empty, unavailable team, stale query, partial optimistic edit.
- **Actions:** create/open issue; filter; group/order; switch list/Kanban; edit status/priority/assignee/labels; move board card; clear filters; open detail panel/full page.
- **Empty/loading/error:** first-use team list offers create; My issues offers navigation to teams; triage explains manual Triage status; filtered empty shows active clauses. Per-row mutation failure reverts only that field and preserves collection position.
- **Acceptance:** stable row/card keys; virtualization does not break focus; Kanban has menus/keyboard alternatives to drag; URL restores filters/layout; inaccessible objects never affect counts/groups.

## SCR-05 — Create issue

**Covers:** SCR-ISSUE-CREATE
**Release:** MVP
**Permission:** issue-create permission in selected team

- **States:** pristine, dirty draft, validating references, submitting, created, retryable failure, reference conflict, permission revoked.
- **Actions:** choose team, title, description, status, priority, assignee, labels; submit; submit-and-create-another if enabled; cancel.
- **Empty/loading/error:** title is the only content requirement; fields unavailable until team is selected; closing a dirty draft confirms or retains account/workspace-scoped recovery draft.
- **Acceptance:** `C` opens; `Cmd/Ctrl+Enter` submits; duplicate requests return same issue; assignee/labels are authorized and belong to workspace/team rules; success exposes canonical key/URL.

## SCR-06 — Issue detail

**Covers:** SCR-ISSUE
**Release:** MVP; NEXT sections for relations/attachments/watchers
**Permission:** issue read access; actions filtered by field-level operation policy

- **States:** full page/panel, field editing, comment draft/submission, activity loading, archived, not found/unauthorized, version conflict; NEXT upload/relations/notification state.
- **Actions:** edit title/description/properties; comment/reply; archive/restore; copy key/link; open full page/close panel; NEXT relate, attach, watch/mute.
- **Empty/loading/error:** description/comments have intentional empty prompts; failed comment retains draft; archived screen disables mutation except restore where permitted; attachment errors remain per-file.
- **Acceptance:** canonical URL and browser history; panel traps no focus and returns focus on close; activity accurately attributes mutations; comments sanitize rich content; stale permission immediately clears content on refetch/realtime revocation.

## SCR-07 — Search

**Covers:** SCR-SEARCH
**Release:** MVP
**Permission:** results limited to authorized workspace/team objects

- **States:** initial suggestions/recent permitted items, typing/debounced, searching, results, no results, degraded identifier/title-only search, error.
- **Actions:** enter query, narrow team/status, keyboard navigate, open result, clear, close.
- **Empty/loading/error:** no-result copy never reveals inaccessible matches; loading preserves query; timeout offers retry/fallback; stale recent items are re-authorized.
- **Acceptance:** `/` opens outside text inputs; Escape returns focus; issue key exact match ranks first; server applies permission predicates before ranking/counting.

## SCR-08 — Account settings

**Covers:** SCR-ACCOUNT
**Release:** MVP
**Permission:** authenticated user for own account

- **States:** profile, appearance, active sessions, saving, upload error, session-revoke confirmation.
- **Actions:** update display name/avatar; select light/dark/system; inspect/revoke sessions; sign out all other sessions.
- **Empty/loading/error:** avatar validates type/size; current session is clearly marked; revoking current session requires reauthentication/redirect.
- **Acceptance:** no account can edit another account; theme updates without inaccessible flash; session revocation takes effect promptly and is audited without exposing tokens.

## SCR-09 — Workspace settings

**Covers:** SCR-WORKSPACE-SETTINGS
**Release:** MVP
**Permission:** General/member/team actions per Owner/Admin rules; Members may view limited directory if policy permits

- **States:** general, members/invites, teams, labels, validation, pending invite, suspended member, archived team, destructive impact preview.
- **Actions:** rename/icon; invite/revoke; change role/team access; suspend/reactivate/remove; create/update/archive team; create/update/archive shared labels; transfer ownership; request workspace deletion according to policy.
- **Empty/loading/error:** member/team empty states describe impact; last-Owner constraints are inline; failed role change preserves prior authority and displays no optimistic success.
- **Acceptance:** every mutation uses current role and target hierarchy checks; last Owner remains; suspension revokes sessions; team archive handles open issues through an explicit policy; all administrative changes audited.

## SCR-10 — Team settings

**Covers:** SCR-TEAM-SETTINGS
**Release:** MVP
**Permission:** workspace Owner/Admin or team Manager; team Member read access only where specified

- **States:** general, members, workflow categories/statuses, reorder, dependency impact, validation/error.
- **Actions:** update name/icon; manage team members/managers within workspace constraints; create/edit/reorder/archive statuses.
- **Empty/loading/error:** workflow may never have zero usable statuses; destructive changes list affected issue count and require replacement status; label archive does not erase issue activity.
- **Acceptance:** managers cannot promote workspace roles; status category invariants and positions are transactionally valid; issue references migrate atomically; changes are audited. Shared labels are managed only from authorized Workspace settings.

## SCR-11 — Inbox

**Covers:** SCR-INBOX
**Release:** NEXT
**Permission:** notification recipient; referenced issue re-authorized at read time

- **States:** unread/all, grouped event list, selected preview, empty, stale/unavailable target, loading more, disconnected.
- **Actions:** open, mark read/unread, mark all read, mute/unmute issue, clear item, open canonical issue.
- **Empty/loading/error:** empty explains participation rules; inaccessible target reveals no cached title/body; count remains clearly stale while disconnected and reconciles on reconnect.
- **Acceptance:** recipient isolation; deduplication/grouping rules deterministic; read state idempotent; clearing notification never changes issue activity.

## SCR-12 — Views

**Covers:** SCR-VIEWS, SCR-VIEW
**Release:** NEXT
**Permission:** creator/authorized viewer; edit/delete by creator or scoped manager policy

- **States:** directory, create/edit form, saved result collection, invalid references, empty/no results, bookmark state, concurrent edit.
- **Actions:** name/describe, configure filters/layout/group/order/scope/visibility, save, duplicate, bookmark, share URL, edit, delete.
- **Empty/loading/error:** invalid team/status/label clauses are identified and repairable; no-result view retains definition; unauthorized clauses/results never leak.
- **Acceptance:** definition is versioned structured data; stable URL; query re-authorized on every use; sharing does not confer access; saved presentation round-trips exactly.

## SCR-13 — Projects

**Covers:** SCR-PROJECTS, SCR-PROJECT
**Release:** NEXT
**Permission:** authorized workspace members view; create/manage by Owner/Admin or configured project-management role

- **States:** directory, create/edit, overview, issue collection, no issues, partial team access, archived/completed, progress loading.
- **Actions:** create/update status/lead/teams/dates; add/remove/create issue; filter/group; archive project.
- **Empty/loading/error:** partial-access users see only permitted issue counts with a clear “limited access” statement, not hidden totals; unlink differs from delete.
- **Acceptance:** project owns no duplicate issue content; progress derives from visible/authorized scope with clearly defined denominator; team removal handles linked issues explicitly.

## SCR-14 — Cycles

**Covers:** SCR-CYCLES, SCR-CYCLE
**Release:** NEXT
**Permission:** team members view; team Manager manages cycle metadata and close/rollover

- **States:** current/upcoming/past, create/edit, empty scope, active, closing/rollover, closed, date conflict.
- **Actions:** create dates/name, add/remove team issues, start/close, choose rollover destination, inspect progress.
- **Empty/loading/error:** overlapping date conflict identifies cycle; closing requires explicit disposition for incomplete issues; failed rollover is transactional.
- **Acceptance:** cycle belongs to one team; issue belongs to at most one cycle; active overlap prohibited; close/rollover auditable and idempotent.

## SCR-15 — Integrations

**Covers:** SCR-INTEGRATIONS, SCR-INTEGRATION
**Release:** LATER
**Permission:** workspace Owner/Admin or explicit Integration Admin role if later approved

- **States:** catalog, scope preview, authorizing/callback, connected/degraded/revoked, delivery history, retrying, uninstalling.
- **Actions:** select teams/scopes, install/test, rotate/reconnect, inspect delivery, retry permitted failure, disable/uninstall.
- **Empty/loading/error:** provider outage does not block settings/core issue work; OAuth state failure reveals no credential detail; delivery payloads redact secrets/content by permission.
- **Acceptance:** exact scopes and fields shown before consent; credentials encrypted; signed/replay-safe callbacks; team grants enforced; uninstall/revoke audited.

## SCR-16 — Templates

**Covers:** SCR-TEMPLATES
**Release:** LATER
**Permission:** team Manager creates/updates; team members apply

- **States:** list, create/edit preview, invalid archived references, empty, delete confirmation.
- **Actions:** name, choose team, define issue defaults, save, duplicate, apply, archive/delete.
- **Empty/loading/error:** applying old template validates status/assignee/label references and asks for replacements; never silently drops required semantics.
- **Acceptance:** issue-only schema is versioned; applying creates an editable draft, not an issue; template cannot grant access or select an unauthorized assignee.

## SCR-17 — Developer settings

**Covers:** SCR-DEVELOPER
**Release:** LATER
**Permission:** user manages own personal tokens; Owner/Admin manages service credentials/webhooks according to policy

- **States:** token list/create-once-secret/revoke, webhook list/create/test/delivery log/rotate/disable, rate-limit/error.
- **Actions:** name/scope/expire/revoke token; configure verified HTTPS destination/events/teams; rotate secret; retry/test; disable/delete.
- **Empty/loading/error:** token secret is shown once; URL verification blocks private/link-local/loopback destinations; delivery logs redact headers/secrets and bound payload retention.
- **Acceptance:** stored token is one-way hashed where possible; least scopes; last-used visible; webhook signatures/replay protection/retries/idempotency; all lifecycle operations audited.
