import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  accessProblem,
  agentDefaults,
  agentRequest,
  grantSummary,
  mcpEndpoint,
  mcpSnippets,
  type AgentAccessValues,
} from 'modules/settings/workspace-settings/members/agent-access';

const values = (over: Partial<AgentAccessValues> = {}): AgentAccessValues => ({
  name: ' coder ',
  teamIds: ['t1', 't2'],
  driver: 'external',
  ...agentDefaults('external'),
  ...over,
});

describe('agent access (the Add agent dialog)', () => {
  it('gives an external agent a least-privilege token by default', () => {
    assert.deepEqual(agentRequest(values()), {
      name: 'coder',
      teamIds: ['t1', 't2'],
      driver: 'external',
      token: {
        ttlHours: 8760,
        scopes: ['work', 'issues:write', 'comments:write'],
        teamIds: ['t1', 't2'],
      },
    });
  });

  it("keeps a runtime agent's full-authority token", () => {
    const req = agentRequest(
      values({ driver: 'runtime', ...agentDefaults('runtime') }),
    );
    assert.deepEqual(req.token, { ttlHours: 87600 });
  });

  it('sends scopes in canonical order and a team limit only when asked', () => {
    const req = agentRequest(
      values({ scopes: ['comments:read', 'work'], teamLimited: false }),
    );
    assert.deepEqual(req.token, {
      ttlHours: 8760,
      scopes: ['work', 'comments:read'],
    });
  });

  it('refuses what the server would refuse', () => {
    assert.match(accessProblem(values({ scopes: [] }))!, /at least one scope/);
    assert.match(
      accessProblem(values({ scopes: ['sync:read'] }))!,
      /cannot be limited to teams/,
    );
    assert.equal(
      accessProblem(values({ scopes: ['sync:read'], teamLimited: false })),
      undefined,
    );
    assert.equal(
      accessProblem(values({ access: 'full', scopes: [] })),
      undefined,
    );
  });

  it('summarizes the grant the server issued', () => {
    const name = (id: string) => ({ t1: 'Eng' })[id] ?? id;
    assert.deepEqual(
      grantSummary({ tokenScopes: ['work'], tokenTeamIds: ['t1'] }, name),
      { scopes: 'work', teams: 'Eng' },
    );
    assert.deepEqual(
      grantSummary({ tokenScopes: null, tokenTeamIds: null }, name),
      {
        scopes: 'full access',
        teams: 'all of its teams',
      },
    );
  });

  it('builds MCP setup snippets without the token', () => {
    const endpoint = mcpEndpoint('https://converge.example.com/');
    assert.equal(endpoint, 'https://converge.example.com/api/v1/mcp');
    const snippets = mcpSnippets(endpoint);
    assert.deepEqual(
      snippets.map((s) => s.client),
      ['Claude Code', 'Cursor', 'Codex'],
    );
    const cursor = JSON.parse(snippets[1].text);
    assert.equal(cursor.mcpServers.converge.url, endpoint);
    assert.equal(
      cursor.mcpServers.converge.headers.Authorization,
      'Bearer ${env:CONVERGE_TOKEN}',
    );
    assert.match(
      snippets[2].text,
      /^url = "https:\/\/converge\.example\.com\/api\/v1\/mcp"$/m,
    );
    for (const s of snippets) {
      assert.doesNotMatch(s.text, /conv_agent_/);
    }
  });
});
