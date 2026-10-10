#!/usr/bin/env node
/**
 * Phase 2: production /auth must SSR with CSP nonces on script tags, and
 * the policy's connect-src/img-src stay narrowed (no scheme-wide sources).
 * Requires `next build` first. Prefers standalone server.js (Docker layout).
 */
import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const port = process.env.CSP_SMOKE_PORT ?? '3098';
const base = `http://127.0.0.1:${port}`;
const standaloneDir = join(root, '.next/standalone/web');
const standaloneServer = join(standaloneDir, 'server.js');

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function waitForAuth(maxMs = 90_000) {
  const start = Date.now();
  while (Date.now() - start < maxMs) {
    try {
      const res = await fetch(`${base}/auth`, { redirect: 'manual' });
      if (res.status === 200) {
        return res;
      }
    } catch {
      // server not listening yet
    }
    await sleep(500);
  }
  throw new Error('web server did not serve /auth in time');
}

function startServer() {
  if (existsSync(standaloneServer)) {
    return spawn('node', ['server.js'], {
      cwd: standaloneDir,
      stdio: ['ignore', 'pipe', 'pipe'],
      env: {
        ...process.env,
        NODE_ENV: 'production',
        PORT: port,
        HOSTNAME: '127.0.0.1',
      },
    });
  }
  return spawn('pnpm', ['exec', 'next', 'start', '-p', port], {
    cwd: root,
    stdio: ['ignore', 'pipe', 'pipe'],
    env: { ...process.env, NODE_ENV: 'production' },
  });
}

const child = startServer();

child.stdout?.on('data', (chunk) => process.stdout.write(chunk));
child.stderr?.on('data', (chunk) => process.stderr.write(chunk));

let exitCode = 0;

try {
  const res = await waitForAuth();
  const html = await res.text();
  const csp = res.headers.get('content-security-policy') ?? '';

  if (!csp.includes('script-src')) {
    console.error('csp-nonce-smoke: missing Content-Security-Policy header');
    exitCode = 1;
  }

  // connect-src/img-src are narrowed (common/lib/csp.ts): no scheme-wide
  // sources, and the SSE backend the build was configured with.
  const directive = (name) =>
    (
      csp
        .split(';')
        .map((d) => d.trim())
        .find((d) => d.startsWith(`${name} `)) ?? ''
    )
      .split(/\s+/)
      .slice(1);
  for (const name of ['connect-src', 'img-src']) {
    const sources = directive(name);
    if (sources.length === 0) {
      console.error(`csp-nonce-smoke: missing ${name}`);
      exitCode = 1;
    }
    for (const scheme of ['https:', 'http:', 'ws:', 'wss:']) {
      if (sources.includes(scheme)) {
        console.error(`csp-nonce-smoke: ${name} allows all of ${scheme}`);
        exitCode = 1;
      }
    }
  }
  const backend = new URL(
    process.env.NEXT_PUBLIC_BACKEND_HOST ?? 'http://localhost:3001',
  ).origin;
  if (!directive('connect-src').includes(backend)) {
    console.error(
      `csp-nonce-smoke: connect-src does not allow the SSE backend ${backend}`,
    );
    exitCode = 1;
  }

  const noncedScripts = html.match(/<script[^>]*\snonce="([^"]+)"/g) ?? [];
  if (noncedScripts.length < 3) {
    console.error(
      `csp-nonce-smoke: expected >=3 nonced <script> tags, found ${noncedScripts.length}`,
    );
    exitCode = 1;
  }

  const firstNonce = html.match(/<script[^>]*\snonce="([^"]+)"/)?.[1];
  if (firstNonce && !csp.includes(`'nonce-${firstNonce}'`)) {
    console.error(
      'csp-nonce-smoke: CSP script-src nonce does not match HTML script nonce',
    );
    exitCode = 1;
  }

  if (html.includes('"nextExport":true')) {
    console.error(
      'csp-nonce-smoke: /auth is statically exported (nextExport) — App.getInitialProps missing?',
    );
    exitCode = 1;
  }

  if (exitCode === 0) {
    console.log('csp-nonce-smoke: ok');
  }
} catch (err) {
  console.error('csp-nonce-smoke:', err.message ?? err);
  exitCode = 1;
} finally {
  child.kill('SIGTERM');
  await sleep(400);
}

process.exit(exitCode);
