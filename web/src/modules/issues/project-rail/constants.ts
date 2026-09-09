// The droppable id prefix for a project stack on the board, and the
// prefix of the proxy draggable id (`project:<issueId>`).
//
// Why a prefix (and why a proxy): the board's drag context keeps
// draggable ids in a flat map, so the same issue must not register a
// Draggable in two droppables. A card that belongs to a project is
// rendered in both its workflow-state column (real id) and the project
// stack (proxy id) — same card, two droppables, no collision. The
// prefix is how the shared onDragEnd distinguishes a stack drop from a
// workflow-column drop (spec cs:ui:projects-rail).
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

export function projectProxyDraggableId(issueId: string): string {
  return `${PROJECT_DROPPABLE_PREFIX}${issueId}`;
}

export function realIdFromProxyDraggable(draggableId: string): string {
  return isProjectDroppableId(draggableId)
    ? draggableId.slice(PROJECT_DROPPABLE_PREFIX.length)
    : draggableId;
}
