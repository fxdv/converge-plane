import type { SyncActionRecord, SwarmActivityType } from 'common/types';

import type { SwarmActivityStoreType } from './store';

// SwarmActivity records are in-memory only (no IndexedDB cache): the
// signal is ephemeral, the bootstrap replays the runtime's live state,
// and caching it would let a stale "deciding" chip outlive its worker.
export async function saveSwarmActivityData(
  data: SyncActionRecord[],
  store: SwarmActivityStoreType,
): Promise<void> {
  for (const record of data) {
    if (record.action === 'D') {
      // DELETE carries only the id (sync contract).
      store.remove(record.modelId);
    } else {
      store.upsert(record.data as SwarmActivityType);
    }
  }
}
