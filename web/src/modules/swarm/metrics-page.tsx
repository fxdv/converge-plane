import {
  type CodebaseMetrics,
  type FleetNodeStat,
  type ProductMetrics,
  type ProxyMetrics,
  type SwarmMetrics,
  type WorkspaceMetrics,
} from '@converge/services';
import { cn } from '@converge/ui/lib/utils';
import { formatDistanceToNow } from 'date-fns';
import * as React from 'react';

import { HeaderLayout } from 'common/header-layout';
import { AppLayout } from 'common/layouts/app-layout';
import { MainLayout } from 'common/layouts/main-layout';
import { formatCost } from 'common/lib/run-format';
import { withApplicationStore } from 'common/wrappers/with-application-store';

import { useCurrentWorkspace } from 'hooks/workspace';

import { useMetricsQuery } from 'services/workspace';

// spec cs:swarm:metrics
/** The metrics plane (docs/spec ch. 6, §Metrics): one screen for
 * everything a foreman watches while the product, the codebase, and the
 * swarm are alive. Four read-only sections — product (what the tenant
 * uses and what moved), codebase (the running artifact and its live
 * process), swarm (the fleet), proxy (the LLM fleet router) — plus the
 * full registry as a sortable advanced table.
 *
 * Visual language (shared with the swarm plane): hairline rules, no card
 * fills, tabular numerals right-aligned, direct labels, and status colors
 * reserved for meaning (green healthy, amber degraded, red error).
 */

// ---- formatting (durations arrive as int64 nanoseconds) ----

const nsToMs = (ns: number) => ns / 1e6;

