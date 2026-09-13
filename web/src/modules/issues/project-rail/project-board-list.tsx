import {
  Draggable,
  Droppable,
  type DraggableProvided,
  type DraggableStateSnapshot,
  type DroppableProvided,
  type DroppableStateSnapshot,
} from '@hello-pangea/dnd';
import { WorkflowCategoryEnum } from '@converge/types';
import { DeleteLine, EditLine } from '@converge/ui/icons';
import { cn } from '@converge/ui/lib/utils';
import { Button } from '@converge/ui/components/button';
import { Input } from '@converge/ui/components/input';
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
import { observer } from 'mobx-react-lite';
import React from 'react';
import ReactDOM from 'react-dom';
import {
  AutoSizer,
  CellMeasurer,
  CellMeasurerCache,
  List,
  type ListRowProps,
} from 'react-virtualized';

import type { IssueType, ProjectType } from 'common/types';

import { BoardIssueItem } from 'modules/issues/components/issue-board-item';

import {
  useDeleteProjectMutation,
  useUpdateProjectMutation,
} from 'services/projects';

import { useContextStore } from 'store/global-context-provider';

import {
  projectDroppableId,
  projectProxyDraggableId,
  realIdFromProxyDraggable,
} from './constants';

// A body taller than this starts scrolling inside the stack (column-like);
// shorter stacks size to their content and the rail scrolls instead.
const MAX_STACK_LIST_HEIGHT = 480;
// The drop-strip height for a brand-new (issue-less) project: one quiet
// dashed line, not a block. A 0/0 stack stays ~60px tall end to end.
const EMPTY_STACK_HEIGHT = 18;

interface ProjectBoardListProps {
  project: ProjectType;
  // The project's members already restricted to the current view (the
  // same filtered set the columns render), in the view's order.
  issues: IssueType[];
  isAdmin: boolean;
}

