import axios from 'axios';

export interface AgentData {
  id: string;
  name: string;
  email: string;
  kind: 'human' | 'agent';
  role: string;
  status: 'ACTIVE' | 'SUSPENDED';
  teamIds: string[];
  // Present on create/rotate responses only; shown exactly once.
  token?: string;
  tokenPrefix: string;
  tokenExpiresAt?: string;
}

export interface AgentListEntry {
  id: string;
  name: string;
  email: string;
  status: 'ACTIVE' | 'SUSPENDED';
  teamIds: string[];
  tokenCount: number;
  lastUsedAt?: string | null;
  createdAt: string;
}

export async function createAgent(
  workspaceId: string,
  data: { name: string; teamIds: string[] },
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
