import { MODELS } from './models';

// R-9 (v1.1): issue-scoped + inbox models are MST-authoritative.
// Workspace metadata (teams, labels, views, …) still uses Dexie.
const MEMORY_AUTHORITY = new Set<string>([
  MODELS.Issue,
  MODELS.Workflow,
  MODELS.IssueComment,
  MODELS.IssueHistory,
  MODELS.IssueArtifact,
  MODELS.AgentRun,
  MODELS.IssuePullRequest,
  MODELS.Notification,
]);

export function persistModelToDexie(modelName: string): boolean {
  return !MEMORY_AUTHORITY.has(modelName);
}

export function isMemoryAuthorityModel(modelName: string): boolean {
  return MEMORY_AUTHORITY.has(modelName);
}
