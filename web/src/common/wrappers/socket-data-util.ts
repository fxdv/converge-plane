import { runInAction } from 'mobx';

import type { SyncActionRecord } from 'common/types';

import { saveCommentsData } from 'store/comments';
import { saveIssueHistoryData } from 'store/issue-history';
import { saveIssuesData } from 'store/issues';
import { saveLabelData } from 'store/labels';
import { MODELS } from 'store/models';
import { saveSwarmActivityData } from 'store/swarm-activity';
import { saveTeamData } from 'store/teams';
import { saveViewData } from 'store/views';
import { saveWorkflowData } from 'store/workflows';
import { saveWorkspaceData } from 'store/workspace';

// Saves the data from the socket and call explicitly functions from individual models
export async function saveSocketData(
  data: SyncActionRecord[],
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  MODEL_STORE_MAP: Record<string, any>,
): Promise<void> {
  // Defensive: a server may serialize an empty record list as null;
  // never let that take the app down.
  if (!Array.isArray(data)) {
    return;
  }

  await runInAction(async () => {
    // Pre-initialize the accumulator object with known model names
    const groupedRecords: Record<string, SyncActionRecord[]> = Object.values(
      MODELS,
    ).reduce(
      (acc, model) => {
        acc[model] = [];
        return acc;
      },
      {} as Record<string, SyncActionRecord[]>,
    );

    // Use for...of instead of reduce for better performance with large arrays
    for (const record of data) {
      if (groupedRecords[record.modelName]) {
        groupedRecords[record.modelName].push(record);
      }
    }

    // Create a map of model names to their save functions to avoid switch statement
    // eslint-disable-next-line @typescript-eslint/ban-types
    const saveHandlers: Record<string, Function> = {
      [MODELS.Label]: saveLabelData,
      [MODELS.Team]: saveTeamData,
      [MODELS.Workflow]: saveWorkflowData,
      [MODELS.Workspace]: saveWorkspaceData,
      [MODELS.UsersOnWorkspaces]: saveWorkspaceData,
      [MODELS.Issue]: saveIssuesData,
      [MODELS.IssueHistory]: saveIssueHistoryData,
      [MODELS.IssueComment]: saveCommentsData,
      [MODELS.View]: saveViewData,
      [MODELS.SwarmActivity]: saveSwarmActivityData,
    };

    // Process records using the handler map
    return Promise.all(
      Object.entries(groupedRecords)
        .map(([modelName, records]) => {
          if (records.length === 0) {
            return null;
          }
          const handler = saveHandlers[modelName];
          return handler ? handler(records, MODEL_STORE_MAP[modelName]) : null;
        })
        .filter(Boolean),
    );
  });
}
