import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  isMemoryAuthorityModel,
  persistModelToDexie,
} from 'store/client-cache-policy';
import { MODELS } from 'store/models';

const MEMORY_MODELS = [
  MODELS.Issue,
  MODELS.Workflow,
  MODELS.IssueComment,
  MODELS.IssueHistory,
  MODELS.IssueArtifact,
  MODELS.AgentRun,
  MODELS.IssuePullRequest,
  MODELS.Notification,
] as const;

describe('client-cache-policy (R-9)', () => {
  it('keeps issue-scoped and inbox models in memory authority', () => {
    for (const model of MEMORY_MODELS) {
      assert.equal(isMemoryAuthorityModel(model), true);
      assert.equal(persistModelToDexie(model), false);
    }
  });

  it('still persists workspace metadata to Dexie', () => {
    assert.equal(persistModelToDexie(MODELS.Team), true);
    assert.equal(persistModelToDexie(MODELS.Label), true);
    assert.equal(persistModelToDexie(MODELS.View), true);
  });
});
