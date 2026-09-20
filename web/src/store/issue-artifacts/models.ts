import { types } from 'mobx-state-tree';

// spec cs:swarm:artifact
// SWR-56: the swarm's artifact channel (a document posted to an issue:
// an audit, a plan, a manifest). The wire shape is pinned by
// server/internal/api/artifact_test.go (the artifactData builder) and by
// wire-contract.test.ts on this side: the body is plain text (the fenced
// markdown/JSON document the client renders preformatted), never a
// rich-text envelope; sourceMetadata is present (null in v1) because the
// client field is union(string, null) without undefined — a missing key
// would crash the store.
export const IssueArtifact = types.model({
  id: types.string,
  createdAt: types.string,
  updatedAt: types.string,
  userId: types.string,
  issueId: types.string,
  title: types.string,
  body: types.string,
  sourceMetadata: types.union(types.string, types.null),
});

export const IssueArtifactArray = types.array(IssueArtifact);
