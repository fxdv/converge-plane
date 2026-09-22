import axios from 'axios';

import type { SwarmAgent } from './swarm';

// The metrics plane (docs/spec ch. 6, §Metrics): one read-only surface
// for everything a foreman watches — the tenant's usage (product), the
// running artifact and live process (codebase), the fleet plus
// swarm-level rates (swarm), and the LLM fleet router (proxy).
//
// Duration fields (uptime, avgLatency, maxLatency, timeout) are int64
// nanoseconds on the wire (Go time.Duration); the page divides by 1e6.

// One workflow state's share of the workspace's live issues.
export interface MetricsStateCount {
  name: string;
  category: string;
  count: number;
}

// The tenant's usage: what exists now, and what moved in the bounded
// windows (24h / 7d).
export interface ProductMetrics {
  members: number;
  membersActive: number;
  teams: number;
  workflows: number;
  labels: number;
  projects: number;
  views: number;
  issues: number;
  states: MetricsStateCount[];
  comments: number;
  historyRows: number;
  issuesCreated24h: number;
  issuesCreated7d: number;
  issuesDone24h: number;
  issuesDone7d: number;
  comments24h: number;
  comments7d: number;
  handoffs24h: number;
  handoffs7d: number;
}

// The database pool's live gauges.
export interface PoolGauges {
  acquired: number;
  idle: number;
  max: number;
}

// The running artifact (what is deployed) plus the live process (how it
// is doing right now).
export interface CodebaseMetrics {
  version: string;
  gitSha: string;
  buildTime: string;
  platform: string;
  /** nanoseconds on the wire */
  uptime: number;
  goroutines: number;
  pool: PoolGauges;
  sseSubscribers: number;
  feedDepth: number;
  feedSequence: string;
}

// The fleet: the roster (identical to the swarm panel's) plus the
// swarm-level rates over the guard windows.
export interface SwarmMetrics {
  agents: number;
  activeAgents: number;
  busy: number;
  openIssues: number;
  pausedIssues: number;
  tokens24h: number;
  ops24h: number;
  handoffs24h: number;
  pauses24h: number;
  /** pauses / (pauses + completions), 24h — 1.0 means every outcome paused */
  pauseRate24h: number;
  /** 0 when nothing resumed in the window */
  meanResumeMs: number;
  /** median time-to-human-resume (the approved statistic); 0 when nothing resumed */
  medianResumeMs: number;
  completions24h: number;
  /** model tokens of the completed issues in the window — median (0 = no spend data) */
  costMedianTokens24h: number;
  /** model tokens of the completed issues in the window — mean */
  costMeanTokens24h: number;
  /** completed issues in the window that had spend data (0 = the medians read "no data", not "free") */
  costedIssues24h: number;
  /** the floor's share of the runtime's decisions in the window (0 = no fallbacks, or none reported) */
  fallbackRate24h: number;
  /** the deepest handoff chain on the board in the window (the most-chased issue's hops) */
  longestHandoffChain24h: number;
  completedByAgents7d: number;
  completedByHumans7d: number;
  /** agent completions / all completions, 7d */
  agentShare7d: number;
  roster: SwarmAgent[];
}

// One account's rate-limiter usage (the D2 burn proxy, over its window).
export interface AgentBurnRow {
  accountId: string;
  name: string;
  status: string;
  usage24h: number;
}

// One fleet node's row in the LLM-fleet section. A node the fleet never
// reached is a row of zeros with the error fields absent.
export interface FleetNodeStat {
  /** 1-based position in the configured fleet */
  node: number;
  url: string;
  requests: number;
  successes: number;
  failures: number;
  /** nanoseconds; over successes, 0 when none */
  avgLatency: number;
  /** nanoseconds */
  maxLatency: number;
  tokens: number;
  lastError?: string;
  lastErrorAt?: string;
  lastSuccessAt?: string;
}

// The LLM fleet router: the nodes (where decisions are made) and the
// accounts' request usage (where the flood guard sits).
export interface ProxyMetrics {
  /** "llm" | "floor" | "off" */
  mode: string;
  model?: string;
  /** nanoseconds; the configured decision bound */
  timeout?: number;
  nodes: FleetNodeStat[];
  agentBurn: AgentBurnRow[];
  rateRps: number;
  rateBurst: number;
}

// The GET /api/v1/workspaces/{id}/metrics payload.
export interface WorkspaceMetrics {
  workspaceId: string;
  generatedAt: string;
  product: ProductMetrics;
  codebase: CodebaseMetrics;
  swarm: SwarmMetrics;
  proxy: ProxyMetrics;
}

// Any active workspace member may read (the same rule as the swarm
// status): every datum is an aggregate of rows the member can already
// see, or of in-memory slots the plane exists to expose.
export async function getWorkspaceMetrics(workspaceId: string) {
  const response = await axios.get<WorkspaceMetrics>(
    `/api/v1/workspaces/${workspaceId}/metrics`,
  );

  return response.data;
}
