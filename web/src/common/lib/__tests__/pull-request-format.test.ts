import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  pullRequestDisplay,
  pullRequestHeadline,
  pullRequestHref,
} from 'common/lib/pull-request-format';

const pr = (state: string, draft = false) => ({ state, draft });

test('pullRequestDisplay tells a draft apart and maps the unknown', () => {
  assert.equal(pullRequestDisplay(pr('open')), 'open');
  assert.equal(pullRequestDisplay(pr('open', true)), 'draft');
  assert.equal(pullRequestDisplay(pr('merged')), 'merged');
  assert.equal(pullRequestDisplay(pr('closed', true)), 'closed');
  assert.equal(pullRequestDisplay(pr('pending')), 'pending');
  assert.equal(pullRequestDisplay(pr('unavailable')), 'unavailable');
  assert.equal(pullRequestDisplay(pr('locked')), 'unavailable');
});

test('pullRequestHeadline shows merged only when nothing is left', () => {
  assert.equal(pullRequestHeadline([]), null);
  assert.equal(pullRequestHeadline([pr('merged'), pr('open')]), 'open');
  assert.equal(pullRequestHeadline([pr('merged'), pr('open', true)]), 'draft');
  assert.equal(pullRequestHeadline([pr('merged'), pr('pending')]), 'pending');
  assert.equal(pullRequestHeadline([pr('closed'), pr('merged')]), 'merged');
  assert.equal(pullRequestHeadline([pr('closed')]), 'closed');
  assert.equal(pullRequestHeadline([pr('unavailable')]), 'unavailable');
});

test('pullRequestHref only ever points at a github.com pull request', () => {
  assert.equal(
    pullRequestHref({ repo: 'acme/app', number: 12 }),
    'https://github.com/acme/app/pull/12',
  );
  assert.equal(pullRequestHref({ repo: 'acme/app', number: 0 }), null);
  assert.equal(pullRequestHref({ repo: 'acme/app', number: 1.5 }), null);
  assert.equal(pullRequestHref({ repo: 'Acme/App', number: 1 }), null);
  assert.equal(pullRequestHref({ repo: 'acme/..', number: 1 }), null);
  assert.equal(pullRequestHref({ repo: 'acme/app/x', number: 1 }), null);
  assert.equal(pullRequestHref({ repo: 'evil.example/x', number: 1 }), null);
  assert.equal(pullRequestHref({ repo: '', number: 1 }), null);
});
