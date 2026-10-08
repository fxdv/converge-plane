import axios from 'axios';

import { useMutation } from 'common/lib/react-query';
import type { IssuePullRequestType } from 'common/types';

import { useContextStore } from 'store/global-context-provider';

// spec cs:agents:prlinks — a member links or unlinks a pull request by
// hand. The server broadcasts both on the sync feed; writing its answer
// into the store here only saves the socket round-trip.

export async function linkPullRequest(
  issueId: string,
  url: string,
): Promise<IssuePullRequestType> {
  const response = await axios.post<IssuePullRequestType>(
    `/api/v1/issues/${issueId}/pull_requests`,
    { url },
  );
  return response.data;
}

export async function unlinkPullRequest(
  issueId: string,
  linkId: string,
): Promise<IssuePullRequestType> {
  const response = await axios.delete<IssuePullRequestType>(
    `/api/v1/issues/${issueId}/pull_requests/${linkId}`,
  );
  return response.data;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const errorText = (errorResponse: any): string =>
  errorResponse?.response?.data?.error || 'Error occurred';

interface MutationParams {
  onSuccess?: (pr: IssuePullRequestType) => void;
  onError?: (error: string) => void;
}

export function useLinkPullRequestMutation({
  onSuccess,
  onError,
}: MutationParams) {
  const { issuePullRequestsStore } = useContextStore();

  return useMutation({
    mutationFn: ({ issueId, url }: { issueId: string; url: string }) =>
      linkPullRequest(issueId, url),
    onSuccess: (pr: IssuePullRequestType) => {
      issuePullRequestsStore.update(pr, pr.id);
      onSuccess && onSuccess(pr);
    },
    onError: (errorResponse) => onError && onError(errorText(errorResponse)),
  });
}

export function useUnlinkPullRequestMutation({
  onSuccess,
  onError,
}: MutationParams) {
  const { issuePullRequestsStore } = useContextStore();

  return useMutation({
    mutationFn: ({ issueId, linkId }: { issueId: string; linkId: string }) =>
      unlinkPullRequest(issueId, linkId),
    onSuccess: (pr: IssuePullRequestType) => {
      issuePullRequestsStore.deleteById(pr.id);
      onSuccess && onSuccess(pr);
    },
    onError: (errorResponse) => onError && onError(errorText(errorResponse)),
  });
}
