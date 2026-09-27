import type { AgentRunsStoreType } from './store';

import type {
  AgentRunType,
  RunEvidenceType,
  SyncActionRecord,
} from 'common/types';

import { convergeDatabase } from 'store/database';

// Maps an AgentRun wire record to the store shape. Pure, so the wire
// contract harness can pin it.
export function agentRunFromRecord(
  data: SyncActionRecord['data'],
): AgentRunType {
  const evidence: RunEvidenceType[] = Array.isArray(data.evidence)
    ? data.evidence.map((ev: RunEvidenceType) => ({
        kind: ev.kind,
        url: ev.url,
        title: ev.title ?? null,
      }))
    : [];
  return {
    id: data.id,
    createdAt: data.createdAt,
    updatedAt: data.updatedAt,
    issueId: data.issueId,
    agentId: data.agentId,
    claimId: data.claimId ?? null,
    startedAt: data.startedAt,
    endedAt: data.endedAt ?? null,
    endReason: data.endReason ?? null,
    outcome: data.outcome ?? null,
    summary: data.summary ?? null,
    model: data.model ?? null,
    inputTokens: Number(data.inputTokens ?? 0),
    outputTokens: Number(data.outputTokens ?? 0),
    costMicros: Number(data.costMicros ?? 0),
    eventCount: Number(data.eventCount ?? 0),
    evidence,
  };
}

export async function saveAgentRunsData(
  data: SyncActionRecord[],
  agentRunsStore: AgentRunsStoreType,
) {
  await Promise.all(
    data.map(async (record: SyncActionRecord) => {
      switch (record.action) {
        case 'I':
        case 'U': {
          const run = agentRunFromRecord(record.data);
          await convergeDatabase.agentRuns.put(run);
          return agentRunsStore && agentRunsStore.update(run, run.id);
        }

        case 'D': {
          await convergeDatabase.agentRuns.delete(record.data.id);
          return agentRunsStore && agentRunsStore.deleteById(record.data.id);
        }
      }
    }),
  );
}
