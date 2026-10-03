import type {
  AgentListEntry,
  AgentTokenInfo,
  AgentTokenSpec,
} from '@converge/services';

import { Button } from '@converge/ui/components/button';
import { Checkbox } from '@converge/ui/components/checkbox';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@converge/ui/components/dialog';
import { Input } from '@converge/ui/components/input';
import { Loader } from '@converge/ui/components/loader';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@converge/ui/components/select';
import { useToast } from '@converge/ui/components/use-toast';
import { formatDistanceToNow } from 'date-fns';
import React from 'react';

import type { TeamType } from 'common/types';

import { useCurrentWorkspace } from 'hooks/workspace/use-current-workspace';

import {
  useGetAgentsQuery,
  useIssueAgentTokenMutation,
  useRevokeAgentTokenMutation,
  useRotateAgentTokenMutation,
} from 'services/workspace';

import { useContextStore } from 'store/global-context-provider';

import {
  AGENT_SCOPES,
  EXPIRY_OPTIONS,
  GRACE_OPTIONS,
  accessProblem,
  agentDefaults,
  agentRequest,
  expiryLabel,
  grantSummary,
  usedRecently,
  type AgentAccess,
  type AgentAccessValues,
} from './agent-access';
import { TokenScreen, type ShownToken } from './token-screen';

