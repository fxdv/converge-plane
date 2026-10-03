import { observer } from 'mobx-react-lite';
import * as React from 'react';

import { isEmpty } from 'modules/issues/filters-view/filter-utils';
import { useFilterIssues } from 'modules/issues/issues-utils';

import { useCurrentTeam } from 'hooks/teams';
import { useComputedWorkflows } from 'hooks/workflows';

import { ViewEnum } from 'store/application';
import { useContextStore } from 'store/global-context-provider';

import { AssigneeView } from './views/assignee';
import { CategoryView } from './views/category';
import { LabelView } from './views/label';
import { PriorityView } from './views/priority';
import { TableView } from './views/table-view';
import { TeamView } from './views/team';

const FilteredEmpty = observer(() => {
  const team = useCurrentTeam();
  const { applicationStore, issuesStore } = useContextStore();
  const { workflows } = useComputedWorkflows();
  const issues = issuesStore.getIssues({ teamId: team?.id });
  const filtered = useFilterIssues(issues, workflows);

  if (isEmpty(applicationStore.filters) || filtered.length > 0) {
    return null;
  }

  return (
    <p className="px-6 py-6 text-sm text-muted-foreground">
      No issues match this filter. Clear the filter to see the board.
    </p>
  );
});

export const ListView = observer(() => {
  const { applicationStore } = useContextStore();

  const {
    displaySettings: { view },
  } = applicationStore;
  const grouping = applicationStore.displaySettings.grouping;

  const body =
    view === ViewEnum.sheet ? (
      <TableView />
    ) : grouping === 'assignee' ? (
      <AssigneeView />
    ) : grouping === 'priority' ? (
      <PriorityView />
    ) : grouping === 'label' ? (
      <LabelView />
    ) : grouping === 'team' ? (
      <TeamView />
    ) : (
      <CategoryView />
    );

  return (
    <>
      <FilteredEmpty />
      {body}
    </>
  );
});
