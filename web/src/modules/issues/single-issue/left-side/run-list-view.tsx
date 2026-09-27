import { ChevronDown, CodingLine, LinkLine } from '@converge/ui/icons';
import { observer } from 'mobx-react-lite';
import * as React from 'react';

import {
  formatCost,
  formatTokens,
  safeEvidenceHref,
} from 'common/lib/run-format';
import type { AgentRunType, User } from 'common/types';

import { useIssueData } from 'hooks/issues';
import { useUsersData } from 'hooks/users';

import { useRunEventsQuery } from 'services/workspace';

import { useContextStore } from 'store/global-context-provider';

// spec cs:agents:runs — the Agent runs section: every external agent's
// run on this issue, with what it reported spending, how it ended, its
// evidence, and its trace (fetched on demand). Everything a run carries
// is agent-authored: it renders as text, and links only when the URL is
// http(s). The section hides when the issue has no runs.

const OUTCOME_STYLE: Record<string, string> = {
  done: 'text-emerald-600 dark:text-emerald-400',
  partial: 'text-sky-600 dark:text-sky-400',
  blocked: 'text-amber-600 dark:text-amber-400',
  failed: 'text-red-600 dark:text-red-400',
};

const END_LABEL: Record<string, string> = {
  released: 'released',
  expired: 'lease lapsed',
  superseded: 'restarted',
  reassigned: 'reassigned',
  paused: 'paused for a human',
  closed: 'issue closed',
  revoked: 'revoked',
};

const EVIDENCE_LABEL: Record<string, string> = {
  pull_request: 'PR',
  commit: 'commit',
  ci_run: 'CI',
  deployment: 'deploy',
  link: 'link',
};

function RunTrace({ run }: { run: AgentRunType }) {
  const { data, isLoading, isError, fetchNextPage, hasNextPage } =
    useRunEventsQuery(run.issueId, run.id, run.eventCount, true);
  if (run.eventCount === 0) {
    return (
      <div className="text-xs text-muted-foreground">No trace reported.</div>
    );
  }
  if (isLoading) {
    return <div className="text-xs text-muted-foreground">Loading trace…</div>;
  }
  if (isError) {
    return (
      <div className="text-xs text-muted-foreground">
        The trace could not be loaded.
      </div>
    );
  }
  const events = data?.pages.flatMap((page) => page.events) ?? [];
  return (
    <div className="bg-grayAlpha-100 max-h-[360px] overflow-auto rounded-md p-2 font-mono text-xs leading-relaxed">
      {events.map((event) => (
        <div key={event.seq} className="flex gap-2">
          <span className="text-muted-foreground shrink-0 tabular-nums">
            {new Date(event.at).toLocaleTimeString()}
          </span>
          <span
            className={`shrink-0 ${event.kind === 'error' ? 'text-red-600 dark:text-red-400' : 'text-muted-foreground'}`}
          >
            {event.kind}
          </span>
          <span className="whitespace-pre-wrap break-words">
            {event.message}
          </span>
        </div>
      ))}
      {hasNextPage ? (
        <button
          type="button"
          className="mt-1 text-muted-foreground underline"
          onClick={() => fetchNextPage()}
        >
          Load more
        </button>
      ) : null}
    </div>
  );
}

