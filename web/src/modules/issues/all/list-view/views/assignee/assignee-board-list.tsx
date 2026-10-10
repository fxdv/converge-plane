import { AvatarText } from '@converge/ui/components/avatar';
import { AssigneeLine } from '@converge/ui/icons';
import {
  Draggable,
  Droppable,
  type DraggableProvided,
  type DraggableStateSnapshot,
  type DroppableProvided,
  type DroppableStateSnapshot,
} from '@hello-pangea/dnd';
import { observer } from 'mobx-react-lite';
import { AutoSizer, List, type ListRowProps } from 'react-virtualized';

import { BoardIssueItem } from 'modules/issues/components/issue-board-item';

import type { User } from 'common/types';

import { useCurrentTeam } from 'hooks/teams';
import { useComputedWorkflows } from 'hooks/workflows';

import { useContextStore } from 'store/global-context-provider';

import { useFilterIssues } from '../../../../issues-utils';
import {
  BoardMeasuredRow,
  rowHeightOr,
  useBoardMeasureCache,
} from '../../board-measured-row';
import {
  BOARD_DEFAULT_ROW_HEIGHT,
  BOARD_OVERSCAN_ROW_COUNT,
  boardVirtualRowCount,
} from '../../board-virtual-config';

interface AssigneeBoardListProps {
  user: User;
}

export const AssigneeBoardList = observer(
  ({ user }: AssigneeBoardListProps) => {
    const { issuesStore, applicationStore } = useContextStore();
    const team = useCurrentTeam();
    const { workflows } = useComputedWorkflows();

    const issues = issuesStore.getIssuesForUser({
      userId: user.id,
      teamId: team?.id,
    });

    const computedIssues = useFilterIssues(issues, workflows);
    const cache = useBoardMeasureCache(
      computedIssues.map((issue) => issue.id),
    );

    if (
      computedIssues.length === 0 &&
      !applicationStore.displaySettings.showEmptyGroups
    ) {
      return null;
    }

    const rowRender = ({ index, style, key, parent }: ListRowProps) => {
      const issue = computedIssues[index];

      if (!issue) {
        return null;
      }

      return (
        <Draggable key={issue.id} draggableId={issue.id} index={index}>
          {(
            dragProvided: DraggableProvided,
            dragSnapshot: DraggableStateSnapshot,
          ) => (
            <BoardMeasuredRow
              cache={cache}
              parent={parent}
              index={index}
              rowKey={key}
              style={style}
            >
              {(measure) => (
                <BoardIssueItem
                  issueId={issue.id}
                  isDragging={dragSnapshot.isDragging}
                  provided={dragProvided}
                  measure={measure}
                />
              )}
            </BoardMeasuredRow>
          )}
        </Draggable>
      );
    };

    return (
      <Droppable
        droppableId={user.id}
        type="BoardColumn"
        mode="virtual"
        ignoreContainerClipping
        renderClone={(provided, snapshot) => {
          return (
            <BoardIssueItem
              issueId={provided.draggableProps['data-rfd-draggable-id']}
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
          const itemCount = boardVirtualRowCount(
            computedIssues.length,
            snapshot.isUsingPlaceholder,
          );

          return (
            <div className="flex flex-col max-h-[100%] w-[350px]">
              <div className="flex gap-1 items-center mb-2 w-[310px]">
                <div className="flex items-center w-fit h-8 rounded-2xl px-4 py-2 bg-grayAlpha-100">
                  <AvatarText
                    text={user.fullname}
                    className="h-5 w-5 text-[9px]"
                  />
                  <h3 className="pl-2">{user.fullname}</h3>
                </div>

                <div className="rounded-2xl bg-grayAlpha-100 p-1.5 px-2 font-mono">
                  {computedIssues.length}
                </div>
              </div>

              <div className="flex flex-col grow mr-3">
                <AutoSizer className="pb-10 h-full">
                  {({ width, height }) => (
                    <List
                      height={height}
                      overscanRowCount={BOARD_OVERSCAN_ROW_COUNT}
                      noRowsRenderer={() => <></>}
                      width={width}
                      rowCount={itemCount}
                      elementRef={droppableProvided.innerRef}
                      rowHeight={rowHeightOr(cache, BOARD_DEFAULT_ROW_HEIGHT)}
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
  },
);

export const NoAssigneeView = observer(() => {
  const { issuesStore, applicationStore } = useContextStore();
  const team = useCurrentTeam();
  const { workflows } = useComputedWorkflows();

  const issues = issuesStore.getIssuesForUser({
    userId: undefined,
    teamId: team?.id,
  });
  const computedIssues = useFilterIssues(issues, workflows);
  const cache = useBoardMeasureCache(computedIssues.map((issue) => issue.id));

  if (
    computedIssues.length === 0 &&
    !applicationStore.displaySettings.showEmptyGroups
  ) {
    return null;
  }

  const rowRender = ({ index, style, key, parent }: ListRowProps) => {
    const issue = computedIssues[index];

    if (!issue) {
      return null;
    }

    return (
      <Draggable key={issue.id} draggableId={issue.id} index={index}>
        {(
          dragProvided: DraggableProvided,
          dragSnapshot: DraggableStateSnapshot,
        ) => (
          <BoardMeasuredRow
            cache={cache}
            parent={parent}
            index={index}
            rowKey={key}
            style={style}
          >
            {(measure) => (
              <BoardIssueItem
                issueId={issue.id}
                isDragging={dragSnapshot.isDragging}
                provided={dragProvided}
                measure={measure}
              />
            )}
          </BoardMeasuredRow>
        )}
      </Draggable>
    );
  };

  return (
    <Droppable
      droppableId="no-user"
      type="BoardColumn"
      mode="virtual"
      ignoreContainerClipping
      renderClone={(provided, snapshot) => {
        return (
          <BoardIssueItem
            issueId={provided.draggableProps['data-rfd-draggable-id']}
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
        const itemCount = boardVirtualRowCount(
          computedIssues.length,
          snapshot.isUsingPlaceholder,
        );

        return (
          <div className="flex flex-col max-h-[100%] w-[350px]">
            <div className="flex gap-1 items-center mb-2 w-[310px]">
              <div className="flex items-center w-fit h-8 rounded-2xl px-4 py-2 bg-grayAlpha-100">
                <AssigneeLine size={20} />
                <h3 className="pl-2">No Assignee</h3>
              </div>

              <div className="rounded-2xl bg-grayAlpha-100 p-1.5 px-2 font-mono">
                {computedIssues.length}
              </div>
            </div>

            <div className="flex flex-col grow mr-3">
              <AutoSizer className="pb-10 h-full">
                {({ width, height }) => (
                  <List
                    height={height}
                    overscanRowCount={BOARD_OVERSCAN_ROW_COUNT}
                    noRowsRenderer={() => <></>}
                    width={width}
                    rowCount={itemCount}
                    elementRef={droppableProvided.innerRef}
                    rowHeight={rowHeightOr(cache, BOARD_DEFAULT_ROW_HEIGHT)}
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
});
