/** spec cs:swarm:trace — the history save-data is where the trace
 * (action + summary, incl. handoffs and pauses) lands in the client. */
import type { IssueHistoryStoreType } from './store';

import type { SyncActionRecord } from 'common/types';

import { persistModelToDexie } from 'store/client-cache-policy';
import { convergeDatabase } from 'store/database';
import { MODELS } from 'store/models';

export async function saveIssueHistoryData(
  data: SyncActionRecord[],
  issueHistoryStore: IssueHistoryStoreType,
) {
  await Promise.all(
    data.map(async (record: SyncActionRecord) => {
      const issueHistory = {
        id: record.data.id,
        createdAt: record.data.createdAt,
        updatedAt: record.data.updatedAt,
        userId: record.data.userId,
        issueId: record.data.issueId,
        addedLabelIds: record.data.addedLabelIds,
        removedLabelIds: record.data.removedLabelIds,
        fromPriority: record.data.fromPriority,
        toPriority: record.data.toPriority,
        fromStateId: record.data.fromStateId,
        toStateId: record.data.toStateId,
        fromEstimate: record.data.fromEstimate,
        toEstimate: record.data.toEstimate,
        fromAssigneeId: record.data.fromAssigneeId,
        toAssigneeId: record.data.toAssigneeId,
        fromParentId: record.data.fromParentId,
        toParentId: record.data.toParentId,
        action: record.data.action ?? null,
        summary: record.data.summary ?? null,
        relationChanges: record.data.relationChanges,
        sourceMetadata: JSON.stringify(record.data.sourceMetaData),
      };

      const persist = persistModelToDexie(MODELS.IssueHistory);

      switch (record.action) {
        case 'I': {
          if (persist) {
            await convergeDatabase.issueHistory.put(issueHistory);
          }
          return (
            issueHistoryStore &&
            (await issueHistoryStore.update(issueHistory, record.data.id))
          );
        }

        case 'U': {
          if (persist) {
            await convergeDatabase.issueHistory.put(issueHistory);
          }
          return (
            issueHistoryStore &&
            (await issueHistoryStore.update(issueHistory, record.data.id))
          );
        }

        case 'D': {
          if (persist) {
            await convergeDatabase.issueHistory.delete(record.data.id);
          }
          return (
            issueHistoryStore &&
            (await issueHistoryStore.deleteById(record.data.id))
          );
        }
      }
    }),
  );
}
