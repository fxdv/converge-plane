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
  const lastDataRef = React.useRef<TData | undefined>(undefined);

  React.useEffect(() => {
    if (!onSuccess || !result.isSuccess || result.data === undefined) {
      return;
    }
    // Identity, not the watermark string: a later snapshot can share
    // lastSequenceId with the stale delta that triggered it and must
    // still be applied.
    if (lastDataRef.current === result.data) {
      return;
    }
    lastDataRef.current = result.data as TData;
    onSuccess(result.data as TData);
  }, [onSuccess, result.data, result.isSuccess]);

  return result;
}
