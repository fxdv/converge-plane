import {
  type IAnyStateTreeNode,
  type Instance,
  types,
} from 'mobx-state-tree';

import type { SwarmActivityType as WireSwarmActivity } from 'common/types';

import { SwarmActivity, type SwarmActivityType } from './models';

// swarmActivityTTL bounds how long a signal may stand unrefreshed
// before the client treats it as stale: longer than the LLM decision
// bound (the slowest phase) plus emission slack, shorter than any
// plausible human "is that stuck?" patience.
export const swarmActivityTTL = 2 * 60 * 1000;

// The in-flight swarm signals for one workspace: one entry per agent
// (keyed by agent id — the sync record's model id). In-memory only by
// design: the signal is ephemeral, the server replays the live runtime
// state on every bootstrap, and a stale entry is noise, not data.
export const SwarmActivityStore: IAnyStateTreeNode = types
  .model('SwarmActivityStore', {
    activities: types.map(SwarmActivity),
  })
  .views((self) => ({
    // The signal for one issue (the board's live chip reads this).
    forIssue(issueId: string): SwarmActivityType | undefined {
      for (const activity of self.activities.values()) {
        if (activity.issueId === issueId) {
          return activity;
        }
      }
      return undefined;
    },
    isFresh(activity: SwarmActivityType): boolean {
      return Date.now() - new Date(activity.since).getTime() < swarmActivityTTL;
    },
  }))
  .actions((self) => ({
    upsert(record: WireSwarmActivity) {
      // .set, not .put: every model in this repo declares a plain
      // string id (no MST identifier attribute), and .put demands one
      // (the wire-contract test pins the crash). .set passes the key
      // explicitly, the same convention as the issues/workflows maps.
      self.activities.set(record.id, record);
    },
    remove(agentId: string) {
      if (agentId && self.activities.has(agentId)) {
        self.activities.delete(agentId);
      }
    },
    // Drops signals that outlived the TTL. Called on a slow tick while
    // the workspace is mounted; it is a hygiene pass, not a clock.
    expireStale() {
      const cutoff = Date.now() - swarmActivityTTL;
      for (const [agentId, activity] of self.activities) {
        if (new Date(activity.since).getTime() < cutoff) {
          self.activities.delete(agentId);
        }
      }
    },
  }));

export type SwarmActivityStoreType = Instance<typeof SwarmActivityStore>;
