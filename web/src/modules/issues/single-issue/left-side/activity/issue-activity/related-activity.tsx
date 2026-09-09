import { RiFileTransferLine } from '@remixicon/react';
import { TimelineItem } from '@converge/ui/components/timeline';
import { BlockedFill, BlocksFill, DuplicateLine } from '@converge/ui/icons';
import { cn } from '@converge/ui/lib/utils';
import { useRouter } from 'next/router';

import { IssueRelationEnum } from 'common/types';
import { type IssueHistoryType } from 'common/types';

import { useTeamWithId } from 'hooks/teams';

import { useContextStore } from 'store/global-context-provider';

// spec cs:api:relations — the timeline entry for a relation change.
// Every type the server can emit has a branch here (the render-total
// policy): a value without an entry would crash the icon lookup.

interface StatusActivityProps {
  issueHistory: IssueHistoryType;
  fullname: string;
  showTime?: boolean;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const ICON_MAP: Record<string, { icon: any; color: string }> = {
  [IssueRelationEnum.BLOCKS]: {
    icon: BlocksFill,
    color: 'text-red-500',
  },
  [IssueRelationEnum.BLOCKED]: {
    icon: BlockedFill,
    color: 'text-red-500',
  },
  [IssueRelationEnum.RELATED]: {
    icon: RiFileTransferLine,
    color: 'text-muted-foreground',
  },
  [IssueRelationEnum.DUPLICATE]: {
    icon: DuplicateLine,
    color: 'text-muted-foreground',
  },
  [IssueRelationEnum.DUPLICATE_OF]: {
    icon: DuplicateLine,
    color: 'text-muted-foreground',
  },
  [IssueRelationEnum.SIMILAR]: {
    icon: RiFileTransferLine,
    color: 'text-muted-foreground',
  },
};

export function RelatedActivity({
  issueHistory,
  fullname,
  showTime = false,
}: StatusActivityProps) {
  const relatedChanges = issueHistory.relationChanges;
  const Icon =
    ICON_MAP[relatedChanges.type] ??
    ICON_MAP[IssueRelationEnum.RELATED];
  const {
    query: { workspaceSlug },
  } = useRouter();
  const { issuesStore } = useContextStore();
  const relatedIssue = issuesStore.getIssueById(relatedChanges.relatedIssueId);
  const team = useTeamWithId(relatedIssue ? relatedIssue.teamId : '');

  // The related issue may have been deleted since the change (or not
  // synced to this client yet): degrade to a plain mention, never crash.
  const relatedRef = relatedIssue ? (
    <a
      className="text-foreground mx-1"
      href={`/${workspaceSlug}/issue/${team?.identifier ?? ''}-${relatedIssue.number}`}
    >
      {team?.identifier ?? ''}-{relatedIssue.number}
    </a>
  ) : (
    <span className="text-muted-foreground mx-1 italic">
      a removed issue
    </span>
  );

  const getText = () => {
    if (relatedChanges.type === IssueRelationEnum.RELATED) {
      return (
        <div className="flex items-center">
          <span className="text-foreground mr-2">{fullname}</span>
          <span>
            {relatedChanges.isDeleted ? 'removed' : 'added'} related issue
          </span>
          {relatedRef}
        </div>
      );
    }

    if (relatedChanges.type === IssueRelationEnum.BLOCKED) {
      return (
        <div className="flex items-center">
          <span className="text-foreground mr-2">{fullname}</span>
          <span>
            {relatedChanges.isDeleted ? 'removed' : 'marked'} this issue as
            being blocked by
          </span>
          {relatedRef}
        </div>
      );
    }

    if (relatedChanges.type === IssueRelationEnum.BLOCKS) {
      return (
        <div className="flex items-center">
          <span className="text-foreground mr-2">{fullname}</span>
          <span>
            {relatedChanges.isDeleted ? 'removed' : 'marked'} this issue as
            blocking
          </span>
          {relatedRef}
        </div>
      );
    }

    if (relatedChanges.type === IssueRelationEnum.DUPLICATE_OF) {
      return (
        <div className="flex items-center">
          <span className="text-foreground mr-2">{fullname}</span>
          <span>
            {relatedChanges.isDeleted ? 'removed' : 'marked'} this issue as
            duplicate of
          </span>
          {relatedRef}
        </div>
      );
    }

    if (relatedChanges.type === IssueRelationEnum.DUPLICATE) {
      return (
        <div className="flex items-center">
          <span className="text-foreground mr-2">{fullname}</span>
          <span>
            {relatedChanges.isDeleted ? 'removed' : 'marked'} this issue as
            duplicated by
          </span>
          {relatedRef}
        </div>
      );
    }

    if (relatedChanges.type === IssueRelationEnum.SIMILAR) {
      return (
        <div className="flex items-center">
          <span className="text-foreground mr-2">{fullname}</span>
          <span>
            {relatedChanges.isDeleted ? 'removed' : 'marked'} this issue as
            similar to
          </span>
          {relatedRef}
        </div>
      );
    }

    // An unknown type from a future server: still render the row, never
    // crash the feed.
    return (
      <div className="flex items-center">
        <span className="text-foreground mr-2">{fullname}</span>
        <span>made a relation change</span>
        {relatedRef}
      </div>
    );
  };

  return (
    <TimelineItem
      key={`${issueHistory.id}-related`}
      hasMore
      date={showTime && issueHistory.updatedAt}
    >
      <div className="flex items-center text-muted-foreground">
        <div className="h-[15px] w-[20px] flex items-center justify-center mr-2">
          <Icon.icon
            size={20}
            className={cn(
              'text-muted-foreground',
              !relatedChanges.isDeleted && Icon.color,
            )}
          />
        </div>

        {getText()}
      </div>
    </TimelineItem>
  );
}
