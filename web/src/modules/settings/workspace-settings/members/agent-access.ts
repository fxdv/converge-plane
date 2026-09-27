// agent-access.ts — the agent dialogs' choices as pure data: the token a
// new agent is issued (server/internal/api/agent.go tokenSpec), how a
// rotation retires the old one, and how an external tool connects to the
// MCP endpoint.
import type {
  AgentData,
  AgentDriver,
  AgentScope,
  AgentTokenSpec,
} from '@converge/services';

export const AGENT_SCOPES: Array<{ value: AgentScope; hint: string }> = [
  { value: 'work', hint: 'Queue, claim, heartbeat, report, release' },
  { value: 'issues:write', hint: 'Change issues' },
  { value: 'comments:write', hint: 'Post comments' },
  { value: 'issues:read', hint: 'Read issues, teams, labels, search' },
  { value: 'comments:read', hint: 'Read comments' },
  {
    value: 'sync:read',
    hint: 'The whole workspace feed; not with a team limit',
  },
];

export const DRIVER_HINT: Record<AgentDriver, string> = {
  runtime: "The built-in swarm works this agent's issues.",
  external:
    'A coding agent you run (Claude Code, Cursor, Codex, a script) works its issues over MCP or the API. The swarm leaves it alone.',
};

export const EXPIRY_OPTIONS: Array<{ hours: number; label: string }> = [
  { hours: 720, label: '30 days' },
  { hours: 2160, label: '90 days' },
  { hours: 8760, label: '1 year' },
  { hours: 87600, label: '10 years' },
];

export type AgentAccess = 'scoped' | 'full';

export interface AgentAccessValues {
  name: string;
  teamIds: string[];
  driver: AgentDriver;
  access: AgentAccess;
  scopes: AgentScope[];
  teamLimited: boolean;
  expiryHours: number;
}

// An external agent starts with least privilege. A runtime agent keeps the
// token it always had (full access, ten years): the runtime never uses it,
// and runner scripts written against it keep working.
export function agentDefaults(
  driver: AgentDriver,
): Pick<
  AgentAccessValues,
  'access' | 'scopes' | 'teamLimited' | 'expiryHours'
> {
  if (driver === 'external') {
    return {
      access: 'scoped',
      scopes: ['work', 'issues:write', 'comments:write'],
      teamLimited: true,
      expiryHours: 8760,
    };
  }
  return { access: 'full', scopes: [], teamLimited: false, expiryHours: 87600 };
}

export function accessProblem(v: AgentAccessValues): string | undefined {
  if (v.access === 'scoped' && v.scopes.length === 0) {
    return 'Pick at least one scope, or give full access';
  }
  if (
    v.teamLimited &&
    v.access === 'scoped' &&
    v.scopes.includes('sync:read')
  ) {
    return 'sync:read covers the whole workspace, so it cannot be limited to teams';
  }
  return undefined;
}

export function agentRequest(v: AgentAccessValues): {
  name: string;
  teamIds: string[];
  driver: AgentDriver;
  token: AgentTokenSpec;
} {
  const token: AgentTokenSpec = { ttlHours: v.expiryHours };
  if (v.access === 'scoped') {
    token.scopes = AGENT_SCOPES.map((s) => s.value).filter((s) =>
      v.scopes.includes(s),
    );
  }
  if (v.teamLimited) {
    token.teamIds = [...v.teamIds];
  }
  return { name: v.name.trim(), teamIds: v.teamIds, driver: v.driver, token };
}

// grantSummary describes what the server actually issued.
export function grantSummary(
  data: Pick<AgentData, 'tokenScopes' | 'tokenTeamIds'>,
  teamName: (id: string) => string,
): { scopes: string; teams: string } {
  return {
    scopes: data.tokenScopes ? data.tokenScopes.join(', ') : 'full access',
    teams: data.tokenTeamIds
      ? data.tokenTeamIds.map(teamName).join(', ')
      : 'all of its teams',
  };
}

// When a rotated-out token stops working (the rotation's graceHours).
export const GRACE_OPTIONS: Array<{ hours: number; label: string }> = [
  { hours: 0, label: 'now' },
  { hours: 1, label: 'in 1 hour' },
  { hours: 24, label: 'in 24 hours' },
];

const HOUR = 60 * 60 * 1000;

// The server stamps a token's last use at most once an hour, so "recent"
// has to span two.
export function usedRecently(lastUsedAt: string | null, now: Date): boolean {
  return (
    lastUsedAt !== null &&
    now.getTime() - new Date(lastUsedAt).getTime() < 2 * HOUR
  );
}

// A token ending within two days shows the time left rather than a date.
export function expiryLabel(expiresAt: string | null, now: Date): string {
  if (!expiresAt) {
    return 'never expires';
  }
  const at = new Date(expiresAt);
  const left = at.getTime() - now.getTime();
  if (left < 48 * HOUR) {
    const hours = Math.max(1, Math.round(left / HOUR));
    return `expires in ${hours} ${hours === 1 ? 'hour' : 'hours'}`;
  }
  return `expires ${at.toLocaleDateString()}`;
}

export function mcpEndpoint(origin: string): string {
  return `${origin.replace(/\/+$/, '')}/api/v1/mcp`;
}

// The token itself never goes into a snippet: it lives in CONVERGE_TOKEN.
export function mcpSnippets(
  endpoint: string,
): Array<{ client: string; where?: string; text: string }> {
  return [
    {
      client: 'Claude Code',
      text: `claude mcp add --transport http converge '${endpoint}' --header "Authorization: Bearer $CONVERGE_TOKEN"`,
    },
    {
      client: 'Cursor',
      where: '~/.cursor/mcp.json',
      text: JSON.stringify(
        {
          mcpServers: {
            converge: {
              url: endpoint,
              headers: { Authorization: 'Bearer $' + '{env:CONVERGE_TOKEN}' },
            },
          },
        },
        null,
        2,
      ),
    },
    {
      client: 'Codex',
      where: '~/.codex/config.toml',
      text: `[mcp_servers.converge]\nurl = ${JSON.stringify(endpoint)}\nbearer_token_env_var = "CONVERGE_TOKEN"`,
    },
  ];
}
