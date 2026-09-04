import { Warning } from '@converge/ui/icons';
import { cn } from '@converge/ui/lib/utils';
import type { SwarmAgent } from '@converge/services';
import { formatDistanceToNow } from 'date-fns';
import { observer } from 'mobx-react-lite';
import * as React from 'react';

import { useContextStore } from 'store/global-context-provider';

// One fleet row: who the agent is, what it owns, its last handoff, and
// its 24h burn. The dot is the panel's main signal: green working,
// gray idle, red suspended.
export const SwarmAgentRow = observer(({ agent }: { agent: SwarmAgent }) => {
  const { teamsStore } = useContextStore();

  const teams = agent.teamIds
    .map((id) => teamsStore.getTeamWithId(id)?.identifier)
    .filter(Boolean)
    .join(' · ');

  const lastActivity = agent.lastActivityAt
    ? formatDistanceToNow(new Date(agent.lastActivityAt), {
        addSuffix: true,
      })
    : null;

  return (
    <div className="px-4 py-2.5 border-b border-grayAlpha-100 dark:border-grayAlpha-300">
      <div className="flex items-center gap-2 min-w-0">
        <span
          className={cn(
            'size-2 rounded-full shrink-0',
            agent.status === 'SUSPENDED'
              ? 'bg-red-500'
              : agent.busy
                ? 'bg-emerald-500'
                : 'bg-gray-400 dark:bg-gray-500',
          )}
        />
        <span
          className={cn(
            'text-sm font-medium truncate',
            agent.status === 'SUSPENDED' &&
              'line-through text-grayAlpha-500 dark:text-grayAlpha-400',
          )}
        >
          {agent.name}
        </span>
        {teams && (
          <span className="text-xs text-grayAlpha-500 dark:text-grayAlpha-400 truncate">
            {teams}
          </span>
        )}
        <span className="ml-auto text-xs text-grayAlpha-600 dark:text-grayAlpha-300 shrink-0">
          {agent.openIssueCount > 0 && `${agent.openIssueCount} open`}
          {agent.pausedIssueCount > 0 && (
            <span className="text-amber-600 dark:text-amber-400">
              {' '}
              {agent.pausedIssueCount} paused
            </span>
          )}
        </span>
      </div>

      {agent.lastHandoff && (
        <div
          className="mt-1 pl-4 text-xs text-grayAlpha-600 dark:text-grayAlpha-300 truncate"
          title={agent.lastHandoff.summary}
        >
          {agent.lastHandoff.direction === 'in' ? '←' : '→'}{' '}
          {agent.lastHandoff.counterpartName} ·{' '}
          {agent.lastHandoff.issueTitle} ·{' '}
          {formatDistanceToNow(new Date(agent.lastHandoff.createdAt), {
            addSuffix: true,
          })}
        </div>
      )}
      {!agent.lastHandoff && lastActivity && (
        <div className="mt-1 pl-4 text-xs text-grayAlpha-500 dark:text-grayAlpha-400">
          active {lastActivity}
        </div>
      )}

      <div className="mt-1 pl-4 text-xs text-grayAlpha-500 dark:text-grayAlpha-400">
        {agent.ops24h} ops · {agent.handoffs24h} handoffs ·{' '}
        {agent.requests24h.toLocaleString()} calls
        {agent.tokens24h > 0 && ` · ${agent.tokens24h.toLocaleString()} tokens`}
        (24h)
      </div>

      {agent.pausedIssueCount > 0 && (
        <div className="mt-1 pl-4 text-xs text-amber-600 dark:text-amber-400 flex items-center gap-1">
          <Warning size={12} /> waiting on a human
        </div>
      )}
    </div>
  );
});

