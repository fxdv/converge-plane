import axios from 'axios';

// runtime: the built-in swarm works the agent's issues. external: an
// outside process works them through the work API or MCP.
export type AgentDriver = 'runtime' | 'external';

export type AgentScope =
  | 'issues:read'
  | 'issues:write'
  | 'comments:read'
  | 'comments:write'
  | 'work'
  | 'sync:read';

// Omitted scopes / teamIds keep the agent's full authority.
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

export async function rotateAgentToken(
  workspaceId: string,
  accountId: string,
  data?: { name?: string },
) {
  const response = await axios.post<{
    tokenId: string;
    token: string;
    tokenPrefix: string;
    tokenExpiresAt: string;
  }>(`/api/v1/workspaces/${workspaceId}/agents/${accountId}/token`, data);

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
