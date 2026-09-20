import {
  type IAnyStateTreeNode,
  type Instance,
  types,
  flow,
} from 'mobx-state-tree';

import type { IssueArtifactType } from 'common/types';

import { convergeDatabase } from 'store/database';

import { IssueArtifactArray } from './models';

// SWR-56: the documents the swarm has posted, keyed by issue (the same
// shape as the comments store: one array per issue, upsert + delete by
// id, hydrated from the object cache on the single-issue page).
export const IssueArtifactsStore: IAnyStateTreeNode = types
  .model({
    issueArtifacts: types.map(IssueArtifactArray),
  })
  .actions((self) => {
    const update = (artifact: IssueArtifactType, id: string) => {
      const issueId = artifact.issueId;
      if (!self.issueArtifacts.has(issueId)) {
        self.issueArtifacts.set(issueId, IssueArtifactArray.create([]));
      }

      const artifactsArray = self.issueArtifacts.get(issueId);
      const indexToUpdate = artifactsArray.findIndex((obj) => obj.id === id);

      if (indexToUpdate !== -1) {
        // Update the object at the found index with the new data
        artifactsArray[indexToUpdate] = {
          ...artifactsArray[indexToUpdate],
          ...artifact,
        };
      } else {
        artifactsArray.push(artifact);
      }
    };

    const deleteById = (id: string) => {
      // Iterate through all artifact arrays in the map
      for (const [issueId, artifactsArray] of self.issueArtifacts.entries()) {
        const indexToDelete = artifactsArray.findIndex((obj) => obj.id === id);

        if (indexToDelete !== -1) {
          artifactsArray.splice(indexToDelete, 1);
          // If the array is empty, remove the issue key from the map
          if (artifactsArray.length === 0) {
            self.issueArtifacts.delete(issueId);
          }
          break; // Exit loop once we've found and deleted the artifact
        }
      }
    };

    const load = flow(function* (issueId: string) {
      const artifacts = issueId
        ? yield convergeDatabase.issueArtifacts
            .where({
              issueId,
            })
            .toArray()
        : [];

      // Create a new IssueArtifactArray for this issueId
      if (artifacts.length > 0) {
        self.issueArtifacts.set(issueId, IssueArtifactArray.create(artifacts));
      }
    });

    return { update, deleteById, load };
  })
  .views((self) => ({
    getArtifacts(issueId: string): IssueArtifactType[] {
      return self.issueArtifacts.has(issueId)
        ? self.issueArtifacts.get(issueId)
        : [];
    },
  }));

export type IssueArtifactsStoreType = Instance<typeof IssueArtifactsStore>;
