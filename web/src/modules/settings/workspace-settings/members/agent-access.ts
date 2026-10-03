// agent-access.ts — the agent dialogs' choices as pure data: the token a
// new agent is issued (server/internal/api/agent.go tokenSpec), how a
// rotation retires the old one, and how an external tool connects to the
// MCP endpoint.
import type {
  AgentData,
  AgentDriver,
  AgentListEntry,
  AgentScope,
  AgentTokenSpec,
  AgentTokenInfo,
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
  runtime:
    'The in-process floor triages this agent’s issues and hands them on. It does not complete them.',
  external:
    'A coding agent you run (Claude Code, Cursor, Codex) works its issues over MCP. The floor leaves it alone.',
};

export const EXPIRY_OPTIONS: Array<{ hours: number; label: string }> = [
  { hours: 720, label: '30 days' },
  { hours: 2160, label: '90 days' },
  { hours: 8760, label: '1 year' },
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

// Both drivers start narrow: the scopes a coding agent needs, for 90 days,
// limited to the teams named on the form. "Every scope" is an explicit
// list, never an omitted grant.
export function agentDefaults(
  driver: AgentDriver,
): Pick<
  AgentAccessValues,
  'access' | 'scopes' | 'teamLimited' | 'expiryHours'
> {
  switch (driver) {
    case 'runtime':
    case 'external':
      return {
        access: 'scoped',
        scopes: ['work', 'issues:write', 'comments:write'],
        teamLimited: true,
        expiryHours: 2160,
      };
  }
}

export function accessProblem(v: AgentAccessValues): string | undefined {
  const scopes =
    v.access === 'full' ? AGENT_SCOPES.map((s) => s.value) : v.scopes;
  if (scopes.length === 0) {
    return 'Pick at least one scope';
  }
  if (v.teamLimited && scopes.includes('sync:read')) {
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
  const chosen =
    v.access === 'full' ? AGENT_SCOPES.map((s) => s.value) : v.scopes;
  token.scopes = AGENT_SCOPES.map((s) => s.value).filter((s) =>
    chosen.includes(s),
  );
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

/** One line for the Members list (Phase 1). */
export function agentTokensAtAGlance(
  entry: Pick<AgentListEntry, 'tokens' | 'driver'>,
  teamName: (id: string) => string,
  now: Date = new Date(),
): string {
  const driver =
    entry.driver === 'runtime' ? 'Floor runtime' : 'External (MCP)';
  if (entry.tokens.length === 0) {
    return `${driver} · no live tokens`;
  }
  const primary: AgentTokenInfo = entry.tokens[0];
  const grant = grantSummary(
    { tokenScopes: primary.scopes, tokenTeamIds: primary.teamIds },
    teamName,
  );
  const count =
    entry.tokens.length === 1
      ? '1 token'
      : `${entry.tokens.length} tokens`;
  return `${driver} · ${count} · ${grant.scopes} · ${expiryLabel(primary.expiresAt, now)}`;
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
