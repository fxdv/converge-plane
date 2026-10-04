#!/usr/bin/env node
/**
 * Phase 3 — WB-2 open-issue panel structural smoke (no auth server required).
 */
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');

const checks = [
  {
    file: 'src/components/side-issue-view/side-issue-view.tsx',
    includes: [
      'side-issue-view',
      'w-full max-w-none md:w-[50vw]',
      'IssueView sideView',
    ],
  },
  {
    file: 'src/modules/issues/components/issue-board-item/issue-board-item.tsx',
    includes: ['IssueViewContext', 'openIssue(issue.id'],
  },
  {
    file: 'src/modules/issues/single-issue/issue-view.tsx',
    includes: ['IssueViewContext', 'closeIssueView'],
  },
];

let failed = 0;
for (const { file, includes } of checks) {
  const path = join(root, file);
  const text = readFileSync(path, 'utf8');
  for (const needle of includes) {
    if (!text.includes(needle)) {
      console.error(`issue-panel-smoke: ${file} missing ${needle}`);
      failed += 1;
    }
  }
}

if (failed > 0) {
  process.exit(1);
}
console.log('issue-panel-smoke: ok');
