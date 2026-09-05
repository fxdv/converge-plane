import { Button } from '@converge/ui/components/button';
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from '@converge/ui/components/resizable';
import { ActivityLine, AI, RightSidebarClosed, RightSidebarOpen } from '@converge/ui/icons';
import { RoleEnum } from '@converge/types';
import { observer } from 'mobx-react-lite';
import React from 'react';

import { AppLayout } from 'common/layouts/app-layout';
import { MainLayout } from 'common/layouts/main-layout';
import type { UsersOnWorkspaceType } from 'common/types';
import { SCOPES } from 'common/scopes';
import { withApplicationStore } from 'common/wrappers/with-application-store';

import { IssueViewContext } from 'components/side-issue-view';
import { useScope } from 'hooks';
import { useCurrentTeam } from 'hooks/teams';
import { useLocalState } from 'hooks/use-local-state';

import { useContextStore } from 'store/global-context-provider';

import { Header } from './header';
import { IssuesViewOptions } from './issues-view-options';
import { ListView } from './list-view';
import { NoTeamContainer } from './no-team-container';
import { ActivityFeed } from '../activity-feed';
import { FiltersView } from '../filters-view/filters-view';
import { OverviewInsights } from '../overview-insights';
import { SwarmPanel } from '../swarm-panel';

export const AllIssues = withApplicationStore(
  observer(() => {
    useScope(SCOPES.AllIssues);

    const team = useCurrentTeam();
    const { workspaceStore } = useContextStore();
    const [overview, setOverview] = useLocalState('insightsSidebar', false);
    // D2: the swarm panel (fleet roster + paused issues), toggleable
    // from the header next to the insights panel.
    const [swarm, setSwarm] = useLocalState('swarmPanel', false);
    // spec cs:swarm:activity — Surface B: the workspace activity feed
    // (handoffs, moves, comments, live signals), derived entirely from
    // the synced stores.
    const [activity, setActivity] = useLocalState('activityPanel', false);
    const { closeIssueView } = React.useContext(IssueViewContext);

    // The button appears only when the workspace has machine members
    // (M6): read from the synced users store, no extra request.
    const hasAgents = workspaceStore.usersOnWorkspaces.some(
      (u: UsersOnWorkspaceType) => u.role === RoleEnum.AGENT,
    );

    React.useEffect(() => {
      return () => {
        closeIssueView();
      };
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    return (
      <MainLayout
        header={
          <Header
            title="All issues"
            team={team}
            actions={
              <>
                <Button
                  variant="ghost"
                  onClick={() => setActivity(!activity)}
                  isActive={activity}
                  size="sm"
                  className="gap-1.5"
                >
                  <ActivityLine size={16} />
                  <span className="text-xs font-medium">Activity</span>
                </Button>
                {hasAgents && (
                  <Button
                    variant="ghost"
                    onClick={() => setSwarm(!swarm)}
                    isActive={swarm}
                    size="sm"
                    className="gap-1.5"
                  >
                    <AI size={16} />
                    <span className="text-xs font-medium">Swarm</span>
                  </Button>
                )}
                <Button
                  variant="ghost"
                  onClick={() => setOverview(!overview)}
                  isActive={overview}
                  size="sm"
                >
                  {overview ? (
                    <RightSidebarOpen size={18} />
                  ) : (
                    <RightSidebarClosed size={18} />
                  )}
                </Button>
              </>
            }
          />
        }
      >
        {team ? (
          <ResizablePanelGroup direction="horizontal">
            <ResizablePanel
              collapsible={false}
              order={1}
              id="issues"
              className="w-full flex flex-col"
            >
              <FiltersView Actions={<IssuesViewOptions />} />
              <ListView />
            </ResizablePanel>
            {overview && (
              <>
                <ResizableHandle />
                <ResizablePanel
                  collapsible={false}
                  maxSize={25}
                  minSize={25}
                  defaultSize={25}
                  order={2}
                  id="rightScreen"
                >
                  <OverviewInsights />
                </ResizablePanel>
              </>
            )}
            {swarm && (
              <>
                <ResizableHandle />
                <ResizablePanel
                  collapsible={false}
                  maxSize={25}
                  minSize={25}
                  defaultSize={25}
                  order={3}
                  id="swarm"
                >
                  <SwarmPanel />
                </ResizablePanel>
              </>
            )}
            {activity && (
              <>
                <ResizableHandle />
                <ResizablePanel
                  collapsible={false}
                  maxSize={25}
                  minSize={25}
                  defaultSize={25}
                  order={4}
                  id="activity"
                >
                  <ActivityFeed />
                </ResizablePanel>
              </>
            )}
          </ResizablePanelGroup>
        ) : (
          <NoTeamContainer />
        )}
      </MainLayout>
    );
  }),
);

AllIssues.getLayout = function getLayout(page: React.ReactElement) {
  return <AppLayout>{page}</AppLayout>;
};
