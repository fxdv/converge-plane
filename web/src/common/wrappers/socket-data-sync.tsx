import { Loader } from '@tegonhq/ui/components/loader';
import { observer } from 'mobx-react-lite';
import getConfig from 'next/config';
import * as React from 'react';

import { hash } from 'common/common-utils';

import { useCurrentWorkspace } from 'hooks/workspace';

import { useContextStore } from 'store/global-context-provider';
import { MODELS } from 'store/models';
import { UserContext } from 'store/user-context';

import { saveSocketData } from './socket-data-util';

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
      integrationAccountsStore,
      linkedIssuesStore,
      issueRelationsStore,
      notificationsStore,
      viewsStore,
      issueSuggestionsStore,
      actionsStore,
      projectsStore,
      projectMilestonesStore,
      cyclesStore,
      conversationsStore,
      conversationHistoryStore,
      templatesStore,
      peopleStore,
      companiesStore,
      supportStore,
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
      const url = `${base}/api/v1/sync_actions/stream?workspaceId=${workspaceStore.workspace.id}&userId=${user.id}`;
      const socket = new EventSource(url, { withCredentials: true });
      setSocket(socket);

      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const MODEL_STORE_MAP = {
        [MODELS.Label]: labelsStore,
        [MODELS.Workspace]: workspaceStore,
        [MODELS.UsersOnWorkspaces]: workspaceStore,
        [MODELS.Team]: teamsStore,
        [MODELS.Workflow]: workflowsStore,
        [MODELS.Issue]: issuesStore,
        [MODELS.IssueHistory]: issuesHistoryStore,
        [MODELS.IssueComment]: commentsStore,
        [MODELS.IntegrationAccount]: integrationAccountsStore,
        [MODELS.LinkedIssue]: linkedIssuesStore,
        [MODELS.IssueRelation]: issueRelationsStore,
        [MODELS.Notification]: notificationsStore,
        [MODELS.View]: viewsStore,
        [MODELS.IssueSuggestion]: issueSuggestionsStore,
        [MODELS.Action]: actionsStore,
        [MODELS.Project]: projectsStore,
        [MODELS.ProjectMilestone]: projectMilestonesStore,
        [MODELS.Cycle]: cyclesStore,
        [MODELS.Conversation]: conversationsStore,
        [MODELS.ConversationHistory]: conversationHistoryStore,
        [MODELS.Template]: templatesStore,
        [MODELS.People]: peopleStore,
        [MODELS.Company]: companiesStore,
        [MODELS.Support]: supportStore,
      };

      socket.onmessage = async (event: MessageEvent) => {
        const data = JSON.parse(event.data);

        await saveSocketData([data], MODEL_STORE_MAP);
        localStorage.setItem(
          `lastSequenceId_${hash(hashKey)}`,
          `${data.sequenceId}`,
        );
      };

      // EventSource reconnects automatically on drop; on reconnect the
      // client will re-run delta to reconcile anything missed.
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
