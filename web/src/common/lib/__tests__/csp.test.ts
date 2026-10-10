import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { backendOrigin, buildCsp } from 'common/lib/csp';

const directive = (csp: string, name: string): string[] => {
  const found = csp
    .split(';')
    .map((d) => d.trim())
    .find((d) => d.startsWith(`${name} `));
  assert.ok(found, `missing ${name}`);
  return found.split(/\s+/).slice(1);
};

describe('buildCsp', () => {
  it('narrows connect-src to self plus the backend origin (SSE)', () => {
    const csp = buildCsp({
      nonce: 'n1',
      isDev: false,
      backendHost: 'https://api.converge.dev/some/path',
    });
    assert.deepEqual(directive(csp, 'connect-src'), [
      "'self'",
      'https://api.converge.dev',
    ]);
  });

  it('never allows scheme-wide sources in production', () => {
    const csp = buildCsp({
      nonce: 'n1',
      isDev: false,
      backendHost: 'http://localhost:3001',
    });
    for (const name of ['connect-src', 'img-src']) {
      for (const scheme of ['https:', 'http:', 'ws:', 'wss:']) {
        assert.equal(
          directive(csp, name).includes(scheme),
          false,
          `${name} allows ${scheme}`,
        );
      }
    }
    assert.deepEqual(directive(csp, 'img-src'), ["'self'", 'data:', 'blob:']);
    assert.ok(directive(csp, 'script-src').includes("'nonce-n1'"));
    assert.equal(directive(csp, 'script-src').includes("'unsafe-eval'"), false);
  });

  it('falls back to self when the backend host is unset or bogus', () => {
    for (const backendHost of [undefined, '', 'not a url', 'javascript:x']) {
      const csp = buildCsp({ nonce: 'n', isDev: false, backendHost });
      assert.deepEqual(directive(csp, 'connect-src'), ["'self'"]);
    }
  });

  it('dev adds ws: for HMR and unsafe-eval only', () => {
    const csp = buildCsp({ nonce: 'n', isDev: true, backendHost: undefined });
    assert.deepEqual(directive(csp, 'connect-src'), ["'self'", 'ws:']);
    assert.ok(directive(csp, 'script-src').includes("'unsafe-eval'"));
  });

  it('backendOrigin strips paths and keeps the port', () => {
    assert.equal(
      backendOrigin('http://localhost:3001/api'),
      'http://localhost:3001',
    );
  });
});
