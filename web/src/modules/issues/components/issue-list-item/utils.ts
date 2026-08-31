import { WorkflowCategoryEnum } from '@converge/types';

import { workflowSort } from 'common/sorting';
import type { IssueType, WorkflowType } from 'common/types';

import { useContextStore } from 'store/global-context-provider';

export function useSortIssues(issues: IssueType[]) {
  const { workflowsStore } = useContextStore();

  // Step 1: Get unique workflowIds from issue.stateId
  const uniqueWorkflowIds = Array.from(
    new Set(issues.map((issue) => issue.stateId)),
  );

  const categorySequence = [
    WorkflowCategoryEnum.TRIAGE,
    WorkflowCategoryEnum.BACKLOG,
    WorkflowCategoryEnum.UNSTARTED,
    WorkflowCategoryEnum.STARTED,
    WorkflowCategoryEnum.COMPLETED,
    WorkflowCategoryEnum.CANCELED,
  ];

  const sorting = (a: WorkflowType, b: WorkflowType) => {
    return workflowSort(a, b, categorySequence);
  };

  const workflows = uniqueWorkflowIds
    .map((id) => workflowsStore.getWorkflowWithId(id))
    .sort(sorting)
    .map((workflow: WorkflowType) => workflow.id);

  // Sort issues using the mapped workflow category
  return issues.sort((a, b) => {
    return workflows.indexOf(a.stateId) - workflows.indexOf(b.stateId);
  });
}
