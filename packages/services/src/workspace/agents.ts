import axios from 'axios';

// runtime: the in-process floor triages the agent's issues. external: a
// coding agent works them through the work API or MCP.
export type AgentDriver = 'runtime' | 'external';

export type AgentScope =
  | 'issues:read'
  | 'issues:write'
  | 'comments:read'
  | 'comments:write'
  | 'work'
  | 'sync:read';

// scopes is required on issuance. teamIds omitted means every team the
// agent belongs to. A stored token may still report null scopes: that
// grant predates the requirement and keeps full authority until rotated.
export interface AgentTokenSpec {
  scopes?: AgentScope[];
  teamIds?: string[];
  ttlHours?: number;
}

export interface AgentData {
  id: string;
  name: string;
  email: string;
  kind: 'human' | 'agent';
  role: string;
  status: 'ACTIVE' | 'SUSPENDED';
  driver: AgentDriver;
  teamIds: string[];
  // Present on create/rotate responses only; shown exactly once.
  token?: string;
  tokenPrefix: string;
  tokenExpiresAt?: string;
  // null: the agent's full authority.
  tokenScopes: AgentScope[] | null;
  tokenTeamIds: string[] | null;
}

export interface AgentTokenInfo {
  id: string;
  name: string;
  scopes: AgentScope[] | null;
  teamIds: string[] | null;
  expiresAt: string | null;
  lastUsedAt: string | null;
  createdAt: string;
}

export interface AgentListEntry {
  id: string;
  name: string;
  email: string;
  status: 'ACTIVE' | 'SUSPENDED';
  driver: AgentDriver;
  teamIds: string[];
  tokenCount: number;
  lastUsedAt?: string | null;
  createdAt: string;
  tokens: AgentTokenInfo[];
}

export async function createAgent(
  workspaceId: string,
  data: {
    name: string;
    teamIds: string[];
    driver?: AgentDriver;
    token?: AgentTokenSpec;
  },
) {
  const response = await axios.post<AgentData>(
    `/api/v1/workspaces/${workspaceId}/agents`,
    data,
  );

  return response.data;
}

export async function getAgents(workspaceId: string) {
  const response = await axios.get<AgentListEntry[]>(
    `/api/v1/workspaces/${workspaceId}/agents`,
  );

  return response.data;
}

// A new token beside the agent's others. The spec must name scopes.
export async function issueAgentToken(
  workspaceId: string,
  accountId: string,
  data?: { name?: string } & AgentTokenSpec,
) {
  const response = await axios.post<{
    tokenId: string;
    // Shown exactly once.
    token: string;
    tokenPrefix: string;
    tokenExpiresAt: string;
    scopes: AgentScope[] | null;
    teamIds: string[] | null;
  }>(`/api/v1/workspaces/${workspaceId}/agents/${accountId}/token`, data);

  return response.data;
}

export interface AgentTokenRotation {
  tokenId: string;
  // Shown exactly once.
  token: string;
  tokenPrefix: string;
  tokenExpiresAt: string;
  name: string;
  scopes: AgentScope[] | null;
  teamIds: string[] | null;
  oldTokenId: string;
  oldTokenEndsAt: string;
}

// Replaces one token with one of the same name, scopes and lifetime. The
// old token stops working now (graceHours 0) or after graceHours.
export async function rotateAgentToken(
  workspaceId: string,
  accountId: string,
  data: { tokenId: string; graceHours: number },
) {
  const response = await axios.post<AgentTokenRotation>(
    `/api/v1/workspaces/${workspaceId}/agents/${accountId}/token/rotate`,
    data,
  );

  return response.data;
}

export async function revokeAgentToken(
  workspaceId: string,
  accountId: string,
  data?: { tokenId?: string },
) {
  const response = await axios.post<{ revoked: number }>(
    `/api/v1/workspaces/${workspaceId}/agents/${accountId}/token/revoke`,
    data,
  );

  return response.data;
}

export async function deleteAgent(workspaceId: string, accountId: string) {
  const response = await axios.delete<{ deleted: boolean }>(
    `/api/v1/workspaces/${workspaceId}/agents/${accountId}`,
  );

  return response.data;
}
