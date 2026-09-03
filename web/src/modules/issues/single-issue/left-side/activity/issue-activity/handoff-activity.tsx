import { TimelineItem } from '@converge/ui/components/timeline';
import { ArrowForwardLine, UserLine } from '@converge/ui/icons';
import { observer } from 'mobx-react-lite';
import * as React from 'react';

import { type IssueHistoryType, type User } from 'common/types';

import { useUsersData } from 'hooks/users';

interface HandoffActivityProps {
  issueHistory: IssueHistoryType;
  fullname: string;
  showTime?: boolean;
}

/**
 * D1: the handoff timeline item (docs/spec/12). A handoff row carries
 * fromAssigneeId/toAssigneeId plus the summary note — the trace that
 * lets a human reconstruct what the swarm did, one step at a time.
 */
export const HandoffActivity = observer(
  ({ issueHistory, fullname, showTime = false }: HandoffActivityProps) => {
    const { users } = useUsersData(true);
    const target = React.useMemo(
      () =>
        issueHistory.toAssigneeId
          ? (users.find((user: User) => user.id === issueHistory.toAssigneeId) ??
            undefined)
          : undefined,
      // eslint-disable-next-line react-hooks/exhaustive-deps
      [issueHistory.toAssigneeId, users],
    );

    return (
      <TimelineItem hasMore date={showTime && issueHistory.updatedAt}>
        <div className="flex items-center text-muted-foreground">
          <ArrowForwardLine size={20} className="mr-2" />

          <div className="flex items-center">
            <span className="text-foreground mr-2">{fullname}</span>
            handed off
            {target ? (
              <>
                <span className="mx-2 inline-flex items-center gap-1 text-foreground">
                  <UserLine size={14} className="opacity-70" />
                  {target.fullname}
                </span>
                {target.kind === 'agent' && (
                  <span className="ml-1 text-xs uppercase tracking-wide text-muted-foreground">
                    agent
                  </span>
                )}
              </>
            ) : (
              <span className="text-foreground mx-2">to a new owner</span>
            )}
          </div>
        </div>
        {issueHistory.summary ? (
          <p className="mt-1 ml-6 max-w-[480px] text-sm text-foreground/80 whitespace-pre-wrap break-words">
            {issueHistory.summary}
          </p>
        ) : null}
      </TimelineItem>
    );
  },
);
