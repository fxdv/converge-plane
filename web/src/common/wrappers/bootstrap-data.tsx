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

import { saveSocketData } from './socket-data-util';

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

  const {
    commentsStore,
    issuesHistoryStore,
    issuesStore,
    workflowsStore,
    workspaceStore,
    teamsStore,
    labelsStore,
    viewsStore,
    swarmActivityStore,
  } = useContextStore();

  const MODEL_STORE_MAP = {
    [MODELS.Label]: labelsStore,
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

      localStorage.setItem(
        `lastSequenceId_${hash(hashKey)}`,
        `${data.lastSequenceId}`,
      );
    },
  });

  const { refetch: syncRecords } = useDeltaRecords({
    modelNames: Object.values(MODELS),
    workspaceId: workspace?.id,
    lastSequenceId,
    userId: user.id,
    onSuccess: async (data: BootstrapResponse) => {
      await saveSocketData(data.syncActions, MODEL_STORE_MAP);
      if (data.stale) {
        // The server's change feed was trimmed past our cursor: the delta
        // is incomplete, so forget the watermark and take a full snapshot.
        localStorage.removeItem(`lastSequenceId_${hash(hashKey)}`);
        setLoading(true);
        await bootstrapRecords();
        return;
      }
      localStorage.setItem(
        `lastSequenceId_${hash(hashKey)}`,
        `${data.lastSequenceId}`,
      );
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
