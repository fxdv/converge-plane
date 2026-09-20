'use client';

import { Loader } from '@converge/ui/components/loader';
import * as React from 'react';

import { hash } from 'common/common-utils';
import type { BootstrapResponse } from 'common/types';

import { useCurrentWorkspace } from 'hooks/workspace';

import { useBootstrapRecords, useDeltaRecords } from 'services/sync';

import { convergeDatabase } from 'store/database';
import { useContextStore } from 'store/global-context-provider';
import { MODELS } from 'store/models';
import { UserContext } from 'store/user-context';

import {
  pruneStaleLocalRecords,
  saveLiveSocketData,
  saveSocketData,
  seedTabHighWater,
} from './socket-data-util';

interface Props {
  children: React.ReactElement;
}

export function BootstrapWrapper({ children }: Props) {
  const workspace = useCurrentWorkspace();
  const user = React.useContext(UserContext);
  const [loading, setLoading] = React.useState(true);
  const hashKey = `${workspace.id}__${user.id}`;
  const lastSequenceId =
    localStorage && localStorage.getItem(`lastSequenceId_${hash(hashKey)}`);

  // This tab's applied high-water (SWR-51): the shared key seeds it at
  // mount; live paths from here on dedupe and fetch against the tab's own
  // cursor, and every write to the shared key is max-only.
  React.useEffect(() => {
    seedTabHighWater(lastSequenceId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspace?.id]);

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
  } = useContextStore();

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
  };

  React.useEffect(() => {
    if (workspace) {
      initStore();
    }

    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // The swarm's in-flight signals are ephemeral: a worker that died
  // without a delete record (crash, OOM) must not keep its chip
  // pulsing forever. A slow hygiene tick expires signals that outlived
  // their TTL; live signals refresh their own timestamps continuously.
  React.useEffect(() => {
    if (!workspace) {
      return undefined;
    }
    const tick = setInterval(() => {
      swarmActivityStore.expireStale();
    }, 15000);
    return () => clearInterval(tick);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspace?.id]);

  const { refetch: bootstrapRecords } = useBootstrapRecords({
    modelNames: Object.values(MODELS),
    workspaceId: workspace?.id,
    userId: user.id,
    onSuccess: async (data: BootstrapResponse) => {
      await saveSocketData(data.syncActions, MODEL_STORE_MAP);

      // The snapshot subsumes every record up to its watermark (its own
      // per-record sequences are a local counter — never the watermark),
      // so the tab cursor advances to the watermark in one step.
      seedTabHighWater(`${data.lastSequenceId}`);

      // The snapshot is the server's full tenant set, but the upsert-only
      // apply above can never remove rows: anything deleted server-side
      // without a DELETE record reaching this client (an operator's SQL
      // fix, a dropped stream record) would resurrect on every load.
      // Reconcile the residue away before the UI wakes. Never throws.
      await pruneStaleLocalRecords(
        data.syncActions,
        workspace?.id ?? '',
        MODEL_STORE_MAP,
      );

      // Max-only (SWR-51): another tab may have advanced the shared key
      // past this snapshot's watermark — never write it backwards.
      const stored = Number(
        localStorage.getItem(`lastSequenceId_${hash(hashKey)}`) || '0',
      );
      if (Number(data.lastSequenceId) > stored) {
        localStorage.setItem(
          `lastSequenceId_${hash(hashKey)}`,
          `${data.lastSequenceId}`,
        );
      }
    },
  });

  const { refetch: syncRecords } = useDeltaRecords({
    modelNames: Object.values(MODELS),
    workspaceId: workspace?.id,
    lastSequenceId,
    userId: user.id,
    onSuccess: async (data: BootstrapResponse) => {
      // Live records: the idempotency guard (SWR-51) drops anything this
      // tab already applied.
      await saveLiveSocketData(data.syncActions, MODEL_STORE_MAP);
      if (data.stale) {
        // The server's change feed was trimmed past our cursor: the delta
        // is incomplete, so forget the watermark and take a full snapshot
        // (which re-seeds the tab cursor from the new watermark).
        localStorage.removeItem(`lastSequenceId_${hash(hashKey)}`);
        setLoading(true);
        await bootstrapRecords();
        return;
      }
      seedTabHighWater(`${data.lastSequenceId}`);
      // Max-only, as in the bootstrap handler above.
      const stored = Number(
        localStorage.getItem(`lastSequenceId_${hash(hashKey)}`) || '0',
      );
      if (Number(data.lastSequenceId) > stored) {
        localStorage.setItem(
          `lastSequenceId_${hash(hashKey)}`,
          `${data.lastSequenceId}`,
        );
      }
    },
  });

  const initStore = async () => {
    const storeWorkspace = await convergeDatabase.workspaces.get({
      id: workspace.id,
    });

    if (storeWorkspace?.id && lastSequenceId) {
      setLoading(false);
      await syncRecords();
    } else {
      await bootstrapRecords();
      setLoading(false);
    }
  };

  if (loading) {
    return <Loader text="Syncing data..." />;
  }

  return <>{children}</>;
}
