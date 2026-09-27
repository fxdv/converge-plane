import {
  type IAnyStateTreeNode,
  type Instance,
  types,
  flow,
} from 'mobx-state-tree';

import type { IssuePullRequestType } from 'common/types';

import { convergeDatabase } from 'store/database';

import { IssuePullRequest } from './models';

// The workspace's tracked pull requests, keyed by id. Loaded whole at
// workspace start: every board card reads its issue's PRs.
export const IssuePullRequestsStore: IAnyStateTreeNode = types
  .model({
    pullRequests: types.map(IssuePullRequest),
  })
  .actions((self) => {
    const update = (pr: IssuePullRequestType, id: string) => {
      self.pullRequests.set(id, pr);
    };

    const deleteById = (id: string) => {
      self.pullRequests.delete(id);
    };

    const load = flow(function* () {
      const rows: IssuePullRequestType[] =
        yield convergeDatabase.issuePullRequests.toArray();
      rows.forEach((pr) => self.pullRequests.set(pr.id, pr));
    });

    return { update, deleteById, load };
  })
  .views((self) => ({
    // One pass, cached until a PR changes; each issue's list is oldest
    // link first (ISO stamps sort lexicographically).
    get byIssue(): Map<string, IssuePullRequestType[]> {
      const out = new Map<string, IssuePullRequestType[]>();
      for (const pr of self.pullRequests.values()) {
        const list = out.get(pr.issueId) ?? [];
        list.push(pr);
        out.set(pr.issueId, list);
      }
      for (const list of out.values()) {
        list.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
      }
      return out;
    },
  }))
  .views((self) => ({
    getForIssue(issueId: string): IssuePullRequestType[] {
      return self.byIssue.get(issueId) ?? [];
    },
  }));

export type IssuePullRequestsStoreType = Instance<
  typeof IssuePullRequestsStore
>;
