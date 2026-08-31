import { types, type Instance } from 'mobx-state-tree';
import React from 'react';

import { ApplicationStore, defaultApplicationStoreValue } from './application';
import { CommentsStore } from './comments';
import { CommonStore, defaultCommonStoreValue } from './common';
import { IssueHistoryStore } from './issue-history';
import { IssuesStore } from './issues';
import { LabelsStore } from './labels';
import { TeamsStore } from './teams';
import { ViewsStore } from './views';
import { WorkflowsStore } from './workflows';
import { WorkspaceStore } from './workspace';

// v1 store set per the release scope (docs/spec 09/11): the issue loop,
// workflow, labels, members, and saved views. Forked features outside the
// active release (projects, cycles, notifications, AI, support/CRM,
// integrations, ...) were trimmed in the M3 dead-feature pass.
const StoreContextModel = types.model({
  commentsStore: CommentsStore,
  issuesHistoryStore: IssueHistoryStore,
  issuesStore: IssuesStore,
  workflowsStore: WorkflowsStore,
  labelsStore: LabelsStore,
  teamsStore: TeamsStore,
  workspaceStore: WorkspaceStore,
  applicationStore: ApplicationStore,
  viewsStore: ViewsStore,
  commonStore: CommonStore,
});

export const storeContextStore = StoreContextModel.create({
  commentsStore: {
    comments: {},
  },
  issuesHistoryStore: {
    issueHistories: {},
  },
  issuesStore: {
    teamId: undefined,
  },
  workflowsStore: {
    workflows: {},
  },
  labelsStore: {
    labels: [],
    workspaceId: undefined,
  },
  teamsStore: {
    teams: [],
    workspaceId: undefined,
  },
  workspaceStore: {
    workspace: undefined,
    usersOnWorkspaces: [],
  },
  applicationStore: {
    filters: {},
    silentFilters: {},
    identifier: '',
    displaySettings: defaultApplicationStoreValue.displaySettings,
    sidebarCollapsed: false,
  },
  viewsStore: {
    views: [],
  },
  commonStore: defaultCommonStoreValue,
});

export type StoreContextInstanceType = Instance<typeof StoreContextModel>;
export const StoreContext =
  React.createContext<null | StoreContextInstanceType>(null);

export function useContextStore(): StoreContextInstanceType {
  const store = React.useContext(StoreContext);
  if (store === null) {
    throw new Error('Store cannot be null, please add a context provider');
  }
  return store;
}
