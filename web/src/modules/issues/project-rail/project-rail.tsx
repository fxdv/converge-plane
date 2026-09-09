import { RoleEnum } from '@converge/types';
import { AddLine } from '@converge/ui/icons';
import { Button } from '@converge/ui/components/button';
import { Input } from '@converge/ui/components/input';
import { observer } from 'mobx-react-lite';
import React from 'react';

import { generateOklchColor } from 'common/color-utils';
import type {
  IssueType,
  ProjectType,
  UsersOnWorkspaceType,
  WorkflowType,
} from 'common/types';

import { useCurrentTeam } from 'hooks/teams';
import { useCurrentWorkspace } from 'hooks/workspace';

import { useFilterIssues } from 'modules/issues/issues-utils';

import { useCreateProjectMutation } from 'services/projects';

import { useContextStore } from 'store/global-context-provider';
import { UserContext } from 'store/user-context';

import { ProjectBoardList } from './project-board-list';

interface ProjectRailProps {
  // The current view's workflow set (drives the view filtering).
  workflows: WorkflowType[];
}

// The project rail (v1.1, spec cs:ui:projects-rail): a stack per project
// that has at least one issue in the current view, beside the board's
// state columns. A card shows in its project stack AND its state column
// (a project is a grouping, never a state); dropping a card on a stack
// assigns the project, dragging it out clears it (the board's shared
// onDragEnd carries both mutations through the issue patch).
//
// Owners/admins get a create row; the rail renders nothing for a member
// whose view has no project issues, so it never dead-weights the board.
export const ProjectRail = observer(({ workflows }: ProjectRailProps) => {
  const team = useCurrentTeam();
  const workspace = useCurrentWorkspace();
  const user = React.useContext(UserContext);

  const { projectsStore, issuesStore, workspaceStore } = useContextStore();

  // The rail's source is the same filtered set the columns render:
  // this team's issues through the view's filters (single hook call).
  const teamIssues: IssueType[] = team
    ? issuesStore.getIssues({ teamId: team.id })
    : [];
  const viewIssues = useFilterIssues(teamIssues, workflows);

  const [creating, setCreating] = React.useState(false);
  const [name, setName] = React.useState('');
  const color = React.useMemo(() => generateOklchColor(), [creating]);
  const { mutate: createProject, isLoading: creatingBusy } =
    useCreateProjectMutation({
      onSuccess: () => {
        setCreating(false);
        setName('');
      },
    });

  if (!team || !workspace) {
    return null;
  }

  const isAdmin =
    workspaceStore.usersOnWorkspaces.find(
      (u: UsersOnWorkspaceType) => u.userId === user.id,
    )?.role === RoleEnum.ADMIN;

  const projects: ProjectType[] =
    projectsStore.getProjectsForTeam(team.id);

  // Only stacks with at least one issue in view (the approved design);
  // keep the rail visible for an admin so the create row has a home.
  const stacks = projects
    .map((project) => ({
      project,
      issues: viewIssues.filter((issue) =>
        issue.projectIds?.includes(project.id),
      ),
    }))
    .filter((stack) => stack.issues.length > 0);

  if (stacks.length === 0 && !isAdmin) {
    return null;
  }

  const submitCreate = () => {
    if (!team || !workspace || name.trim().length === 0) {
      return;
    }
    createProject({
      name: name.trim(),
      color,
      teams: [team.id],
      workspaceId: workspace.id,
    });
  };

  return (
    <div className="flex flex-col gap-2 w-[350px] shrink-0 max-h-full pr-2">
      {isAdmin && (
        creating ? (
          <div className="group flex justify-between mb-0 bg-background-3 dark:bg-grayAlpha-100 rounded-xl p-2 px-4">
            <div className="flex items-center justify-center gap-3 w-full">
              <div
                className="h-3 w-3 rounded-full shrink-0"
                style={{ backgroundColor: color }}
              />
              <div className="grow min-w-0">
                <Input
                  value={name}
                  className="w-full"
                  placeholder="Project name"
                  onChange={(e) => setName(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && name.trim()) {
                      submitCreate();
                    }
                    if (e.key === 'Escape') {
                      setCreating(false);
                    }
                  }}
                />
              </div>
              <div className="flex gap-2 shrink-0">
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={creatingBusy}
                  onClick={() => setCreating(false)}
                >
                  Cancel
                </Button>
                <Button
                  isLoading={creatingBusy}
                  variant="secondary"
                  size="sm"
                  disabled={name.trim().length === 0}
                  onClick={() => submitCreate()}
                >
                  Save
                </Button>
              </div>
            </div>
          </div>
        ) : (
          <button
            className="flex items-center gap-2 w-full bg-background-3 dark:bg-grayAlpha-100 hover:bg-background-3/70 rounded-xl p-2.5 px-3 text-xs font-medium text-muted-foreground"
            onClick={() => {
              setName('');
              setCreating(true);
            }}
          >
            <AddLine size={14} />
            New project
          </button>
        )
      )}

      {stacks.map(({ project, issues }) => (
        <ProjectBoardList
          key={project.id}
          project={project}
          issues={issues}
          isAdmin={isAdmin}
        />
      ))}
    </div>
  );
});
