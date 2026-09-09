import { useToast } from '@converge/ui/components/use-toast';
import {
  BlocksFill,
  BlockedFill,
  DuplicateLine,
  RelatedIssueLine,
} from '@converge/ui/icons';
import { RiCloseLine, RiFileTransferLine } from '@remixicon/react';
import { observer } from 'mobx-react-lite';
import { useRouter } from 'next/router';
import * as React from 'react';

import {
  IssueRelationEnum,
  type IssueRelationType,
  type IssueType,
} from 'common/types';

import { useTeamWithId } from 'hooks/teams';

import { useDeleteIssueRelationMutation } from 'services/issues';

import { useContextStore } from 'store/global-context-provider';

// spec cs:api:relations — the current-state surface. The timeline
// (RelatedActivity) shows the history; this section shows the issue's
// live edges, each from the reader's perspective, with removal.

interface IconEntry {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  icon: any;
  color: string;
  verb: string;
}

const RELATION_META: Record<string, IconEntry> = {
  [IssueRelationEnum.BLOCKS]: {
    icon: BlocksFill,
    color: 'text-red-500',
    verb: 'Blocks',
  },
  [IssueRelationEnum.BLOCKED]: {
    icon: BlockedFill,
    color: 'text-red-500',
    verb: 'Blocked by',
  },
  [IssueRelationEnum.RELATED]: {
    icon: RelatedIssueLine,
    color: 'text-muted-foreground',
    verb: 'Related to',
  },
  [IssueRelationEnum.DUPLICATE]: {
    icon: DuplicateLine,
    color: 'text-muted-foreground',
    verb: 'Duplicates',
  },
  [IssueRelationEnum.DUPLICATE_OF]: {
    icon: DuplicateLine,
    color: 'text-muted-foreground',
    verb: 'Duplicate of',
  },
  [IssueRelationEnum.SIMILAR]: {
    icon: RiFileTransferLine,
    color: 'text-muted-foreground',
    verb: 'Similar to',
  },
};

interface RelatedRowProps {
  relation: IssueRelationType;
  onDelete: (relationId: string) => void;
  deleting: boolean;
}

const RelatedRow = observer(
  function RelatedRowInner({ relation, onDelete, deleting }: RelatedRowProps) {
    const meta = RELATION_META[relation.type] ?? {
      icon: RelatedIssueLine,
      color: 'text-muted-foreground',
      verb: 'Linked to',
    };
    const Icon = meta.icon;
    const { query: { workspaceSlug } } = useRouter();
    const { issuesStore } = useContextStore();
    const relatedIssue = issuesStore.getIssueById(relation.relatedIssueId);
    const team = useTeamWithId(relatedIssue ? relatedIssue.teamId : '');

    return (
      <div className="flex items-center gap-2 px-6 py-1.5 group/row">
        <Icon size={14} className={meta.color} />
        {relatedIssue ? (
          <a
            href={`/${workspaceSlug}/issue/${team?.identifier ?? ''}-${relatedIssue.number}`}
            className="flex items-center gap-1 min-w-0 flex-1"
            onClick={(e) => e.stopPropagation()}
          >
            <span className="text-muted-foreground">{meta.verb}</span>
            <span className="text-foreground font-mono">
              {team?.identifier ?? ''}-{relatedIssue.number}
            </span>
            <span className="text-foreground truncate max-w-[160px]">
              {relatedIssue.title}
            </span>
          </a>
        ) : (
          <span className="text-muted-foreground flex-1">
            {meta.verb} an unavailable issue
          </span>
        )}
        <button
          type="button"
          title="Remove relation"
          disabled={deleting}
          className="opacity-0 group-hover/row:opacity-100 transition-opacity text-muted-foreground hover:text-foreground disabled:opacity-40"
          onClick={(e) => {
            e.stopPropagation();
            onDelete(relation.id);
          }}
        >
          <RiCloseLine size={14} />
        </button>
      </div>
    );
  },
);

interface RelatedIssueViewProps {
  issue: IssueType;
}

export const RelatedIssueView = observer(
  function RelatedIssueViewInner({ issue }: RelatedIssueViewProps) {
    const { toast } = useToast();
    const { mutate: deleteRelation, isLoading: deleting } =
      useDeleteIssueRelationMutation({
        onError: (message) => {
          toast({
            title: 'Could not remove the relation',
            description: message,
          });
        },
      });

    const relations: IssueRelationType[] = issue.relations ?? [];
    if (relations.length === 0) {
      return null;
    }

    return (
      <div className="py-2">
        <div className="px-6 py-1 text-md text-foreground">Related</div>
        {relations.map((relation) => (
          <RelatedRow
            key={relation.id}
            relation={relation}
            deleting={deleting}
            onDelete={(relationId) =>
              deleteRelation({ issueId: issue.id, relationId })
            }
          />
        ))}
      </div>
    );
  },
);
