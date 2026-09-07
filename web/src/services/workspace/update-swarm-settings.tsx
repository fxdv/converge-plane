import { updateSwarmSettings } from '@converge/services';
import { useMutation } from 'react-query';

interface MutationParams {
  onMutate?: () => void;
  onSuccess?: () => void;
  onError?: (error: string) => void;
}

// D4: save the swarm plane's fleet settings (topology + foreman
// designation). The server enforces owner/admin (agents 422); the
// saved settings take effect on the swarm's next decision.
export function useUpdateSwarmSettingsMutation({
  onMutate,
  onSuccess,
  onError,
}: MutationParams) {
  const onMutationTriggered = () => {
    onMutate && onMutate();
  };

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const onMutationError = (errorResponse: any) => {
    const errorText =
      errorResponse?.response?.data?.error || 'Error occurred';

    onError && onError(errorText);
  };

  const onMutationSuccess = () => {
    onSuccess && onSuccess();
  };

  return useMutation(
    (variables: {
      workspaceId: string;
      topology: string;
      foremanAccountId: string | null;
    }) =>
      updateSwarmSettings(variables.workspaceId, {
        topology: variables.topology,
        foremanAccountId: variables.foremanAccountId,
      }),
    {
      onError: onMutationError,
      onMutate: onMutationTriggered,
      onSuccess: onMutationSuccess,
    },
  );
}
