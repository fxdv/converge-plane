import { AI, Warning } from '@converge/ui/icons';
import { RoleEnum } from '@converge/types';
import { formatDistanceToNow } from 'date-fns';
import { cn } from '@converge/ui/lib/utils';
import { observer } from 'mobx-react-lite';
import * as React from 'react';

import { HeaderLayout } from 'common/header-layout';
import { AppLayout } from 'common/layouts/app-layout';
import { MainLayout } from 'common/layouts/main-layout';
import { withApplicationStore } from 'common/wrappers/with-application-store';

import { IssueViewContext } from 'components/side-issue-view';
import { useCurrentWorkspace } from 'hooks/workspace';

import {
  useSwarmQuery,
  useUpdateSwarmSettingsMutation,
} from 'services/workspace';

import { UserContext } from 'store/user-context';
import { useContextStore } from 'store/global-context-provider';

import { PausedIssueRow } from '../issues/swarm-panel/swarm-panel';
import { SwarmAgentRow } from '../issues/swarm-panel/swarm-agent-row';

// spec cs:swarm:panel
// spec cs:swarm:topology
/** The swarm plane (D4): a dedicated screen for the swarm — how the
 * fleet coordinates (topology + foreman), how each agent is doing
 * (the stats roster), and the work that waits on a human. The board's
 * Swarm button opens the same fleet as a quick-glance side panel; this
 * is where the fleet is steered.
 */

const TOPOLOGY_DESCRIPTIONS = {
  foreman:
    'A lead agent (the foreman) dispatches work and receives returns; workers hand back to the foreman. One dispatcher, no stampede.',
  flat: 'Any agent may hand blocked work to any peer on the same team. More nimble, no single dispatcher.',
} as const;

