import { Button } from '@converge/ui/components/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@converge/ui/components/dialog';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@converge/ui/components/select';
import { Textarea } from '@converge/ui/components/textarea';
import { useToast } from '@converge/ui/components/use-toast';
import { observer } from 'mobx-react-lite';
import React from 'react';

import type { User } from 'common/types';

import { useIssueData } from 'hooks/issues';
import { useTeamWorkflows } from 'hooks/workflows';
import { useUsersData } from 'hooks/users';
import { useCurrentTeam } from 'hooks/teams';

import { useHandoffIssueMutation } from 'services/issues';

import { useContextStore } from 'store/global-context-provider';

// The server enforces the same cap (spec 12, rule 2): 4 KB.
const SUMMARY_MAX = 4096;

// Placeholder for the state Select; SelectItem rejects "".
const KEEP_STATE = '__keep__';

interface HandoffIssueDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/**
 * spec cs:swarm:handoff
 *
 * D1: hand off the issue to another agent (docs/spec/12). The picker
 * offers the workspace's active agents and the team's workflow states;
 * the summary is the bounded context the next agent works from.
 */
export const HandoffIssueDialog = observer(
  ({ open, onOpenChange }: HandoffIssueDialogProps) => {
    const issue = useIssueData();
    const team = useCurrentTeam();
    const { users, isLoading: usersLoading } = useUsersData(false);
    const { toast } = useToast();
    const { workspaceStore } = useContextStore();

    const [agentId, setAgentId] = React.useState('');
    const [stateId, setStateId] = React.useState(KEEP_STATE);
    const [summary, setSummary] = React.useState('');
    const [touched, setTouched] = React.useState(false);

    // Agents join the users list as machine members (M6).
    const agents = users.filter((user: User) => {
      const role = workspaceStore.getUserData(user.id)?.role;
      const active =
        workspaceStore.getUserData(user.id)?.status !== 'SUSPENDED';
      return (
        active &&
        user.id !== issue.assigneeId &&
        (user.kind === 'agent' || role === 'AGENT')
      );
    });

    // The store holds only active workflows (the server filters them).
    const activeWorkflows = useTeamWorkflows(team?.identifier ?? '') || [];

    React.useEffect(() => {
      if (open) {
        setAgentId('');
        setStateId(KEEP_STATE);
        setSummary('');
        setTouched(false);
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open, issue?.id]);

    const { mutate: handoff, isLoading } = useHandoffIssueMutation({
      onSuccess: () => {
        onOpenChange(false);
      },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      onError: (message: any) => {
        toast({
          title: 'Handoff failed',
          description: message,
        });
      },
    });

    const submit = () => {
      setTouched(true);
      if (!agentId) {
        toast({ title: 'Choose an agent to hand off to' });
        return;
      }
      if (!summary.trim()) {
        toast({
          title: 'A summary is required',
          description: 'Tell the next agent what was done and what remains',
        });
        return;
      }
      if (summary.length > SUMMARY_MAX) {
        toast({
          title: 'Summary too long',
          description: 'Keep it under 4 KB — it becomes the next agent’s context',
        });
        return;
      }
      handoff({
        id: issue.id,
        toAccountId: agentId,
        summary: summary.trim(),
        ...(stateId !== KEEP_STATE ? { stateId } : {}),
      });
    };

    return (
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="sm:max-w-[520px] p-6">
          <DialogHeader className="pb-2">
            <DialogTitle>Hand off {team?.identifier}-{issue.number}</DialogTitle>
            <DialogDescription>
              Move this issue to another agent with a short handoff note.
              The note and the move appear on the issue timeline.
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-4 py-2">
            {agents.length === 0 && !usersLoading ? (
              <p className="text-sm text-muted-foreground">
                No other agents are active in this workspace. Create one in
                Settings → Members.
              </p>
            ) : (
              <div className="grid gap-2">
                <span className="text-sm font-medium">Hand off to</span>
                <Select value={agentId} onValueChange={setAgentId}>
                  <SelectTrigger>
                    <SelectValue placeholder="Choose an agent" />
                  </SelectTrigger>
                  <SelectContent>
                    {agents.map((agent) => (
                      <SelectItem key={agent.id} value={agent.id}>
                        {agent.fullname}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {touched && !agentId && (
                  <span className="text-xs text-destructive">
                    Required
                  </span>
                )}
              </div>
            )}

            <div className="grid gap-2">
              <span className="text-sm font-medium">Move to status</span>
              <Select value={stateId} onValueChange={setStateId}>
                <SelectTrigger>
                  <SelectValue placeholder="Keep current status" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={KEEP_STATE}>
                    Keep current status
                  </SelectItem>
                  {activeWorkflows.map((workflow) => (
                    <SelectItem key={workflow.id} value={workflow.id}>
                      {workflow.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="grid gap-2">
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium">
                  Handoff note{' '}
                  <span className="text-muted-foreground font-normal">
                    (required)
                  </span>
                </span>
                <span
                  className={
                    'text-xs ' +
                    (summary.length > SUMMARY_MAX
                      ? 'text-destructive'
                      : 'text-muted-foreground')
                  }
                >
                  {summary.length}/{SUMMARY_MAX}
                </span>
              </div>
              <Textarea
                value={summary}
                onChange={(e) => setSummary(e.target.value)}
                maxLength={SUMMARY_MAX}
                placeholder="What was done, what was found, what remains, and why this agent is the right next owner."
                rows={5}
              />
              {touched && !summary.trim() && (
                <span className="text-xs text-destructive">
                  A note is required — it is the context the next agent
                  starts with.
                </span>
              )}
            </div>
          </div>

          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => onOpenChange(false)}
              disabled={isLoading}
            >
              Cancel
            </Button>
            <Button onClick={submit} disabled={isLoading}>
              Hand off
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  },
);
