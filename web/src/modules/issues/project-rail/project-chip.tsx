import { WorkflowCategoryEnum } from '@converge/types';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@converge/ui/components/alert-dialog';
import { Button } from '@converge/ui/components/button';
import { Input } from '@converge/ui/components/input';
import { DeleteLine, EditLine } from '@converge/ui/icons';
import { cn } from '@converge/ui/lib/utils';
import {
  Droppable,
  type DroppableProvided,
  type DroppableStateSnapshot,
} from '@hello-pangea/dnd';
import { observer } from 'mobx-react-lite';
import React from 'react';

import type { IssueType, ProjectType } from 'common/types';

import {
  useDeleteProjectMutation,
  useUpdateProjectMutation,
} from 'services/projects';

import { useContextStore } from 'store/global-context-provider';

import { projectDroppableId } from './constants';

interface ProjectChipProps {
  project: ProjectType;
  // The project's cards under the current view (the same filtered set
  // the columns render): drives the done/total rollup and the empty
  // state. Never rendered — the card renders exactly once, in its
  // state column.
  issues: IssueType[];
  isAdmin: boolean;
  // The board's focus lens: which project is focused (`null` = none).
  // The chip derives its own state from it.
  focusedProjectId: string | null;
  onToggleFocus: (projectId: string | null) => void;
}

