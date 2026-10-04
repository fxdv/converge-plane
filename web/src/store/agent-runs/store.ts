import {
  type IAnyStateTreeNode,
  type Instance,
  types,
  flow,
} from 'mobx-state-tree';

import type { AgentRunType } from 'common/types';

import { isMemoryAuthorityModel } from 'store/client-cache-policy';
import { convergeDatabase } from 'store/database';
import { MODELS } from 'store/models';

import { AgentRun } from './models';

export interface IssueRunTotals {
  runs: number;
  costMicros: number;
  tokens: number;
  running: boolean;
}

// The workspace's agent runs, keyed by id. Loaded whole at workspace
// start (the board's cost chip needs every issue's total, not only the
// open issue's).
export const AgentRunsStore: IAnyStateTreeNode = types
  .model({
    runs: types.map(AgentRun),
  })
  .actions((self) => {
    const update = (run: AgentRunType, id: string) => {
      self.runs.set(id, run);
    };

    const deleteById = (id: string) => {
      self.runs.delete(id);
    };

    const load = flow(function* () {
      if (isMemoryAuthorityModel(MODELS.AgentRun)) {
        return;
      }
      const runs: AgentRunType[] = yield convergeDatabase.agentRuns.toArray();
      runs.forEach((run) => self.runs.set(run.id, run));
    });

    return { update, deleteById, load };
  })
  .views((self) => ({
    // One pass over the runs, cached until a run changes: every board
    // card reads its totals from here.
    get totalsByIssue(): Map<string, IssueRunTotals> {
      const out = new Map<string, IssueRunTotals>();
      for (const run of self.runs.values()) {
        const totals = out.get(run.issueId) ?? {
          runs: 0,
          costMicros: 0,
          tokens: 0,
          running: false,
        };
        totals.runs += 1;
        totals.costMicros += run.costMicros;
        totals.tokens += run.inputTokens + run.outputTokens;
        totals.running = totals.running || run.endedAt === null;
        out.set(run.issueId, totals);
      }
      return out;
    },
  }))
  .views((self) => ({
    // Oldest first (ISO stamps sort lexicographically).
    getRunsForIssue(issueId: string): AgentRunType[] {
      return Array.from(self.runs.values())
        .filter((run: AgentRunType) => run.issueId === issueId)
        .sort((a: AgentRunType, b: AgentRunType) =>
          a.startedAt.localeCompare(b.startedAt),
        );
    },
    getIssueTotals(issueId: string): IssueRunTotals | undefined {
      return self.totalsByIssue.get(issueId);
    },
  }));

export type AgentRunsStoreType = Instance<typeof AgentRunsStore>;
