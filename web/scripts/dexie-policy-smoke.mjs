#!/usr/bin/env node
/**
 * Phase 4 — R-9 Dexie policy: memory-authority models never touch Dexie.
 *
 * The models listed in client-cache-policy.ts have no table: database.ts
 * may name them only as `[MODELS.X]: null` (the v25 drop), and their
 * store directories must not reference convergeDatabase.
 */
import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const storeRoot = join(root, 'src/store');

const policySrc = readFileSync(
  join(storeRoot, 'client-cache-policy.ts'),
  'utf8',
);
const memoryModels = [...policySrc.matchAll(/MODELS\.(\w+)/g)]
  .map((m) => m[1])
  .filter((name, idx, arr) => arr.indexOf(name) === idx);

// Store directory per memory-authority model.
const STORE_DIRS = {
  Issue: 'issues',
  Workflow: 'workflows',
  IssueComment: 'comments',
  IssueHistory: 'issue-history',
  IssueArtifact: 'issue-artifacts',
  AgentRun: 'agent-runs',
  IssuePullRequest: 'issue-pull-requests',
  Notification: 'notifications',
};

let failed = 0;
const fail = (msg) => {
  console.error(`dexie-policy-smoke: ${msg}`);
  failed += 1;
};

const dbSrc = readFileSync(join(storeRoot, 'database.ts'), 'utf8');
let filesChecked = 0;
for (const model of memoryModels) {
  const mentions = [
    ...dbSrc.matchAll(new RegExp(`MODELS\\.${model}\\b[^\\n]*`, 'g')),
  ].map((m) => m[0]);
  for (const line of mentions) {
    if (!/^MODELS\.\w+\]: null,$/.test(line)) {
      fail(`database.ts declares a table for memory model ${model}: ${line}`);
    }
  }

  const dir = STORE_DIRS[model];
  if (!dir) {
    fail(`no store directory mapped for memory model ${model}`);
    continue;
  }
  for (const entry of readdirSync(join(storeRoot, dir))) {
    if (!entry.endsWith('.ts') && !entry.endsWith('.tsx')) {
      continue;
    }
    filesChecked += 1;
    const text = readFileSync(join(storeRoot, dir, entry), 'utf8');
    if (text.includes('convergeDatabase')) {
      fail(`${dir}/${entry} touches Dexie for memory model ${model}`);
    }
  }
}

if (failed > 0) {
  process.exit(1);
}
console.log('dexie-policy-smoke: ok');
console.log(`  memory-authority models: ${memoryModels.join(', ')}`);
console.log(`  store files checked: ${filesChecked}`);
