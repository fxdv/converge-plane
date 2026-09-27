import axios from 'axios';

// The run ledger (docs/spec 08, "Work API"): one page of an agent run's
// trace. The run itself arrives over the sync feed (AgentRun); the trace
// is fetched on demand because it can hold up to 1000 lines per run.
// Messages are agent-authored text: render them as text, never markup.
export interface RunEvent {
  seq: number;
  at: string;
  kind: 'step' | 'tool' | 'note' | 'error';
  message: string;
}

export interface RunEventsPage {
  events: RunEvent[];
  // The seq to pass as `after` for the next page; null on the last page.
  nextAfter: number | null;
}

export async function getRunEvents(
  issueId: string,
  runId: string,
  after = 0,
): Promise<RunEventsPage> {
  const response = await axios.get(
    `/api/v1/issues/${issueId}/runs/${runId}/events`,
    { params: { after, limit: 200 } },
  );

  return response.data;
}
