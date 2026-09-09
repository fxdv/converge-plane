import { useMutation } from 'react-query';

import { deleteIssueRelation } from '@converge/services';

import type { IssueRelation } from '@converge/types';

import type { IssueRelationType } from 'common/types';

import { useContextStore } from 'store/global-context-provider';

// spec cs:api:relations — the client's removal op. The server refreshes
// both endpoints' denormalized arrays and broadcasts them, so the sync
// stream is the source of truth; the optimistic strip below only saves
// the socket round-trip.

interface DeleteRelationParams {
  // The issue whose record carries the entry (the reader's perspective;
  // used only for the optimistic store update).
  issueId: string;
  relationId: string;
}

interface MutationParams {
  onMutate?: () => void;
  onSuccess?: (relation: IssueRelation) => void;
  onError?: (error: string) => void;
}

export function useDeleteIssueRelationMutation({
  onMutate,
  onSuccess,
  onError,
}: MutationParams) {
  const { issuesStore } = useContextStore();

  const onMutationTriggered = () => {
    onMutate && onMutate();
  };

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const onMutationError = (errorResponse: any) => {
    const errorText =
      errorResponse?.response?.data?.error || 'Error occurred';
    onError && onError(errorText);
  };

  const onMutationSuccess = (relation: IssueRelation) => {
    onSuccess && onSuccess(relation);
  };

  const deleteRelation = ({ issueId, relationId }: DeleteRelationParams) => {
    const issue = issuesStore.getIssueById(issueId);
    if (issue && issue.relations) {
      const next: IssueRelationType[] = issue.relations.filter(
        (r: IssueRelationType) => r.id !== relationId,
      );
      issuesStore.updateIssue({ relations: next }, issueId);
    }
    return deleteIssueRelation({ issueRelationId: relationId });
  };

  return useMutation(deleteRelation, {
    onError: onMutationError,
    onMutate: onMutationTriggered,
    onSuccess: onMutationSuccess,
  });
}
