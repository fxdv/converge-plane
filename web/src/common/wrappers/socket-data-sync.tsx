import { Loader } from '@converge/ui/components/loader';
import { observer } from 'mobx-react-lite';
import getConfig from 'next/config';
import * as React from 'react';

import { hash } from 'common/common-utils';
import type { BootstrapResponse, SyncActionRecord } from 'common/types';

import { useCurrentWorkspace } from 'hooks/workspace';

import { getDeltaRecords } from 'services/sync';

import { useContextStore } from 'store/global-context-provider';
import { MODELS } from 'store/models';
import { UserContext } from 'store/user-context';

import {
  acceptDeltaBatch,
  acceptStreamHead,
  acceptStreamRecord,
  dedupeLiveRecords,
  hasSequenceGap,
  saveSocketData,
  seedTabHighWater,
  tabHighWater,
} from './socket-data-util';

// How long a stream record may wait for the sequence before it. Commits
// publish from separate goroutines, so small reorderings resolve within
// milliseconds; a hole that lasts longer is a lost record, repaired by a
// delta from the cursor.
const GAP_GRACE_MS = 1500;

interface Props {
  children: React.ReactElement;
}

// This wrapper ensures the data received from the socket is passed to indexed DB
export const SocketDataSyncWrapper: React.FC<Props> = observer(
  (props: Props) => {
    const { children } = props;
    const workspace = useCurrentWorkspace();

    const {
      commentsStore,
      issueArtifactsStore,
      issuesHistoryStore,
      issuesStore,
      workflowsStore,
      workspaceStore,
      teamsStore,
      labelsStore,
      projectsStore,
      viewsStore,
      swarmActivityStore,
      notificationsStore,
    } = useContextStore();
    const user = React.useContext(UserContext);
    const hashKey = `${workspace.id}__${user.id}`;

    // R-8: realtime is an SSE stream, not socket.io. It is a hint — the
    // delta endpoint remains authoritative (refetch-after-gap). Refs, not
    // state: the effect cleanup must see the live stream and timer, not
    // the values captured by the render that started them.
    const socketRef = React.useRef<EventSource | undefined>(undefined);
    const gapTimerRef = React.useRef<ReturnType<typeof setTimeout> | undefined>(
      undefined,
    );

    const { publicRuntimeConfig } = getConfig();

    React.useEffect(() => {
      if (!socketRef.current && workspaceStore.workspace?.id) {
        initSocket();
      }

      return () => {
        socketRef.current?.close();
        socketRef.current = undefined;
        clearTimeout(gapTimerRef.current);
        gapTimerRef.current = undefined;
      };

      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [workspaceStore.workspace]);

    function initSocket() {
      const base = publicRuntimeConfig.NEXT_PUBLIC_BACKEND_HOST;
      if (!base || !workspaceStore.workspace?.id) {
        return;
      }
      // This tab's applied high-water, seeded from the shared watermark
      // (SWR-51): from here on the tab fetches and dedupes against its
      // own cursor, never against what another tab wrote to the shared
      // key. The shared key remains the cross-tab floor.
      seedTabHighWater(localStorage.getItem(`lastSequenceId_${hash(hashKey)}`));
      const url = `${base}/api/v1/sync_actions/stream?workspaceId=${workspaceStore.workspace.id}&userId=${user.id}`;
      const socket = new EventSource(url, { withCredentials: true });
      socketRef.current = socket;

      // The shared watermark is max-only: another tab may have advanced
      // it, and this tab may have applied past it — a backwards write
      // would let a third tab skip records (SWR-51).
      const advanceShared = (seq: number) => {
        const stored = Number(
          localStorage.getItem(`lastSequenceId_${hash(hashKey)}`) || '0',
        );
        if (seq > stored) {
          localStorage.setItem(`lastSequenceId_${hash(hashKey)}`, `${seq}`);
        }
      };

      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const MODEL_STORE_MAP = {
        [MODELS.Label]: labelsStore,
        [MODELS.Project]: projectsStore,
        [MODELS.Workspace]: workspaceStore,
        [MODELS.UsersOnWorkspaces]: workspaceStore,
        [MODELS.Team]: teamsStore,
        [MODELS.Workflow]: workflowsStore,
        [MODELS.Issue]: issuesStore,
        [MODELS.IssueHistory]: issuesHistoryStore,
        [MODELS.IssueComment]: commentsStore,
        [MODELS.IssueArtifact]: issueArtifactsStore,
        [MODELS.View]: viewsStore,
        [MODELS.SwarmActivity]: swarmActivityStore,
        [MODELS.Notification]: notificationsStore,
      };

      // Applies run one at a time in cursor order: two overlapping saves
      // for the same row could otherwise land out of order.
      let applyQueue: Promise<void> = Promise.resolve();
      const apply = (records: SyncActionRecord[]) => {
        if (records.length > 0) {
          applyQueue = applyQueue
            .then(() => saveSocketData(records, MODEL_STORE_MAP))
            .catch((err: unknown) => {
              console.warn('[converge] sync: apply failed', err);
            });
        }
        return applyQueue;
      };

      // Once the server says the feed was trimmed past our cursor, no
      // delta can close the gap; only the next full load can.
      let feedStale = false;
      let reconciling = false;
      let rerun = false;

      // Realtime is a hint, the delta endpoint is authoritative. It runs
      // on every (re)connect — the EventSource flaps on session-cookie
      // expiry and tab suspension — and when a stream gap outlives its
      // grace period. Single-flight: overlapping deltas would race the
      // cursor, so a request during a run queues one more run (a reconnect
      // mid-flight may postdate the running delta's snapshot).
      const reconcile = async () => {
        if (feedStale || !workspaceStore.workspace?.id) {
          return;
        }
        if (reconciling) {
          rerun = true;
          return;
        }
        reconciling = true;
        try {
          // Fetch from this tab's own cursor (SWR-51): whatever another
          // tab wrote to the shared key cannot make this tab skip or
          // rewind.
          const resp: BootstrapResponse = await getDeltaRecords(
            workspaceStore.workspace.id,
            Object.values(MODELS),
            String(tabHighWater()),
            user.id,
          );
          if (resp.stale) {
            // The delta is incomplete: forget the watermark so the next
            // load takes a full snapshot; don't advance past the gap.
            await apply(dedupeLiveRecords(resp.syncActions));
            localStorage.removeItem(`lastSequenceId_${hash(hashKey)}`);
            feedStale = true;
            return;
          }
          await apply(acceptDeltaBatch(resp.syncActions, resp.lastSequenceId));
          advanceShared(tabHighWater());
        } catch {
          // Reconciliation failed (session expired mid-flight, etc.).
          // The next reconnect or gap retries; a reload re-syncs.
        } finally {
          reconciling = false;
          if (rerun) {
            rerun = false;
            void reconcile();
          } else if (hasSequenceGap()) {
            scheduleGapRepair();
          }
        }
      };

      const scheduleGapRepair = () => {
        if (gapTimerRef.current || feedStale) {
          return;
        }
        gapTimerRef.current = setTimeout(() => {
          gapTimerRef.current = undefined;
          void reconcile();
        }, GAP_GRACE_MS);
      };

      socket.onmessage = (event: MessageEvent) => {
        const { ready, gap } = acceptStreamRecord(JSON.parse(event.data));
        void apply(ready).then(() => advanceShared(tabHighWater()));
        if (gap) {
          scheduleGapRepair();
        } else {
          clearTimeout(gapTimerRef.current);
          gapTimerRef.current = undefined;
        }
      };

      // The server's heartbeat carries the workspace's committed head, so a
      // lost final record surfaces within one heartbeat instead of waiting
      // for the next write.
      socket.addEventListener('head', (event: MessageEvent) => {
        if (acceptStreamHead(JSON.parse(event.data)?.sequenceId)) {
          scheduleGapRepair();
        }
      });

      socket.onopen = () => {
        void reconcile();
      };

      // EventSource reconnects automatically on drop.
      socket.onerror = () => {
        // No-op: the browser handles reconnect. Kept to avoid console noise
        // and to give a single place to log in the future.
      };
    }

    if (workspaceStore?.workspace) {
      return <>{children}</>;
    }

    return <Loader height={500} text="Loading workspace..." />;
  },
);
