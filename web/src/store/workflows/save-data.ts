import type { WorkflowsStoreType } from './store';

import type { SyncActionRecord } from 'common/types';

import { persistModelToDexie } from 'store/client-cache-policy';
import { convergeDatabase } from 'store/database';
import { MODELS } from 'store/models';

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

      const persist = persistModelToDexie(MODELS.Workflow);

      switch (record.action) {
        case 'I': {
          if (persist) {
            await convergeDatabase.workflows.put(workflow);
          }
          return (
            workflowsStore &&
            (await workflowsStore.update(workflow, record.data.id))
          );
        }

        case 'U': {
          if (persist) {
            await convergeDatabase.workflows.put(workflow);
          }
          return (
            workflowsStore &&
            (await workflowsStore.update(workflow, record.data.id))
          );
        }

        case 'D': {
          if (persist) {
            await convergeDatabase.workflows.delete(record.data.id);
          }
          return (
            workflowsStore && (await workflowsStore.deleteById(record.data.id))
          );
        }
      }
    }),
  );
}
