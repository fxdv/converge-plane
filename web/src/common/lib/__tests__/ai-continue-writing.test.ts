import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { AI_CONTINUE_WRITING_API } from 'services/issues/ai/ai-continue-writing-path';

describe('AI continue-writing API path', () => {
  it('uses the /api/v1 proxy route like other issue AI calls', () => {
    assert.equal(
      AI_CONTINUE_WRITING_API,
      '/api/v1/issues/ai/stream/description',
    );
    assert.ok(AI_CONTINUE_WRITING_API.startsWith('/api/v1/'));
  });
});
