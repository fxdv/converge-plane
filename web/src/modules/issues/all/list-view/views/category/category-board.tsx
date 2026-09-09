import type { DropResult } from '@hello-pangea/dnd';

import { Board } from '@converge/ui/components/board';
import { observer } from 'mobx-react-lite';

import type { WorkflowType } from 'common/types';

import { useComputedWorkflows } from 'hooks/workflows';

import { useUpdateIssueMutation } from 'services/issues';

import { useContextStore } from 'store/global-context-provider';

import { ProjectRail } from '../../../../project-rail';
import {
  isProjectDroppableId,
  projectIdFromDroppable,
} from '../../../../project-rail/constants';

import { CategoryBoardList } from './category-board-list';

interface CategoryBoardProps {
  workflows: WorkflowType[];
}

export const CategoryBoard = observer(({ workflows }: CategoryBoardProps) => {
  const { mutate: updateIssue } = useUpdateIssueMutation({});
  const { issuesStore } = useContextStore();
  const { workflowMap } = useComputedWorkflows();

  const onDragEnd = (result: DropResult) => {
    const issueId = result.draggableId;
    const sourceId = result.source?.droppableId;
    const destId = result.destination?.droppableId ?? '';

    const issue = issuesStore.getIssueById(issueId);
    if (!issue) {
      return;
    }

    // Project rail (v1.1): a drop on a stack is a membership-only change
    // — the card keeps its state (a project is a grouping, never a state)
    // and stays in its column while it also appears in the stack.
    if (isProjectDroppableId(destId)) {
      const projectId = projectIdFromDroppable(destId);
      if (issue.projectIds?.[0] !== projectId) {
        updateIssue({
          id: issueId,
          projectIds: [projectId],
          teamId: issue.teamId,
        });
      }
      return;
    }

    // Released on the board background: no state change; a card leaving
    // its stack clears its membership.
    if (!result.destination || destId === 'board') {
      if (
        isProjectDroppableId(sourceId) &&
        (issue.projectIds?.length ?? 0) > 0
      ) {
        updateIssue({ id: issueId, projectIds: [], teamId: issue.teamId });
      }
      return;
    }

    const workflow = workflows.find((w) => w.name === destId);
    if (!workflow) {
      return;
    }

    const workflowId = workflow.ids.find(
      (id) => workflowMap[id]?.teamId === issue.teamId,
    );
    if (!workflowId || issue.stateId === workflowId) {
      return;
    }

    // A card dragged out of its stack into a column leaves the project
    // (projectIds = []); a card moving between columns keeps it.
    updateIssue({
      id: issueId,
      stateId: workflowId,
      teamId: issue.teamId,
      ...(isProjectDroppableId(sourceId) &&
      (issue.projectIds?.length ?? 0) > 0
        ? { projectIds: [] }
        : {}),
    });
  };

  return (
    <Board onDragEnd={onDragEnd} className="pl-4">
      <>
        <ProjectRail workflows={workflows} />
        {workflows.map((workflow: WorkflowType) => {
          return (
            <CategoryBoardList
              key={workflow.name}
              workflow={workflow}
              workflows={workflows}
            />
          );
        })}
      </>
    </Board>
  );
});
