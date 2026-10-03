import { MODELS } from './models';

// R-9 (v1.1): listed models are authoritative in MST + SSE/bootstrap.
// Dexie is not written for them; the store is hydrated from sync.
const MEMORY_AUTHORITY = new Set<string>([
  MODELS.Issue,
  MODELS.Workflow,
  MODELS.IssueComment,
  MODELS.IssueHistory,
]);

export function persistModelToDexie(modelName: string): boolean {
  return !MEMORY_AUTHORITY.has(modelName);
}

export function isMemoryAuthorityModel(modelName: string): boolean {
  return MEMORY_AUTHORITY.has(modelName);
}
