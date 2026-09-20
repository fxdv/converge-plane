import { ChevronDown, DocumentLine } from '@converge/ui/icons';
import { observer } from 'mobx-react-lite';
import * as React from 'react';

import type { IssueArtifactType, User } from 'common/types';

import { useIssueData } from 'hooks/issues';
import { useUsersData } from 'hooks/users';

import { useContextStore } from 'store/global-context-provider';

// SWR-56: the Documents section — the documents the swarm has posted to
// this issue (audits, plans, manifests). Each document collapses to one
// line; expanding renders the full body preformatted (the body is plain
// text — the fenced document, never rich text). The section hides when
// the issue has no documents: a header for the ninety-five percent
// without one is noise.

const DocumentRow = observer(
  ({ artifact }: { artifact: IssueArtifactType }) => {
    const [expanded, setExpanded] = React.useState(false);
    const { users } = useUsersData(true);
    const author = users.find((user: User) => user.id === artifact.userId);

    return (
      <div className="border-t border-grayAlpha-100 py-2">
        <button
          type="button"
          onClick={() => setExpanded((value) => !value)}
          className="flex w-full items-center gap-2 text-left"
        >
          <DocumentLine size={16} className="shrink-0 text-muted-foreground" />
          <span className="text-foreground flex-1 truncate">
            {artifact.title}
          </span>
          <span className="text-xs text-muted-foreground shrink-0">
            {author?.fullname ?? 'swarm'}
          </span>
          <ChevronDown
            size={14}
            className={`text-muted-foreground shrink-0 transition-transform ${
              expanded ? 'rotate-180' : ''
            }`}
          />
        </button>
        {expanded ? (
          <pre className="bg-grayAlpha-100 mt-2 max-h-[420px] overflow-auto rounded-md p-3 text-xs leading-relaxed whitespace-pre-wrap break-words">
            {artifact.body}
          </pre>
        ) : null}
      </div>
    );
  },
);

export const ArtifactListView = observer(() => {
  const issue = useIssueData();
  const { issueArtifactsStore } = useContextStore();
  const artifacts = issueArtifactsStore.getArtifacts(issue.id);

  if (artifacts.length === 0) {
    return null;
  }

  // The sync apply order is arrival order; chronological reads better.
  // ISO stamps sort lexicographically.
  const ordered = [...artifacts].sort((a, b) =>
    a.createdAt.localeCompare(b.createdAt),
  );

  return (
    <div className="mt-2 px-6">
      <h2 className="text-md mb-1 flex items-center gap-1">
        <DocumentLine size={16} className="text-muted-foreground" />
        Documents
      </h2>
      <div>
        {ordered.map((artifact) => (
          <DocumentRow key={artifact.id} artifact={artifact} />
        ))}
      </div>
    </div>
  );
});
