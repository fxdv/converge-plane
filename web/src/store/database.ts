'use client';

import Dexie from 'dexie';

import type {
  LabelType,
  ProjectType,
  TeamType,
  UsersOnWorkspaceType,
  ViewType,
  WorkspaceType,
} from 'common/types';

import { MODELS } from './models';

// Local object-cache for workspace metadata (teams, labels, views, …).
// Issue-scoped models and notifications are MST-authoritative (R-9, see
// client-cache-policy.ts) and have no table here.
export class ConvergeDatabase extends Dexie {
  workspaces: Dexie.Table<WorkspaceType, string>;
  labels: Dexie.Table<LabelType, string>;
  teams: Dexie.Table<TeamType, string>;
  usersOnWorkspaces: Dexie.Table<UsersOnWorkspaceType, string>;
  views: Dexie.Table<ViewType, string>;
  projects: Dexie.Table<ProjectType, string>;

  constructor(databaseName: string) {
    super(databaseName);

    // v25 drops the memory-authority tables v24 still declared (never
    // written since R-9) and the unqueried Team.currentCycle index.
    // Dexie 4 diffs against the installed schema, so only the current
    // version needs declaring; a null entry deletes that table on upgrade.
    this.version(25).stores({
      [MODELS.Workspace]: 'id,createdAt,updatedAt,name,slug,preferences',
      [MODELS.Label]:
        'id,createdAt,updatedAt,name,color,description,workspaceId,groupId,teamId',
      [MODELS.Team]:
        'id,createdAt,updatedAt,name,identifier,workspaceId,preferences',
      [MODELS.UsersOnWorkspaces]:
        'id,createdAt,updatedAt,userId,workspaceId,teamIds,settings,role,status',
      [MODELS.View]:
        'id,createdAt,updatedAt,workspaceId,name,description,filters,isBookmarked,teamId',
      [MODELS.Project]:
        'id,createdAt,updatedAt,name,color,description,workspaceId',
      [MODELS.Workflow]: null,
      [MODELS.Issue]: null,
      [MODELS.IssueHistory]: null,
      [MODELS.IssueComment]: null,
      [MODELS.IssueArtifact]: null,
      [MODELS.AgentRun]: null,
      [MODELS.IssuePullRequest]: null,
      [MODELS.Notification]: null,
    });

    this.workspaces = this.table(MODELS.Workspace);
    this.labels = this.table(MODELS.Label);
    this.teams = this.table(MODELS.Team);
    this.usersOnWorkspaces = this.table(MODELS.UsersOnWorkspaces);
    this.views = this.table(MODELS.View);
    this.projects = this.table(MODELS.Project);
  }
}

// The shared sync watermark for one workspace+user cache, keyed by the
// same hash the database name carries (bootstrap-data, socket-data-sync).
export function lastSequenceIdKey(hash: number): string {
  return `lastSequenceId_${hash}`;
}

export let convergeDatabase: ConvergeDatabase;
let databaseHash: number | undefined;

export function initDatabase(hash: number) {
  databaseHash = hash;
  convergeDatabase = new ConvergeDatabase(`Converge_${hash}`);
}

export async function resetDatabase() {
  // The watermark must go with the cache: a surviving cursor would make
  // the next load a delta from a sequence the empty database never saw.
  if (databaseHash !== undefined) {
    localStorage.removeItem(lastSequenceIdKey(databaseHash));
  }

  if (convergeDatabase) {
    await convergeDatabase.delete();
  }
}
