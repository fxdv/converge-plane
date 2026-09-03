import { getSwarmStatus, type SwarmStatus } from '@converge/services';

import { useQuery } from 'react-query';

// D2: the swarm panel polls the fleet roster while open (30s keeps it
// live without pressuring the API; issue-level changes already arrive
// in realtime through the sync feed, this is the fleet-level view).
// Disabled while the panel is closed — the board's "has agents" check
// reads the synced users store, so no fetch is needed for the button.
export function useSwarmQuery(
  workspaceId: string,
  enabled = true,
): {
  data: SwarmStatus | undefined;
  isLoading: boolean;
  refetch: () => void;
} {
  const { data, isLoading, refetch } = useQuery(
    ['swarm', workspaceId],
    () => getSwarmStatus(workspaceId),
    {
      enabled: Boolean(workspaceId) && enabled,
      staleTime: 15_000,
      refetchInterval: enabled ? 30_000 : false,
      refetchOnWindowFocus: false,
    },
  );

  return { data, isLoading, refetch };
}
