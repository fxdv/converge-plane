import { AI, Warning } from '@converge/ui/icons';
import type { SwarmPausedIssue } from '@converge/services';
import { formatDistanceToNow } from 'date-fns';
import { observer } from 'mobx-react-lite';
import * as React from 'react';

import { IssueViewContext } from 'components/side-issue-view';
import { useCurrentWorkspace } from 'hooks/workspace';

import { useSwarmQuery } from 'services/workspace';

import { useContextStore } from 'store/global-context-provider';

import { SwarmAgentRow } from './swarm-agent-row';

// The swarm panel (D2, docs/spec/12): the fleet at a glance — who is
// busy on what, who is idle, each agent's last handoff and 24h burn,
// and the issues currently paused for a human (the D1 escalations, on
// top). Topology-agnostic: foreman or flat swarms render the same.
export const SwarmPanel = observer(() => {
  const workspace = useCurrentWorkspace();
  const { teamsStore } = useContextStore();
  const { openIssue } = React.useContext(IssueViewContext);
  const { data, isLoading, refetch } = useSwarmQuery(workspace?.id ?? '');

  const issuePrefix = (teamId: string, number: number) =>
    `${teamsStore.getTeamWithId(teamId)?.identifier ?? '?'}-${number}`;

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="px-4 py-3 border-b border-grayAlpha-100 dark:border-grayAlpha-300 flex items-center gap-2">
        <AI size={16} />
        <h2 className="text-sm font-semibold">Swarm</h2>
        {data && data.agents.length > 0 && (
          <span className="text-xs text-grayAlpha-500 dark:text-grayAlpha-400">
            {data.agents.length} agent{data.agents.length === 1 ? '' : 's'}
          </span>
        )}
        <button
          type="button"
          onClick={() => refetch()}
          className="ml-auto text-xs text-grayAlpha-500 dark:text-grayAlpha-400 hover:underline"
        >
          refresh
        </button>
      </div>

      <div className="overflow-auto flex-1">
        {isLoading && !data && (
          <div className="p-4 text-sm text-grayAlpha-500 dark:text-grayAlpha-400">
            Loading the fleet…
          </div>
        )}

        {data && data.pausedIssues.length > 0 && (
          <section className="p-4 pb-2">
            <h3 className="text-xs font-semibold uppercase tracking-wide text-amber-600 dark:text-amber-400 mb-2 flex items-center gap-1.5">
              <Warning size={14} /> Needs human ({data.pausedIssues.length})
            </h3>
            {data.pausedIssues.map((issue: SwarmPausedIssue) => (
              <PausedIssueRow
                key={issue.id}
                issue={issue}
                prefix={issuePrefix(issue.teamId, issue.number)}
                onOpen={() => openIssue(issue.id)}
              />
            ))}
          </section>
        )}

        <section className="pb-4">
          <h3 className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-grayAlpha-500 dark:text-grayAlpha-400 border-b border-grayAlpha-100 dark:border-grayAlpha-300">
            Fleet
          </h3>
          {data && data.agents.length === 0 ? (
            <div className="p-4 text-sm text-grayAlpha-500 dark:text-grayAlpha-400">
              No agents in this workspace yet. Create one in Settings →
              Members.
            </div>
          ) : (
            data?.agents.map((agent) => <SwarmAgentRow key={agent.id} agent={agent} />)
          )}
        </section>
      </div>
    </div>
  );
});

// A paused issue (D1 escalation): the swarm stopped here and a human is
// being pointed at it. The guard's own words explain why.
const PausedIssueRow = ({
  issue,
  prefix,
  onOpen,
}: {
  issue: SwarmPausedIssue;
  prefix: string;
  onOpen: () => void;
}) => (
  <button
    type="button"
    onClick={onOpen}
    className="w-full mb-2 rounded-md border border-amber-300 dark:border-amber-500/40 bg-amber-50 dark:bg-amber-500/10 p-2.5 text-left hover:bg-amber-100 dark:hover:bg-amber-500/20 transition-colors"
  >
    <div className="text-sm font-medium truncate">
      {prefix} {issue.title}
    </div>
    <div className="mt-0.5 text-xs text-amber-700 dark:text-amber-300 truncate">
      {issue.assigneeName ? `${issue.assigneeName} · ` : ''}
      {issue.reason || 'quiet guard tripped'}
    </div>
    <div className="mt-0.5 text-xs text-amber-600/80 dark:text-amber-400/80">
      paused {formatDistanceToNow(new Date(issue.pausedAt), { addSuffix: true })}
    </div>
  </button>
);
