import {
  AI,
  ActivityLine,
  ArrowForwardLine,
  AssigneeLine,
  CheckLine,
  CreateIssueLine,
  DeleteLine,
  DocumentLine,
  EditLine,
  Fire,
  LabelLine,
  Warning,
} from '@converge/ui/icons';
import { cn } from '@converge/ui/lib/utils';
import type {
  IssueCommentType,
  IssueHistoryType,
  User,
  UsersOnWorkspaceType,
} from 'common/types';
import { formatDistanceToNow } from 'date-fns';
import { observer } from 'mobx-react-lite';
import * as React from 'react';

import { IssueViewContext } from 'components/side-issue-view';

import { useGetUsersQuery } from 'services/users';

import { useContextStore } from 'store/global-context-provider';
import type { SwarmActivityType } from 'store/swarm-activity';

// spec cs:swarm:activity — Surface B of the live swarm surfaces
// (docs/spec/12): the workspace activity feed.
//
// Every entry is derived client-side from stores the client already
// syncs in full (IssueHistory, IssueComment, SwarmActivity) — there is
// deliberately no /activity endpoint; a server one would be a parallel
// source of truth for data the outbox already delivers. The board stays
// signal-only (one line per card); the full disclosure lives here.
type FeedKind =
  | 'handoff'
  | 'status'
  | 'paused'
  | 'assign'
  | 'priority'
  | 'labels'
  | 'created'
  | 'deleted'
  | 'comment'
  | 'activity'
  | 'updated';

interface FeedEntry {
  id: string;
  kind: FeedKind;
  createdAt: string;
  issueId: string;
  issueRef: string; // "ENG-23"
  actorName: string;
  actorIsAgent: boolean;
  text: string;
  live?: boolean; // in-flight signal (SwarmActivity): pulsing, no archive
}

// The feed's window: newest first, bounded (the client holds the full
// workspace trace; the feed shows the working set, not the archive).
const FEED_LIMIT = 50;

/**
 * spec cs:swarm:activity
 *
 * The workspace activity feed (Surface B): one scroll of what the swarm
 * — and its human owners — did, newest first. Handoffs carry their
 * summary (the D1 trace), status moves name the destination, pauses
 * carry the guard's reason, and live signals mark what is happening
 * right now. The All/Agents/Humans filter is how a human keeps the
 * swarm's output from drowning out their own.
 */
