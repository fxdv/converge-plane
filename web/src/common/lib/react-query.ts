import {
  keepPreviousData,
  useMutation as useTanstackMutation,
  useQuery as useTanstackQuery,
  type QueryKey,
  type UseMutationOptions,
  type UseMutationResult,
  type UseQueryOptions,
  type UseQueryResult,
} from '@tanstack/react-query';
import * as React from 'react';

export {
  HydrationBoundary,
  QueryCache,
  QueryClient,
  QueryClientProvider,
  useInfiniteQuery,
  useQueryClient,
} from '@tanstack/react-query';

export type { UseMutateFunction, UseQueryResult } from '@tanstack/react-query';

type CompatMutationResult<TData, TError, TVariables, TContext> =
  UseMutationResult<TData, TError, TVariables, TContext> & {
    isLoading: boolean;
  };

function withLoadingAlias<TData, TError, TVariables, TContext>(
  result: UseMutationResult<TData, TError, TVariables, TContext>,
): CompatMutationResult<TData, TError, TVariables, TContext> {
  return { ...result, isLoading: result.isPending };
}

type LegacyQueryOptions<
  TQueryFnData,
  TError,
  TData,
  TQueryKey extends QueryKey,
> = Omit<
  UseQueryOptions<TQueryFnData, TError, TData, TQueryKey>,
  'queryKey' | 'queryFn'
> & {
  onSuccess?: (data: TData) => void;
  /** react-query v3 name; maps to placeholderData: keepPreviousData. */
  keepPreviousData?: boolean;
};

/** v3-shaped useQuery for incremental migration to TanStack Query v5. */
export function useQuery<
  TQueryFnData = unknown,
  TError = Error,
  TData = TQueryFnData,
  TQueryKey extends QueryKey = QueryKey,
>(
  queryKey: TQueryKey,
  queryFn: () => Promise<TQueryFnData>,
  options?: LegacyQueryOptions<TQueryFnData, TError, TData, TQueryKey>,
): UseQueryResult<TData, TError> {
  const { onSuccess, keepPreviousData: keepPrev, ...rest } = options ?? {};
  const result = useTanstackQuery({
    queryKey,
    queryFn,
    ...rest,
    ...(keepPrev ? { placeholderData: keepPreviousData } : {}),
  });

  const lastHandled = React.useRef<TData | undefined>(undefined);
  React.useEffect(() => {
    if (!onSuccess || !result.isSuccess || result.data === undefined) {
      return;
    }
    if (lastHandled.current === result.data) {
      return;
    }
    lastHandled.current = result.data as TData;
    onSuccess(result.data as TData);
  }, [onSuccess, result.data, result.isSuccess]);

  return result;
}

/** v3-shaped useMutation (`isLoading` alias) for TanStack Query v5. */
export function useMutation<
  TData = unknown,
  TError = Error,
  TVariables = void,
  TContext = unknown,
>(
  mutationFnOrOptions:
    | ((variables: TVariables) => Promise<TData>)
    | UseMutationOptions<TData, TError, TVariables, TContext>,
  options?: Omit<
    UseMutationOptions<TData, TError, TVariables, TContext>,
    'mutationFn'
  >,
): CompatMutationResult<TData, TError, TVariables, TContext> {
  if (typeof mutationFnOrOptions === 'function') {
    return withLoadingAlias(
      useTanstackMutation({ mutationFn: mutationFnOrOptions, ...options }),
    );
  }
  return withLoadingAlias(useTanstackMutation(mutationFnOrOptions));
}
