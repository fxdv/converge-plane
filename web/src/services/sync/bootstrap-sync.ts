import type { UseQueryResult } from '@tanstack/react-query';

import { useQueryWithOnSuccess } from 'common/lib/query-on-success';
import type { BootstrapResponse } from 'common/types';

import { type XHRErrorResponse, ajaxGet } from 'services/utils';

/**
 * Query Key for Get bootstrap records.
 */
export const GetBootstrapRecords = 'getBootstrapRecords';

export function getBootstrapRecords(
  workspaceId: string,
  modelNames: string[],
  userId: string,
) {
  return ajaxGet({
    url: `/api/v1/sync_actions/bootstrap`,
    query: {
      workspaceId,
      userId,
      modelNames: modelNames.join(','),
    },
  });
}

interface QueryParams {
  workspaceId: string;
  userId: string;
  modelNames: string[];
  onSuccess?: (data: BootstrapResponse) => void;
}

export function useBootstrapRecords({
  workspaceId,
  userId,
  modelNames,
  onSuccess,
}: QueryParams): UseQueryResult<BootstrapResponse, XHRErrorResponse> {
  return useQueryWithOnSuccess({
    queryKey: [GetBootstrapRecords, modelNames, workspaceId, userId],
    queryFn: () => getBootstrapRecords(workspaceId, modelNames, userId),
    retry: 1,
    staleTime: 1,
    enabled: false,
    onSuccess,
    refetchOnWindowFocus: false,
  });
}
