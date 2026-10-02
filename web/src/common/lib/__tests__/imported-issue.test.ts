import assert from 'node:assert/strict';
import test from 'node:test';

import { importedIssueText } from 'common/lib/imported-issue';

test('importedIssueText splits a stored GitHub import', () => {
  const raw = JSON.stringify(
    'Imported from https://github.com/acme/app/issues/141\n\nwant a delete',
  );
  assert.deepEqual(importedIssueText(raw), {
    href: 'https://github.com/acme/app/issues/141',
    body: 'want a delete',
  });
});

test('importedIssueText leaves a rich-text document alone', () => {
  assert.equal(
    importedIssueText(JSON.stringify({ type: 'doc', content: [] })),
    null,
  );
});
