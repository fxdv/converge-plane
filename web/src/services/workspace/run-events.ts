import { getRunEvents, type RunEventsPage } from '@converge/services';
import { useInfiniteQuery, type InfiniteData } from '@tanstack/react-query';

// A run's trace, page by page (200 lines each), fetched only while the
// trace is open. The run's eventCount is part of the key, so a live run
// that reports more lines refetches.
export function useRunEventsQuery(
  issueId: string,
  runId: string,
  eventCount: number,
  enabled: boolean,
) {
  return useInfiniteQuery<
    RunEventsPage,
    Error,
    InfiniteData<RunEventsPage>,
    readonly ['runEvents', string, number],
    number
  >({
    queryKey: ['runEvents', runId, eventCount],
    queryFn: ({ pageParam }) => getRunEvents(issueId, runId, pageParam),
    initialPageParam: 0,
    enabled: enabled && eventCount > 0,
    getNextPageParam: (last) => last.nextAfter ?? undefined,
    staleTime: 10_000,
    refetchOnWindowFocus: false,
  });
}
