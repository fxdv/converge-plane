import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  GRACE_OPTIONS,
  accessProblem,
  agentDefaults,
  agentRequest,
  expiryLabel,
  grantSummary,
  mcpEndpoint,
  mcpSnippets,
  usedRecently,
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
        ttlHours: 2160,
        scopes: ['work', 'issues:write', 'comments:write'],
        teamIds: ['t1', 't2'],
      },
    });
  });

  it('gives a runtime agent the same narrow token', () => {
    const req = agentRequest(
      values({ driver: 'runtime', ...agentDefaults('runtime') }),
    );
    assert.deepEqual(req.token, {
      ttlHours: 2160,
      scopes: ['work', 'issues:write', 'comments:write'],
      teamIds: ['t1', 't2'],
    });
    assert.equal(req.driver, 'runtime');
  });

  it('sends scopes in canonical order and a team limit only when asked', () => {
    const req = agentRequest(
      values({ scopes: ['comments:read', 'work'], teamLimited: false }),
    );
    assert.deepEqual(req.token, {
      ttlHours: 2160,
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
    assert.match(
      accessProblem(values({ access: 'full', scopes: [] }))!,
      /cannot be limited to teams/,
    );
    assert.equal(
      accessProblem(values({ access: 'full', scopes: [], teamLimited: false })),
      undefined,
    );
    assert.deepEqual(
      agentRequest(values({ access: 'full', teamLimited: false })).token.scopes,
      [
        'work',
        'issues:write',
        'comments:write',
        'issues:read',
        'comments:read',
        'sync:read',
      ],
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

describe('token rotation (the API tokens dialog)', () => {
  const now = new Date('2026-09-27T12:00:00Z');

  it('offers only graces the server accepts, retiring the old token now first', () => {
    assert.equal(GRACE_OPTIONS[0].hours, 0);
    for (const option of GRACE_OPTIONS) {
      assert.ok(option.hours >= 0 && option.hours <= 168, option.label);
    }
  });

  it('counts two hours as recent use, since last use is stamped hourly', () => {
    assert.equal(usedRecently(null, now), false);
    assert.equal(usedRecently('2026-09-27T10:30:00Z', now), true);
    assert.equal(usedRecently('2026-09-27T09:59:00Z', now), false);
  });

  it('shows the hours left on a token ending within two days', () => {
    assert.equal(expiryLabel(null, now), 'never expires');
    assert.equal(expiryLabel('2026-09-27T12:10:00Z', now), 'expires in 1 hour');
    assert.equal(
      expiryLabel('2026-09-28T12:00:00Z', now),
      'expires in 24 hours',
    );
    assert.doesNotMatch(expiryLabel('2027-09-27T12:00:00Z', now), / in /);
  });
});
