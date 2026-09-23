import {
  type IAnyStateTreeNode,
  type Instance,
  flow,
  types,
} from 'mobx-state-tree';

import type { NotificationType } from 'common/types';

import { convergeDatabase } from 'store/database';

import { NotificationArray } from './models';

export const NotificationsStore: IAnyStateTreeNode = types
  .model('NotificationsStore', {
    notifications: NotificationArray,
    // The addressed key (set by the workspace wrapper from the current
    // user): the bootstrap is recipient-filtered server-side, and the
    // live feed is filtered against this slot in the save handler and
    // the bootstrap prune.
    recipientId: types.maybe(types.string),
  })
  .actions((self) => {
    const setRecipient = (id: string | null) => {
      const next = id ?? undefined;
      if (self.recipientId === next) {
        return;
      }
      self.recipientId = next;
      // A different account: the previous recipient's rows are not
      // ours. The array is rebuilt by the workspace load; clear so the
      // badge never shows another account's count.
      self.notifications.clear();
    };

    const update = (notification: NotificationType) => {
      const index = self.notifications.findIndex(
        (n) => n.id === notification.id,
      );
      if (index !== -1) {
        // The 24h dedup upserts in place: the newest event refreshes
        // the pending row (created_at, actor, number) under the same id.
        self.notifications[index] = {
          ...self.notifications[index],
          ...notification,
        };
      } else {
        self.notifications.push(notification);
      }
    };

    const deleteById = (id: string) => {
      const index = self.notifications.findIndex((n) => n.id === id);
      if (index !== -1) {
        self.notifications.splice(index, 1);
      }
    };

    const load = flow(function* (workspaceId: string) {
      const recipientId = self.recipientId;
      if (!recipientId) {
        self.notifications.clear();
        return;
      }
      // The annotation is load-bearing: a yield in a flow types as any
      // (noImplicitAny would otherwise flag the callback below).
      const rows: NotificationType[] = yield convergeDatabase.notifications
        .where('recipientId')
        .equals(recipientId)
        .toArray();
      self.notifications = NotificationArray.create(
        rows.filter((r) => r.workspaceId === workspaceId),
      );
    });

    return { setRecipient, update, deleteById, load };
  })
  .views((self) => {
    // The shared body (a view cannot reference another view through
    // self in MST's typings; the filter is a plain array, so the sort
    // touches a copy, never the store's array).
    const rowsFor = (workspaceId: string): NotificationType[] =>
      self.notifications
        .filter(
          (n) =>
            n.workspaceId === workspaceId &&
            (!self.recipientId || n.recipientId === self.recipientId),
        )
        .sort((a, b) => b.createdAt.localeCompare(a.createdAt));

    return {
      // The recipient's rows for the workspace, newest first. The
      // inbox list and the badge both read this.
      forWorkspace: (workspaceId: string) => rowsFor(workspaceId),

      // The badge: the pending rows (a read row keeps its history, not
      // its urgency).
      unreadIn: (workspaceId: string): NotificationType[] =>
        rowsFor(workspaceId).filter((n) => n.readAt === null),
    };
  });

export type NotificationsStoreType = Instance<typeof NotificationsStore>;
