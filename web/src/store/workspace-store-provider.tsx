import { Loader } from '@converge/ui/components/loader';
import { observer } from 'mobx-react-lite';
import * as React from 'react';

import { useCurrentWorkspace } from 'hooks/workspace';

import { useContextStore } from './global-context-provider';

export const WorkspaceStoreInit = observer(
  ({ children }: { children: React.ReactNode }) => {
    const [loading, setLoading] = React.useState(true);

    const {
      workspaceStore,
      teamsStore,
      labelsStore,
      projectsStore,
      issuesStore,
      workflowsStore,
      viewsStore,
    } = useContextStore();

    const currentWorkspace = useCurrentWorkspace();

    React.useEffect(() => {
      if (currentWorkspace) {
        // Setting this to help upload the images to this workspace
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        (window as any).workspaceId = currentWorkspace.id;
        initWorkspaceBasedStores();
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [currentWorkspace]);

    // All data related to workspace
    const initWorkspaceBasedStores = React.useCallback(async () => {
      await workspaceStore.load(currentWorkspace.id);
      await workflowsStore.load();
      await teamsStore.load();
      setLoading(false);

      await Promise.all([
        labelsStore.load(),
        projectsStore.load(),
        issuesStore.load(),
        viewsStore.load(),
      ]);

      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [currentWorkspace.id]);

    if (loading) {
      return <Loader height={500} text="Loading workspace..." />;
    }

    return <>{children}</>;
  },
);
