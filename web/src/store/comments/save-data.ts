import type { CommentsStoreType } from './store';

import type { SyncActionRecord } from 'common/types';

import { persistModelToDexie } from 'store/client-cache-policy';
import { convergeDatabase } from 'store/database';
import { MODELS } from 'store/models';

export async function saveCommentsData(
  data: SyncActionRecord[],
  commentsStore: CommentsStoreType,
) {
  await Promise.all(
    data.map(async (record: SyncActionRecord) => {
      const comment = {
        id: record.data.id,
        createdAt: record.data.createdAt,
        updatedAt: record.data.updatedAt,

        issueId: record.data.issueId,
        userId: record.data.userId,
        body: record.data.body,
        parentId: record.data.parentId,
        sourceMetadata: JSON.stringify(record.data.sourceMetadata),
      };

      const persist = persistModelToDexie(MODELS.IssueComment);

      switch (record.action) {
        case 'I': {
          if (persist) {
            await convergeDatabase.comments.put(comment);
          }
          return (
            commentsStore &&
            (await commentsStore.update(comment, record.data.id))
          );
        }

        case 'U': {
          if (persist) {
            await convergeDatabase.comments.put(comment);
          }
          return (
            commentsStore &&
            (await commentsStore.update(comment, record.data.id))
          );
        }

        case 'D': {
          if (persist) {
            await convergeDatabase.comments.delete(record.data.id);
          }
          return (
            commentsStore && (await commentsStore.deleteById(record.data.id))
          );
        }
      }
    }),
  );
}
