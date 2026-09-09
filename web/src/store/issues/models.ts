import { types } from 'mobx-state-tree';

// One denormalized relations entry on the Issue record (v1.1). The
// server rewrites each edge to the reader's perspective before it
// ships, so the array is always the issue's own side of every edge.
// createdById is a union: the reader-side subselect emits null for a
// NULL created_by, the standalone record serializes it as ''.
// type stays a plain string (the UI maps via a Record with a fallback,
// the same total-render policy as the swarm phase field).
export const IssueRelationEntry = types.model('IssueRelationEntry', {
  id: types.string,
  createdAt: types.string,
  updatedAt: types.string,
  issueId: types.string,
  createdById: types.union(types.string, types.null),
  relatedIssueId: types.string,
  type: types.string,
});

export const Issue = types.model('Issue', {
  id: types.string,
  createdAt: types.string,
  updatedAt: types.string,
  title: types.string,
  number: types.number,
  description: types.union(types.string, types.null),
  priority: types.union(types.number, types.null),
  dueDate: types.union(types.string, types.null),
  sortOrder: types.union(types.number, types.null),
  estimate: types.union(types.number, types.null),
  teamId: types.string,
  createdById: types.union(types.string, types.null),
  assigneeId: types.union(types.string, types.null),
  labelIds: types.array(types.string),
  parentId: types.union(types.string, types.null),
  stateId: types.string,
  subscriberIds: types.array(types.string),
  cycleId: types.union(types.string, types.null, types.undefined),
  projectId: types.union(types.string, types.null, types.undefined),
  projectMilestoneId: types.union(types.string, types.null, types.undefined),
  sourceMetadata: types.union(types.string, types.null, types.undefined),
  children: types.array(types.string),
  // D1: agent swarm paused on this issue (escalation flag). Agents
  // cannot act; a human mutation resumes it. Old payloads (outbox
  // retention) predate the field, so it is optional with a default.
  agentPaused: types.optional(types.boolean, false),
  // v1.1: the denormalized relations array (spec cs:api:relations).
  // Optional with an empty default — outbox retention and the local
  // cache hold pre-v1.1 payloads without the field.
  relations: types.optional(types.array(IssueRelationEntry), []),
});

export const IssuesMap = types.map(Issue);
