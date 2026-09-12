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

interface ProjectBoardListProps {
  project: ProjectType;
  // The project's members already restricted to the current view (the
  // same filtered set the columns render), in the view's order.
  issues: IssueType[];
  isAdmin: boolean;
}

// One stack on the project rail: a droppable column whose cards are
// proxy draggables (see constants.ts) mirroring the card in its state
// column. The header shows the done/total rollup; owners and admins get
// rename and delete affordances.
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

    // The Droppable's own wrapper div carries no sizing of its own, so
    // it is wrapped in a flex item with a definite height: the wrapper
    // stretches to it, and the content's h-full (and the AutoSizer that
    // measures inside it) resolves against a real box.
    const stack = (
      <div className="flex-1 min-h-0 flex">
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
          const itemCount: number = snapshot.isUsingPlaceholder
            ? issues.length + 1
            : issues.length;

          return (
            <div className="flex flex-col h-full w-[350px] min-h-0 rounded-xl overflow-hidden">
              <div
                className="flex items-center gap-1.5 px-2.5 py-2 bg-background-3 dark:bg-grayAlpha-100"
                style={{
                  borderLeft: `3px solid ${project.color ?? 'transparent'}`,
                }}
              >
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
                  <h3 className="truncate text-sm font-medium">{project.name}</h3>
                )}
                <span className="flex-1" />
                <span className="font-mono text-xs text-muted-foreground">
                  {doneCount}/{issues.length}
                </span>
                {isAdmin && !editing && (
                  <>
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
                  </>
                )}
              </div>

              {editing && (
                <div className="flex items-center gap-2 px-2.5 pb-1.5">
                  <div
                    className="h-3 w-3 rounded-full shrink-0"
                    style={{ backgroundColor: project.color ?? '#888888' }}
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

              <div className="flex-1 min-h-0 px-2 pt-2 bg-grayAlpha-50/60 dark:bg-grayAlpha-100/40">
                <AutoSizer className="h-full w-full">
                  {({ width, height }) => (
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
                      height={height}
                      overscanRowCount={10}
                      noRowsRenderer={() => (
                        <div className="m-1 flex h-full min-h-[56px] items-center justify-center rounded-lg border border-dashed border-grayAlpha-300/70 dark:border-grayAlpha-200/40">
                          <span className="px-2 text-center text-[11px] text-muted-foreground">
                            Drop a card here to add it to {project.name}
                          </span>
                        </div>
                      )}
                      width={width}
                      rowCount={itemCount}
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
      </div>
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
