import type { WorkflowsStoreType } from './store';

import type { SyncActionRecord } from 'common/types';

export async function saveWorkflowData(
  data: SyncActionRecord[],
  workflowsStore: WorkflowsStoreType,
) {
  await Promise.all(
    data.map(async (record: SyncActionRecord) => {
      const workflow = {
        id: record.data.id,
        createdAt: record.data.createdAt,
        updatedAt: record.data.updatedAt,
        name: record.data.name,
        description: record.data.description,
        position: record.data.position,
        workflowId: record.data.teamId,
        color: record.data.color,
        category: record.data.category,
        teamId: record.data.teamId,
      };

      switch (record.action) {
        case 'I': {
          return (
            workflowsStore &&
            (await workflowsStore.update(workflow, record.data.id))
          );
        }

        case 'U': {
          return (
            workflowsStore &&
            (await workflowsStore.update(workflow, record.data.id))
          );
        }

        case 'D': {
          return (
            workflowsStore && (await workflowsStore.deleteById(record.data.id))
          );
        }
      }
    }),
  );
}
