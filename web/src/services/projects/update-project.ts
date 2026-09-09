import { useMutation } from 'react-query';

import type { ProjectType } from 'common/types';

import { ajaxPost } from 'services/utils';

import { useContextStore } from 'store/global-context-provider';
import { convergeDatabase } from 'store/database';

export interface UpdateProjectParams {
  projectId: string;
  workspaceId: string;
  name?: string;
  color?: string;
  description?: string;
  startDate?: string;
  endDate?: string;
  teams?: string[];
  leadId?: string;
}

export function updateProject(params: UpdateProjectParams) {
  const { projectId, workspaceId, ...otherParams } = params;

  return ajaxPost<Partial<UpdateProjectParams>, ProjectType>({
    url: `/api/v1/projects/${projectId}?workspaceId=${workspaceId}`,
    data: otherParams,
  });
}

interface MutationParams {
  onMutate?: () => void;
  onSuccess?: (data: ProjectType) => void;
  onError?: (error: string) => void;
}

export function useUpdateProjectMutation({
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

  const onMutationSuccess = (data: ProjectType) => {
    projectsStore.update(data, data.id);
    void convergeDatabase.projects.put(data);
    onSuccess && onSuccess(data);
  };

  return useMutation(updateProject, {
    onError: onMutationError,
    onMutate: onMutationTriggered,
    onSuccess: onMutationSuccess,
  });
}
