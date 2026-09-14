import {
  getWorkspaceMetrics,
  type WorkspaceMetrics,
} from '@converge/services';

import { useQuery } from 'react-query';

// The metrics plane polls on the swarm's cadence (30s): the sections are
// live gauges (goroutines, pool, registry), so a slower clock would show
// a process that is moving as one that is frozen.
export function useMetricsQuery(
  workspaceId: string,
  enabled = true,
): {
  data: WorkspaceMetrics | undefined;
  isLoading: boolean;
  refetch: () => void;
} {
  const { data, isLoading, refetch } = useQuery(
    ['metrics', workspaceId],
    () => getWorkspaceMetrics(workspaceId),
    {
      enabled: Boolean(workspaceId) && enabled,
      staleTime: 15_000,
      refetchInterval: enabled ? 30_000 : false,
      refetchOnWindowFocus: false,
    },
  );

  return { data, isLoading, refetch };
}
