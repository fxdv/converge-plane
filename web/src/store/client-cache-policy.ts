import { MODELS } from './models';

// R-9 slice 1 (v1.1): issues and workflows are authoritative in MST +
// SSE/bootstrap. Dexie is not written for these models; the store is
// hydrated from sync. Later slices drop Dexie for more models.
const MEMORY_AUTHORITY = new Set<string>([MODELS.Issue, MODELS.Workflow]);

export function persistModelToDexie(modelName: string): boolean {
  return !MEMORY_AUTHORITY.has(modelName);
}

export function isMemoryAuthorityModel(modelName: string): boolean {
  return MEMORY_AUTHORITY.has(modelName);
}
