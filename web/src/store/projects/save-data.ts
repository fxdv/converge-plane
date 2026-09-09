import type { ProjectsStoreType } from './store';

import type { SyncActionRecord } from 'common/types';

import { convergeDatabase } from 'store/database';

export async function saveProjectData(
  data: SyncActionRecord[],
  projectsStore: ProjectsStoreType,
) {
  await Promise.all(
    data.map(async (record: SyncActionRecord) => {
      const project = {
        id: record.data.id,
        createdAt: record.data.createdAt,
        updatedAt: record.data.updatedAt,
        name: record.data.name,
        color: record.data.color,
        description: record.data.description,
        startDate: record.data.startDate,
        endDate: record.data.endDate,
        status: record.data.status,
        leadUserId: record.data.leadUserId,
        teams: record.data.teams,
        workspaceId: record.data.workspaceId,
      };

      switch (record.action) {
        case 'I': {
          await convergeDatabase.projects.put(project);
          return (
            projectsStore && (await projectsStore.update(project, record.data.id))
          );
        }

        case 'U': {
          await convergeDatabase.projects.put(project);
          return (
            projectsStore && (await projectsStore.update(project, record.data.id))
          );
        }

        case 'D': {
          await convergeDatabase.projects.delete(record.data.id);
          return (
            projectsStore && (await projectsStore.deleteById(record.data.id))
          );
        }
      }
    }),
  );
}
