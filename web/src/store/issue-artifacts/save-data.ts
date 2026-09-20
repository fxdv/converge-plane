import type { IssueArtifactsStoreType } from './store';

import type { SyncActionRecord } from 'common/types';

import { convergeDatabase } from 'store/database';

// SWR-56: the sync handler for posted documents. Same contract as the
// comments handler: the record's data is the full wire shape (8 keys),
// I/U upsert the object-cache row + store node, D removes by id.
export async function saveIssueArtifactsData(
  data: SyncActionRecord[],
  issueArtifactsStore: IssueArtifactsStoreType,
) {
  await Promise.all(
    data.map(async (record: SyncActionRecord) => {
      const artifact = {
        id: record.data.id,
        createdAt: record.data.createdAt,
        updatedAt: record.data.updatedAt,

        issueId: record.data.issueId,
        userId: record.data.userId,
        title: record.data.title,
        body: record.data.body,
        sourceMetadata: JSON.stringify(record.data.sourceMetadata),
      };

      switch (record.action) {
        case 'I': {
          await convergeDatabase.issueArtifacts.put(artifact);
          return (
            issueArtifactsStore &&
            (await issueArtifactsStore.update(artifact, record.data.id))
          );
        }

        case 'U': {
          await convergeDatabase.issueArtifacts.put(artifact);
          return (
            issueArtifactsStore &&
            (await issueArtifactsStore.update(artifact, record.data.id))
          );
        }

        case 'D': {
          await convergeDatabase.issueArtifacts.delete(record.data.id);
          return (
            issueArtifactsStore &&
            (await issueArtifactsStore.deleteById(record.data.id))
          );
        }
      }
    }),
  );
}
