// The board's project-focus lens, in pure form (spec cs:ui:projects-rail).
//
// When a reader focuses a project (clicks its rail chip), the board's
// cards split into three kinds. The rule lives in a pure function with
// no React import on purpose: the wire-contract harness compiles .ts
// files only, so this is the testable seam of the rail redesign.

export type ProjectFocusKind = 'match' | 'dim' | 'none';

// Classify one card under the lens:
//   - no focused project        -> 'none'  (the board is unchanged; no
//                                                       card is styled);
//   - the card is of the focused project -> 'match' (a 2px ring in the
//                                                       project's color);
//   - everything else           -> 'dim'   (faded, so the matched cards
//                                                       stand out).
// v1 carries at most one project id per issue; membership is an
// includes-lookup, so v2's multi-project shape needs no change here.
export function projectFocusKind(
  projectIds: readonly string[] | null | undefined,
  focusedProjectId: string | null | undefined,
): ProjectFocusKind {
  if (!focusedProjectId) {
    return 'none';
  }

  return projectIds?.includes(focusedProjectId) ? 'match' : 'dim';
}

// The rail's visibility rule: a project renders when the current view
// shows at least one of its cards, or when it has no cards on this
// board at all (a brand-new project must appear at once, as a drop
// target to fill). It is hidden only when the project HAS cards and the
// current view filters every one of them out.
export function railStackVisible(
  inViewCount: number,
  anywhereCount: number,
): boolean {
  return inViewCount > 0 || anywhereCount === 0;
}
