import type { WorkspaceStoreType } from './store';

import type { SyncActionRecord } from 'common/types';

import { convergeDatabase } from 'store/database';
import { MODELS } from 'store/models';

export async function saveWorkspaceData(
  data: SyncActionRecord[],
  workspaceStore: WorkspaceStoreType,
) {
  await Promise.all(
    data.map(async (record: SyncActionRecord) => {
      if (record.modelName === MODELS.UsersOnWorkspaces) {
        const userOnWorkspace = {
          id: record.data.id,
          createdAt: record.data.createdAt,
          updatedAt: record.data.updatedAt,
          userId: record.data.userId,
          workspaceId: record.data.workspaceId,
          teamIds: record.data.teamIds,
          role: record.data.role,
          status: record.data.status,
          settings: record.data.settings,
        };

        switch (record.action) {
          case 'I': {
            await convergeDatabase.usersOnWorkspaces.put(userOnWorkspace);

            // Update the store
            return (
              workspaceStore &&
              (await workspaceStore.updateUsers(userOnWorkspace, record.data.id))
            );
          }

          case 'U': {
            await convergeDatabase.usersOnWorkspaces.put(userOnWorkspace);

            // Update the store
            return (
              workspaceStore &&
              (await workspaceStore.updateUsers(userOnWorkspace, record.data.id))
            );
          }

          case 'D': {
            // DELETE records carry only the id (sync contract): the row
            // is gone, so the cache entry and the MST node are removed by
            // id. A partial record must never be upserted into the tree —
            // the model's required fields would fail validation and take
            // the app down with it.
            const id = record.data?.id ?? record.modelId;
            await convergeDatabase.usersOnWorkspaces.delete(id);
            return workspaceStore && (await workspaceStore.deleteUser(id));
          }
        }
        return null;
      }

      const workspace = {
        id: record.data.id,
        createdAt: record.data.createdAt,
        updatedAt: record.data.updatedAt,
        name: record.data.name,
        actionsEnabled: record.data.actionsEnabled,
        preferences: record.data.preferences,
        slug: record.data.slug,
      };

      switch (record.action) {
        case 'I': {
          await convergeDatabase.workspaces.put(workspace);

          // Update the store
          return workspaceStore && (await workspaceStore.update(workspace));
        }

        case 'U': {
          await convergeDatabase.workspaces.put(workspace);

          // Update the store
          return workspaceStore && (await workspaceStore.update(workspace));
        }

        case 'D': {
          const id = record.data?.id ?? record.modelId;
          await convergeDatabase.workspaces.delete(id);
          // The store is anchored on this workspace; a deletion un-anchors
          // it (the union admits undefined and the app falls back to the
          // workspace picker).
          if (workspaceStore && workspaceStore.workspace?.id === id) {
            return workspaceStore.update(undefined);
          }
          return null;
        }
      }
      return null;
    }),
  );
}
