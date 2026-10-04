# 27 — Frontend phases 2–5 (execution log)

**Updated:** 2026-10-04

## Phase 2 — Design system hygiene

| Item | Status |
| --- | --- |
| Remove hardcoded `.text-primary` hex | Done — use theme `text-primary` only |
| Icon/date policy | Done — `packages/ui/README.md`, `format-date.ts` |
| Issue detail section primitive | Done — all left-side blocks use `IssueDetailSection` |
| Typography/spacing doc | Done — `packages/ui/README.md` |

## Phase 3 — R-9 data layer

| Slice | Status |
| --- | --- |
| 1 — Issues + workflows MST-only (no Dexie writes) | Done — `client-cache-policy.ts`, save-data, prune, load |
| 2 — Comments/history + optimistic mutations | Done — memory authority; optimistic comment in `issue-comment.tsx` |
| 3 — Runs/PRs/notifications; Dexie retirement | Done — + `IssueArtifact`; Dexie kept for workspace metadata only |

`SYNC_SCHEMA` bumped to `2026-10-04.r9-slice3` for one full rebootstrap.

## Phase 4 — Scale and reach

| Item | Status |
| --- | --- |
| Responsive issue side panel (&lt;768px full width) | Done |
| R-10 trim built-in AI writing on new issue | Done |
| Board virtualization pass at 10k | Done — stable measurer cache, row-count fix, `board-profile.mjs`, overscan 5 |

## Phase 5 — Hardening

| Item | Status |
| --- | --- |
| CSP nonces (H1) | Done — `src/middleware.ts` + `_document` nonce |
| TipTap 3 (H4) | Blocked — `novel@0.5` / editor bundle needs migration before `@tiptap` 3 |
| TanStack Query v5 | Done — `@tanstack/react-query` + `common/lib/react-query` shim |
| axe smoke on `/auth` in CI | Done — static HTML a11y checks |