const RunRow = observer(({ run }: { run: AgentRunType }) => {
  const [expanded, setExpanded] = React.useState(false);
  const [showTrace, setShowTrace] = React.useState(false);
  const { users } = useUsersData(true);
  const agent = users.find((user: User) => user.id === run.agentId);
  const running = run.endedAt === null;
  const tokens = run.inputTokens + run.outputTokens;

  return (
    <div className="border-t border-grayAlpha-100 py-2">
      <button
        type="button"
        onClick={() => setExpanded((value) => !value)}
        className="flex w-full items-center gap-2 text-left"
      >
        {running ? (
          <span className="relative flex size-2 shrink-0">
            <span className="absolute inline-flex size-full rounded-full bg-sky-400 opacity-75 animate-ping" />
            <span className="relative inline-flex size-2 rounded-full bg-sky-500" />
          </span>
        ) : (
          <CodingLine size={16} className="shrink-0 text-muted-foreground" />
        )}
        <span className="text-foreground truncate">
          {agent?.fullname ?? 'agent'}
        </span>
        <span
          className={`text-xs shrink-0 ${run.outcome ? (OUTCOME_STYLE[run.outcome] ?? '') : 'text-muted-foreground'}`}
        >
          {running
            ? 'working'
            : (run.outcome ??
              END_LABEL[run.endReason ?? ''] ??
              run.endReason ??
              'ended')}
        </span>
        <span className="flex-1" />
        <span className="text-xs text-muted-foreground shrink-0 tabular-nums">
          {run.costMicros > 0
            ? formatCost(run.costMicros)
            : `${formatTokens(tokens)} tok`}
        </span>
        <span className="text-xs text-muted-foreground shrink-0">
          {new Date(run.startedAt).toLocaleString()}
        </span>
        <ChevronDown
          size={14}
          className={`text-muted-foreground shrink-0 transition-transform ${
            expanded ? 'rotate-180' : ''
          }`}
        />
      </button>
      {expanded ? (
        <div className="mt-2 flex flex-col gap-2 pl-6 text-xs">
          <div className="text-muted-foreground">
            {run.model ? `${run.model} · ` : ''}
            {formatTokens(run.inputTokens)} in ·{' '}
            {formatTokens(run.outputTokens)} out · {formatCost(run.costMicros)}{' '}
            (agent-reported)
            {run.endedAt
              ? ` · ended ${new Date(run.endedAt).toLocaleString()} (${END_LABEL[run.endReason ?? ''] ?? run.endReason})`
              : ''}
          </div>
          {run.summary ? (
            <div className="whitespace-pre-wrap break-words">{run.summary}</div>
          ) : null}
          {run.evidence.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {run.evidence.map((ev) => {
                const href = safeEvidenceHref(ev.url);
                const label = `${EVIDENCE_LABEL[ev.kind] ?? ev.kind}: ${ev.title ?? ev.url}`;
                return href ? (
                  <a
                    key={ev.url}
                    href={href}
                    target="_blank"
                    rel="noopener noreferrer nofollow"
                    className="inline-flex max-w-full items-center gap-1 truncate rounded bg-grayAlpha-100 px-1.5 py-0.5 hover:underline"
                    title={ev.url}
                  >
                    <LinkLine size={12} className="shrink-0" />
                    <span className="truncate">{label}</span>
                  </a>
                ) : (
                  <span key={ev.url} className="truncate">
                    {label}
                  </span>
                );
              })}
            </div>
          ) : null}
          <div>
            <button
              type="button"
              className="text-muted-foreground underline"
              onClick={() => setShowTrace((value) => !value)}
            >
              {showTrace ? 'Hide trace' : `Trace (${run.eventCount} lines)`}
            </button>
          </div>
          {showTrace ? <RunTrace run={run} /> : null}
        </div>
      ) : null}
    </div>
  );
});

export const RunListView = observer(() => {
  const issue = useIssueData();
  const { agentRunsStore } = useContextStore();
  const runs: AgentRunType[] = agentRunsStore.getRunsForIssue(issue.id);

  if (runs.length === 0) {
    return null;
  }
  const totals = agentRunsStore.getIssueTotals(issue.id);

  return (
    <div className="mt-2 px-6">
      <h2 className="text-md mb-1 flex items-center gap-1">
        <CodingLine size={16} className="text-muted-foreground" />
        Agent runs
        {totals ? (
          <span className="ml-2 text-xs font-normal text-muted-foreground tabular-nums">
            {totals.runs} · {formatCost(totals.costMicros)} ·{' '}
            {formatTokens(totals.tokens)} tokens
          </span>
        ) : null}
      </h2>
      <div>
        {runs.map((run) => (
          <RunRow key={run.id} run={run} />
        ))}
      </div>
    </div>
  );
});
