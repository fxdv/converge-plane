import { RoleEnum } from '@converge/types';
import {
  AI,
  ChartLine,
  Inbox,
  MyIssues,
  StackLine,
  TeamLine,
} from '@converge/ui/icons';
import { cn } from '@converge/ui/lib/utils';
import { observer } from 'mobx-react-lite';
import { useRouter } from 'next/router';
import * as React from 'react';

import { GlobalShortcuts, IssueShortcutDialogs } from 'modules/shortcuts';

import type { UsersOnWorkspaceType } from 'common/types';
import { AllProviders } from 'common/wrappers/all-providers';

import { useCurrentTeam } from 'hooks/teams';

import { useContextStore } from 'store/global-context-provider';

import { Header } from './header';
import { Nav } from './nav';
import { TeamList } from './team-list';
import { useSidebarShortcut } from './use-sidebar-shortcut';
import { WorkspaceDropdown } from './workspace-dropdown';

interface LayoutProps {
  defaultCollapsed?: boolean;
  children: React.ReactNode;
}

export const AppLayoutChild = observer(({ children }: LayoutProps) => {
  const { applicationStore, workspaceStore, notificationsStore } =
    useContextStore();
  useSidebarShortcut();

  const {
    query: { workspaceSlug },
  } = useRouter();
  const team = useCurrentTeam();

  // The inbox badge: the store holds only this account's rows, so the
  // pending count is the unread view (the badge's truth is the feed,
  // the database settles it on the next bootstrap).
  const inboxUnread = workspaceStore.workspace
    ? notificationsStore.unreadIn(workspaceStore.workspace.id).length
    : 0;

  // The Swarm link appears only when the workspace has machine
  // members (the same check as the board's Swarm button).
  const hasAgents = workspaceStore.usersOnWorkspaces.some(
    (u: UsersOnWorkspaceType) => u.role === RoleEnum.AGENT,
  );

  return (
    <>
      <div className="h-[100vh] w-[100vw] flex">
        {!applicationStore.sidebarCollapsed && (
          <div className="w-[190px] flex flex-col h-full overflow-auto">
            <div className="flex py-3 px-4 pr-2 pt-5 items-center justify-between">
              <WorkspaceDropdown />
              <Header />
            </div>

            <div className="px-4 pr-2 mt-1 grow">
              <Nav
                links={[
                  {
                    title: 'My issues',
                    icon: MyIssues,
                    href: `/${workspaceSlug}/my-issues`,
                  },
                  {
                    title: 'Views',
                    icon: StackLine,
                    href: `/${workspaceSlug}/views`,
                  },
                  {
                    title: 'Teams',
                    icon: TeamLine,
                    href: `/${workspaceSlug}/teams`,
                  },
                  ...(hasAgents
                    ? [
                        {
                          title: 'Swarm',
                          icon: AI,
                          href: `/${workspaceSlug}/swarm`,
                        },
                        {
                          title: 'Metrics',
                          icon: ChartLine,
                          href: `/${workspaceSlug}/metrics`,
                        },
                      ]
                    : []),
                  {
                    title: 'Inbox',
                    icon: Inbox,
                    href: `/${workspaceSlug}/inbox`,
                    count: inboxUnread,
                  },
                ]}
              />
              <TeamList />
            </div>
          </div>
        )}

        <div
          className={cn(
            'w-full',
            applicationStore.sidebarCollapsed && 'max-w-[100vw]',
            !applicationStore.sidebarCollapsed && 'max-w-[calc(100vw_-_190px)]',
          )}
        >
          {children}
        </div>
      </div>

      <GlobalShortcuts />

      {team && <IssueShortcutDialogs />}
    </>
  );
});

export function AppLayout(props: LayoutProps) {
  return (
    <AllProviders>
      <AppLayoutChild {...props} />
    </AllProviders>
  );
}
