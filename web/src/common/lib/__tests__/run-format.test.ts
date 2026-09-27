import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  formatCost,
  formatTokens,
  safeEvidenceHref,
} from 'common/lib/run-format';

test('formatCost renders micro-USD', () => {
  assert.equal(formatCost(0), '$0');
  assert.equal(formatCost(-5), '$0');
  assert.equal(formatCost(4_200), '<$0.01');
  assert.equal(formatCost(10_000), '$0.01');
  assert.equal(formatCost(1_234_567), '$1.23');
  assert.equal(formatCost(250_000_000), '$250');
  assert.equal(formatCost(12_345_000_000), '$12,345');
});

test('formatTokens is compact', () => {
  assert.equal(formatTokens(0), '0');
  assert.equal(formatTokens(999), '999');
  assert.equal(formatTokens(1_250), '1.3k');
  assert.equal(formatTokens(48_000), '48k');
  assert.equal(formatTokens(2_500_000), '2.5M');
  assert.equal(formatTokens(40_000_000), '40M');
});

test('safeEvidenceHref admits only http(s)', () => {
  assert.equal(
    safeEvidenceHref('https://github.com/o/r/pull/1'),
    'https://github.com/o/r/pull/1',
  );
  assert.equal(safeEvidenceHref('http://ci.local/1'), 'http://ci.local/1');
  assert.equal(safeEvidenceHref('javascript:alert(1)'), null);
  assert.equal(safeEvidenceHref('data:text/html,x'), null);
  assert.equal(safeEvidenceHref('/relative'), null);
  assert.equal(safeEvidenceHref(''), null);
});
