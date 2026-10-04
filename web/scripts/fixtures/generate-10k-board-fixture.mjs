#!/usr/bin/env node
/**
 * OD-15: print expected board virtualization stats for N issues (manual seed aid).
 */
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '../..');
const configPath = join(
  root,
  'src/modules/issues/all/list-view/board-virtual-config.ts',
);
const configSrc = readFileSync(configPath, 'utf8');
const overscanMatch = configSrc.match(
  /BOARD_OVERSCAN_ROW_COUNT\s*=\s*(\d+)/,
);
const overscan = Number(overscanMatch?.[1] ?? 5);

const count = Number(process.argv[2] ?? 10_000);
const columns = Number(process.argv[3] ?? 6);
if (!Number.isFinite(count) || count < 1) {
  console.error(
    'usage: generate-10k-board-fixture.mjs [issueCount] [columnCount]',
  );
  process.exit(1);
}

const perColumn = Math.ceil(count / columns);
const mounted = Math.min(perColumn, overscan * 2 + 1);

console.log(JSON.stringify({ count, columns, perColumn, overscan, mounted }, null, 2));