// One project on the rail (v1.2, spec cs:ui:projects-rail): a single
// compact chip — the project's color, its name, its done/total.
//
// The v1.1 stack is dead: the virtualized list of proxy-id card
// mirrors, its CellMeasurer machinery, and the second scroll it carried
// are all gone. A card renders exactly once — in its state column,
// under its real id — and the rail is a color index into the board.
//
// What the chip still does: it stays a droppable (a column card
// dropped on it assigns the project), clicking it focuses the board on
// the project (its cards ring, the rest dim), and it carries the
// admin rename/delete.
export const ProjectChip = observer(
  ({
    project,
    issues,
    isAdmin,
    focusedProjectId,
    onToggleFocus,
  }: ProjectChipProps) => {
    const { workflowsStore } = useContextStore();
    const [editing, setEditing] = React.useState(false);
    const [name, setName] = React.useState(project.name);
    const [confirmDelete, setConfirmDelete] = React.useState(false);
    const { mutate: updateProject } = useUpdateProjectMutation({});
    const { mutate: deleteProject, isLoading: deleting } =
      useDeleteProjectMutation({});

    React.useEffect(() => {
      setName(project.name);
    }, [project.name]);

    // A card counts as done when its state sits in a completed/canceled
    // workflow category — the same rollup rule the saved views use.
    const isDone = (issue: IssueType) => {
      const entry = workflowsStore.workflows.get(issue.stateId ?? '');
      return (
        entry?.category === WorkflowCategoryEnum.COMPLETED ||
        entry?.category === WorkflowCategoryEnum.CANCELED
      );
    };

    const doneCount = issues.filter(isDone).length;
    const empty = issues.length === 0;

    const focused = focusedProjectId === project.id;
    // A lens is active on another project: this chip dims with the
    // board, so the whole surface reads as one focus.
    const dimmed = focusedProjectId !== null && !focused;

    // The accent the chip speaks in: the project's color, or a quiet
    // neutral when a legacy project has none.
    const accent = project.color || '#9ca3af';

    const chip = (
      <Droppable
        droppableId={projectDroppableId(project.id)}
        type="BoardColumn"
        mode="virtual"
        ignoreContainerClipping
      >
        {(
          droppableProvided: DroppableProvided,
          snapshot: DroppableStateSnapshot,
        ) => {
          return (
            <div
              ref={droppableProvided.innerRef}
              {...droppableProvided.droppableProps}
              className={cn(
                'group flex w-full shrink-0 h-10 items-center gap-2 rounded-md pl-2 pr-1.5 transition-[opacity,box-shadow,background-color] duration-150',
                'bg-background-3/50 dark:bg-grayAlpha-100/30',
                // A brand-new project: one quiet dashed drop line.
                empty &&
                  'border border-dashed border-grayAlpha-300/70 dark:border-grayAlpha-200/40',
                // A colorless project gets the old neutral tint on
                // drag-over; colored ones tint with themselves below.
                snapshot.isDraggingOver &&
                  !project.color &&
                  'bg-background-3 dark:bg-grayAlpha-100/50',
              )}
              style={{
                borderLeft: `3px solid ${accent}`,
                opacity: dimmed ? 0.55 : 1,
                backgroundColor:
                  snapshot.isDraggingOver && project.color
                    ? `color-mix(in srgb, ${project.color} 15%, transparent)`
                    : undefined,
                // The focused chip wears the project's color as a ring;
                // nothing shifts, only light.
                boxShadow: focused ? `0 0 0 2px ${accent}` : undefined,
              }}
            >
              {editing ? (
                <>
                  <span
                    className="h-2 w-2 rounded-full shrink-0"
                    style={{ backgroundColor: accent }}
                  />
                  <Input
                    value={name}
                    className="h-7 flex-1 min-w-0"
                    onChange={(e) => setName(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Escape') {
                        // The board's Esc releases the lens; the
                        // rename's Esc must not reach it.
                        e.stopPropagation();
                        setEditing(false);
                        setName(project.name);
                      }
                      if (e.key === 'Enter' && name.trim()) {
                        updateProject({
                          projectId: project.id,
                          workspaceId: project.workspaceId,
                          name: name.trim(),
                        });
                        setEditing(false);
                      }
                    }}
                    placeholder="Project name"
                  />
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={name.trim().length === 0}
                    onClick={() => {
                      updateProject({
                        projectId: project.id,
                        workspaceId: project.workspaceId,
                        name: name.trim(),
                      });
                      setEditing(false);
                    }}
                  >
                    Save
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setEditing(false);
                      setName(project.name);
                    }}
                  >
                    Cancel
                  </Button>
                </>
              ) : (
                <>
                  <button
                    type="button"
                    className="flex items-center gap-2 min-w-0 flex-1 text-left"
                    aria-pressed={focused}
                    title={
                      focused
                        ? `${project.name} — click to release focus`
                        : `Focus the board on ${project.name}`
                    }
                    onClick={() => onToggleFocus(focused ? null : project.id)}
                  >
                    <span
                      className="h-2 w-2 rounded-full shrink-0"
                      style={{ backgroundColor: accent }}
                    />
                    <span className="truncate text-sm font-medium">
                      {project.name}
                    </span>
                  </button>
                  <span className="font-mono text-xs text-muted-foreground shrink-0">
                    {doneCount}/{issues.length}
                  </span>
                  {empty && (
                    <span className="truncate text-[10px] italic text-muted-foreground shrink-0">
                      drop a card to add
                    </span>
                  )}
                  {isAdmin && (
                    // Quiet by default: visible on hover, always
                    // reachable by keyboard (opacity, not display,
                    // keeps them focusable).
                    <span className="flex items-center opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100 transition-opacity shrink-0">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-5 w-5 shrink-0"
                        aria-label={`Rename ${project.name}`}
                        onClick={() => setEditing(true)}
                      >
                        <EditLine size={11} />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-5 w-5 shrink-0"
                        aria-label={`Delete ${project.name}`}
                        onClick={() => setConfirmDelete(true)}
                      >
                        <DeleteLine size={11} />
                      </Button>
                    </span>
                  )}
                </>
              )}
              {/* No draggables live in the chip, but the placeholder
                    slot is part of the droppable contract. */}
              {droppableProvided.placeholder}
            </div>
          );
        }}
      </Droppable>
    );

    return (
      <>
        {chip}
        <DeleteProjectAlert
          open={confirmDelete}
          setOpen={setConfirmDelete}
          deleting={deleting}
          name={project.name}
          onDelete={() =>
            deleteProject({
              projectId: project.id,
              workspaceId: project.workspaceId,
            })
          }
        />
      </>
    );
  },
);

function DeleteProjectAlert({
  open,
  setOpen,
  deleting,
  name,
  onDelete,
}: {
  open: boolean;
  setOpen: (value: boolean) => void;
  deleting: boolean;
  name: string;
  onDelete: () => void;
}) {
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {name}?</AlertDialogTitle>
          <AlertDialogDescription>
            The project is removed and its issues keep their state — they simply
            stop belonging to it.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={deleting}
            onClick={() => {
              onDelete();
              setOpen(false);
            }}
          >
            {deleting ? 'Deleting…' : 'Delete'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