function formatUptime(ns: number): string {
  const total = Math.max(0, Math.floor(ns / 1e9));
  const d = Math.floor(total / 86_400);
  const h = Math.floor((total % 86_400) / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  if (d > 0) {
    return `${d}d ${h}h`;
  }
  if (h > 0) {
    return `${h}h ${m}m`;
  }
  if (m > 0) {
    return `${m}m ${s}s`;
  }
  return `${s}s`;
}

function formatCount(n: number): string {
  if (n >= 1_000_000) {
    return `${(n / 1_000_000).toFixed(1)}m`;
  }
  if (n >= 1_000) {
    return `${(n / 1_000).toFixed(1)}k`;
  }
  return String(n);
}

function formatMs(ms: number): string {
  if (ms <= 0) {
    return '—';
  }
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(1)}s`;
  }
  return `${Math.round(ms)}ms`;
}

const formatPct = (x: number) => `${Math.round(x * 100)}%`;

// ---- shared table primitives (the plane's visual language) ----

const SectionTitle = ({ children }: { children: React.ReactNode }) => (
  <h3 className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground border-b border-grayAlpha-100 dark:border-grayAlpha-300">
    {children}
  </h3>
);

const Section = ({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) => (
  <section className="border-b border-grayAlpha-100 dark:border-grayAlpha-300">
    <SectionTitle>{title}</SectionTitle>
    {children}
  </section>
);

const Th = ({
  children,
  right = false,
}: {
  children: React.ReactNode;
  right?: boolean;
}) => (
  <th
    className={cn(
      'py-1.5 pr-4 text-[11px] font-medium uppercase tracking-wide text-muted-foreground',
      right ? 'text-right' : 'text-left',
    )}
  >
    {children}
  </th>
);

const Td = ({
  children,
  right = false,
  className,
  colSpan,
}: {
  children: React.ReactNode;
  right?: boolean;
  className?: string;
  colSpan?: number;
}) => (
  <td
    colSpan={colSpan}
    className={cn(
      'py-1.5 pr-4 text-sm border-t border-grayAlpha-100 dark:border-grayAlpha-300',
      right && 'text-right font-mono tabular-nums',
      className,
    )}
  >
    {children}
  </td>
);

// The vitals strip: eight small multiples, one glance.
const Vitals = ({ data }: { data: WorkspaceMetrics }) => {
  const fleetErrors = data.proxy.nodes.reduce((sum, n) => sum + n.failures, 0);
  const vitals: Array<{
    label: string;
    value: string;
    tone?: 'amber' | 'red';
  }> = [
    { label: 'Open issues', value: String(data.swarm.openIssues) },
    { label: 'Done moves 7d', value: String(data.product.issuesDone7d) },
    {
      label: 'Agents busy',
      value: `${data.swarm.busy}/${data.swarm.agents}`,
    },
    { label: 'Tokens 24h', value: formatCount(data.swarm.tokens24h) },
    {
      label: 'Fleet errors',
      value: String(fleetErrors),
      tone: fleetErrors > 0 ? 'red' : undefined,
    },
    { label: 'Uptime', value: formatUptime(data.codebase.uptime) },
    { label: 'Handoffs 24h', value: String(data.swarm.handoffs24h) },
    {
      label: 'Paused now',
      value: String(data.swarm.pausedIssues),
      tone: data.swarm.pausedIssues > 0 ? 'amber' : undefined,
    },
  ];
  return (
    <div className="grid grid-cols-4 border-b border-grayAlpha-100 dark:border-grayAlpha-300">
      {vitals.map((v) => (
        <div key={v.label} className="px-4 py-3">
          <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
            {v.label}
          </div>
          <div
            className={cn(
              'mt-1 text-xl font-mono tabular-nums',
              v.tone === 'red' && 'text-red-600 dark:text-red-400',
              v.tone === 'amber' && 'text-amber-600 dark:text-amber-400',
            )}
          >
            {v.value}
          </div>
        </div>
      ))}
    </div>
  );
};

// ---- the four sections ----

const fleetHasNoTraffic = (nodes: FleetNodeStat[]) =>
  nodes.length > 0 &&
  nodes.every((node) => !node.lastError && !node.lastSuccessAt);

const SwarmSection = ({
  swarm,
  noTraffic,
}: {
  swarm: SwarmMetrics;
  noTraffic: boolean;
}) => (
  <Section
    title={`Fleet — ${swarm.agents} agents, ${swarm.activeAgents} active`}
  >
    {swarm.roster.length === 0 ? (
      <div className="p-4 text-sm text-muted-foreground">
        No agents in this workspace yet.
      </div>
    ) : (
      <table className="w-full">
        <thead>
          <tr>
            <Th>Agent</Th>
            <Th>Account</Th>
            <Th right>Open</Th>
            <Th right>Paused</Th>
            <Th right>Ops 24h</Th>
            <Th right>Tokens 24h</Th>
            <Th right>Last activity</Th>
          </tr>
        </thead>
        <tbody>
          {swarm.roster.map((agent) => (
            <tr key={agent.id}>
              <Td className="font-medium">{agent.name}</Td>
              <Td
                className={cn(
                  'text-xs',
                  agent.status === 'SUSPENDED'
                    ? 'text-red-600 dark:text-red-400'
                    : agent.busy
                      ? 'text-emerald-600 dark:text-emerald-400'
                      : 'text-muted-foreground',
                )}
              >
                {agent.status.toLowerCase()}
                {agent.busy ? ' · working' : ''}
              </Td>
              <Td right>{agent.openIssueCount}</Td>
              <Td
                right
                className={
                  agent.pausedIssueCount > 0
                    ? 'text-amber-600 dark:text-amber-400'
                    : undefined
                }
              >
                {agent.pausedIssueCount}
              </Td>
              <Td right>{agent.ops24h}</Td>
              <Td right>{formatCount(agent.tokens24h)}</Td>
              <Td right className="text-xs text-muted-foreground">
                {agent.lastActivityAt
                  ? formatDistanceToNow(new Date(agent.lastActivityAt), {
                      addSuffix: true,
                    })
                  : '—'}
              </Td>
            </tr>
          ))}
        </tbody>
      </table>
    )}
    <div className="px-4 py-2 text-xs text-muted-foreground flex flex-wrap gap-x-4">
      <span>
        Pause rate 24h{' '}
        <span className="font-mono tabular-nums">
          {formatPct(swarm.pauseRate24h)}
        </span>
      </span>
      <span>
        Median time-to-resume{' '}
        <span className="font-mono tabular-nums">
          {swarm.medianResumeMs > 0 ? formatMs(swarm.medianResumeMs) : '—'}
        </span>
      </span>
      <span>
        Mean time-to-resume{' '}
        <span className="font-mono tabular-nums">
          {swarm.meanResumeMs > 0 ? formatMs(swarm.meanResumeMs) : '—'}
        </span>
      </span>
      <span title="Reported micro-USD on agent runs for issues that reached Done in 24 hours">
        Reported cost per done 24h{' '}
        <span className="font-mono tabular-nums">
          {swarm.reportedCostIssues24h > 0
            ? formatCost(swarm.reportedCostPerDoneMicros24h)
            : '—'}
        </span>
      </span>
      <span title="Model tokens the runtime attributed to issues that reached Done in 24 hours">
        Model tokens per done 24h{' '}
        <span className="font-mono tabular-nums">
          {swarm.costedIssues24h > 0
            ? `${formatCount(swarm.costMedianTokens24h)} median`
            : '—'}
        </span>
      </span>
      <span title="The floor's share of decisions in 24 hours. An em dash means the fleet reported no traffic.">
        Fallback rate 24h{' '}
        <span className="font-mono tabular-nums">
          {noTraffic ? '—' : formatPct(swarm.fallbackRate24h)}
        </span>
      </span>
      <span>
        Longest handoff chain 24h{' '}
        <span className="font-mono tabular-nums">
          {String(swarm.longestHandoffChain24h)}
        </span>
      </span>
      <span title="Status changes into Done in 7 days. A system move counts for the agent when that issue has a run.">
        Agent share of 7d Done moves{' '}
        <span className="font-mono tabular-nums">
          {swarm.completions7d > 0 ? formatPct(swarm.agentShare7d) : '—'}
        </span>
      </span>
    </div>
  </Section>
);

const ProxySection = ({ proxy }: { proxy: ProxyMetrics }) => {
  const hasFleet = proxy.nodes.length > 0;
  return (
    <Section title={`LLM fleet — brain: ${proxy.mode}`}>
      <div className="px-4 py-2 text-xs text-muted-foreground flex flex-wrap gap-x-4">
        <span>
          Model <span className="font-mono">{proxy.model ?? '—'}</span>
        </span>
        {proxy.timeout !== undefined && proxy.timeout > 0 && (
          <span>
            Decision bound{' '}
            <span className="font-mono tabular-nums">
              {formatMs(nsToMs(proxy.timeout))}
            </span>
          </span>
        )}
        {proxy.mode !== 'llm' && (
          <span className="text-amber-600 dark:text-amber-400">
            the deterministic floor is deciding
          </span>
        )}
        {proxy.rateRps > 0 && (
          <span>
            Rate guard{' '}
            <span className="font-mono tabular-nums">
              {proxy.rateRps}/s, burst {proxy.rateBurst}
            </span>
          </span>
        )}
      </div>
      {hasFleet ? (
        <table className="w-full">
          <thead>
            <tr>
              <Th>Node</Th>
              <Th right>Routed</Th>
              <Th right>Ok</Th>
              <Th right>Err</Th>
              <Th right>Avg</Th>
              <Th right>Max</Th>
              <Th right>Tokens</Th>
              <Th right>Last event</Th>
            </tr>
          </thead>
          <tbody>
            {proxy.nodes.map((node) => (
              <NodeRow key={node.node} node={node} />
            ))}
          </tbody>
        </table>
      ) : (
        <div className="px-4 py-3 text-sm text-muted-foreground">
          No fleet configured — every decision runs on the deterministic floor.
        </div>
      )}
      {proxy.agentBurn.length > 0 && (
        <div className="px-4 py-2 text-xs text-muted-foreground flex flex-wrap gap-x-4 border-t border-grayAlpha-100 dark:border-grayAlpha-300">
          {proxy.agentBurn.map((b) => (
            <span key={b.accountId}>
              {b.name}{' '}
              <span className="font-mono tabular-nums">
                {formatCount(b.usage24h)} reqs/24h
              </span>
            </span>
          ))}
        </div>
      )}
    </Section>
  );
};

const NodeRow = ({ node }: { node: FleetNodeStat }) => {
  const url = node.url.replace(/^https?:\/\//, '').replace(/:\d+$/, '');
  const lastEvent = node.lastError
    ? { text: node.lastError.slice(0, 40), tone: 'red' as const }
    : node.lastSuccessAt
      ? {
          text: formatDistanceToNow(new Date(node.lastSuccessAt), {
            addSuffix: true,
          }),
          tone: 'green' as const,
        }
      : { text: 'no traffic yet', tone: 'muted' as const };
  return (
    <tr>
      <Td className="font-mono tabular-nums text-xs">
        #{node.node} <span className="text-muted-foreground">{url}</span>
      </Td>
      <Td right>{node.requests}</Td>
      <Td
        right
        className={
          node.successes > 0
            ? 'text-emerald-600 dark:text-emerald-400'
            : undefined
        }
      >
        {node.successes}
      </Td>
      <Td
        right
        className={
          node.failures > 0 ? 'text-red-600 dark:text-red-400' : undefined
        }
      >
        {node.failures}
      </Td>
      <Td right>{formatMs(nsToMs(node.avgLatency))}</Td>
      <Td right>{formatMs(nsToMs(node.maxLatency))}</Td>
      <Td right>{formatCount(node.tokens)}</Td>
      <Td right className="text-xs">
        <span
          className={cn(
            lastEvent.tone === 'red' && 'text-red-600 dark:text-red-400',
            lastEvent.tone === 'green' &&
              'text-emerald-600 dark:text-emerald-400',
            lastEvent.tone === 'muted' && 'text-muted-foreground',
          )}
        >
          {lastEvent.text}
        </span>
      </Td>
    </tr>
  );
};

const ProductSection = ({ product }: { product: ProductMetrics }) => {
  const statesSummary = product.states
    .slice(0, 5)
    .map((s) => `${s.count} ${s.name.toLowerCase()}`)
    .join(' · ');
  const rest = Math.max(0, product.states.length - 5);
  const inDone = product.states
    .filter((s) => s.category === 'COMPLETED')
    .reduce((sum, s) => sum + s.count, 0);
  const withoutMove = Math.max(0, inDone - product.issuesDoneAll);
  return (
    <Section
      title={`Product — ${product.issues} issues, ${product.membersActive}/${product.members} members active`}
    >
      <table className="w-full">
        <thead>
          <tr>
            <Th>Metric</Th>
            <Th right>24h</Th>
            <Th right>7d</Th>
            <Th right>All time</Th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <Td>Issues created</Td>
            <Td right>{product.issuesCreated24h}</Td>
            <Td right>{product.issuesCreated7d}</Td>
            <Td right>not counted</Td>
          </tr>
          <tr>
            <Td>Done moves</Td>
            <Td right>{product.issuesDone24h}</Td>
            <Td right>{product.issuesDone7d}</Td>
            <Td right>{product.issuesDoneAll}</Td>
          </tr>
          <tr>
            <Td>Comments</Td>
            <Td right>{product.comments24h}</Td>
            <Td right>{product.comments7d}</Td>
            <Td right>{formatCount(product.comments)}</Td>
          </tr>
          <tr>
            <Td>Handoffs</Td>
            <Td right>{product.handoffs24h}</Td>
            <Td right>{product.handoffs7d}</Td>
            <Td right>—</Td>
          </tr>
          <tr>
            <Td>Teams</Td>
            <Td right>—</Td>
            <Td right>—</Td>
            <Td right>{product.teams}</Td>
          </tr>
          <tr>
            <Td>Workflow states</Td>
            <Td right>—</Td>
            <Td right>—</Td>
            <Td right>{product.workflows}</Td>
          </tr>
          <tr>
            <Td>Labels</Td>
            <Td right>—</Td>
            <Td right>—</Td>
            <Td right>{product.labels}</Td>
          </tr>
          <tr>
            <Td>Projects</Td>
            <Td right>—</Td>
            <Td right>—</Td>
            <Td right>{product.projects}</Td>
          </tr>
          <tr>
            <Td>Saved views</Td>
            <Td right>—</Td>
            <Td right>—</Td>
            <Td right>{product.views}</Td>
          </tr>
          <tr>
            <Td className="text-xs text-muted-foreground">
              {statesSummary}
              {rest > 0 ? ` · +${rest} more` : ''}
              {withoutMove > 0
                ? ` · ${withoutMove} in Done with no Done move`
                : ''}
            </Td>
            <Td right className="text-xs text-muted-foreground" colSpan={3}>
              by state
            </Td>
          </tr>
        </tbody>
      </table>
    </Section>
  );
};

const CodebaseSection = ({ codebase }: { codebase: CodebaseMetrics }) => {
  const rows: Array<[string, string]> = [
    ['Build', `${codebase.version} (${codebase.gitSha})`],
    ['Built', codebase.buildTime],
    ['Platform', codebase.platform],
    ['Uptime', formatUptime(codebase.uptime)],
    ['Goroutines', String(codebase.goroutines)],
    [
      'Database pool',
      `${codebase.pool.acquired} acquired · ${codebase.pool.idle} idle · ${codebase.pool.max} max`,
    ],
    ['Realtime subscribers', String(codebase.sseSubscribers)],
    ['Outbox depth', `${codebase.feedDepth} rows`],
    ['Feed sequence', codebase.feedSequence],
  ];
  return (
    <Section title="Codebase — the running process">
      <dl className="grid grid-cols-2">
        {rows.map(([k, v]) => (
          <div
            key={k}
            className="px-4 py-1.5 border-t border-grayAlpha-100 dark:border-grayAlpha-300 first:border-t-0 flex items-baseline gap-2"
          >
            <dt className="text-xs text-muted-foreground w-40 shrink-0">{k}</dt>
            <dd className="text-sm font-mono tabular-nums truncate">{v}</dd>
          </div>
        ))}
      </dl>
    </Section>
  );
};

// ---- the page ----

export const MetricsPage = withApplicationStore(() => {
  const workspace = useCurrentWorkspace();
  const { data, isLoading, refetch } = useMetricsQuery(workspace?.id ?? '');

  return (
    <MainLayout
      header={
        <HeaderLayout>
          <h3> Metrics </h3>
        </HeaderLayout>
      }
    >
      <div className="p-6 max-w-4xl mx-auto w-full">
        <div className="border border-grayAlpha-100 dark:border-grayAlpha-300">
          {isLoading && !data ? (
            <div className="p-6 text-sm text-muted-foreground">
              Reading the gauges…
            </div>
          ) : data ? (
            <>
              <Vitals data={data} />
              <SwarmSection
                swarm={data.swarm}
                noTraffic={fleetHasNoTraffic(data.proxy.nodes)}
              />
              <ProxySection proxy={data.proxy} />
              <ProductSection product={data.product} />
              <CodebaseSection codebase={data.codebase} />
            </>
          ) : (
            <div className="p-6 text-sm text-muted-foreground">
              The metrics plane is unavailable.{' '}
              <button
                type="button"
                className="underline"
                onClick={() => refetch()}
              >
                Retry
              </button>
            </div>
          )}
        </div>
        {data && (
          <div className="mt-2 px-4 text-[11px] text-muted-foreground">
            Generated{' '}
            {formatDistanceToNow(new Date(data.generatedAt), {
              addSuffix: true,
            })}{' '}
            · updates every 30s · the fleet registry is a live instrument since
            process start, not a ledger
          </div>
        )}
      </div>
    </MainLayout>
  );
});

MetricsPage.getLayout = function getLayout(page: React.ReactElement) {
  return <AppLayout>{page}</AppLayout>;
};
