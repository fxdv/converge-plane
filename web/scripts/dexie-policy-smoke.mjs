#!/usr/bin/env node
/**
 * Phase 4 — R-9 Dexie write policy: memory-authority save-data paths gate Dexie.
 */
import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const storeRoot = join(root, 'src/store');

const policySrc = readFileSync(join(storeRoot, 'client-cache-policy.ts'), 'utf8');
const memoryModels = [
  ...policySrc.matchAll(/MODELS\.(\w+)/g),
]
  .map((m) => m[1])
  .filter((name, idx, arr) => arr.indexOf(name) === idx);

const saveDataFiles = [];
function walk(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, entry.name);
    if (entry.isDirectory()) {
      walk(p);
    } else if (entry.name === 'save-data.ts') {
      saveDataFiles.push(p);
    }
  }
}
walk(storeRoot);

let failed = 0;
for (const file of saveDataFiles) {
  const text = readFileSync(file, 'utf8');
  if (!text.includes('persistModelToDexie')) {
    continue;
  }
  if (!text.includes('if (persist)')) {
    console.error(`dexie-policy-smoke: ${file} missing if (persist) guard`);
    failed += 1;
  }
}

if (failed > 0) {
  process.exit(1);
}
console.log('dexie-policy-smoke: ok');
console.log(`  memory-authority models: ${memoryModels.join(', ')}`);
console.log(`  save-data files checked: ${saveDataFiles.length}`);
