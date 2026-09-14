import { sort } from 'fast-sort';
import {
  type IAnyStateTreeNode,
  type Instance,
  getSnapshot,
  types,
  flow,
} from 'mobx-state-tree';

import type { ViewType } from 'common/types';

import { convergeDatabase } from 'store/database';

import { View, Views } from './models';

export const ViewsStore: IAnyStateTreeNode = types
  .model({
    views: Views,
    workspaceId: types.union(types.string, types.undefined),
  })
  .actions((self) => {
    const update = (view: ViewType, id: string) => {
      const indexToUpdate = self.views.findIndex((obj) => obj.id === id);

      if (indexToUpdate !== -1) {
        // Update the object at the found index with the new data
        // Array slots hold model instances, so the merged snapshot is
        // re-created into one. getSnapshot is the typed snapshot (MST
        // instances expose no toJSON in their typings) and create()
        // validates the wire merge field by field.
        self.views[indexToUpdate] = View.create({
          ...getSnapshot(self.views[indexToUpdate]),
          ...view,
        });
      } else {
        self.views.push(view);
      }
    };
    const deleteById = (id: string) => {
      const indexToDelete = self.views.findIndex((obj) => obj.id === id);

      if (indexToDelete !== -1) {
        self.views.splice(indexToDelete, 1);
      }
    };

    const load = flow(function* () {
      const views = yield convergeDatabase.views.toArray();

      self.views = Views.create(
        sort(views).asc((view: ViewType) => new Date(view.createdAt)),
      );
    });

    return { update, deleteById, load };
  })
  .views((self) => ({
    getViewWithId(viewId: string) {
      const view = self.views.find((view) => {
        return view.id === viewId;
      });

      return view;
    },
    getWorkspaceViews() {
      const view = self.views.filter((view) => {
        return !view.teamId;
      });

      return view;
    },
    getViewsForTeam(teamId: string) {
      return self.views.filter((view) => {
        return view.teamId === teamId;
      });
    },
  }));

export type ViewsStoreType = Instance<typeof ViewsStore>;
