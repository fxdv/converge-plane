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
| 3 — Runs/PRs/notifications; Dexie retirement | Planned |

`SYNC_SCHEMA` bumped to `2026-10-04.r9-slice2` for one full rebootstrap.

## Phase 4 — Scale and reach

| Item | Status |
| --- | --- |
| Responsive issue side panel (&lt;768px full width) | Done |
| R-10 trim built-in AI writing on new issue | Done |
| Board virtualization pass at 10k | Planned (profile before OD-15 run) |

## Phase 5 — Hardening

| Item | Status |
| --- | --- |
| CSP nonces (H1) | Planned — needs Next middleware + script policy |
| TipTap 3 (H4) | Planned — prosemirror lockfile gate in CI |
| TanStack Query v5 | Planned |
| axe smoke on `/auth` in CI | Done — static HTML a11y checks |
