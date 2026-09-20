import { TimelineItem } from '@converge/ui/components/timeline';
import { DocumentLine } from '@converge/ui/icons';
import { observer } from 'mobx-react-lite';
import * as React from 'react';

import { type IssueHistoryType } from 'common/types';

interface ArtifactActivityProps {
  issueHistory: IssueHistoryType;
  fullname: string;
  showTime?: boolean;
}

/**
 * SWR-56: the timeline breadcrumb for a posted document. The row's
 * summary names the document ("posted a document: <title>") — the feed
 * stays step-text density; the body renders in the Documents section,
 * never here.
 */
export const ArtifactActivity = observer(
  ({ issueHistory, fullname, showTime = false }: ArtifactActivityProps) => {
    // The server writes the fixed prefix plus the document's title; keep
    // whatever follows it (a title may itself contain a colon).
    const title = (issueHistory.summary ?? '').replace(
      /^posted a document: /,
      '',
    );

    return (
      <TimelineItem hasMore date={showTime && issueHistory.updatedAt}>
        <div className="flex items-center text-muted-foreground">
          <DocumentLine size={20} className="mr-2" />

          <div className="flex items-center">
            <span className="text-foreground mr-2">{fullname}</span>
            posted a document
            {title ? (
              <span className="text-foreground ml-1 font-medium">{title}</span>
            ) : null}
          </div>
        </div>
      </TimelineItem>
    );
  },
);
