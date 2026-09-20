import type { DropResult } from '@hello-pangea/dnd';

import { Board } from '@converge/ui/components/board';
import { observer } from 'mobx-react-lite';
import React from 'react';

import type { WorkflowType } from 'common/types';

import { useComputedWorkflows } from 'hooks/workflows';

import { useUpdateIssueMutation } from 'services/issues';

import { useContextStore } from 'store/global-context-provider';

import { CategoryBoardList } from './category-board-list';
import { ProjectRail } from '../../../../project-rail';
import {
  isProjectDroppableId,
  projectIdFromDroppable,
} from '../../../../project-rail/constants';
import {
  ProjectFocusContext,
  type ProjectFocus,
} from '../../../../project-rail/project-focus-context';

interface CategoryBoardProps {
  workflows: WorkflowType[];
}

export const CategoryBoard = observer(({ workflows }: CategoryBoardProps) => {
  const { mutate: updateIssue } = useUpdateIssueMutation({});
  const { issuesStore, projectsStore } = useContextStore();
  const { workflowMap } = useComputedWorkflows();

  // The board's project-focus lens (spec cs:ui:projects-rail): the
  // ephemeral client-side state behind the rail's chips. It mutates
  // nothing, syncs nothing, and persists nothing — it is a viewing aid.
  const [focusedProjectId, setFocusedProjectId] = React.useState<string | null>(
    null,
  );

  // Esc releases the lens from anywhere on the board. Inputs that own
  // their Esc (the rail's rename / create forms) stop it on the way up.
  React.useEffect(() => {
    if (focusedProjectId === null) {
      return undefined;
    }

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setFocusedProjectId(null);
      }
    };

    window.addEventListener('keydown', onKey);

    return () => window.removeEventListener('keydown', onKey);
  }, [focusedProjectId]);

  const onDragEnd = (result: DropResult) => {
    // Real ids only: the card renders exactly once, in its state column
    // (the rail's chips carry no draggables of their own).
    const issueId = result.draggableId;
    const destId = result.destination?.droppableId ?? '';

    const issue = issuesStore.getIssueById(issueId);
    if (!issue) {
      return;
    }

    // A drop on a project chip is a membership-only change: the card
    // keeps its state (a project is a grouping, never a state) and
    // stays in its column.
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

    // Released on the board background: no state change.
    if (!result.destination || destId === 'board') {
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

    updateIssue({
      id: issueId,
      stateId: workflowId,
      teamId: issue.teamId,
    });
  };

  // The lens value the cards read: it resolves to `null` the moment the
  // focused project no longer exists (deleted in another tab, say), so
  // a stale focus can never dim the board.
  const focus: ProjectFocus | null = (() => {
    if (focusedProjectId === null) {
      return null;
    }
    const project = projectsStore.getProjectById(focusedProjectId);

    return project
      ? { projectId: project.id, color: project.color ?? '' }
      : null;
  })();

  return (
    <ProjectFocusContext.Provider value={focus}>
      <Board onDragEnd={onDragEnd} className="pl-4">
        <>
          <ProjectRail
            workflows={workflows}
            focusedProjectId={focusedProjectId}
            onToggleFocus={setFocusedProjectId}
          />
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
    </ProjectFocusContext.Provider>
  );
});
