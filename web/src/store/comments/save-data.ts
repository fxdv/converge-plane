import type { CommentsStoreType } from './store';

import type { SyncActionRecord } from 'common/types';

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

      switch (record.action) {
        case 'I': {
          return (
            commentsStore &&
            (await commentsStore.update(comment, record.data.id))
          );
        }

        case 'U': {
          return (
            commentsStore &&
            (await commentsStore.update(comment, record.data.id))
          );
        }

        case 'D': {
          return (
            commentsStore && (await commentsStore.deleteById(record.data.id))
          );
        }
      }
    }),
  );
}
