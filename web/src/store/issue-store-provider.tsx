'use client';

import { Loader } from '@converge/ui/components/loader';
import { useParams } from 'next/navigation';
import React from 'react';

import { IssueViewContext } from 'components/side-issue-view';

import { useContextStore } from './global-context-provider';

export const IssueStoreInit = ({
  children,
  sideView,
}: {
  children: React.ReactNode;
  sideView: boolean;
}) => {
  const [loading, setLoading] = React.useState(true);
  const { issuesHistoryStore, commentsStore, issueArtifactsStore, issuesStore, teamsStore } =
    useContextStore();

  const { issueId: paramIssueId } = useParams();
  const { issueId: viewIssueId } = React.useContext(IssueViewContext);
  const issueId = sideView ? viewIssueId : paramIssueId;

  const initIssueBasedStored = React.useCallback(async () => {
    setLoading(true);

    let issueData;
    if (!sideView) {
      const teamIdentifier = (issueId as string).split('-')[0];
      const team = teamsStore.getTeamWithIdentifier(teamIdentifier);
      if (!team) {
        setLoading(false);
        return;
      }
      issueData = issuesStore.getIssueByNumber(issueId as string, team.id);
    } else {
      issueData = issuesStore.getIssueById(issueId as string);
    }

    if (!issueData?.id) {
      setLoading(false);
      return;
    }

    await issuesHistoryStore.load(issueData.id);
    await commentsStore.load(issueData.id);
    await issueArtifactsStore.load(issueData.id);

    setLoading(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [issueId]);

  React.useEffect(() => {
    if (issueId) {
      void initIssueBasedStored();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [issueId]);

  if (loading) {
    return <Loader height={500} />;
  }

  return <>{children}</>;
};
