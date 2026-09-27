import type { IssuePullRequestsStoreType } from './store';

import type { IssuePullRequestType, SyncActionRecord } from 'common/types';

import { convergeDatabase } from 'store/database';

// Maps an IssuePullRequest wire record to the store shape. Pure, so the
// wire contract harness can pin it.
export function pullRequestFromRecord(
  data: SyncActionRecord['data'],
): IssuePullRequestType {
  return {
    id: data.id,
    createdAt: data.createdAt,
    updatedAt: data.updatedAt,
    issueId: data.issueId,
    repo: String(data.repo ?? ''),
    number: Number(data.number ?? 0),
    url: String(data.url ?? ''),
    state: String(data.state ?? 'pending'),
    draft: data.draft === true,
    title: data.title ?? null,
    mergedAt: data.mergedAt ?? null,
    linkedById: data.linkedById ?? null,
    runId: data.runId ?? null,
  };
}

export async function saveIssuePullRequestsData(
  data: SyncActionRecord[],
  issuePullRequestsStore: IssuePullRequestsStoreType,
) {
  await Promise.all(
    data.map(async (record: SyncActionRecord) => {
      switch (record.action) {
        case 'I':
        case 'U': {
          const pr = pullRequestFromRecord(record.data);
          await convergeDatabase.issuePullRequests.put(pr);
          return (
            issuePullRequestsStore && issuePullRequestsStore.update(pr, pr.id)
          );
        }

        case 'D': {
          await convergeDatabase.issuePullRequests.delete(record.data.id);
          return (
            issuePullRequestsStore &&
            issuePullRequestsStore.deleteById(record.data.id)
          );
        }
      }
    }),
  );
}
