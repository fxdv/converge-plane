// Post-sign-in redirect guard: only same-origin absolute paths survive.

import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { safeRedirectPath } from 'common/lib/safe-redirect';

describe('safeRedirectPath', () => {
  it('keeps same-origin paths with query and hash', () => {
    assert.equal(
      safeRedirectPath('/acme/issues?view=1#top'),
      '/acme/issues?view=1#top',
    );
    assert.equal(safeRedirectPath('/'), '/');
  });

  it('rejects absolute and protocol-relative URLs', () => {
    for (const raw of [
      'https://evil.example',
      'javascript:alert(1)',
      '//evil.example/path',
      '/\\evil.example',
      '/\t/evil.example',
      '\n//evil.example',
      'acme/issues',
    ]) {
      assert.equal(safeRedirectPath(raw), '/', raw);
    }
  });

  it('falls back for missing or non-string input', () => {
    assert.equal(safeRedirectPath(undefined), '/');
    assert.equal(safeRedirectPath(''), '/');
    assert.equal(safeRedirectPath(['/a', '/b']), '/');
    assert.equal(safeRedirectPath(undefined, '/auth'), '/auth');
  });
});
