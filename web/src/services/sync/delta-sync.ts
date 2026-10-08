import type { UseQueryResult } from '@tanstack/react-query';

import { useQueryWithOnSuccess } from 'common/lib/query-on-success';
import type { BootstrapResponse } from 'common/types';

import { type XHRErrorResponse, ajaxGet } from 'services/utils';

/**
 * Query Key for Get Delta records.
 */
export const GetDeltaRecords = 'getDeltaRecords';

export function getDeltaRecords(
  workspaceId: string,
  modelNames: string[],
  lastSequenceId: string,
  userId: string,
): Promise<BootstrapResponse> {
  return ajaxGet<BootstrapResponse, XHRErrorResponse>({
    url: `/api/v1/sync_actions/delta`,
    query: {
      workspaceId,
      userId,
      modelNames: modelNames.join(','),
      lastSequenceId,
    },
  });
}

export interface QueryParams {
  workspaceId: string;
  userId: string;
  modelNames: string[];
  lastSequenceId: string;
  onSuccess?: (data: BootstrapResponse) => void;
}

export function useDeltaRecords({
  workspaceId,
  lastSequenceId,
  modelNames,
  userId,
  onSuccess,
}: QueryParams): UseQueryResult<BootstrapResponse, XHRErrorResponse> {
  return useQueryWithOnSuccess({
    queryKey: [
      GetDeltaRecords,
      modelNames,
      lastSequenceId,
      workspaceId,
      userId,
    ],
    queryFn: () =>
      getDeltaRecords(workspaceId, modelNames, lastSequenceId, userId),
    retry: 1,
    staleTime: 1,
    enabled: false,
    onSuccess,
    refetchOnWindowFocus: false,
  });
}
