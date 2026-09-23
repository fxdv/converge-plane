import type { NotificationsStoreType } from './store';

import type { NotificationType, SyncActionRecord } from 'common/types';

import { convergeDatabase } from 'store/database';

// The addressed guard (SWR-13): the plane's stream and delta are
// per-workspace — every member of the tenant receives every
// notification record, so the client keeps only the rows addressed to
// its user. Bootstrap is recipient-filtered server-side; this covers
// the live feed. A row with no recognizable recipient is dropped, not
// guessed (a notification is addressed, not broadcast).
function isOurs(
  record: SyncActionRecord,
  store: NotificationsStoreType,
): boolean {
  return (
    typeof record.data.recipientId === 'string' &&
    record.data.recipientId === store.recipientId
  );
}

export async function saveNotificationsData(
  data: SyncActionRecord[],
  notificationsStore: NotificationsStoreType,
) {
  await Promise.all(
    data.map(async (record: SyncActionRecord) => {
      switch (record.action) {
        case 'I':
        case 'U': {
          if (!notificationsStore || !isOurs(record, notificationsStore)) {
            return null;
          }
          const notification = record.data as NotificationType;
          await convergeDatabase.notifications.put(notification);
          return notificationsStore.update(notification);
        }

        case 'D': {
          // The retention horizon and the bootstrap prune's synthetic
          // deletes carry only the id. The store holds only this
          // recipient's rows, so an unknown id is a natural no-op.
          await convergeDatabase.notifications.delete(record.data.id);
          return notificationsStore?.deleteById(record.data.id);
        }

        default: {
          return null;
        }
      }
    }),
  );
}
