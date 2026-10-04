# OD-15 board fixture (10k issues)

The **representative scale** for web performance budgets is documented in
`docs/spec/24-od15-web-performance-budgets.md`: about **10k issues** in a
workspace, **~1k active per team**, **100 concurrent sessions**.

## CI (automated)

CI does not seed Postgres with 10k rows. It validates virtualization math and
policy instead:

- `pnpm --filter=web test:board-profile` — 10k split + mounted row cap
- `web/src/modules/issues/all/list-view/__tests__/board-virtual-config.test.ts`
  (via `pnpm --filter=web test`)

## Local full-stack seed (manual)

To measure WB-1/WB-2 on a live board:

1. Run API + Postgres locally (or Bravo tunnel).
2. Use the workspace bootstrap API or a one-off SQL/script to insert ~10k issues
   for one team (same shape as production sync records).
3. Open the team board, wait for `DatabaseWrapper` ready, then profile first
   paint (WB-1) and open a card (WB-2).

`generate-10k-board-fixture.mjs` prints expected virtualization stats for a
given issue count (sanity check before/after local seeds).
