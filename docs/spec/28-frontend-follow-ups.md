# 28 — Frontend follow-ups (post phases 2–5)

**Updated:** 2026-10-04

Phases 2–5 are complete (`docs/spec/27-frontend-phases-2-5.md`). This log tracks the next tranche.

## Phase 1 — Dev access and origin hygiene

| Item | Status |
| --- | --- |
| `CONVERGE_WEB_ORIGIN_EXTRA` for explicit browser origins | Done |
| Compose default extras for localhost `:3000` / `:3001` | Done |
| `scripts/bravo-tunnel.sh` (reconnect loop, web + API forwards) | Done |
| `.env.example` + deploy README origin/tunnel notes | Done |
| Spec §08 cross-origin paragraph (extras + loopback aliases) | Done |

## Phase 2 — CSP and SSR regression guards

| Item | Status |
| --- | --- |
| CI: production `web` build + `/auth` HTML has `nonce=` on scripts | Pending |
| Document `App.getInitialProps` requirement for Pages CSP | Pending |

## Phase 3 — OD-15 performance CI

| Item | Status |
| --- | --- |
| Seeded 10k fixture job (or documented manual seed) | Pending |
| WB-1 board profile gate in CI (extend `test:board-profile`) | Partial — script exists, fixture seed TBD |
| Playwright smoke for WB-2 open issue panel | Pending |

## Phase 4 — Data layer cleanup

| Item | Status |
| --- | --- |
| Audit remaining Dexie writes outside metadata policy | Pending |
| Migrate off React Query v3 shim (`common/lib/react-query.ts`) | Pending |
| Runbook note for `SYNC_SCHEMA` bumps | Pending |

## Phase 5 — Editor hardening

| Item | Status |
| --- | --- |
| `converge-editor` smoke tests (slash, bubble, upload) | Pending |
| Dedupe slash tunnel vs extension render path | Pending |
| TipTap extension bundle size pass | Pending |
