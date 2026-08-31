'use client';

import { Loader } from '@converge/ui/components/loader';
import { useParams } from 'next/navigation';
import React from 'react';

import { IssueViewContext } from 'components/side-issue-view';

import { convergeDatabase } from './database';
import { useContextStore } from './global-context-provider';

export const IssueStoreInit = ({
  children,
  sideView,
}: {
  children: React.ReactNode;
  sideView: boolean;
}) => {
  const [loading, setLoading] = React.useState(true);
  const { issuesHistoryStore, commentsStore } = useContextStore();

  const { issueId: paramIssueId } = useParams();
  const { issueId: viewIssueId } = React.useContext(IssueViewContext);
  const issueId = sideView ? viewIssueId : paramIssueId;

  // All data related to team
  const initIssueBasedStored = React.useCallback(async () => {
    setLoading(true);

    let issueData;
    if (!sideView) {
      const teamIdentifier = (issueId as string).split('-')[0];
      const id = (issueId as string).split('-')[1];

      const team = await convergeDatabase.teams.get({
        identifier: teamIdentifier,
      });

      issueData = await convergeDatabase.issues.get({
        number: parseInt(id),
        teamId: team.id,
      });
    } else {
      issueData = await convergeDatabase.issues.get({
        id: issueId,
      });
    }

    await issuesHistoryStore.load(issueData.id);
    await commentsStore.load(issueData.id);

    setLoading(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [issueId]);

  React.useEffect(() => {
    if (issueId) {
      initIssueBasedStored();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [issueId]);

  if (loading) {
    return <Loader height={500} />;
  }

  return <>{children}</>;
};
