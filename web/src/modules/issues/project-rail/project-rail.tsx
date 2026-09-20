import { RoleEnum } from '@converge/types';
import { Button } from '@converge/ui/components/button';
import { Input } from '@converge/ui/components/input';
import { AddLine } from '@converge/ui/icons';
import { observer } from 'mobx-react-lite';
import React from 'react';

import { useFilterIssues } from 'modules/issues/issues-utils';

import { generateOklchColor } from 'common/color-utils';
import type {
  IssueType,
  ProjectType,
  UsersOnWorkspaceType,
  WorkflowType,
} from 'common/types';

import { useCurrentTeam } from 'hooks/teams';
import { useCurrentWorkspace } from 'hooks/workspace';

import { useCreateProjectMutation } from 'services/projects';

import { useContextStore } from 'store/global-context-provider';
import { UserContext } from 'store/user-context';

import { ProjectChip } from './project-chip';
import { railStackVisible } from './project-focus';

interface ProjectRailProps {
  // The current view's workflow set (drives the view filtering).
  workflows: WorkflowType[];
  // The board's focus lens (spec cs:ui:projects-rail): which project
  // is focused (`null` = no lens), and how to set or release it. The
  // board owns the state; the rail only reports it.
  focusedProjectId: string | null;
  onToggleFocus: (projectId: string | null) => void;
}

// The project rail (v1.2, spec cs:ui:projects-rail): one compact chip
// per project, beside the board's state columns.
//
// A project is a grouping, never a state: the card renders exactly
// once — in its state column — and the chip is a color index into the
// board (its color, its name, its done/total). Clicking a chip focuses
// the board on that project (its cards ring, the rest dim); dropping a
// column card on a chip assigns the project. The rail's single scroll
// container is the only scroll on the board.
//
// Owners/admins get a create row; the rail renders nothing for a member
// whose view has no project cards, so it never dead-weights the board.
export const ProjectRail = observer(
  ({ workflows, focusedProjectId, onToggleFocus }: ProjectRailProps) => {
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

    const projects: ProjectType[] = projectsStore.getProjectsForTeam(team.id);

    // A chip renders for every project on this team's board — including
    // brand-new empty ones (a created project must appear at once, as a
    // drop target to fill). A chip hides only when the project HAS
    // issues but the current view filters every one of them out.
    const stacks = projects
      .map((project) => {
        const inView = viewIssues.filter((issue) =>
          issue.projectIds?.includes(project.id),
        );
        const anywhere = teamIssues.filter((issue) =>
          issue.projectIds?.includes(project.id),
        );
        return {
          project,
          issues: inView,
          hidden: !railStackVisible(inView.length, anywhere.length),
        };
      })
      .filter((stack) => !stack.hidden);

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

    // A click on the rail's own background (not a chip) releases the
    // lens. The inner scroll container gets the same rule: the space
    // between and below chips is background.
    const releaseLens = (e: React.MouseEvent) => {
      if (e.target === e.currentTarget) {
        onToggleFocus(null);
      }
    };

    return (
      // 350px content (the columns' width) + a 12px right gutter (the
      // columns' spacing): the rail aligns with the board and keeps a
      // visible gap before the first column.
      <div
        className="flex flex-col w-[362px] shrink-0 h-full pr-3"
        onClick={releaseLens}
      >
        {isAdmin && (
          <div className="shrink-0 mb-2">
            {creating ? (
              <div className="flex justify-between bg-background-3 dark:bg-grayAlpha-100 rounded-md p-2 px-3">
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
                          // The board's Esc releases the lens; the
                          // create form's Esc must not reach it.
                          e.stopPropagation();
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
              // Quiet by default: a dashed outline that only warms on
              // hover — the chips, not the create row, are the content.
              <button
                className="flex items-center gap-2 w-full border border-dashed border-grayAlpha-300/70 dark:border-grayAlpha-200/40 hover:bg-background-3/50 dark:hover:bg-grayAlpha-100/25 rounded-md p-2.5 px-3 text-xs font-medium text-muted-foreground transition-colors"
                onClick={() => {
                  setName('');
                  setCreating(true);
                }}
              >
                <AddLine size={14} />
                New project
              </button>
            )}
          </div>
        )}

        <div
          className="flex-1 min-h-0 overflow-y-auto flex flex-col gap-2 pb-2"
          onClick={releaseLens}
        >
          {stacks.map(({ project, issues }) => (
            <ProjectChip
              key={project.id}
              project={project}
              issues={issues}
              isAdmin={isAdmin}
              focusedProjectId={focusedProjectId}
              onToggleFocus={onToggleFocus}
            />
          ))}
        </div>
      </div>
    );
  },
);
