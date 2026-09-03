import { useMutation } from 'react-query';

import type { IssueType } from 'common/types';

import { ajaxPost } from 'services/utils';

import { useContextStore } from 'store/global-context-provider';

export interface HandoffIssueParams {
  id: string;
  toAccountId: string;
  stateId?: string;
  summary: string;
}

export function handoffIssue({ id, ...otherParams }: HandoffIssueParams) {
  return ajaxPost({
    url: `/api/v1/issues/${id}/handoff`,
    data: otherParams,
  });
}

interface MutationParams {
  onMutate?: () => void;
  onSuccess?: (data: IssueType) => void;
  onError?: (error: string) => void;
}

/**
 * D1: hand off the issue to another agent (spec 12). Optimistic update:
 * assignee (and state) move locally so the board reacts instantly; the
 * server's broadcast re-applies the authoritative row. The summary is
 * the bounded context the next agent works from (4 KB cap, enforced
 * server-side).
 */
export function useHandoffIssueMutation({
  onMutate,
  onSuccess,
  onError,
}: MutationParams) {
  const { issuesStore } = useContextStore();

  const update = ({
    id,
    toAccountId,
    stateId,
    summary,
  }: HandoffIssueParams) => {
    const issue = issuesStore.getIssueById(id);

    try {
      issuesStore.updateIssue(
        {
          assigneeId: toAccountId,
          ...(stateId ? { stateId } : {}),
        },
        id,
      );

      return handoffIssue({ id, toAccountId, stateId, summary });
    } catch (e) {
      issuesStore.updateIssue(issue as IssueType, id);
      return undefined;
    }
  };

  const onMutationTriggered = () => {
    onMutate && onMutate();
  };

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const onMutationError = (errorResponse: any) => {
    const errorText =
      errorResponse?.errors?.message || errorResponse?.message || 'Error occurred';
    onError && onError(errorText);
  };

  const onMutationSuccess = (data: IssueType) => {
    if (data.id) {
      issuesStore.updateIssue(data, data.id);
    }
    onSuccess && onSuccess(data);
  };

  return useMutation(update, {
    onError: onMutationError,
    onMutate: onMutationTriggered,
    onSuccess: onMutationSuccess,
  });
}
