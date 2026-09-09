import { useMutation } from 'react-query';

import type { ProjectType } from 'common/types';

import { ajaxPost } from 'services/utils';

import { useContextStore } from 'store/global-context-provider';
import { convergeDatabase } from 'store/database';

export interface DeleteProjectParams {
  projectId: string;
  workspaceId: string;
}

// The server soft-deletes the project and clears its issues' membership
// in one transaction, then broadcasts the removal.
export function deleteProject(params: DeleteProjectParams) {
  const { projectId, workspaceId } = params;

  return ajaxPost<object, ProjectType>({
    url: `/api/v1/projects/${projectId}/delete?workspaceId=${workspaceId}`,
    data: {},
  });
}

interface MutationParams {
  onMutate?: () => void;
  onSuccess?: (data: ProjectType) => void;
  onError?: (error: string) => void;
}

export function useDeleteProjectMutation({
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

  // The response body is the pre-delete payload (not a tombstone), so the
  // actor's own delete must be applied locally: the record is removed from
  // the store and the cache. Member issues clear their projectIds via the
  // Issue records the server broadcasts.
  const onMutationSuccess = (data: ProjectType) => {
    projectsStore.deleteById(data.id);
    void convergeDatabase.projects.delete(data.id);
    onSuccess && onSuccess(data);
  };

  return useMutation(deleteProject, {
    onError: onMutationError,
    onMutate: onMutationTriggered,
    onSuccess: onMutationSuccess,
  });
}
