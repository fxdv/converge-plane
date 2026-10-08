#!/usr/bin/env node
/**
 * Phase 3 — board virtualization gate (OD-15 / 10k fixture math).
 * Run: node web/scripts/board-profile.mjs [count]
 */
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const configSrc = readFileSync(
  join(root, 'src/modules/issues/all/list-view/board-virtual-config.ts'),
  'utf8',
);
const overscanMatch = configSrc.match(/BOARD_OVERSCAN_ROW_COUNT\s*=\s*(\d+)/);
const overscan = Number(overscanMatch?.[1]);
if (!Number.isFinite(overscan)) {
  console.error('board-profile: could not read BOARD_OVERSCAN_ROW_COUNT');
  process.exit(1);
}

const count = Number(process.argv[2] ?? 10_000);
if (!Number.isFinite(count) || count < 1) {
  console.error('usage: board-profile.mjs [issueCount]');
  process.exit(1);
}

const columns = 6;
const perColumn = Math.ceil(count / columns);
const mounted = Math.min(perColumn, overscan * 2 + 1);

if (count >= 10_000 && mounted >= perColumn) {
  console.error(
    'board-profile: 10k fixture must virtualize (mounted rows < per-column rows)',
  );
  process.exit(1);
}

console.log('board-profile: ok');
console.log(`  issues: ${count}`);
console.log(`  columns (typical workflow): ${columns}`);
console.log(`  rows per column (even split): ${perColumn}`);
console.log(`  overscanRowCount: ${overscan}`);
console.log(`  mounted DOM rows per column (approx): ${mounted}`);
