# 28 — Frontend follow-ups (post phases 2–5)

**Updated:** 2026-10-05

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
| CI: production `web` build + `/auth` HTML has `nonce=` on scripts | Done — `web/scripts/csp-nonce-smoke.mjs`, `pnpm --filter=web test:csp-nonce` |
| Document `App.getInitialProps` requirement for Pages CSP | Done — comment in `web/src/pages/_app.tsx`, this section |

**How it works.** `src/middleware.ts` sets `Content-Security-Policy` and forwards the same policy on the request as `x-nonce` for SSR. Next injects that nonce only when pages are rendered per request, not at static export time. `MyApp.getInitialProps` disables automatic static optimization for the Pages router so `/auth` (and other routes) get nonced scripts. The smoke test runs after `next build`, starts `next start`, and fails if `/auth` is `nextExport` or scripts lack `nonce=`.

## Phase 3 — OD-15 performance CI

| Item | Status |
| --- | --- |
| Seeded 10k fixture job (or documented manual seed) | Done — `web/scripts/fixtures/README.md`, `generate-10k-board-fixture.mjs` |
| WB-1 board profile gate in CI (extend `test:board-profile`) | Done — reads `BOARD_OVERSCAN_ROW_COUNT`, fails if 10k would not virtualize |
| Playwright smoke for WB-2 open issue panel | Done — `web/e2e/wb2-issue-panel.spec.ts` + `test:issue-panel` static smoke |

## Phase 4 — Data layer cleanup

| Item | Status |
| --- | --- |
| Audit remaining Dexie writes outside metadata policy | Done — `persistModelToDexie` gates in save-data; CI `test:dexie-policy` |
| Migrate off React Query v3 shim (`common/lib/react-query.ts`) | Done — TanStack v5 object API; `query-on-success.ts` for bootstrap/delta only |
| Runbook note for `SYNC_SCHEMA` bumps | Done — `deploy/README.md` (Upgrades) |

## Phase 5 — Editor hardening

| Item | Status |
| --- | --- |
| `converge-editor` smoke tests (slash, bubble, upload) | Done — `pnpm --filter=web test:editor` |
| Dedupe slash tunnel vs extension render path | Done — `handleCommandNavigation` in `render-items.tsx` only |
| TipTap extension bundle size pass | Done — CI editor smoke + existing single `prosemirror-model` lock |
