import { Loader } from '@converge/ui/components/loader';
import { observer } from 'mobx-react-lite';
import getConfig from 'next/config';
import * as React from 'react';

import { hash } from 'common/common-utils';
import type { BootstrapResponse } from 'common/types';

import { useCurrentWorkspace } from 'hooks/workspace';

import { getDeltaRecords } from 'services/sync';

import { useContextStore } from 'store/global-context-provider';
import { MODELS } from 'store/models';
import { UserContext } from 'store/user-context';

import {
  saveLiveSocketData,
  seedTabHighWater,
  tabHighWater,
} from './socket-data-util';

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
      issuesHistoryStore,
      issuesStore,
      workflowsStore,
      workspaceStore,
      teamsStore,
      labelsStore,
      projectsStore,
      viewsStore,
      swarmActivityStore,
    } = useContextStore();
    const user = React.useContext(UserContext);
    const hashKey = `${workspace.id}__${user.id}`;

    // R-8: realtime is an SSE stream, not socket.io. The EventSource is
    // kept under the `socket` name to minimize the diff; it is a hint —
    // the delta endpoint remains authoritative (refetch-after-gap).
    const [socket, setSocket] = React.useState<EventSource | undefined>(
      undefined,
    );

    const { publicRuntimeConfig } = getConfig();

    React.useEffect(() => {
      if (!socket && workspaceStore.workspace?.id) {
        initSocket();
      }

      return () => {
        socket && socket.close();
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
      seedTabHighWater(
        localStorage.getItem(`lastSequenceId_${hash(hashKey)}`),
      );
      const url = `${base}/api/v1/sync_actions/stream?workspaceId=${workspaceStore.workspace.id}&userId=${user.id}`;
      const socket = new EventSource(url, { withCredentials: true });
      setSocket(socket);

      // The shared watermark is max-only: another tab may have advanced
      // it, and this tab may have applied past it — a backwards write
      // would let a third tab skip records (SWR-51).
      const advanceShared = (seq: number) => {
        const stored =
          Number(localStorage.getItem(`lastSequenceId_${hash(hashKey)}`) || '0');
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
        [MODELS.View]: viewsStore,
        [MODELS.SwarmActivity]: swarmActivityStore,
      };

      socket.onmessage = async (event: MessageEvent) => {
        const data = JSON.parse(event.data);

        // The live-record path: the idempotency guard drops a record this
        // tab already applied (stream/delta double delivery, SWR-51).
        await saveLiveSocketData([data], MODEL_STORE_MAP);
        advanceShared(Number(data.sequenceId) || 0);
      };

      // Realtime is a hint, the delta endpoint is authoritative. The
      // stream is now long-lived (the 60s write-timeout cap is gone), but
      // the EventSource still flaps on session-cookie expiry and tab
      // suspension; onopen fires on every (re)connect, so re-run the delta
      // to cover whatever happened during the gap.
      socket.onopen = () => {
        void (async () => {
          if (!workspaceStore.workspace?.id) {
            return;
          }
          // Fetch from this tab's own cursor (SWR-51): whatever another
          // tab wrote to the shared key cannot make this tab skip or
          // rewind; the guard below dedupes the overlap either way.
          const last = String(Math.max(tabHighWater(), 0)) || '0';
          try {
            const resp: BootstrapResponse = await getDeltaRecords(
              workspaceStore.workspace.id,
              Object.values(MODELS),
              last,
              user.id,
            );
            await saveLiveSocketData(resp.syncActions, MODEL_STORE_MAP);
            if (resp.stale) {
              // The delta is incomplete: the server's change feed was
              // trimmed past our cursor. Forget the watermark so the next
              // load takes a full snapshot (which re-seeds the tab cursor
              // from the new watermark); don't advance past the gap.
              localStorage.removeItem(`lastSequenceId_${hash(hashKey)}`);
              return;
            }
            seedTabHighWater(resp.lastSequenceId);
            advanceShared(Number(resp.lastSequenceId) || 0);
          } catch {
            // Reconciliation failed (session expired mid-flight, etc.).
            // The next successful reconnect retries; a page reload always
            // re-syncs from scratch.
          }
        })();
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
