import { Button } from '@converge/ui/components/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@converge/ui/components/dropdown-menu';
import { useToast } from '@converge/ui/components/use-toast';
import { CanceledLine, DeleteLine, MoreLine } from '@converge/ui/icons';
import { RiKey2Line } from '@remixicon/react';
import React from 'react';

import { useCurrentWorkspace } from 'hooks/workspace/use-current-workspace';

import { useRemoveTeamMemberMutation } from 'services/team';
import { useSuspendUserMutation } from 'services/workspace';

import { AgentTokensDialog } from './agent-tokens-dialog';

interface MemberOptionsDropdownProps {
  userId: string;
  teamId: string;
  isAdmin: boolean;
  isSuspended: boolean;
  isAgent?: boolean;
}

export function MemberOptionsDropdown({
  userId,
  teamId,
  isAdmin,
  isSuspended,
  isAgent,
}: MemberOptionsDropdownProps) {
  const { toast } = useToast();
  const workspace = useCurrentWorkspace();
  const [tokensDialog, setTokensDialog] = React.useState(false);
  const { mutate: removeMember } = useRemoveTeamMemberMutation({
    onError: (err: string) => {
      toast({
        variant: 'destructive',
        title: 'Error!',
        description: err,
      });
    },
  });

  const { mutate: suspendUser } = useSuspendUserMutation({
    onSuccess: () => {
      toast({
        title: 'Success',
        description: 'User has been suspended',
      });
    },
  });

  if (!isAdmin || isSuspended) {
    return null;
  }

  return (
    <div>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="link"
            size="sm"
            onClick={(e) => {
              e.preventDefault();
            }}
          >
            <MoreLine size={16} />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuGroup>
            {isAgent && (
              <DropdownMenuItem onClick={() => setTokensDialog(true)}>
                <div className="flex items-center gap-1">
                  <RiKey2Line size={16} /> API tokens
                </div>
              </DropdownMenuItem>
            )}
            {isAdmin && (
              <>
                <DropdownMenuItem
                  onClick={() => {
                    suspendUser({
                      userId,
                      workspaceId: workspace?.id,
                    });
                  }}
                >
                  <div className="flex items-center gap-1">
                    <CanceledLine size={16} /> Suspend
                  </div>
                </DropdownMenuItem>
              </>
            )}
            {teamId && isAdmin && (
              <DropdownMenuItem
                onClick={() => {
                  removeMember({
                    userId,
                    teamId,
                  });
                }}
              >
                <div className="flex items-center gap-1">
                  <DeleteLine size={16} /> Remove from team
                </div>
              </DropdownMenuItem>
            )}
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {tokensDialog && (
        <AgentTokensDialog
          agentId={userId}
          open={tokensDialog}
          onOpenChange={setTokensDialog}
        />
      )}
    </div>
  );
}
