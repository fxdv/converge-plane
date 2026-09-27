import { PullRequestLine } from '@converge/ui/icons';
import { observer } from 'mobx-react-lite';
import * as React from 'react';

import {
  PULL_REQUEST_LABEL,
  PULL_REQUEST_STYLE,
  pullRequestDisplay,
  pullRequestHref,
} from 'common/lib/pull-request-format';
import type { IssuePullRequestType } from 'common/types';

import { useIssueData } from 'hooks/issues';

import { useContextStore } from 'store/global-context-provider';

// spec cs:agents:prlinks — the Pull requests section: every GitHub PR an
// agent reported on this issue, with the state and title GitHub last
// returned. Titles render as text. The section hides when there are none.

const STATE_HINT: Partial<Record<string, string>> = {
  pending: 'Not checked on GitHub yet',
  unavailable:
    'GitHub did not return this pull request: it may be private to the configured token, deleted, or moved',
};

const PullRequestRow = ({ pr }: { pr: IssuePullRequestType }) => {
  const display = pullRequestDisplay(pr);
  const href = pullRequestHref(pr);
  const ref = `${pr.repo}#${pr.number}`;

  return (
    <div className="flex items-center gap-2 border-t border-grayAlpha-100 py-2">
      <PullRequestLine
        size={16}
        className={`shrink-0 ${PULL_REQUEST_STYLE[display]}`}
      />
      {href ? (
        <a
          href={href}
          target="_blank"
          rel="noopener noreferrer nofollow"
          className="shrink-0 font-mono text-xs text-muted-foreground hover:underline"
        >
          {ref}
        </a>
      ) : (
        <span className="shrink-0 font-mono text-xs text-muted-foreground">
          {ref}
        </span>
      )}
      <span className="text-foreground truncate" title={pr.title ?? undefined}>
        {pr.title ?? ''}
      </span>
      <span className="flex-1" />
      <span
        className={`shrink-0 text-xs ${PULL_REQUEST_STYLE[display]}`}
        title={
          display === 'merged' && pr.mergedAt
            ? `Merged ${new Date(pr.mergedAt).toLocaleString()}`
            : STATE_HINT[display]
        }
      >
        {PULL_REQUEST_LABEL[display]}
      </span>
    </div>
  );
};

export const PullRequestListView = observer(() => {
  const issue = useIssueData();
  const { issuePullRequestsStore } = useContextStore();
  const pullRequests: IssuePullRequestType[] =
    issuePullRequestsStore.getForIssue(issue.id);

  if (pullRequests.length === 0) {
    return null;
  }

  return (
    <div className="mt-2 px-6">
      <h2 className="text-md mb-1 flex items-center gap-1">
        <PullRequestLine size={16} className="text-muted-foreground" />
        Pull requests
      </h2>
      <div>
        {pullRequests.map((pr) => (
          <PullRequestRow key={pr.id} pr={pr} />
        ))}
      </div>
    </div>
  );
});
