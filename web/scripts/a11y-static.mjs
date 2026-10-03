#!/usr/bin/env node
/**
 * Phase 5: static a11y smoke — required patterns on shipped UI source.
 * Full axe against a running server is OD-21/22 follow-up.
 */
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');

const checks = [
  {
    file: 'src/common/wrappers/sync-feed-status.tsx',
    includes: ['role="status"'],
  },
  {
    file: 'src/common/layouts/app-layout/header.tsx',
    includes: ['aria-label="Keyboard shortcuts"'],
  },
  {
    file: 'src/modules/issues/all/all-issues.tsx',
    includes: ['id="board"', 'tabIndex={-1}'],
  },
];

let failed = 0;
for (const { file, includes } of checks) {
  const path = join(root, file);
  const text = readFileSync(path, 'utf8');
  for (const needle of includes) {
    if (!text.includes(needle)) {
      console.error(`a11y-static: ${file} missing ${needle}`);
      failed += 1;
    }
  }
}

if (failed > 0) {
  process.exit(1);
}
console.log('a11y-static: ok');
