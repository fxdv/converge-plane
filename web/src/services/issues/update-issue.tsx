import { useToast } from '@converge/ui/components/use-toast';
import React from 'react';
import { useMutation } from 'common/lib/react-query';

import { useIssueConflict } from 'modules/issues/issue-conflict';

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
  const { issuesStore, teamsStore } = useContextStore();
  const { toast } = useToast();
  const { openConflict } = useIssueConflict();
  const rollbackRef = React.useRef<IssueType | undefined>(undefined);

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
      if (issue) {
        issuesStore.updateIssue(issue, id);
      }
      return undefined;
    }
  };

  const onMutationTriggered = (variables: UpdateIssueParams) => {
    onMutate && onMutate();
    const snapshot = issuesStore.getIssueById(variables.id);
    rollbackRef.current = snapshot ? { ...snapshot } : undefined;
  };

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const onMutationError = (errorResponse: any, variables: UpdateIssueParams) => {
    const snapshot = rollbackRef.current;
    rollbackRef.current = undefined;
    if (snapshot && snapshot.id === variables.id) {
      issuesStore.updateIssue(snapshot, variables.id);
    }

    if (errorResponse?.resStatus === 412) {
      const body = errorResponse?.errors as
        | { version?: number; error?: string }
        | undefined;
      if (typeof body?.version === 'number') {
        issuesStore.updateIssue({ version: body.version }, variables.id);
      }
      const team = teamsStore.getTeamWithId(variables.teamId);
      const number =
        snapshot?.number ?? issuesStore.getIssueById(variables.id)?.number;
      const issueLabel =
        team && number !== undefined
          ? `${team.identifier}-${number}`
          : 'This issue';
      openConflict({ issueLabel });
      onError && onError(body?.error ?? 'version conflict');
      return;
    }

    const errorText =
      errorResponse?.errors?.message ||
      errorResponse?.message ||
      'The server refused the change';

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

  return useMutation({ mutationFn: update,
    onError: onMutationError,
    onMutate: onMutationTriggered,
    onSuccess: onMutationSuccess,
  });
}
