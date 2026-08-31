import type { Pat } from '@converge/types';

import { getPats } from '@converge/services';
import { type UseQueryResult, useQuery } from 'react-query';

import { type XHRErrorResponse } from 'services/utils';

/**
 * Query Key for Get user.
 */
export const GetPats = 'getPats';

export function useGetPatsQuery(): UseQueryResult<Pat[], XHRErrorResponse> {
  return useQuery([GetPats], () => getPats(), {
    retry: 1,
    staleTime: 1,
    refetchOnWindowFocus: false, // Frequency of Change would be Low
  });
}
