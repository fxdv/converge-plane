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
    { label: 'Done 7d', value: String(data.product.issuesDone7d) },
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

const SwarmSection = ({ swarm }: { swarm: SwarmMetrics }) => (
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
            <Th>Status</Th>
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
      <span>
        Cost per done 24h{' '}
        <span className="font-mono tabular-nums">
          {swarm.costedIssues24h > 0
            ? `${formatCount(swarm.costMedianTokens24h)} median · ${swarm.costedIssues24h} issues`
            : 'no data'}
        </span>
      </span>
      <span>
        Fallback rate 24h{' '}
        <span className="font-mono tabular-nums">
          {formatPct(swarm.fallbackRate24h)}
        </span>
      </span>
      <span>
        Longest handoff chain 24h{' '}
        <span className="font-mono tabular-nums">
          {String(swarm.longestHandoffChain24h)}
        </span>
      </span>
      <span>
        Agent share of 7d completions{' '}
        <span className="font-mono tabular-nums">
          {formatPct(swarm.agentShare7d)}
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
            <Td right>—</Td>
          </tr>
          <tr>
            <Td>Issues done</Td>
            <Td right>{product.issuesDone24h}</Td>
            <Td right>{product.issuesDone7d}</Td>
            <Td right>—</Td>
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

// ---- the advanced registry (client-side flattening + sort) ----

interface RegistryRow {
  domain: 'product' | 'codebase' | 'swarm' | 'proxy';
  metric: string;
  value: string;
  unit: string;
  window: string;
  source: string;
  // numeric for the value column's sort (0 for non-numeric)
  numeric: number;
}

const DOMAIN_ORDER: Record<RegistryRow['domain'], number> = {
  product: 0,
  codebase: 1,
  swarm: 2,
  proxy: 3,
};

const SOURCE = 'GET /workspaces/:id/metrics';

function buildRegistry(data: WorkspaceMetrics): RegistryRow[] {
  const { product: p, codebase: c, swarm: s, proxy: pr } = data;
  const rows: RegistryRow[] = [];
  const add = (
    domain: RegistryRow['domain'],
    metric: string,
    value: string,
    unit: string,
    window: string,
    numeric = 0,
  ) => {
    rows.push({ domain, metric, value, unit, window, source: SOURCE, numeric });
  };

  add('product', 'members', String(p.members), 'count', 'all time', p.members);
  add(
    'product',
    'active members',
    String(p.membersActive),
    'count',
    'all time',
    p.membersActive,
  );
  add('product', 'teams', String(p.teams), 'count', 'all time', p.teams);
  add(
    'product',
    'workflow states',
    String(p.workflows),
    'count',
    'all time',
    p.workflows,
  );
  add('product', 'labels', String(p.labels), 'count', 'all time', p.labels);
  add(
    'product',
    'projects',
    String(p.projects),
    'count',
    'all time',
    p.projects,
  );
  add('product', 'saved views', String(p.views), 'count', 'all time', p.views);
  add(
    'product',
    'live issues',
    String(p.issues),
    'count',
    'all time',
    p.issues,
  );
  add(
    'product',
    'comments',
    String(p.comments),
    'count',
    'all time',
    p.comments,
  );
  add(
    'product',
    'history rows',
    String(p.historyRows),
    'count',
    'all time',
    p.historyRows,
  );
  add(
    'product',
    'issues created',
    String(p.issuesCreated24h),
    'count',
    '24h',
    p.issuesCreated24h,
  );
  add(
    'product',
    'issues created',
    String(p.issuesCreated7d),
    'count',
    '7d',
    p.issuesCreated7d,
  );
  add(
    'product',
    'issues done',
    String(p.issuesDone24h),
    'count',
    '24h',
    p.issuesDone24h,
  );
  add(
    'product',
    'issues done',
    String(p.issuesDone7d),
    'count',
    '7d',
    p.issuesDone7d,
  );
  add(
    'product',
    'comments',
    String(p.comments24h),
    'count',
    '24h',
    p.comments24h,
  );
  add('product', 'comments', String(p.comments7d), 'count', '7d', p.comments7d);
  add(
    'product',
    'handoffs',
    String(p.handoffs24h),
    'count',
    '24h',
    p.handoffs24h,
  );
  add('product', 'handoffs', String(p.handoffs7d), 'count', '7d', p.handoffs7d);

  add('codebase', 'build', `${c.version} (${c.gitSha})`, '', 'live');
  add('codebase', 'built', c.buildTime, '', 'live');
  add('codebase', 'platform', c.platform, '', 'live');
  add('codebase', 'uptime', formatUptime(c.uptime), '', 'live', c.uptime);
  add(
    'codebase',
    'goroutines',
    String(c.goroutines),
    'count',
    'live',
    c.goroutines,
  );
  add(
    'codebase',
    'db pool',
    `${c.pool.acquired}/${c.pool.idle}/${c.pool.max}`,
    'acquired/idle/max',
    'live',
    c.pool.max,
  );
  add(
    'codebase',
    'realtime subscribers',
    String(c.sseSubscribers),
    'count',
    'live',
    c.sseSubscribers,
  );
  add(
    'codebase',
    'outbox depth',
    String(c.feedDepth),
    'rows',
    'live',
    c.feedDepth,
  );
  add('codebase', 'feed sequence', c.feedSequence, '', 'live');

  add('swarm', 'agents', String(s.agents), 'count', 'all time', s.agents);
  add(
    'swarm',
    'active agents',
    String(s.activeAgents),
    'count',
    'all time',
    s.activeAgents,
  );
  add('swarm', 'busy agents', String(s.busy), 'count', 'live', s.busy);
  add(
    'swarm',
    'open issues',
    String(s.openIssues),
    'count',
    'live',
    s.openIssues,
  );
  add(
    'swarm',
    'paused issues',
    String(s.pausedIssues),
    'count',
    'live',
    s.pausedIssues,
  );
  add(
    'swarm',
    'model tokens',
    formatCount(s.tokens24h),
    'tokens',
    '24h',
    s.tokens24h,
  );
  add('swarm', 'agent ops', String(s.ops24h), 'count', '24h', s.ops24h);
  add(
    'swarm',
    'handoffs',
    String(s.handoffs24h),
    'count',
    '24h',
    s.handoffs24h,
  );
  add('swarm', 'pauses', String(s.pauses24h), 'count', '24h', s.pauses24h);
  add(
    'swarm',
    'pause rate',
    formatPct(s.pauseRate24h),
    '',
    '24h',
    Math.round(s.pauseRate24h * 1000) / 10,
  );
  add(
    'swarm',
    'mean time-to-resume',
    s.meanResumeMs > 0 ? formatMs(s.meanResumeMs) : '—',
    '',
    '24h',
    s.meanResumeMs,
  );
  add(
    'swarm',
    'median time-to-resume',
    s.medianResumeMs > 0 ? formatMs(s.medianResumeMs) : '—',
    '',
    '24h',
    s.medianResumeMs,
  );
  add(
    'swarm',
    'cost per done (median)',
    s.costedIssues24h > 0 ? formatCount(s.costMedianTokens24h) : '—',
    s.costedIssues24h > 0 ? 'tokens' : '',
    '24h',
    s.costMedianTokens24h,
  );
  add(
    'swarm',
    'cost per done (mean)',
    s.costedIssues24h > 0 ? formatCount(s.costMeanTokens24h) : '—',
    s.costedIssues24h > 0 ? 'tokens' : '',
    '24h',
    s.costMeanTokens24h,
  );
  add(
    'swarm',
    'done with spend data',
    String(s.costedIssues24h),
    'count',
    '24h',
    s.costedIssues24h,
  );
  add(
    'swarm',
    'fallback rate',
    formatPct(s.fallbackRate24h),
    '',
    '24h',
    Math.round(s.fallbackRate24h * 1000) / 10,
  );
  add(
    'swarm',
    'longest handoff chain',
    String(s.longestHandoffChain24h),
    'hops',
    '24h',
    s.longestHandoffChain24h,
  );
  add(
    'swarm',
    'completions',
    String(s.completions24h),
    'count',
    '24h',
    s.completions24h,
  );
  add(
    'swarm',
    'agent share of completions',
    formatPct(s.agentShare7d),
    '',
    '7d',
    Math.round(s.agentShare7d * 1000) / 10,
  );
  add(
    'swarm',
    'human completions',
    String(s.completedByHumans7d),
    'count',
    '7d',
    s.completedByHumans7d,
  );

  add('proxy', 'brain mode', pr.mode, '', 'live');
  if (pr.model) {
    add('proxy', 'model', pr.model, '', 'config');
  }
  if (pr.timeout !== undefined && pr.timeout > 0) {
    add(
      'proxy',
      'decision bound',
      formatMs(nsToMs(pr.timeout)),
      '',
      'config',
      pr.timeout,
    );
  }
  add(
    'proxy',
    'rate guard',
    `${pr.rateRps}/s burst ${pr.rateBurst}`,
    '',
    'config',
    pr.rateRps,
  );
  pr.nodes.forEach((n) => {
    const prefix = `node ${n.node} `;
    add(
      'proxy',
      `${prefix}routed`,
      String(n.requests),
      'count',
      'live',
      n.requests,
    );
    add(
      'proxy',
      `${prefix}ok`,
      String(n.successes),
      'count',
      'live',
      n.successes,
    );
    add(
      'proxy',
      `${prefix}errors`,
      String(n.failures),
      'count',
      'live',
      n.failures,
    );
    add(
      'proxy',
      `${prefix}avg latency`,
      formatMs(nsToMs(n.avgLatency)),
      '',
      'live',
      n.avgLatency,
    );
    add(
      'proxy',
      `${prefix}max latency`,
      formatMs(nsToMs(n.maxLatency)),
      '',
      'live',
      n.maxLatency,
    );
    add(
      'proxy',
      `${prefix}tokens`,
      formatCount(n.tokens),
      'tokens',
      'live',
      n.tokens,
    );
  });
  pr.agentBurn.forEach((b) => {
    add(
      'proxy',
      `${b.name} requests`,
      formatCount(b.usage24h),
      'count',
      '24h',
      b.usage24h,
    );
  });
  return rows;
}

type SortKey = keyof Pick<
  RegistryRow,
  'domain' | 'metric' | 'value' | 'unit' | 'window' | 'source'
>;

const AdvancedTable = ({ data }: { data: WorkspaceMetrics }) => {
  const [sortKey, setSortKey] = React.useState<SortKey>('domain');
  const [dir, setDir] = React.useState(1);

  const rows = React.useMemo(() => {
    const all = buildRegistry(data);
    const sorted = [...all].sort((a, b) => {
      let cmp = 0;
      if (sortKey === 'domain') {
        cmp = DOMAIN_ORDER[a.domain] - DOMAIN_ORDER[b.domain];
      } else if (sortKey === 'value') {
        cmp = a.numeric - b.numeric || a.value.localeCompare(b.value);
      } else {
        cmp = a[sortKey].localeCompare(b[sortKey]);
      }
      if (cmp === 0 && sortKey !== 'domain') {
        cmp = DOMAIN_ORDER[a.domain] - DOMAIN_ORDER[b.domain];
      }
      if (cmp === 0) {
        cmp = a.metric.localeCompare(b.metric);
      }
      return cmp * dir;
    });
    return sorted;
  }, [data, sortKey, dir]);

  const header = (key: SortKey, label: string, right = false) => (
    <th
      className={cn(
        'py-1.5 pr-4 text-[11px] font-medium uppercase tracking-wide text-muted-foreground cursor-pointer select-none',
        right ? 'text-right' : 'text-left',
        sortKey === key && 'text-foreground',
      )}
      onClick={() => {
        if (sortKey === key) {
          setDir(-dir);
        } else {
          setSortKey(key);
          setDir(1);
        }
      }}
    >
      {label} {sortKey === key ? (dir > 0 ? '↑' : '↓') : ''}
    </th>
  );

  return (
    <Section title={`Registry — ${rows.length} metrics`}>
      <table className="w-full">
        <thead>
          <tr>
            {header('domain', 'Domain')}
            {header('metric', 'Metric')}
            {header('value', 'Value', true)}
            {header('unit', 'Unit', true)}
            {header('window', 'Window', true)}
            {header('source', 'Source')}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={`${row.domain}-${row.metric}-${row.window}-${i}`}>
              <Td className="text-xs text-muted-foreground">{row.domain}</Td>
              <Td>{row.metric}</Td>
              <Td right>{row.value}</Td>
              <Td right className="text-xs text-muted-foreground">
                {row.unit}
              </Td>
              <Td right className="text-xs text-muted-foreground">
                {row.window}
              </Td>
              <Td className="text-xs text-muted-foreground font-mono">
                {row.source}
              </Td>
            </tr>
          ))}
        </tbody>
      </table>
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
              <SwarmSection swarm={data.swarm} />
              <ProxySection proxy={data.proxy} />
              <ProductSection product={data.product} />
              <CodebaseSection codebase={data.codebase} />
              <details className="group">
                <summary className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground cursor-pointer list-none flex items-center gap-1.5">
                  <span className="group-open:rotate-90 transition-transform inline-block">
                    ▸
                  </span>
                  Advanced — the full registry
                </summary>
                <AdvancedTable data={data} />
              </details>
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
