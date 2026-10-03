import { Button } from '@converge/ui/components/button';
import { Input } from '@converge/ui/components/input';
import { useToast } from '@converge/ui/components/use-toast';
import { AddLine, PullRequestLine } from '@converge/ui/icons';
import { RiCloseLine } from '@remixicon/react';
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

import {
  useLinkPullRequestMutation,
  useUnlinkPullRequestMutation,
} from 'services/issues';

import { useContextStore } from 'store/global-context-provider';
import { UserContext } from 'store/user-context';

// spec cs:agents:prlinks — the Pull requests section: every GitHub PR
// linked to this issue, by an agent's report or by hand, with the state
// and title GitHub last returned. Titles render as text. Members link a
// PR by its URL when the server follows GitHub, and unlink any PR. The
// section hides when there is nothing to show or do.

const STATE_HINT: Partial<Record<string, string>> = {
  pending: 'Not checked on GitHub yet',
  unavailable:
    'GitHub did not return this pull request: it may be private to the configured token, deleted, or moved',
};

interface PullRequestRowProps {
  pr: IssuePullRequestType;
  onUnlink: (pr: IssuePullRequestType) => void;
  unlinking: boolean;
}

const PullRequestRow = ({ pr, onUnlink, unlinking }: PullRequestRowProps) => {
  const display = pullRequestDisplay(pr);
  const href = pullRequestHref(pr);
  const ref = `${pr.repo}#${pr.number}`;

  return (
    <div className="group/row flex items-center gap-2 border-t border-grayAlpha-100 py-2">
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
      <button
        type="button"
        title="Unlink pull request"
        aria-label={`Unlink ${ref}`}
        disabled={unlinking}
        className="shrink-0 text-muted-foreground opacity-0 transition-opacity hover:text-foreground focus-visible:opacity-100 group-hover/row:opacity-100 disabled:opacity-40"
        onClick={() => onUnlink(pr)}
      >
        <RiCloseLine size={14} />
      </button>
    </div>
  );
};

interface LinkPullRequestFormProps {
  issueId: string;
  onDone: () => void;
}

const LinkPullRequestForm = ({ issueId, onDone }: LinkPullRequestFormProps) => {
  const [url, setUrl] = React.useState('');
  const [error, setError] = React.useState<string | undefined>();
  const { mutate: link, isLoading } = useLinkPullRequestMutation({
    onSuccess: onDone,
    onError: setError,
  });

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (url.trim() === '') {
      return;
    }
    setError(undefined);
    link({ issueId, url: url.trim() });
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-1 py-2">
      <div className="flex items-center gap-2">
        <Input
          autoFocus
          value={url}
          onChange={(e) => {
            setUrl(e.target.value);
            setError(undefined);
          }}
          onKeyDown={(e) => {
            if (e.key === 'Escape') {
              e.stopPropagation();
              onDone();
            }
          }}
          placeholder="https://github.com/owner/repo/pull/123"
          aria-label="Pull request URL"
          aria-invalid={error !== undefined}
          className="h-7"
        />
        <Button
          type="submit"
          size="sm"
          variant="secondary"
          disabled={isLoading || url.trim() === ''}
        >
          Link
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
      </div>
      {error && (
        <span role="alert" className="text-xs text-destructive">
          {error}
        </span>
      )}
    </form>
  );
};

export const PullRequestListView = observer(() => {
  const issue = useIssueData();
  const user = React.useContext(UserContext);
  const { issuePullRequestsStore } = useContextStore();
  const { toast } = useToast();
  const [linking, setLinking] = React.useState(false);
  const { mutate: unlink, isLoading: unlinking } = useUnlinkPullRequestMutation(
    {
      onError: (message) => {
        toast({
          title: 'Could not unlink the pull request',
          description: message,
        });
      },
    },
  );
  const pullRequests: IssuePullRequestType[] =
    issuePullRequestsStore.getForIssue(issue.id);
  const canLink = user?.features?.githubPullRequests === true;

  if (pullRequests.length === 0 && !canLink) {
    return null;
  }

  return (
    <div className="mt-6 px-6">
      <div className="mb-1 flex items-center">
        <h2 className="text-md flex flex-1 items-center gap-1">
          <PullRequestLine size={16} className="text-muted-foreground" />
          Pull requests
        </h2>
        {canLink && !linking && (
          <Button
            size="sm"
            variant="ghost"
            className="gap-1 text-muted-foreground"
            onClick={() => setLinking(true)}
          >
            <AddLine size={14} />
            Link pull request
          </Button>
        )}
      </div>
      {linking && (
        <LinkPullRequestForm
          issueId={issue.id}
          onDone={() => setLinking(false)}
        />
      )}
      <div>
        {pullRequests.map((pr) => (
          <PullRequestRow
            key={pr.id}
            pr={pr}
            unlinking={unlinking}
            onUnlink={(target) =>
              unlink({ issueId: issue.id, linkId: target.id })
            }
          />
        ))}
      </div>
    </div>
  );
});
