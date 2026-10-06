/** How the first sync of a tab should run. */
export type InitialSyncMode = 'bootstrap' | 'delta';

/**
 * Memory-authority models (issues, workflows, comments, …) are not in
 * Dexie. A stored watermark plus a cached workspace row cannot rebuild
 * them, so a tab whose MST is still empty must take the snapshot.
 * Delta is only safe once this tab has already applied a snapshot.
 */
export function initialSyncMode(input: {
  memoryHydrated: boolean;
  hasCachedWorkspace: boolean;
  lastSequenceId: string | null;
}): InitialSyncMode {
  if (
    input.memoryHydrated &&
    input.hasCachedWorkspace &&
    input.lastSequenceId
  ) {
    return 'delta';
  }
  return 'bootstrap';
}
