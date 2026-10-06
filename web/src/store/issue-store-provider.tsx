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
  const loadGeneration = React.useRef(0);
  const {
    issuesHistoryStore,
    commentsStore,
    issueArtifactsStore,
    issuesStore,
    teamsStore,
  } = useContextStore();

  const { issueId: paramIssueId } = useParams();
  const { issueId: viewIssueId } = React.useContext(IssueViewContext);
  const issueId = sideView ? viewIssueId : paramIssueId;

  React.useEffect(() => {
    if (!issueId) {
      setLoadState('missing');
      return;
    }

    const generation = ++loadGeneration.current;
    setLoadState('loading');

    void (async () => {
      let issueData;
      if (!sideView) {
        const teamIdentifier = (issueId as string).split('-')[0];
        const team = teamsStore.getTeamWithIdentifier(teamIdentifier);
        if (!team) {
          if (generation === loadGeneration.current) {
            setLoadState('missing');
          }
          return;
        }
        issueData = issuesStore.getIssueByNumber(issueId as string, team.id);
      } else {
        issueData = issuesStore.getIssueById(issueId as string);
      }

      if (!issueData?.id) {
        if (generation === loadGeneration.current) {
          setLoadState('missing');
        }
        return;
      }

      await issuesHistoryStore.load(issueData.id);
      await commentsStore.load(issueData.id);
      await issueArtifactsStore.load(issueData.id);

      if (generation === loadGeneration.current) {
        setLoadState('ready');
      }
    })();
  }, [
    issueId,
    sideView,
    issuesHistoryStore,
    commentsStore,
    issueArtifactsStore,
    issuesStore,
    teamsStore,
  ]);

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
