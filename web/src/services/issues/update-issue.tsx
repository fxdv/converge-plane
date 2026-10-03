import { useToast } from '@converge/ui/components/use-toast';
import { useMutation } from 'react-query';

import type { IssueType, IssueRelationEnum } from 'common/types';

import { ajaxPost } from 'services/utils';

import { useContextStore } from 'store/global-context-provider';

export interface UpdateIssueParams {
  id: string;
  title?: string;
  description?: string;
  priority?: number;

  labelIds?: string[];
  dueDate?: string;
  stateId?: string;
  assigneeId?: string;
  teamId: string;

  parentId?: string;
  version?: number;

  cycleId?: string;
  projectId?: string;
  // v1.1: the project membership (spec cs:api:projects) — the board's
  // project rail assigns/clears it through this patch.
  projectIds?: string[];
  projectMilestoneId?: string;

  issueRelation?: {
    issueId: string;
    relatedIssueId: string;
    type: IssueRelationEnum;
  };
}

export function updateIssue({
  id,
  teamId,
  version,
  ...otherParams
}: UpdateIssueParams) {
  return ajaxPost({
    url: `/api/v1/issues/${id}?teamId=${teamId}`,
    data: otherParams,
    headers:
      version && version > 0 ? { 'If-Match': `"${version}"` } : undefined,
  });
}

interface MutationParams {
  onMutate?: () => void;
  onSuccess?: (data: IssueType) => void;
  onError?: (error: string) => void;
}

export function useUpdateIssueMutation({
  onMutate,
  onSuccess,
  onError,
}: MutationParams) {
  const { issuesStore } = useContextStore();
  const { toast } = useToast();

  const update = ({ id, ...otherParams }: UpdateIssueParams) => {
    const issue = issuesStore.getIssueById(id);

    try {
      issuesStore.updateIssue(otherParams, id);

      return updateIssue({
        ...otherParams,
        id,
        version: issue?.version,
      });
    } catch (e) {
      issuesStore.updateIssue(issue, id);
      return undefined;
    }
  };

  const onMutationTriggered = () => {
    onMutate && onMutate();
  };

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const onMutationError = (errorResponse: any) => {
    const errorText =
      errorResponse?.errors?.message || 'The server refused the change';

    toast({
      variant: 'destructive',
      title: 'Could not save this issue',
      description: `${errorText}. Try again.`,
    });
    onError && onError(errorText);
  };

  const onMutationSuccess = (data: IssueType) => {
    onSuccess && onSuccess(data);
  };

  return useMutation(update, {
    onError: onMutationError,
    onMutate: onMutationTriggered,
    onSuccess: onMutationSuccess,
  });
}
