import axios from 'axios';
import { useMutation } from 'common/lib/react-query';

// spec cs:agents:evidence — a human records that an agent may move this
// issue to Done. The approval is cleared when the issue leaves Done.

export async function approveDone(issueId: string): Promise<void> {
  await axios.post(`/api/v1/issues/${issueId}/done-approval`);
}

export function useApproveDoneMutation({
  onSuccess,
  onError,
}: {
  onSuccess?: () => void;
  onError?: (error: string) => void;
}) {
  return useMutation({ mutationFn: (issueId: string) => approveDone(issueId),
    onSuccess: () => onSuccess && onSuccess(),
    onError: (errorResponse: { response?: { data?: { error?: string } } }) =>
      onError &&
      onError(errorResponse?.response?.data?.error || 'Error occurred'),
  });
}
