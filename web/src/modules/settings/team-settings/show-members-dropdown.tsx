import { RoleEnum } from '@converge/types';
import { Button } from '@converge/ui/components/button';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
} from '@converge/ui/components/command';
import { Loader } from '@converge/ui/components/loader';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@converge/ui/components/popover';
import { ScrollArea } from '@converge/ui/components/scroll-area';
import { useToast } from '@converge/ui/components/use-toast';
import { observer } from 'mobx-react-lite';
import React from 'react';

import type { UsersOnWorkspaceType } from 'common/types';
import { getUserFromUsersData } from 'common/user-util';

import { useCurrentTeam } from 'hooks/teams';
import { useAllUsers } from 'hooks/users';

import { useAddTeamMemberMutation } from 'services/team';

import { useContextStore } from 'store/global-context-provider';

import { AddMemberDialog } from '../workspace-settings/members/add-member-dialog';

export const ShowMembersDropdown = observer(() => {
  const currentTeam = useCurrentTeam();
  const [open, setOpen] = React.useState(false);

  const { toast } = useToast();
  const { workspaceStore } = useContextStore();
  const [newMemberDialog, setNewMemberDialog] = React.useState(false);
  const { mutate: addTeamMember } = useAddTeamMemberMutation({
    onSuccess: () => {
      toast({
        title: 'Team',
        description: 'Member added successfully',
      });
    },
  });

  const { users, isLoading } = useAllUsers();

  const getMembersNotInTeam = () => {
    return workspaceStore.usersOnWorkspaces.filter(
      (user: UsersOnWorkspaceType) => {
        return (
          !user.teamIds.includes(currentTeam.id) && user.role !== RoleEnum.BOT
        );
      },
    );
  };

  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button variant="secondary">Add member</Button>
        </PopoverTrigger>
        <PopoverContent className="w-72 p-0" align="end">
          <Command>
            <CommandInput placeholder="Choose member" autoFocus />
            <ScrollArea className="max-h-48 overflow-auto">
              <CommandGroup>
                {isLoading && <Loader />}
                {!isLoading &&
                  getMembersNotInTeam().map((user: UsersOnWorkspaceType) => (
                    <CommandItem
                      key={user.id}
                      value={getUserFromUsersData(users, user.userId).fullname}
                      onSelect={() => {
                        addTeamMember({
                          userId: user.userId,
                          teamId: currentTeam.id,
                        });
                      }}
                    >
                      {getUserFromUsersData(users, user.userId).fullname}
                    </CommandItem>
                  ))}
                <CommandItem onSelect={() => setNewMemberDialog(true)}>
                  Invite people
                </CommandItem>
                <CommandEmpty>No results found.</CommandEmpty>
              </CommandGroup>
            </ScrollArea>
          </Command>
        </PopoverContent>
      </Popover>

      {newMemberDialog && (
        <AddMemberDialog setDialogOpen={setNewMemberDialog} />
      )}
    </>
  );
});
