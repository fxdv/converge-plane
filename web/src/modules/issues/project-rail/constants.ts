// The droppable id of a project chip on the board.
//
// A prefix (not a bare id) is how the board's shared onDragEnd tells a
// chip drop from a workflow-column drop (spec cs:ui:projects-rail).
//
// The v1.1 proxy-draggable helpers are gone with the stack: a card
// renders exactly once — in its state column, under its real id — so
// no id can collide and no stripping is needed on drop.
export const PROJECT_DROPPABLE_PREFIX = 'project:';

export function projectDroppableId(projectId: string): string {
  return `${PROJECT_DROPPABLE_PREFIX}${projectId}`;
}

export function isProjectDroppableId(droppableId: string): boolean {
  return droppableId.startsWith(PROJECT_DROPPABLE_PREFIX);
}

export function projectIdFromDroppable(droppableId: string): string {
  return droppableId.slice(PROJECT_DROPPABLE_PREFIX.length);
}
