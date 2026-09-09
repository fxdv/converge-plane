import {
  type IAnyStateTreeNode,
  type Instance,
  types,
  flow,
} from 'mobx-state-tree';

import type { ProjectType } from 'common/types';

import { convergeDatabase } from 'store/database';

import { Project } from './models';

export const ProjectsStore: IAnyStateTreeNode = types
  .model({
    projects: types.array(Project),
    workspaceId: types.union(types.string, types.undefined),
  })
  .actions((self) => {
    const update = (project: ProjectType, id: string) => {
      const indexToUpdate = self.projects.findIndex((obj) => obj.id === id);

      if (indexToUpdate !== -1) {
        self.projects[indexToUpdate] = {
          ...self.projects[indexToUpdate],
          ...project,
          // TODO fix the any and have a type with ProjectType
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
        } as any;
      } else {
        self.projects.push(project);
      }
    };
    const deleteById = (id: string) => {
      const indexToDelete = self.projects.findIndex((obj) => obj.id === id);

      if (indexToDelete !== -1) {
        self.projects.splice(indexToDelete, 1);
      }
    };

    const load = flow(function* () {
      const projects = yield convergeDatabase.projects.toArray();

      self.projects = projects;
    });

    return { update, deleteById, load };
  })
  .views((self) => ({
    // A project shows on a team's board when it lists that team, or when
    // it lists no team at all (empty = every team in the workspace).
    getProjectsForTeam(teamId: string) {
      return self.projects.filter(
        (project: ProjectType) =>
          project.teams.length === 0 || project.teams.includes(teamId),
      );
    },
    getProjectById(id: string) {
      return self.projects.find((project: ProjectType) => project.id === id);
    },
  }));

export type ProjectsStoreType = Instance<typeof ProjectsStore>;
