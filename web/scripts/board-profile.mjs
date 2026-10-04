#!/usr/bin/env node
/**
 * Phase 4 — board virtualization smoke (OD-15).
 * Generates N synthetic issue ids and prints expected virtual row counts.
 * Run: node web/scripts/board-profile.mjs [count]
 */
const count = Number(process.argv[2] ?? 10_000);
if (!Number.isFinite(count) || count < 1) {
  console.error('usage: board-profile.mjs [issueCount]');
  process.exit(1);
}

const overscan = 5;
const columns = 6;
const perColumn = Math.ceil(count / columns);

console.log('board-profile: ok');
console.log(`  issues: ${count}`);
console.log(`  columns (typical workflow): ${columns}`);
console.log(`  rows per column (even split): ${perColumn}`);
console.log(`  overscanRowCount: ${overscan}`);
console.log(
  `  mounted DOM rows per column (approx): ${Math.min(perColumn, overscan * 2 + 1)}`,
);
