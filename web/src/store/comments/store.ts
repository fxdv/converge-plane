import { type IAnyStateTreeNode, type Instance, types } from 'mobx-state-tree';

import type { IssueCommentType } from 'common/types';

import { CommentArray } from './models';

export const CommentsStore: IAnyStateTreeNode = types
  .model({
    comments: types.map(CommentArray),
  })
  .actions((self) => {
    const update = (comment: IssueCommentType, id: string) => {
      const issueId = comment.issueId;
      if (!self.comments.has(issueId)) {
        self.comments.set(issueId, CommentArray.create([]));
      }

      const commentsArray = self.comments.get(issueId);
      const indexToUpdate = commentsArray.findIndex((obj) => obj.id === id);

      if (indexToUpdate !== -1) {
        // Update the object at the found index with the new data
        commentsArray[indexToUpdate] = {
          ...commentsArray[indexToUpdate],
          ...comment,
        };
      } else {
        commentsArray.push(comment);
      }
    };
    const deleteById = (id: string) => {
      // Iterate through all comment arrays in the map
      for (const [issueId, commentsArray] of self.comments.entries()) {
        const indexToDelete = commentsArray.findIndex((obj) => obj.id === id);

        if (indexToDelete !== -1) {
          commentsArray.splice(indexToDelete, 1);
          // If the comments array is empty, we can remove it from the map
          if (commentsArray.length === 0) {
            self.comments.delete(issueId);
          }
          break; // Exit loop once we've found and deleted the comment
        }
      }
    };

    return { update, deleteById };
  })
  .views((self) => ({
    getComments(issueId: string): IssueCommentType[] {
      return self.comments.has(issueId) ? self.comments.get(issueId) : [];
    },
  }));

export type CommentsStoreType = Instance<typeof CommentsStore>;
