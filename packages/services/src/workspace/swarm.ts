import axios from 'axios';

// The swarm panel (D2, docs/spec/12): the live fleet roster.
//
// The shape is topology-agnostic — foreman and flat swarms produce the
// identical payload; the panel renders the trace, and topology stays a
// policy layer elsewhere (the D4 selector).

// One atomic work transition, the direction from this agent's point of
// view: "in" it received the issue, "out" it handed it off. Summary is
// trimmed to 200 characters server-side; the full summary is on the
// issue's timeline.
export interface SwarmHandoff {
  issueId: string;
  issueNumber: number;
  issueTitle: string;
  direction: 'in' | 'out';
  counterpartId: string;
  counterpartName: string;
  summary: string;
  createdAt: string;
}

export interface SwarmAgent {
  id: string;
  name: string;
  email: string;
  status: 'ACTIVE' | 'SUSPENDED';
  teamIds: string[];
  // Busy: has work it can actually act on (an open, non-paused issue).
  busy: boolean;
  // Open issues assigned to the agent (terminal states excluded);
  // pausedIssueCount is the subset paused for a human.
  openIssueCount: number;
  pausedIssueCount: number;
  lastActivityAt: string | null;
  lastHandoff: SwarmHandoff | null;
  // The quiet-guard signals over the shared 24h window: handoffs
  // received (the loop guard) and authored operations (the budget's
  // per-agent share).
  handoffs24h: number;
  ops24h: number;
  // The agent's authenticated API request count over 24h (still live
  // for agents that act over HTTP, like the tools/swarm demo process).
  requests24h: number;
  // D3 spend: model tokens the in-process runtime spent for the agent
  // over 24h (zero for the deterministic policy; an LLM policy reports
  // its real usage here).
  tokens24h: number;
  createdAt: string;
}

// A D1 escalation currently open: the issue is paused (no agent may act
// on it) and a human is being pointed at it. Reason is the guard's own
// words from the pause history row.
export interface SwarmPausedIssue {
  id: string;
  number: number;
  title: string;
  teamId: string;
  stateId: string;
  assigneeId: string | null;
  assigneeName: string | null;
  reason: string;
  pausedAt: string;
}

// The swarm plane's fleet settings (D4): the coordination pattern the
// swarm runs, and the foreman in effect. foremanAccountId is the
// human's designation (null = auto: the fleet's oldest active agent);
// foremanName is the effective foreman — the designation while that
// agent is active, else the auto rule.
export interface SwarmSettings {
  topology: string; // "foreman" | "flat"
  foremanAccountId: string | null;
  foremanName: string | null;
}

// The reserved Human Review parking column (D1 escalation): the
// server's needs-human predicate is the pause flag OR this state name,
// so the client mirrors it with the same OR rule.
export const HUMAN_REVIEW_STATE_NAME = 'Human Review';

export interface SwarmStatus {
  // Fleet roster, busy agents first.
  agents: SwarmAgent[];
  pausedIssues: SwarmPausedIssue[];
  // The swarm plane's fleet settings (D4): the Swarm page's save target.
  settings: SwarmSettings;
}

export async function getSwarmStatus(workspaceId: string) {
  const response = await axios.get<SwarmStatus>(
    `/api/v1/workspaces/${workspaceId}/swarm`,
  );

  return response.data;
}

// D4: save the swarm plane's fleet settings. Owner/admin only (agents
// 422, non-admins 404); the decision takes effect on the swarm's next
// decision, no restart.
export async function updateSwarmSettings(
  workspaceId: string,
  settings: { topology: string; foremanAccountId: string | null },
) {
  const response = await axios.post<SwarmSettings>(
    `/api/v1/workspaces/${workspaceId}/swarm/settings`,
    settings,
  );

  return response.data;
}
