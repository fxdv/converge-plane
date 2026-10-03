import { useRouter } from 'next/router';
import React from 'react';
import { useHotkeys } from 'react-hotkeys-hook';

import { AppLayout } from 'common/layouts/app-layout';
import { SCOPES } from 'common/scopes';

import { IssueViewContext } from 'components/side-issue-view';
import { useScope } from 'hooks';
import { useCurrentTeam } from 'hooks/teams';

import { IssueView } from './issue-view';

export function SingleIssue() {
  useScope(SCOPES.AllIssues);
  useScope(SCOPES.SingleIssues);
  const router = useRouter();
  const team = useCurrentTeam();

  useHotkeys(
    's',
    (event) => {
      const button = document.querySelector<HTMLElement>(
        '#issue-status button',
      );
      button?.focus();
      button?.click();
      event.preventDefault();
    },
    { scopes: [SCOPES.SingleIssues] },
  );
  useHotkeys(
    'm',
    (event) => {
      document
        .querySelector<HTMLElement>('#issue-comment [contenteditable]')
        ?.focus();
      event.preventDefault();
    },
    { scopes: [SCOPES.SingleIssues] },
  );
  useHotkeys(
    'u',
    (event) => {
      const slug = router.query.workspaceSlug;
      const path = team
        ? `/${slug}/team/${team.identifier}/all#board`
        : `/${slug}/all#board`;
      void router.push(path);
      event.preventDefault();
    },
    { scopes: [SCOPES.SingleIssues] },
  );
  const { closeIssueView } = React.useContext(IssueViewContext);

  React.useEffect(() => {
    return () => {
      closeIssueView();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return <IssueView />;
}

SingleIssue.getLayout = function getLayout(page: React.ReactElement) {
  return <AppLayout>{page}</AppLayout>;
};
