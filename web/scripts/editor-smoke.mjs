#!/usr/bin/env node
/**
 * Phase 5 — converge-editor bundle surface smoke (slash, bubble, upload).
 */
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const uiRoot = join(
  dirname(fileURLToPath(import.meta.url)),
  '../../packages/ui/src/components/ui/editor',
);

const checks = [
  {
    file: 'converge-editor/index.ts',
    includes: ['EditorBubble', 'EditorContent'],
  },
  {
    file: 'converge-editor/extensions/public.ts',
    includes: ['handleCommandNavigation'],
  },
  {
    file: 'converge-editor/extensions/slash-command.tsx',
    includes: ['EditorCommandOut', 'Suggestion', "char: '/'"],
  },
  {
    file: 'converge-editor/components/editor-bubble.tsx',
    includes: ['BubbleMenu'],
  },
  {
    file: 'converge-editor/plugins/upload-images.tsx',
    includes: ['createImageUpload', 'uploadKey'],
  },
  {
    file: 'utils/render-items.tsx',
    includes: ['handleCommandNavigation', 'EditorCommandTunnelContext'],
  },
  {
    file: 'editor.tsx',
    includes: ['handleCommandNavigation', 'EditorRoot', 'slashCommand'],
  },
];

let failed = 0;
for (const { file, includes } of checks) {
  const path = join(uiRoot, file);
  const text = readFileSync(path, 'utf8');
  for (const needle of includes) {
    if (!text.includes(needle)) {
      console.error(`editor-smoke: ${file} missing ${needle}`);
      failed += 1;
    }
  }
}

const slashExt = readFileSync(
  join(uiRoot, 'converge-editor/extensions/slash-command.tsx'),
  'utf8',
);
if (slashExt.includes('export const handleCommandNavigation')) {
  console.error(
    'editor-smoke: slash-command.tsx must not duplicate handleCommandNavigation (use render-items)',
  );
  failed += 1;
}

if (failed > 0) {
  process.exit(1);
}
console.log('editor-smoke: ok');
