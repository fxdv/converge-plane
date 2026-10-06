import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { initialSyncMode } from '../initial-sync-mode';

describe('initialSyncMode', () => {
  it('bootstraps when memory stores are empty even with a watermark', () => {
    assert.equal(
      initialSyncMode({
        memoryHydrated: false,
        hasCachedWorkspace: true,
        lastSequenceId: '40',
      }),
      'bootstrap',
    );
  });

  it('deltas only after this tab has applied a snapshot', () => {
    assert.equal(
      initialSyncMode({
        memoryHydrated: true,
        hasCachedWorkspace: true,
        lastSequenceId: '40',
      }),
      'delta',
    );
  });

  it('bootstraps without a watermark or a cached workspace', () => {
    assert.equal(
      initialSyncMode({
        memoryHydrated: true,
        hasCachedWorkspace: true,
        lastSequenceId: null,
      }),
      'bootstrap',
    );
    assert.equal(
      initialSyncMode({
        memoryHydrated: true,
        hasCachedWorkspace: false,
        lastSequenceId: '40',
      }),
      'bootstrap',
    );
  });
});