// One stack on the project rail: a droppable whose cards are proxy
// draggables (see constants.ts) mirroring the card in its state column.
//
// Visual language (the clean-rail pass): the stack is a quiet container.
// The 3px project-color spine on the left is the differentiator; the body
// is transparent so the cards float on the board background exactly like
// the column cards; the header is the only loud element, and its
// rename/delete actions appear only on hover (they stay keyboard-
// reachable). A stack sizes to its content (up to the cap, then it
// scrolls like a column); the rail scrolls across stacks.
export const ProjectBoardList = observer(
  ({ project, issues, isAdmin }: ProjectBoardListProps) => {
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

    // One cache instance per stack (same pattern as the state columns).
    const cache = new CellMeasurerCache({
      defaultHeight: 100,
      fixedWidth: true,
    });

    const rowRender = ({ index, style, key, parent }: ListRowProps) => {
      const issue = issues[index];

      if (!issue) {
        // The drag placeholder index maps to no issue: an empty gap row.
        return null;
      }

      return (
        <Draggable
          key={issue.id}
          draggableId={projectProxyDraggableId(issue.id)}
          index={index}
        >
          {(
            dragProvided: DraggableProvided,
            dragSnapshot: DraggableStateSnapshot,
          ) => (
            <CellMeasurer
              key={key}
              cache={cache}
              columnIndex={0}
              parent={parent}
              rowIndex={index}
            >
              <div style={style} key={key}>
                <BoardIssueItem
                  issueId={issue.id}
                  isDragging={dragSnapshot.isDragging}
                  provided={dragProvided}
                  key={key}
                />
              </div>
            </CellMeasurer>
          )}
        </Draggable>
      );
    };

    const stack = (
      <Droppable
        droppableId={projectDroppableId(project.id)}
        type="BoardColumn"
        mode="virtual"
        ignoreContainerClipping
        renderClone={(provided, snapshot) => {
          const realId = realIdFromProxyDraggable(
            provided.draggableProps['data-rfd-draggable-id'],
          );

          return (
            <BoardIssueItem
              issueId={realId}
              isDragging={snapshot.isDragging}
              provided={provided}
            />
          );
        }}
      >
        {(
          droppableProvided: DroppableProvided,
          snapshot: DroppableStateSnapshot,
        ) => {
          const count: number = snapshot.isUsingPlaceholder
            ? issues.length + 1
            : issues.length;

          // Content-sized stack: the list is as tall as its measured
          // rows (the CellMeasurer cache converges after first paint),
          // capped so a large project scrolls like a column.
          let listHeight: number;
          if (count === 0) {
            listHeight = EMPTY_STACK_HEIGHT;
          } else {
            let content = 0;
            for (let i = 0; i < count; i++) {
              // The published type wants {index}, but the runtime API (and
              // the List's own calls) pass the number — keep the number.
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              content += (cache.rowHeight as any)(i) ?? 0;
            }
            listHeight = Math.min(content, MAX_STACK_LIST_HEIGHT);
          }

          return (
            // w-full: the rail owns the width (350px content + its right
            // gutter); the stack must not set its own or it overflows the
            // rail's padding box. Square on purpose — the workflow columns
            // are square, and the color spine does the differentiating.
            <div
              className="group flex flex-col w-full shrink-0"
              style={{
                borderLeft: `3px solid ${project.color || 'transparent'}`,
              }}
            >
              <div className="flex items-center gap-1.5 px-2.5 py-2 bg-background-3 dark:bg-grayAlpha-100">
                <span
                  className="h-2 w-2 rounded-full shrink-0"
                  style={{ backgroundColor: project.color || '#888888' }}
                />
                {editing ? (
                  <Input
                    value={name}
                    className="h-7 flex-1 min-w-0"
                    onChange={(e) => setName(e.target.value)}
                    placeholder="Project name"
                  />
                ) : (
                  <h3 className="truncate text-sm font-medium">
                    {project.name}
                  </h3>
                )}
                <span className="flex-1" />
                <span className="font-mono text-xs text-muted-foreground">
                  {doneCount}/{issues.length}
                </span>
                {isAdmin && !editing && (
                  // Quiet by default: visible on hover, always reachable
                  // by keyboard (opacity, not display, keeps them focusable).
                  <span className="flex items-center opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100 transition-opacity">
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-5 w-5 shrink-0"
                      onClick={() => setEditing(true)}
                    >
                      <EditLine size={11} />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-5 w-5 shrink-0"
                      onClick={() => setConfirmDelete(true)}
                    >
                      <DeleteLine size={11} />
                    </Button>
                  </span>
                )}
              </div>

              {editing && (
                <div className="flex items-center gap-2 px-2.5 pb-1.5">
                  <div
                    className="h-3 w-3 rounded-full shrink-0"
                    style={{ backgroundColor: project.color || '#888888' }}
                  />
                  <span className="text-xs text-muted-foreground">
                    {project.color ?? ''}
                  </span>
                  <div className="flex-1" />
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
                    onClick={() => setEditing(false)}
                  >
                    Cancel
                  </Button>
                </div>
              )}

              {/* Transparent by design: the cards carry their own
                  background and float on the board background. The only
                  state it paints is a drag-over tint — color only, no
                  size change, so nothing shifts mid-drag. */}
              <div
                className={cn(
                  'px-2 py-1.5',
                  snapshot.isDraggingOver &&
                    'bg-background-3/60 dark:bg-grayAlpha-100/25',
                )}
              >
                <AutoSizer className="w-full">
                  {({ width }) => (
                    <List
                      ref={(ref) => {
                        if (ref) {
                          // eslint-disable-next-line react/no-find-dom-node
                          const node = ReactDOM.findDOMNode(ref);
                          if (node instanceof HTMLElement) {
                            droppableProvided.innerRef(node);
                          }
                        }
                      }}
                      height={listHeight}
                      overscanRowCount={10}
                      noRowsRenderer={() => (
                        <div className="m-1 flex h-full items-center justify-center rounded border border-dashed border-grayAlpha-300/70 dark:border-grayAlpha-200/40">
                          <span className="px-2 text-center text-[10px] text-muted-foreground">
                            Drop a card here to add it to {project.name}
                          </span>
                        </div>
                      )}
                      width={width}
                      rowCount={count}
                      outerRef={droppableProvided.innerRef}
                      rowHeight={cache.rowHeight}
                      deferredMeasurementCache={cache}
                      rowRenderer={rowRender}
                      shallowCompare
                    />
                  )}
                </AutoSizer>
              </div>
            </div>
          );
        }}
      </Droppable>
    );

    return (
      <>
        {stack}
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
            The project is removed and its issues keep their state — they
            simply stop belonging to it.
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
