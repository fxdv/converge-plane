import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  isMemoryAuthorityModel,
  persistModelToDexie,
} from 'store/client-cache-policy';
import { MODELS } from 'store/models';

describe('client-cache-policy (R-9)', () => {
  it('keeps issues, workflows, comments, and history in memory authority', () => {
    assert.equal(isMemoryAuthorityModel(MODELS.Issue), true);
    assert.equal(isMemoryAuthorityModel(MODELS.Workflow), true);
    assert.equal(isMemoryAuthorityModel(MODELS.IssueComment), true);
    assert.equal(isMemoryAuthorityModel(MODELS.IssueHistory), true);
    assert.equal(persistModelToDexie(MODELS.Issue), false);
    assert.equal(persistModelToDexie(MODELS.Workflow), false);
    assert.equal(persistModelToDexie(MODELS.IssueComment), false);
    assert.equal(persistModelToDexie(MODELS.IssueHistory), false);
  });

  it('still persists other synced models to Dexie', () => {
    assert.equal(persistModelToDexie(MODELS.IssueArtifact), true);
    assert.equal(persistModelToDexie(MODELS.Team), true);
  });
});
