'use client';

import { Loader } from '@converge/ui/components/loader';
import { useParams } from 'next/navigation';
import React from 'react';

import { IssueViewContext } from 'components/side-issue-view';

import { useContextStore } from './global-context-provider';

type LoadState = 'loading' | 'ready' | 'missing';

export const IssueStoreInit = ({
  children,
  sideView,
}: {
  children: React.ReactNode;
  sideView: boolean;
}) => {
  const [loadState, setLoadState] = React.useState<LoadState>('loading');
  const { issuesStore, teamsStore } = useContextStore();

  const { issueId: paramIssueId } = useParams();
  const { issueId: viewIssueId } = React.useContext(IssueViewContext);
  const issueId = sideView ? viewIssueId : paramIssueId;

  React.useEffect(() => {
    if (!issueId) {
      setLoadState('missing');
      return;
    }

    // Issue-scoped models (history, comments, artifacts) are memory-only
    // (R-9): the sync feed fills their stores, so resolving the issue
    // row is the whole load.
    let issueData;
    if (!sideView) {
      const teamIdentifier = (issueId as string).split('-')[0];
      const team = teamsStore.getTeamWithIdentifier(teamIdentifier);
      if (!team) {
        setLoadState('missing');
        return;
      }
      issueData = issuesStore.getIssueByNumber(issueId as string, team.id);
    } else {
      issueData = issuesStore.getIssueById(issueId as string);
    }

    setLoadState(issueData?.id ? 'ready' : 'missing');
  }, [issueId, sideView, issuesStore, teamsStore]);

  if (loadState === 'loading') {
    return <Loader height={500} />;
  }

  if (loadState === 'missing') {
    return (
      <div
        className="flex flex-col items-center justify-center gap-2 p-8 text-muted-foreground"
        role="status"
      >
        <p className="text-sm">This issue is not available yet.</p>
        <p className="text-xs">
          It may still be syncing, or the link may be wrong.
        </p>
      </div>
    );
  }

  return <>{children}</>;
};
