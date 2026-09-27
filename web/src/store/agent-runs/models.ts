import { types } from 'mobx-state-tree';

// spec cs:agents:runs — the wire shape is pinned by runData in
// server/internal/api/runs.go and by wire-contract.test.ts on this side.
// Every optional field arrives as an explicit null, and evidence is
// always an array.
export const RunEvidence = types.model({
  kind: types.string,
  url: types.string,
  title: types.union(types.string, types.null),
});

export const AgentRun = types.model({
  id: types.string,
  createdAt: types.string,
  updatedAt: types.string,
  issueId: types.string,
  agentId: types.string,
  claimId: types.union(types.string, types.null),
  startedAt: types.string,
  endedAt: types.union(types.string, types.null),
  endReason: types.union(types.string, types.null),
  outcome: types.union(types.string, types.null),
  summary: types.union(types.string, types.null),
  model: types.union(types.string, types.null),
  inputTokens: types.number,
  outputTokens: types.number,
  costMicros: types.number,
  eventCount: types.number,
  evidence: types.array(RunEvidence),
});
