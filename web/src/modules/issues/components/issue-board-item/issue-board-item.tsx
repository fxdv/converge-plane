import type { DraggableProvided } from '@hello-pangea/dnd';

import { observer } from 'mobx-react-lite';
import React, { type CSSProperties } from 'react';

import {
  IssueAssigneeDropdown,
  IssueAssigneeDropdownVariant,
  IssuePriorityDropdown,
  IssuePriorityDropdownVariant,
  IssueStatusDropdown,
  IssueStatusDropdownVariant,
} from 'modules/issues/components';

import { ArrowForwardLine, Warning } from '@converge/ui/icons';

import type { IssueHistoryType } from 'common/types';

import { IssueViewContext } from 'components/side-issue-view';
import { useTeamWithId } from 'hooks/teams/use-current-team';

import { useUpdateIssueMutation } from 'services/issues';

import { useContextStore } from 'store/global-context-provider';

import { IssueDueDate } from '../issue-list-item/issue-duedate';
import { IssueLabels } from '../issue-list-item/issue-labels';

interface BoardIssueItemProps {
  issueId: string;
  isDragging: boolean;
  provided: DraggableProvided;
  style?: CSSProperties;
  key?: string;
  measure?: () => void;
}

function getStyle(provided: DraggableProvided, style?: CSSProperties) {
  if (!style) {
    return provided.draggableProps.style;
  }

  return {
    ...provided.draggableProps.style,
    ...style,
  };
}

export const BoardIssueItem = observer(
  ({
    issueId,
    isDragging,
    provided,
    style,
    key,
    measure,
  }: BoardIssueItemProps) => {
    const { mutate: updateIssue } = useUpdateIssueMutation({});
    const { issuesStore, applicationStore, issuesHistoryStore, swarmActivityStore } =
      useContextStore();
    const {
      openIssue,
      issueId: currentViewIssueId,
      closeIssueView,
    } = React.useContext(IssueViewContext);
    const issue = issuesStore.getIssueById(issueId);
    const team = useTeamWithId(issue.teamId);

    // spec cs:swarm:activity — Surface A: the in-flight signal. One
    // worker per agent, one record per agent; only a fresh signal
    // (within the client TTL) may render, so a stale row can never
    // claim a card. The chip is the only live decoration the board
    // carries — full disclosure lives in the activity feed.
    const liveActivity = swarmActivityStore.forIssue(issue.id);
    const showLiveChip =
      !!liveActivity &&
      liveActivity.issueId === issue.id &&
      swarmActivityStore.isFresh(liveActivity);

    // The latest handoff within 7 days: one muted line, so the card
    // shows where the swarm left off without the feed's full weight.
    let latestHandoff: IssueHistoryType | undefined;
    for (const row of issuesHistoryStore.issueHistories.get(issue.id) ?? []) {
      if (row.action !== 'handoff' || !row.summary) {
        continue;
      }
      const age = Date.now() - new Date(row.createdAt).getTime();
      if (age > 7 * 24 * 60 * 60 * 1000) {
        continue;
      }
      if (
        !latestHandoff ||
        new Date(row.createdAt).getTime() >
          new Date(latestHandoff.createdAt).getTime()
      ) {
        latestHandoff = row;
      }
    }

    const statusChange = (stateId: string) => {
      updateIssue({ id: issue.id, stateId, teamId: issue.teamId });
    };

    const assigneeChange = (assigneeId: string) => {
      updateIssue({ id: issue.id, assigneeId, teamId: issue.teamId });
    };

    const priorityChange = (priority: number) => {
      updateIssue({ id: issue.id, priority, teamId: issue.teamId });
    };

    return (
      <a
        className="p-3 flex flex-col justify-between group rounded-md bg-background-3 dark:bg-grayAlpha-200 w-[100%] gap-2 mb-2 !cursor-default hover:bg-background-3/70"
        onClick={(e) => {
          if (!e.metaKey && currentViewIssueId === issue.id) {
            closeIssueView();

            return;
          }
          openIssue(issue.id, e.metaKey);
        }}
        ref={(el) => {
          provided.innerRef(el);
          // Debounce measure to prevent infinite loop
          if (el) {
            setTimeout(measure, 0);
          }
        }}
        key={key}
        {...provided.draggableProps}
        {...provided.dragHandleProps}
        style={getStyle(provided, style)}
        data-is-dragging={isDragging}
        onMouseOver={() => {
          const { selectedIssues } = applicationStore;
          if (selectedIssues.length === 0) {
            applicationStore.setHoverIssue(issue.id);
          }
        }}
      >
        <div className="flex justify-between">
          <div className="pr-2">
            <IssueStatusDropdown
              value={issue.stateId}
              onChange={statusChange}
              variant={IssueStatusDropdownVariant.NO_BACKGROUND}
              teamIdentifier={team.identifier}
            />
          </div>
          <div className="flex items-center gap-2">
            {/* D1: escalation flag — the swarm is paused on this issue;
                a human must look (agents are blocked from acting). */}
            {issue.agentPaused && (
              <span className="inline-flex items-center gap-1 text-[10px] font-semibold uppercase tracking-wide text-amber-600 dark:text-amber-400">
                <Warning size={12} />
                needs human
              </span>
            )}
            {showLiveChip && liveActivity && (
              <span className="inline-flex items-center gap-1 text-[10px] font-semibold text-emerald-600 dark:text-emerald-400">
                <span className="relative flex size-2">
                  <span className="absolute inline-flex size-full rounded-full bg-emerald-400 opacity-75 animate-ping" />
                  <span className="relative inline-flex size-2 rounded-full bg-emerald-500" />
                </span>
                {liveActivity.agentName}
                {liveActivity.phase === 'deciding' ? ' deciding' : ' working'}
              </span>
            )}
            <div className="text-muted-foreground font-mono">{`${team.identifier}-${issue.number}`}</div>
          </div>
        </div>
        <div className="flex">
          <div className="line-clamp-2">{issue.title}</div>
        </div>

        <IssueLabels labelIds={issue.labelIds} />

        {latestHandoff?.summary && (
          <div className="flex items-start gap-1 text-[11px] text-muted-foreground line-clamp-1">
            <ArrowForwardLine size={12} className="shrink-0 mt-0.5" />
            <span className="truncate" title={latestHandoff.summary}>
              {latestHandoff.summary.length > 200
                ? `${latestHandoff.summary.slice(0, 197)}…`
                : latestHandoff.summary}
            </span>
          </div>
        )}

        <div className="flex gap-2 items-center justify-between">
          <div className="inline-flex gap-2 items-center">
            <IssuePriorityDropdown
              value={issue.priority ?? 0}
              onChange={priorityChange}
              variant={IssuePriorityDropdownVariant.NO_BACKGROUND}
            />
            <IssueDueDate dueDate={issue.dueDate} />
          </div>

          <IssueAssigneeDropdown
            value={issue.assigneeId}
            onChange={assigneeChange}
            teamId={team.id}
            variant={IssueAssigneeDropdownVariant.NO_BACKGROUND}
          />
        </div>
      </a>
    );
  },
);