export const ActivityFeed = observer(() => {
  const { openIssue } = React.useContext(IssueViewContext);
  const {
    issuesStore,
    issuesHistoryStore,
    commentsStore,
    workflowsStore,
    teamsStore,
    labelsStore,
    swarmActivityStore,
    workspaceStore,
  } = useContextStore();
  const { data: users } = useGetUsersQuery();
  const [filter, setFilter] = React.useState<'all' | 'agents' | 'humans'>(
    'all',
  );

  // Actor resolution: the users list carries display name + kind
  // (the Go backend sends both); usersOnWorkspaces is the fallback for
  // members the list call missed (kind only, no name).
  const actorName = React.useCallback(
    (userId?: string | null): { name: string; isAgent: boolean } => {
      if (!userId) {
        return { name: 'Someone', isAgent: false };
      }
      const user: User | undefined = users?.find((u) => u.id === userId);
      if (user) {
        return { name: user.fullname, isAgent: user.kind === 'agent' };
      }
      const member = workspaceStore.usersOnWorkspaces.find(
        (m: UsersOnWorkspaceType) => m.userId === userId,
      );
      if (member) {
        return {
          name: 'A workspace member',
          isAgent: member.role === 'AGENT' || member.role === 'BOT',
        };
      }
      return { name: 'Someone', isAgent: false };
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [users],
  );

  const issueRef = (issueId: string): string => {
    const issue = issuesStore.getIssueById(issueId);
    if (!issue) {
      return '?';
    }
    const prefix = teamsStore.getTeamWithId(issue.teamId)?.identifier;
    return prefix ? `${prefix}-${issue.number}` : `#${issue.number}`;
  };

  // TipTap doc-JSON → one line of plain text (the feed preview of a
  // comment body). Degrades to the raw string on any parse failure.
  const commentText = (body?: string | null): string => {
    if (!body) {
      return '';
    }
    let text = '';
    try {
      const doc = JSON.parse(body);
      const parts: string[] = [];
      const walk = (node: any): void => {
        if (typeof node?.text === 'string') {
          parts.push(node.text);
        }
        if (Array.isArray(node?.content)) {
          for (const child of node.content) {
            walk(child);
          }
        }
      };
      walk(doc);
      text = parts.join(' ').replace(/\s+/g, ' ').trim();
    } catch {
      text = body;
    }
    return text.length > 160 ? `${text.slice(0, 157)}…` : text;
  };

  // --- derive the feed (in render: the observer tracks the stores) ---

  const entries: FeedEntry[] = [];

  // History rows: the durable trace (handoffs, moves, pauses, ...).
  issuesHistoryStore.issueHistories.forEach((rows: IssueHistoryType[]) => {
    rows.forEach((row: IssueHistoryType) => {
      if (!row.issueId) {
        return;
      }
      const entry: FeedEntry = {
        id: `h:${row.id}`,
        kind: 'updated',
        createdAt: row.createdAt,
        issueId: row.issueId,
        issueRef: issueRef(row.issueId),
        actorName: '',
        actorIsAgent: false,
        text: '',
      };
      const actor = actorName(row.userId);
      entry.actorName = actor.name;
      entry.actorIsAgent = actor.isAgent;

      const stateName = (stateId?: string | null): string =>
        stateId
          ? (workflowsStore.getWorkflowWithId(stateId)?.name ?? '')
          : '';
      const assigneeName = (id?: string | null): string =>
        id ? actorName(id).name : '';

      if (row.action === 'handoff') {
        entry.kind = 'handoff';
        entry.text =
          row.summary ||
          (row.toAssigneeId
            ? `handed off to ${assigneeName(row.toAssigneeId)}`
            : 'handed off');
      } else if (row.action === 'paused') {
        entry.kind = 'paused';
        entry.text = row.summary || 'paused for a human';
      } else if (row.action === 'created') {
        entry.kind = 'created';
        entry.text = 'created the issue';
      } else if (row.action === 'deleted') {
        entry.kind = 'deleted';
        entry.text = 'deleted the issue';
      } else if (row.fromStateId || row.toStateId) {
        // Covers both `status_changed` (runtime) and `updated:status`
        // (human board drag): the row's from/to are the discriminator
        // (the wire carries no `field`).
        entry.kind = 'status';
        const to = stateName(row.toStateId);
        entry.text = to ? `moved to ${to}` : 'moved states';
      } else if (
        (row.fromAssigneeId ?? null) !== (row.toAssigneeId ?? null) &&
        (row.fromAssigneeId || row.toAssigneeId)
      ) {
        entry.kind = 'assign';
        entry.text = row.toAssigneeId
          ? `assigned to ${assigneeName(row.toAssigneeId)}`
          : 'unassigned the issue';
      } else if (
        (row.fromPriority ?? null) !== (row.toPriority ?? null)
      ) {
        entry.kind = 'priority';
        entry.text = 'changed the priority';
      } else if (
        row.addedLabelIds.length > 0 ||
        row.removedLabelIds.length > 0
      ) {
        entry.kind = 'labels';
        const names = (ids: string[]) =>
          ids
            .map((id) => labelsStore.getLabelWithId(id)?.name)
            .filter(Boolean)
            .slice(0, 2)
            .map((n) => `"${n}"`)
            .join(', ');
        if (row.addedLabelIds.length > 0 && row.removedLabelIds.length > 0) {
          entry.text = 'updated the labels';
        } else if (row.addedLabelIds.length > 0) {
          entry.text = `added label${names(row.addedLabelIds) ? ` ${names(row.addedLabelIds)}` : ''}`;
        } else {
          entry.text = `removed label${names(row.removedLabelIds) ? ` ${names(row.removedLabelIds)}` : ''}`;
        }
      } else {
        entry.text = 'updated the issue';
      }
      entries.push(entry);
    });
  });

  // Comments: the swarm's (and the team's) running words.
  commentsStore.comments.forEach((comments: IssueCommentType[]) => {
    comments.forEach((comment: IssueCommentType) => {
      const text = commentText(comment.body);
      if (!text) {
        return;
      }
      const actor = actorName(comment.userId);
      entries.push({
        id: `c:${comment.id}`,
        kind: 'comment',
        createdAt: comment.createdAt,
        issueId: comment.issueId,
        issueRef: issueRef(comment.issueId),
        actorName: actor.name,
        actorIsAgent: actor.isAgent,
        text: `commented "${text}"`,
      });
    });
  });

  // Live signals: what the swarm is doing right now (top of the feed;
  // the entry vanishes with the signal — it is a status, not a record).
  swarmActivityStore.activities.forEach((activity: SwarmActivityType) => {
    if (!swarmActivityStore.isFresh(activity)) {
      return;
    }
    entries.push({
      id: `a:${activity.agentId}`,
      kind: 'activity',
      createdAt: activity.since,
      issueId: activity.issueId,
      issueRef: `${activity.issuePrefix}-${activity.issueNumber}`,
      actorName: activity.agentName,
      actorIsAgent: true,
      text: `is ${activity.phase === 'deciding' ? 'deciding' : 'working'}`,
      live: true,
    });
  });

  entries.sort(
    (a, b) =>
      new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime(),
  );
  const visible = entries
    .filter((entry) =>
      filter === 'all'
        ? true
        : filter === 'agents'
          ? entry.actorIsAgent
          : !entry.actorIsAgent,
    )
    .slice(0, FEED_LIMIT);

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="px-4 py-3 border-b border-grayAlpha-100 dark:border-grayAlpha-300 flex items-center gap-2">
        <ActivityLine size={16} />
        <h2 className="text-sm font-semibold">Activity</h2>
        <div className="ml-auto flex rounded-md border border-grayAlpha-200 dark:border-grayAlpha-300 overflow-hidden">
          {(['all', 'agents', 'humans'] as const).map((value) => (
            <button
              key={value}
              type="button"
              onClick={() => setFilter(value)}
              className={cn(
                'px-2 py-0.5 text-xs capitalize',
                filter === value
                  ? 'bg-grayAlpha-100 dark:bg-grayAlpha-300 text-foreground'
                  : 'text-grayAlpha-500 dark:text-grayAlpha-400 hover:text-foreground',
              )}
            >
              {value}
            </button>
          ))}
        </div>
      </div>

      <div className="overflow-auto flex-1">
        {visible.length === 0 ? (
          <div className="p-4 text-sm text-grayAlpha-500 dark:text-grayAlpha-400">
            {filter === 'agents'
              ? 'No agent activity yet. Assign an issue to an agent and watch the swarm work.'
              : filter === 'humans'
                ? 'No team activity yet.'
                : 'Nothing here yet — assign an issue to an agent and watch the swarm work.'}
          </div>
        ) : (
          visible.map((entry) => (
            <FeedRow
              key={entry.id}
              entry={entry}
              onOpen={
                issuesStore.getIssueById(entry.issueId)
                  ? () => openIssue(entry.issueId)
                  : undefined
              }
            />
          ))
        )}
      </div>
    </div>
  );
});

