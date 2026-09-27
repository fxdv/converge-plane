// Display helpers for tracked GitHub pull requests (spec cs:agents:prlinks).

import type { IssuePullRequestType } from 'common/types';

// What a PR shows as: GitHub's state, with an open draft told apart.
export type PullRequestDisplay =
  'open' | 'draft' | 'merged' | 'closed' | 'pending' | 'unavailable';

export function pullRequestDisplay(
  pr: Pick<IssuePullRequestType, 'state' | 'draft'>,
): PullRequestDisplay {
  switch (pr.state) {
    case 'open':
      return pr.draft ? 'draft' : 'open';
    case 'merged':
    case 'closed':
    case 'pending':
      return pr.state;
    default:
      return 'unavailable';
  }
}

export const PULL_REQUEST_LABEL: Record<PullRequestDisplay, string> = {
  open: 'open',
  draft: 'draft',
  merged: 'merged',
  closed: 'closed',
  pending: 'checking',
  unavailable: 'unavailable',
};

// GitHub's own palette: green open, gray draft, purple merged, red closed.
export const PULL_REQUEST_STYLE: Record<PullRequestDisplay, string> = {
  open: 'text-emerald-600 dark:text-emerald-400',
  draft: 'text-muted-foreground',
  merged: 'text-violet-600 dark:text-violet-400',
  closed: 'text-red-600 dark:text-red-400',
  pending: 'text-muted-foreground',
  unavailable: 'text-muted-foreground',
};

// The one state a card shows for all of an issue's PRs: whatever still
// needs work wins, so the chip turns merged only when nothing is open
// or unchecked. Null when there are none.
export function pullRequestHeadline(
  prs: ReadonlyArray<Pick<IssuePullRequestType, 'state' | 'draft'>>,
): PullRequestDisplay | null {
  if (prs.length === 0) {
    return null;
  }
  const seen = new Set(prs.map(pullRequestDisplay));
  for (const state of [
    'open',
    'draft',
    'pending',
    'merged',
    'closed',
  ] as const) {
    if (seen.has(state)) {
      return state;
    }
  }
  return 'unavailable';
}

const REPO = /^[a-z0-9][a-z0-9-]{0,38}\/[a-z0-9._-]{1,100}$/;

// The link is rebuilt from the validated repo and number rather than
// taken from the wire, so it can only ever point at a github.com PR.
export function pullRequestHref(
  pr: Pick<IssuePullRequestType, 'repo' | 'number'>,
): string | null {
  if (
    !REPO.test(pr.repo) ||
    pr.repo.endsWith('/.') ||
    pr.repo.endsWith('/..') ||
    !Number.isSafeInteger(pr.number) ||
    pr.number <= 0
  ) {
    return null;
  }
  return `https://github.com/${pr.repo}/pull/${pr.number}`;
}
