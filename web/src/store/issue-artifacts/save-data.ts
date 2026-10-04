import type { IssueArtifactsStoreType } from './store';

import type { SyncActionRecord } from 'common/types';

import { persistModelToDexie } from 'store/client-cache-policy';
import { convergeDatabase } from 'store/database';
import { MODELS } from 'store/models';

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

      const persist = persistModelToDexie(MODELS.IssueArtifact);

      switch (record.action) {
        case 'I': {
          if (persist) {
            await convergeDatabase.issueArtifacts.put(artifact);
          }
          return (
            issueArtifactsStore &&
            (await issueArtifactsStore.update(artifact, record.data.id))
          );
        }

        case 'U': {
          if (persist) {
            await convergeDatabase.issueArtifacts.put(artifact);
          }
          return (
            issueArtifactsStore &&
            (await issueArtifactsStore.update(artifact, record.data.id))
          );
        }

        case 'D': {
          if (persist) {
            await convergeDatabase.issueArtifacts.delete(record.data.id);
          }
          return (
            issueArtifactsStore &&
            (await issueArtifactsStore.deleteById(record.data.id))
          );
        }
      }
    }),
  );
}
