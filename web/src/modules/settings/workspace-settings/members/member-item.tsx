import { AvatarText } from '@converge/ui/components/avatar';
import { cn } from '@converge/ui/lib/utils';
import { observer } from 'mobx-react-lite';
import React from 'react';

import type { UsersOnWorkspaceType } from 'common/types';

import { useContextStore } from 'store/global-context-provider';

import { MemberOptionsDropdown } from './member-options-dropdown';

interface MemberItemProps {
  className: string;
  name: string;
  email: string;
  id: string;
  teamId?: string;
  isAdmin?: boolean;
  isSuspended?: boolean;
  isAgent?: boolean;
}

export const MemberItem = observer(
  ({
    name,
    className,
    email,
    id,
    teamId,
    isAdmin,
    isSuspended,
    isAgent,
  }: MemberItemProps) => {
    const { workspaceStore } = useContextStore();

    const userOnWorkspace = workspaceStore.usersOnWorkspaces.find(
      (uoW: UsersOnWorkspaceType) => uoW.userId === id,
    );

    return (
      <div
        className={cn(
          className,
          'flex items-center justify-between bg-background-3 px-4 py-2 rounded-lg',
          isSuspended && 'bg-grayAlpha-100',
        )}
      >
        <div className="flex gap-2 items-center">
          <AvatarText text={name} className="text-base w-8 h-8 rounded-md" />

          <div className="flex flex-col">
            <div className="flex items-center gap-2">
              <div>{name}</div>
              {isAgent && (
                <span className="text-xs px-1.5 py-0.5 rounded-full bg-grayAlpha-100 text-muted-foreground">
                  Agent
                </span>
              )}
            </div>
            <div className="text-muted-foreground">{email}</div>
          </div>
        </div>

        <div className="text-sm flex gap-2 items-center">
          <div>{userOnWorkspace?.role}</div>

          <MemberOptionsDropdown
            userId={userOnWorkspace.userId}
            teamId={teamId}
            isAdmin={isAdmin}
            isSuspended={isSuspended}
          />
        </div>
      </div>
    );
  },
);
