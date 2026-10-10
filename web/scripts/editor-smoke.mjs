#!/usr/bin/env node
/**
 * Phase 5 — converge-editor bundle surface smoke (slash, bubble, upload).
 */
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import ts from 'typescript';

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
  {
    file: 'editor-extensions.ts',
    includes: [
      'isAllowedUri:',
      'isAllowedLinkUri',
      'link: false',
      'html: false',
    ],
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

// The link protocol allowlist, executed (it is dependency-free TS).
{
  const src = readFileSync(join(uiRoot, 'link-protocols.ts'), 'utf8');
  const { outputText } = ts.transpileModule(src, {
    compilerOptions: {
      module: ts.ModuleKind.ESNext,
      target: ts.ScriptTarget.ES2020,
    },
  });
  const { isAllowedLinkUri } = await import(
    `data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`
  );
  const allowed = [
    'https://example.com/a?b=c',
    'http://localhost:3000/x',
    'mailto:dev@example.com',
    '/team/ENG/issue/ENG-1',
    '#section',
    '//cdn.example.com/x',
  ];
  const rejected = [
    'javascript:alert(1)',
    'JavaScript:alert(1)',
    ' javascript:alert(1)',
    'java\nscript:alert(1)',
    'java\tscript:alert(1)',
    'data:text/html;base64,PHNjcmlwdD4=',
    'vbscript:msgbox(1)',
    'file:///etc/passwd',
    '',
  ];
  for (const uri of allowed) {
    if (!isAllowedLinkUri(uri)) {
      console.error(
        `editor-smoke: link allowlist rejects ${JSON.stringify(uri)}`,
      );
      failed += 1;
    }
  }
  for (const uri of rejected) {
    if (isAllowedLinkUri(uri)) {
      console.error(
        `editor-smoke: link allowlist accepts ${JSON.stringify(uri)}`,
      );
      failed += 1;
    }
  }
}

if (failed > 0) {
  process.exit(1);
}
console.log('editor-smoke: ok');
