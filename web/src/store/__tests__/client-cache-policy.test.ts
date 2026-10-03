import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  isMemoryAuthorityModel,
  persistModelToDexie,
} from 'store/client-cache-policy';
import { MODELS } from 'store/models';

describe('client-cache-policy (R-9 slice 1)', () => {
  it('keeps issues and workflows in memory authority', () => {
    assert.equal(isMemoryAuthorityModel(MODELS.Issue), true);
    assert.equal(isMemoryAuthorityModel(MODELS.Workflow), true);
    assert.equal(persistModelToDexie(MODELS.Issue), false);
    assert.equal(persistModelToDexie(MODELS.Workflow), false);
  });

  it('still persists other synced models to Dexie', () => {
    assert.equal(persistModelToDexie(MODELS.IssueComment), true);
    assert.equal(persistModelToDexie(MODELS.Team), true);
  });
});