// The icon + accent per feed kind (muted by default: the feed must read
// as a stream, not an alarm board; only pauses and live signals accent).
const kindIcon = (entry: FeedEntry) => {
  const Icon =
    entry.kind === 'handoff'
      ? ArrowForwardLine
      : entry.kind === 'status'
        ? CheckLine
        : entry.kind === 'paused'
          ? Warning
          : entry.kind === 'assign'
            ? AssigneeLine
            : entry.kind === 'comment'
              ? DocumentLine
              : entry.kind === 'priority'
                ? Fire
                : entry.kind === 'labels'
                  ? LabelLine
                  : entry.kind === 'created'
                    ? CreateIssueLine
                    : entry.kind === 'deleted'
                      ? DeleteLine
                      : entry.kind === 'activity'
                        ? AI
                        : EditLine;
  const accent =
    entry.kind === 'paused'
      ? 'text-amber-600 dark:text-amber-400'
      : entry.kind === 'activity'
        ? 'text-emerald-600 dark:text-emerald-400'
        : entry.kind === 'deleted'
          ? 'text-red-500/70'
          : 'text-grayAlpha-400 dark:text-grayAlpha-500';
  return (
    <span className="relative flex items-center justify-center size-5 shrink-0">
      {entry.live && (
        <span className="absolute inline-flex size-full rounded-full bg-emerald-400 opacity-60 animate-ping" />
      )}
      <Icon size={15} className={cn('relative', accent)} />
    </span>
  );
};

const FeedRow = ({
  entry,
  onOpen,
}: {
  entry: FeedEntry;
  onOpen?: () => void;
}) => {
  const content = (
    <>
      {kindIcon(entry)}
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-1.5 min-w-0">
          <span
            className={cn(
              'text-sm font-medium truncate',
              entry.live && 'text-emerald-600 dark:text-emerald-400',
            )}
          >
            {entry.actorName}
          </span>
          <span className="text-sm text-muted-foreground truncate">
            {entry.text}
          </span>
        </div>
      </div>
      <div className="shrink-0 flex flex-col items-end gap-0.5">
        <span className="font-mono text-xs text-grayAlpha-500 dark:text-grayAlpha-400">
          {entry.issueRef}
        </span>
        <span className="text-[11px] text-grayAlpha-400 dark:text-grayAlpha-500">
          {formatDistanceToNow(new Date(entry.createdAt), { addSuffix: true })}
        </span>
      </div>
    </>
  );
  return onOpen ? (
    <button
      type="button"
      onClick={onOpen}
      className="w-full flex items-center gap-2.5 px-4 py-2.5 border-b border-grayAlpha-100 dark:border-grayAlpha-300 text-left hover:bg-grayAlpha-50 dark:hover:bg-grayAlpha-200/50 transition-colors"
    >
      {content}
    </button>
  ) : (
    <div
      className="w-full flex items-center gap-2.5 px-4 py-2.5 border-b border-grayAlpha-100 dark:border-grayAlpha-300 opacity-60"
      title="This issue no longer exists"
    >
      {content}
    </div>
  );
};
