'use client';

import Dexie from 'dexie';

import type {
  IssueCommentType,
  IssueHistoryType,
  IssueType,
  LabelType,
  TeamType,
  UsersOnWorkspaceType,
  ViewType,
  WorkflowType,
  WorkspaceType,
} from 'common/types';

import { MODELS } from './models';

// Local object-cache tables for the models the client syncs (v1 scope).
// Forked models outside the active release were trimmed in the M3
// dead-feature pass. R-9 (data-layer workstream) replaces this cache with
// server-authoritative state + bounded caching.
export class TegonDatabase extends Dexie {
  workspaces: Dexie.Table<WorkspaceType, string>;
  labels: Dexie.Table<LabelType, string>;
  teams: Dexie.Table<TeamType, string>;
  workflows: Dexie.Table<WorkflowType, string>;
  issues: Dexie.Table<IssueType, string>;
  issueHistory: Dexie.Table<IssueHistoryType, string>;
  comments: Dexie.Table<IssueCommentType, string>;
  usersOnWorkspaces: Dexie.Table<UsersOnWorkspaceType, string>;
  views: Dexie.Table<ViewType, string>;

  constructor(databaseName: string) {
    super(databaseName);

    this.version(19).stores({
      [MODELS.Workspace]: 'id,createdAt,updatedAt,name,slug,preferences',
      [MODELS.Label]:
        'id,createdAt,updatedAt,name,color,description,workspaceId,groupId,teamId',
      [MODELS.Team]:
        'id,createdAt,updatedAt,name,identifier,workspaceId,preferences,currentCycle',
      [MODELS.Workflow]:
        'id,createdAt,updatedAt,name,position,color,category,teamId,description',
      [MODELS.Issue]:
        'id,createdAt,updatedAt,title,number,description,priority,dueDate,sortOrder,estimate,teamId,createdById,assigneeId,labelIds,parentId,stateId,sourceMetadata.projectId,projectMilestoneId,cycleId',
      [MODELS.UsersOnWorkspaces]:
        'id,createdAt,updatedAt,userId,workspaceId,teamIds,settings,role,status',
      [MODELS.IssueHistory]:
        'id,createdAt,updatedAt,userId,issueId,assedLabelIds,removedLabelIds,fromPriority,toPriority,fromStateId,toStateId,fromEstimate,toEstimate,fromAssigneeId,toAssigneeId,fromParentId,toParentId,sourceMetadata',
      [MODELS.IssueComment]:
        'id,createdAt,updatedAt,userId,issueId,body,parentId,sourceMetadata',
      [MODELS.View]:
        'id,createdAt,updatedAt,workspaceId,name,description,filters,isBookmarked,teamId',
    });

    this.workspaces = this.table(MODELS.Workspace);
    this.labels = this.table(MODELS.Label);
    this.teams = this.table(MODELS.Team);
    this.workflows = this.table(MODELS.Workflow);
    this.issues = this.table(MODELS.Issue);
    this.usersOnWorkspaces = this.table(MODELS.UsersOnWorkspaces);
    this.issueHistory = this.table(MODELS.IssueHistory);
    this.comments = this.table(MODELS.IssueComment);
    this.views = this.table(MODELS.View);
  }
}

export let tegonDatabase: TegonDatabase;

export function initDatabase(hash: number) {
  tegonDatabase = new TegonDatabase(`Tegon_${hash}`);
}

export async function resetDatabase() {
  localStorage.removeItem('lastSequenceId');

  if (tegonDatabase) {
    await tegonDatabase.delete();
  }
}
