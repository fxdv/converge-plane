import { TimelineItem } from '@converge/ui/components/timeline';
import { Warning } from '@converge/ui/icons';
import { observer } from 'mobx-react-lite';
import * as React from 'react';

import { type IssueHistoryType } from 'common/types';

interface PausedActivityProps {
  issueHistory: IssueHistoryType;
  fullname: string;
  showTime?: boolean;
}

/**
 * D1: the escalation event (docs/spec/12). When the quiet guards trip
 * the issue pauses: the swarm stops spending and a human must look.
 * The summary carries the guard's reason.
 */
export const PausedActivity = observer(
  ({ issueHistory, fullname, showTime = false }: PausedActivityProps) => {
    return (
      <TimelineItem hasMore date={showTime && issueHistory.updatedAt}>
        <div className="flex items-center text-amber-600 dark:text-amber-400">
          <Warning size={20} className="mr-2" />

          <div className="flex items-center">
            <span className="text-foreground mr-2">{fullname}</span>
            <span className="font-medium">paused the swarm</span>
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