interface AgentTokensDialogProps {
  agentId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

interface Confirming {
  tokenId: string;
  action: 'rotate' | 'revoke';
}

export function AgentTokensDialog({
  agentId,
  open,
  onOpenChange,
}: AgentTokensDialogProps) {
  const { toast } = useToast();
  const { teamsStore } = useContextStore();
  const workspace = useCurrentWorkspace();
  const { data, isLoading } = useGetAgentsQuery(
    workspace?.id,
    open && !!workspace?.id,
  );
  const agent = data?.find((entry) => entry.id === agentId);
  const [confirming, setConfirming] = React.useState<Confirming | null>(null);
  const [graceHours, setGraceHours] = React.useState(0);
  // The new token's plaintext lives only here, until the screen closes.
  const [shown, setShown] = React.useState<ShownToken | null>(null);

  const teamName = (id: string) =>
    teamsStore.teams.find((team: TeamType) => team.id === id)?.name ?? id;

  const { mutate: rotate, isLoading: rotating } = useRotateAgentTokenMutation({
    onSuccess: (rotation) => {
      setConfirming(null);
      setShown({
        token: rotation.token,
        agentName: agent?.name ?? '',
        scopes: rotation.scopes,
        teamIds: rotation.teamIds,
        expiresAt: rotation.tokenExpiresAt,
        external: agent?.driver === 'external',
      });
      toast({
        title: 'Token rotated',
        description: 'Copy the new token before closing',
      });
    },
    onError: (message) => {
      toast({
        variant: 'destructive',
        title: 'Could not rotate the token',
        description: message,
      });
    },
  });

  const [issuing, setIssuing] = React.useState(false);
  const { mutate: issue, isLoading: issuingToken } = useIssueAgentTokenMutation(
    {
      onSuccess: (issued) => {
        setIssuing(false);
        setShown({
          token: issued.token,
          agentName: agent?.name ?? '',
          scopes: issued.scopes,
          teamIds: issued.teamIds,
          expiresAt: issued.tokenExpiresAt,
          external: agent?.driver === 'external',
        });
        toast({
          title: 'Token issued',
          description: 'Copy the token before closing',
        });
      },
      onError: (message) => {
        toast({
          variant: 'destructive',
          title: 'Could not issue a token',
          description: message,
        });
      },
    },
  );

  const { mutate: revoke, isLoading: revoking } = useRevokeAgentTokenMutation({
    onSuccess: () => {
      setConfirming(null);
      toast({ title: 'Token revoked' });
    },
    onError: (message) => {
      toast({
        variant: 'destructive',
        title: 'Could not revoke the token',
        description: message,
      });
    },
  });

  const startRotate = (tokenId: string) => {
    setGraceHours(0);
    setConfirming({ tokenId, action: 'rotate' });
  };

  const renderToken = (token: AgentTokenInfo) => {
    const now = new Date();
    const grant = grantSummary(
      { tokenScopes: token.scopes, tokenTeamIds: token.teamIds },
      teamName,
    );
    const action =
      confirming?.tokenId === token.id ? confirming.action : undefined;

    return (
      <div
        key={token.id}
        className="flex flex-col gap-2 bg-background-3 rounded-lg px-4 py-3"
      >
        <div className="flex items-start justify-between gap-2">
          <div className="flex flex-col min-w-0">
            <div className="font-mono text-sm">{token.name}</div>
            <div className="text-muted-foreground text-xs">
              {grant.scopes}; {grant.teams}
            </div>
            <div className="text-muted-foreground text-xs">
              Created {new Date(token.createdAt).toLocaleDateString()} ·{' '}
              {token.lastUsedAt
                ? `used ${formatDistanceToNow(new Date(token.lastUsedAt), { addSuffix: true })}`
                : 'never used'}{' '}
              · {expiryLabel(token.expiresAt, now)}
            </div>
          </div>
          {!action && (
            <div className="flex gap-1 shrink-0">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => startRotate(token.id)}
              >
                Rotate
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  setConfirming({ tokenId: token.id, action: 'revoke' })
                }
              >
                Revoke
              </Button>
            </div>
          )}
        </div>

        {action === 'rotate' && (
          <div className="flex flex-col gap-2 border-t border-border pt-2">
            <div className="text-sm">
              The new token keeps this one&apos;s name, scopes, teams and
              lifetime.
            </div>
            <div className="flex items-center gap-2 text-sm">
              <span>The old token stops working</span>
              <Select
                value={String(graceHours)}
                onValueChange={(value: string) => setGraceHours(Number(value))}
              >
                <SelectTrigger
                  className="w-[140px] flex gap-1 items-center"
                  aria-label="When the old token stops working"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {GRACE_OPTIONS.map((option) => (
                      <SelectItem
                        key={option.hours}
                        value={String(option.hours)}
                      >
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            {graceHours === 0 && usedRecently(token.lastUsedAt, now) && (
              <div className="text-xs text-amber-600 dark:text-amber-400">
                This token was used in the last two hours. Whatever uses it
                fails until it has the new one, unless you give it time.
              </div>
            )}
            <div className="flex justify-end gap-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setConfirming(null)}
              >
                Cancel
              </Button>
              <Button
                variant="secondary"
                size="sm"
                isLoading={rotating}
                onClick={() =>
                  rotate({
                    workspaceId: workspace.id,
                    accountId: agentId,
                    tokenId: token.id,
                    graceHours,
                  })
                }
              >
                Rotate token
              </Button>
            </div>
          </div>
        )}

        {action === 'revoke' && (
          <div className="flex items-center justify-between gap-2 border-t border-border pt-2">
            <span className="text-sm">
              Anything using this token stops working at once.
            </span>
            <div className="flex gap-2 shrink-0">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setConfirming(null)}
              >
                Cancel
              </Button>
              <Button
                variant="destructive"
                size="sm"
                isLoading={revoking}
                onClick={() =>
                  revoke({
                    workspaceId: workspace.id,
                    accountId: agentId,
                    tokenId: token.id,
                  })
                }
              >
                Revoke token
              </Button>
            </div>
          </div>
        )}
      </div>
    );
  };

  const body = () => {
    if (shown) {
      return (
        <TokenScreen
          shown={shown}
          teamName={teamName}
          onClose={() => setShown(null)}
        />
      );
    }
    if (isLoading) {
      return <Loader />;
    }
    if (!agent) {
      return (
        <div className="text-muted-foreground text-sm">Agent not found.</div>
      );
    }
    return (
      <div className="flex flex-col gap-2">
        {agent.tokens.length === 0 && (
          <div className="text-muted-foreground text-sm">
            No live tokens: each one has expired or been revoked. The agent
            cannot sign in until you issue one.
          </div>
        )}
        {agent.tokens.map(renderToken)}
        {issuing ? (
          <IssueTokenForm
            agent={agent}
            isLoading={issuingToken}
            onCancel={() => setIssuing(false)}
            onIssue={(request) =>
              issue({
                workspaceId: workspace.id,
                accountId: agentId,
                ...request,
              })
            }
          />
        ) : (
          <div>
            <Button
              variant="ghost"
              onClick={() => {
                setConfirming(null);
                setIssuing(true);
              }}
            >
              Issue a token
            </Button>
          </div>
        )}
      </div>
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[600px] p-6 max-h-[90vh] overflow-y-auto">
        <DialogHeader className="pb-0">
          <DialogTitle className="font-normal flex flex-col gap-1">
            <div className="flex gap-1 items-center">
              {shown
                ? 'New token'
                : `API tokens${agent ? `: ${agent.name}` : ''}`}
            </div>
            <div className="text-muted-foreground text-left text-base leading-5 max-w-[460px]">
              {shown
                ? 'It will not be shown again.'
                : 'Rotate a token to replace it with a new one, for example when it may have leaked or is about to expire.'}
            </div>
          </DialogTitle>
        </DialogHeader>
        {body()}
      </DialogContent>
    </Dialog>
  );
}

function IssueTokenForm({
  agent,
  isLoading,
  onIssue,
  onCancel,
}: {
  agent: AgentListEntry;
  isLoading: boolean;
  onIssue: (request: { name?: string } & AgentTokenSpec) => void;
  onCancel: () => void;
}) {
  const hasTeams = agent.teamIds.length > 0;
  const [values, setValues] = React.useState<AgentAccessValues>(() => {
    const defaults = agentDefaults(agent.driver);
    return {
      name: '',
      teamIds: agent.teamIds,
      driver: agent.driver,
      ...defaults,
      teamLimited: defaults.teamLimited && hasTeams,
    };
  });
  const set = (over: Partial<AgentAccessValues>) =>
    setValues((current) => ({ ...current, ...over }));
  const problem = accessProblem(values);

  const submit = () => {
    const name = values.name.trim();
    onIssue({ ...agentRequest(values).token, ...(name ? { name } : {}) });
  };

  return (
    <div className="flex flex-col gap-3 bg-background-3 rounded-lg px-4 py-3">
      <Input
        aria-label="Token name"
        placeholder="Name (default)"
        maxLength={64}
        value={values.name}
        onChange={(e) => set({ name: e.currentTarget.value })}
        className="focus-visible:ring-0 bg-grayAlpha-100 w-full"
      />
      <Select
        value={values.access}
        onValueChange={(value: string) => set({ access: value as AgentAccess })}
      >
        <SelectTrigger
          className="flex gap-1 items-center"
          aria-label="Token access"
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem value="scoped">
              Only the scopes below (recommended)
            </SelectItem>
            <SelectItem value="full">Every scope</SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
      {values.access === 'scoped' && (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
          {AGENT_SCOPES.map((scope) => (
            <label
              key={scope.value}
              className="flex items-start gap-2 text-sm cursor-pointer"
            >
              <Checkbox
                className="mt-0.5"
                checked={values.scopes.includes(scope.value)}
                onCheckedChange={(on) =>
                  set({
                    scopes: on
                      ? [...values.scopes, scope.value]
                      : values.scopes.filter((s) => s !== scope.value),
                  })
                }
              />
              <span className="flex flex-col">
                <span className="font-mono">{scope.value}</span>
                <span className="text-muted-foreground text-xs">
                  {scope.hint}
                </span>
              </span>
            </label>
          ))}
        </div>
      )}
      <label className="flex items-center gap-2 text-sm cursor-pointer">
        <Checkbox
          checked={values.teamLimited}
          disabled={!hasTeams}
          onCheckedChange={(on) => set({ teamLimited: on === true })}
        />
        Limit the token to the agent&apos;s teams
      </label>
      <div className="flex items-center gap-2 text-sm">
        <span>Expires after</span>
        <Select
          value={String(values.expiryHours)}
          onValueChange={(value: string) => set({ expiryHours: Number(value) })}
        >
          <SelectTrigger
            className="w-[140px] flex gap-1 items-center"
            aria-label="Token expires after"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              {EXPIRY_OPTIONS.map((option) => (
                <SelectItem key={option.hours} value={String(option.hours)}>
                  {option.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </div>
      {problem && (
        <span role="alert" className="text-xs text-destructive">
          {problem}
        </span>
      )}
      <div className="flex justify-end gap-2">
        <Button variant="ghost" size="sm" onClick={onCancel}>
          Cancel
        </Button>
        <Button
          variant="secondary"
          size="sm"
          isLoading={isLoading}
          disabled={!!problem}
          onClick={submit}
        >
          Issue token
        </Button>
      </div>
    </div>
  );
}