export const SwarmPage = withApplicationStore(
  observer(() => {
    const workspace = useCurrentWorkspace();
    const userContext = React.useContext(UserContext);
    const { teamsStore, workspaceStore } = useContextStore();
    const { openIssue } = React.useContext(IssueViewContext);
    const { data, isLoading, refetch } = useSwarmQuery(workspace?.id ?? '');

    // The settings form, seeded from the saved settings (the page is
    // the save target; the panel polls, so this refetches on the page).
    const [topology, setTopology] = React.useState('foreman');
    const [foremanId, setForemanId] = React.useState(''); // "" = auto
    const [saving, setSaving] = React.useState(false);
    const [saved, setSaved] = React.useState(false);
    const [error, setError] = React.useState<string | null>(null);

    const settings = data?.settings;
    React.useEffect(() => {
      if (!settings) {
        return;
      }
      setTopology(settings.topology);
      setForemanId(settings.foremanAccountId ?? '');
      setSaved(false);
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [settings?.topology, settings?.foremanAccountId]);

    const dirty =
      !!settings &&
      (topology !== settings.topology ||
        foremanId !== (settings.foremanAccountId ?? ''));

    // The server enforces owner/admin; the UI keeps the control honest.
    // (The backend maps owner to ADMIN on the wire.)
    const myRole = userContext
      ? workspaceStore.getUserData(userContext.id)?.role
      : undefined;
    const canEdit = myRole === RoleEnum.ADMIN;

    const mutation = useUpdateSwarmSettingsMutation({
      onSuccess: () => {
        setSaving(false);
        setSaved(true);
        refetch();
      },
      onError: (message) => {
        setSaving(false);
        setError(message);
      },
    });

    const issuePrefix = (teamId: string, number: number) =>
      `${teamsStore.getTeamWithId(teamId)?.identifier ?? '?'}-${number}`;

    const activeAgents = (data?.agents ?? []).filter(
      (agent) => agent.status === 'ACTIVE',
    );
    const designatedIsActive = !settings?.foremanAccountId ||
      activeAgents.some((agent) => agent.id === settings.foremanAccountId);

    const save = () => {
      if (!workspace || !dirty || !canEdit || saving) {
        return;
      }
      setSaving(true);
      setError(null);
      mutation.mutate({
        workspaceId: workspace.id,
        topology,
        foremanAccountId: foremanId || null,
      });
    };

    return (
      <MainLayout
        header={
          <HeaderLayout>
            <h3> Swarm </h3>
          </HeaderLayout>
        }
      >
        <div className="p-6 flex flex-col gap-6 max-w-3xl mx-auto w-full">
          <section className="flex items-start gap-3">
            <AI size={20} className="mt-0.5" />
            <div>
              <h2 className="text-base font-semibold">The swarm plane</h2>
              <p className="text-sm text-muted-foreground mt-1">
                One screen for the swarm: how the fleet coordinates, how
                each agent is doing, and the work that waits on you.
                Changes below take effect on the swarm's next decision —
                no restart, no redeploy.
              </p>
            </div>
          </section>

          <section className="rounded-lg border border-grayAlpha-100 dark:border-grayAlpha-300 bg-background-3/40 p-4 flex flex-col gap-4">
            <h3 className="text-sm font-semibold">Fleet settings</h3>

            {data?.brain && (
              <div className="flex items-center gap-2 text-xs flex-wrap">
                <span
                  className={cn(
                    'inline-block h-2 w-2 rounded-full',
                    data.brain.mode === 'llm'
                      ? 'bg-emerald-500'
                      : 'bg-amber-500',
                  )}
                />
                <span className="text-muted-foreground">
                  Brain{' '}
                  <span
                    className={cn(
                      'font-medium',
                      data.brain.mode === 'llm'
                        ? 'text-emerald-600 dark:text-emerald-400'
                        : 'text-amber-600 dark:text-amber-400',
                    )}
                  >
                    {data.brain.mode === 'llm' ? 'LLM' : 'floor'}
                  </span>
                  {data.brain.model ? ` — ${data.brain.model}` : ''}
                  {data.brain.endpoints
                    ? `, ${data.brain.endpoints} endpoints`
                    : ''}
                  {data.brain.mode !== 'llm' &&
                    ' — the deterministic state machine is deciding'}
                </span>
                {data.brain.lastDecisionAt && (
                  <span className="text-muted-foreground">
                    · last decision{' '}
                    {formatDistanceToNow(
                      new Date(data.brain.lastDecisionAt),
                    )}{' '}
                    ago
                  </span>
                )}
              </div>
            )}

            <div className="flex flex-col gap-2">
              <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                Topology
              </span>
              <div className="grid grid-cols-2 gap-2">
                {(['foreman', 'flat'] as const).map((t) => (
                  <button
                    key={t}
                    type="button"
                    disabled={!canEdit}
                    onClick={() => {
                      setTopology(t);
                      setSaved(false);
                    }}
                    className={cn(
                      'rounded-md border p-3 text-left',
                      topology === t
                        ? 'border-amber-400 bg-amber-50 dark:bg-amber-500/10'
                        : 'border-grayAlpha-100 dark:border-grayAlpha-300',
                      !canEdit && 'opacity-50 cursor-not-allowed',
                    )}
                  >
                    <div className="text-sm font-medium">
                      {t === 'foreman' ? 'Foreman' : 'Flat'}
                    </div>
                    <div className="text-xs text-muted-foreground mt-1">
                      {TOPOLOGY_DESCRIPTIONS[t]}
                    </div>
                  </button>
                ))}
              </div>
            </div>

            <div className="flex flex-col gap-2">
              <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                Foreman
              </span>
              <select
                value={foremanId}
                disabled={!canEdit || topology === 'flat'}
                onChange={(e) => {
                  setForemanId(e.target.value);
                  setSaved(false);
                }}
                className="rounded-md border border-grayAlpha-100 dark:border-grayAlpha-300 bg-background p-2 text-sm disabled:opacity-50"
              >
                <option value="">Auto (oldest agent)</option>
                {activeAgents.map((agent) => (
                  <option key={agent.id} value={agent.id}>
                    {agent.name}
                  </option>
                ))}
              </select>
              <div className="text-xs text-muted-foreground">
                Effective foreman: {settings?.foremanName ?? '—'}
                {!designatedIsActive && (
                  <span className="text-amber-600 dark:text-amber-400">
                    {' '}
                    The designated foreman is not active; the fleet runs
                    the auto rule.
                  </span>
                )}
              </div>
            </div>

            <div className="flex items-center gap-3">
              <button
                type="button"
                disabled={!dirty || !canEdit || saving}
                onClick={save}
                className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-sm font-medium disabled:opacity-50"
              >
                {saving ? 'Saving…' : 'Save'}
              </button>
              {saved && (
                <span className="text-xs text-emerald-600 dark:text-emerald-400">
                  Saved.
                </span>
              )}
              {error && (
                <span className="text-xs text-red-600 dark:text-red-400">
                  {error}
                </span>
              )}
              {!canEdit && (
                <span className="text-xs text-muted-foreground">
                  Only workspace owners and admins can steer the fleet.
                </span>
              )}
            </div>
          </section>

          <section className="rounded-lg border border-grayAlpha-100 dark:border-grayAlpha-300 bg-background-3/40 overflow-hidden">
            <h3 className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground border-b border-grayAlpha-100 dark:border-grayAlpha-300">
              Fleet
            </h3>
            {isLoading && !data ? (
              <div className="p-4 text-sm text-muted-foreground">
                Loading the fleet…
              </div>
            ) : (data?.agents ?? []).length === 0 ? (
              <div className="p-4 text-sm text-muted-foreground">
                No agents in this workspace yet. Create one in Settings →
                Members.
              </div>
            ) : (
              data?.agents.map((agent) => (
                <SwarmAgentRow key={agent.id} agent={agent} />
              ))
            )}
          </section>

          <section className="rounded-lg border border-amber-300 dark:border-amber-500/40 bg-amber-50/50 dark:bg-amber-500/5 overflow-hidden">
            <h3 className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-amber-700 dark:text-amber-400 border-b border-amber-200 dark:border-amber-500/30 flex items-center gap-1.5">
              <Warning size={14} /> Work waiting on you (
              {data?.pausedIssues.length ?? 0})
            </h3>
            {(data?.pausedIssues ?? []).length === 0 ? (
              <div className="p-4 text-sm text-muted-foreground">
                Nothing is waiting on you. When the swarm cannot make
                progress, the card parks here with the reason and the
                path forward.
              </div>
            ) : (
              <div className="p-4 pt-2">
                {data?.pausedIssues.map((issue) => (
                  <PausedIssueRow
                    key={issue.id}
                    issue={issue}
                    prefix={issuePrefix(issue.teamId, issue.number)}
                    onOpen={() => openIssue(issue.id)}
                  />
                ))}
              </div>
            )}
          </section>
        </div>
      </MainLayout>
    );
  }),
);

SwarmPage.getLayout = function getLayout(page: React.ReactElement) {
  return <AppLayout>{page}</AppLayout>;
};
