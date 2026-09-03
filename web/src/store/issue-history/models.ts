import { types } from 'mobx-state-tree';

export const RelationChangeModel = types.model({
  type: types.enumeration([
    'BLOCKS',
    'BLOCKED',
    'RELATED',
    'DUPLICATE',
    'DUPLICATE_OF',
    'SIMILAR',
    'SUB_ISSUE',
    'PARENT',
  ]),
  relatedIssueId: types.union(types.string, types.null),
  issueId: types.union(types.string, types.null),
  isDeleted: types.union(types.boolean, types.undefined),
});

export const IssueHistory = types.model({
  id: types.string,
  createdAt: types.string,
  updatedAt: types.string,
  userId: types.union(types.string, types.null),
  issueId: types.union(types.null, types.undefined, types.string),

  addedLabelIds: types.array(types.string),
  removedLabelIds: types.array(types.string),
  fromPriority: types.union(types.number, types.null),
  toPriority: types.union(types.number, types.null),
  fromStateId: types.union(types.string, types.null),
  toStateId: types.union(types.string, types.null),
  fromEstimate: types.union(types.number, types.null),
  toEstimate: types.union(types.number, types.null),
  fromAssigneeId: types.union(types.string, types.null),
  toAssigneeId: types.union(types.string, types.null),
  fromParentId: types.union(types.string, types.null),
  toParentId: types.union(types.string, types.null),
  // D1: what happened (handoff, paused, ...) — the client's
  // discriminator for summary-carrying rows. Optional: payloads
  // retained before D1 predate the field.
  action: types.union(types.string, types.null, types.undefined),
  // D1: handoff summary note (null on every non-handoff row).
  summary: types.union(types.string, types.null, types.undefined),
  relationChanges: types.union(types.null, RelationChangeModel),
  sourceMetadata: types.union(types.null, types.string, types.undefined),
});

export const IssueHistoriesModel = types.array(IssueHistory);
