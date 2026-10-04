import {
  createAgent,
  deleteAgent,
  getAgents,
  issueAgentToken,
  revokeAgentToken,
  rotateAgentToken,
  type AgentData,
  type AgentDriver,
  type AgentListEntry,
  type AgentTokenRotation,
  type AgentTokenSpec,
} from '@converge/services';
import { useMutation, useQuery, useQueryClient } from 'common/lib/react-query';

import { GetUserQuery } from 'services/users';

interface MutationParams {
  onMutate?: () => void;
  onSuccess?: (data: AgentData) => void;
  onError?: (error: string) => void;
}

function errorText(
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  errorResponse: any,
): string {
  return errorResponse?.response?.data?.error || 'Error occurred';
}

export interface CreateAgentVariables {
  workspaceId: string;
  name: string;
  teamIds: string[];
  driver?: AgentDriver;
  token?: AgentTokenSpec;
}

export function useCreateAgentMutation({
  onMutate,
  onSuccess,
  onError,
}: MutationParams) {
  const queryClient = useQueryClient();

  return useMutation(
    ({ workspaceId, ...data }: CreateAgentVariables) =>
      createAgent(workspaceId, data),
    {
      onMutate: () => onMutate && onMutate(),
      onError: (
        e: {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          response?: { data?: { error?: string } };
        } & Error,
      ) => onError && onError(errorText(e)),
      onSuccess: (data) => {
        // The new membership lands through the realtime stream, but the
        // users list the dialog's team checkboxes read from is query-cached.
        queryClient.invalidateQueries({ queryKey: [GetUserQuery] });
        onSuccess && onSuccess(data);
      },
    },
  );
}

export function useGetAgentsQuery(
  workspaceId: string,
  enabled = true,
): {
  data: AgentListEntry[] | undefined;
  isLoading: boolean;
  refetch: () => void;
} {
  return useQuery(['agents', workspaceId], () => getAgents(workspaceId), {
    enabled,
    staleTime: 30_000,
  });
}

export function useIssueAgentTokenMutation({
  onMutate,
  onSuccess,
  onError,
}: Omit<MutationParams, 'onSuccess'> & {
  onSuccess?: (data: Awaited<ReturnType<typeof issueAgentToken>>) => void;
}) {
  const queryClient = useQueryClient();

  return useMutation(
    ({
      workspaceId,
      accountId,
      ...data
    }: {
      workspaceId: string;
      accountId: string;
      name?: string;
    } & AgentTokenSpec) => issueAgentToken(workspaceId, accountId, data),
    {
      onMutate: () => onMutate && onMutate(),
      onError: (
        e: {
          response?: { data?: { error?: string } };
        } & Error,
      ) => onError && onError(errorText(e)),
      onSuccess: (data, params) => {
        queryClient.invalidateQueries({
          queryKey: ['agents', params.workspaceId],
        });
        onSuccess && onSuccess(data);
      },
    },
  );
}

export function useRotateAgentTokenMutation({
  onMutate,
  onSuccess,
  onError,
}: Omit<MutationParams, 'onSuccess'> & {
  onSuccess?: (data: AgentTokenRotation) => void;
}) {
  const queryClient = useQueryClient();

  return useMutation(
    (params: {
      workspaceId: string;
      accountId: string;
      tokenId: string;
      graceHours: number;
    }) =>
      rotateAgentToken(params.workspaceId, params.accountId, {
        tokenId: params.tokenId,
        graceHours: params.graceHours,
      }),
    {
      onMutate: () => onMutate && onMutate(),
      onError: (
        e: {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          response?: { data?: { error?: string } };
        } & Error,
      ) => onError && onError(errorText(e)),
      onSuccess: (data, params) => {
        queryClient.invalidateQueries({
          queryKey: ['agents', params.workspaceId],
        });
        onSuccess && onSuccess(data);
      },
    },
  );
}

export function useRevokeAgentTokenMutation({
  onMutate,
  onSuccess,
  onError,
}: Omit<MutationParams, 'onSuccess'> & {
  onSuccess?: (data: { revoked: number }) => void;
}) {
  const queryClient = useQueryClient();

  return useMutation(
    (params: { workspaceId: string; accountId: string; tokenId?: string }) =>
      revokeAgentToken(
        params.workspaceId,
        params.accountId,
        params.tokenId ? { tokenId: params.tokenId } : {},
      ),
    {
      onMutate: () => onMutate && onMutate(),
      onError: (
        e: {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          response?: { data?: { error?: string } };
        } & Error,
      ) => onError && onError(errorText(e)),
      onSuccess: (data, params) => {
        queryClient.invalidateQueries({
          queryKey: ['agents', params.workspaceId],
        });
        onSuccess && onSuccess(data);
      },
    },
  );
}

export function useDeleteAgentMutation({
  onMutate,
  onSuccess,
  onError,
}: MutationParams & {
  onSuccess?: (data: { deleted: boolean }) => void;
}) {
  const queryClient = useQueryClient();

  return useMutation(
    (params: { workspaceId: string; accountId: string }) =>
      deleteAgent(params.workspaceId, params.accountId),
    {
      onMutate: () => onMutate && onMutate(),
      onError: (
        e: {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          response?: { data?: { error?: string } };
        } & Error,
      ) => onError && onError(errorText(e)),
      onSuccess: (data) => {
        // The DELETE sync record updates the member list live; refresh the
        // users lookup so the removed agent drops out of pickers.
        queryClient.invalidateQueries({ queryKey: [GetUserQuery] });
        onSuccess && onSuccess(data);
      },
    },
  );
}
