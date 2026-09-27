import { types } from 'mobx-state-tree';

// spec cs:agents:prlinks — the wire shape is pinned by pullRequestData in
// server/internal/api/pullrequests.go and by wire-contract.test.ts on this
// side. Every optional field arrives as an explicit null. state stays a
// plain string so a state a later server adds renders as unknown instead
// of failing the model.
export const IssuePullRequest = types.model({
  id: types.string,
  createdAt: types.string,
  updatedAt: types.string,
  issueId: types.string,
  repo: types.string,
  number: types.number,
  url: types.string,
  state: types.string,
  draft: types.boolean,
  title: types.union(types.string, types.null),
  mergedAt: types.union(types.string, types.null),
  linkedById: types.union(types.string, types.null),
  runId: types.union(types.string, types.null),
});
