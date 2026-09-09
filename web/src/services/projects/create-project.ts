import { useMutation } from 'react-query';

import type { ProjectType } from 'common/types';

import { ajaxPost } from 'services/utils';

import { useContextStore } from 'store/global-context-provider';
import { convergeDatabase } from 'store/database';

export interface CreateProjectParams {
  name: string;
  color?: string;
  description?: string;
  startDate?: string;
  endDate?: string;
  teams?: string[];
  leadId?: string;
  workspaceId: string;
}

export function createProject(params: CreateProjectParams) {
  return ajaxPost<CreateProjectParams, ProjectType>({
    url: '/api/v1/projects',
    data: params,
  });
}

interface MutationParams {
  onMutate?: () => void;
  onSuccess?: (data: ProjectType) => void;
  onError?: (error: string) => void;
}

export function useCreateProjectMutation({
  onMutate,
  onSuccess,
  onError,
}: MutationParams) {
  const { projectsStore } = useContextStore();

  const onMutationTriggered = () => {
    onMutate && onMutate();
  };

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const onMutationError = (errorResponse: any) => {
    const errorText = errorResponse?.errors?.message || 'Error occured';

    onError && onError(errorText);
  };

  // The SSE broadcast also upserts the record (idempotently); this direct
  // update makes the actor's own create appear immediately even when the
  // stream is briefly down.
  const onMutationSuccess = (data: ProjectType) => {
    projectsStore.update(data, data.id);
    void convergeDatabase.projects.put(data);
    onSuccess && onSuccess(data);
  };

  return useMutation(createProject, {
    onError: onMutationError,
    onMutate: onMutationTriggered,
    onSuccess: onMutationSuccess,
  });
}
