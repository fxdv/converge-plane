import {
  useQuery,
  type QueryKey,
  type UseQueryOptions,
  type UseQueryResult,
} from '@tanstack/react-query';
import * as React from 'react';

type OptionsWithOnSuccess<
  TQueryFnData,
  TError,
  TData,
  TQueryKey extends QueryKey,
> = UseQueryOptions<TQueryFnData, TError, TData, TQueryKey> & {
  onSuccess?: (data: TData) => void;
};

function syncWatermark(data: unknown): string | undefined {
  if (data && typeof data === 'object' && 'lastSequenceId' in data) {
    const id = (data as { lastSequenceId: unknown }).lastSequenceId;
    if (id !== undefined && id !== null) {
      return String(id);
    }
  }
  return undefined;
}

/** TanStack Query v5 removed `onSuccess`; used by bootstrap/delta sync hooks only. */
export function useQueryWithOnSuccess<
  TQueryFnData,
  TError = Error,
  TData = TQueryFnData,
  TQueryKey extends QueryKey = QueryKey,
>(
  options: OptionsWithOnSuccess<TQueryFnData, TError, TData, TQueryKey>,
): UseQueryResult<TData, TError> {
  const { onSuccess, ...queryOptions } = options;
  const result = useQuery(queryOptions);
  const lastWatermark = React.useRef<string | undefined>(undefined);
  const lastDataRef = React.useRef<TData | undefined>(undefined);

  React.useEffect(() => {
    if (!onSuccess || !result.isSuccess || result.data === undefined) {
      return;
    }
    const watermark = syncWatermark(result.data);
    if (watermark !== undefined) {
      if (lastWatermark.current === watermark) {
        return;
      }
      lastWatermark.current = watermark;
    } else if (lastDataRef.current === result.data) {
      return;
    }
    lastDataRef.current = result.data as TData;
    onSuccess(result.data as TData);
  }, [onSuccess, result.data, result.isSuccess]);

  return result;
}
