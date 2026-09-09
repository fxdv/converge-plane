// wire-contract.test.ts — the client side of the sync wire contract.
//
// The server side is pinned by server/internal/api/wire_contract_test.go
// (what the server emits); this file pins what the client's MST models
// accept. Together they form the contract: every payload the server can
// emit must validate through the client models, and out-of-domain values
// from the permissive wire must either validate (the client renders
// total) or be normalized by a documented helper — never crash the app.
//
// Run via `pnpm test` (scripts/wire-contract.sh): the harness compiles
// this file plus the real model sources with the project's TypeScript
// and executes them under node's built-in test runner, so it runs in CI
// with zero extra dependencies.
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

import { safePriorityIndex } from 'common/priority';

import { Comment } from 'store/comments/models';
import { Issue } from 'store/issues/models';
import { IssueHistory } from 'store/issue-history/models';
import { Label } from 'store/labels/models';
import { Team } from 'store/teams/models';
import { saveSwarmActivityData } from 'store/swarm-activity/save-data';
import { SwarmActivity } from 'store/swarm-activity/models';
import { SwarmActivityStore, swarmActivityTTL } from 'store/swarm-activity/store';
import { View } from 'store/views/models';
import { Workflow } from 'store/workflows/models';
import { UsersOnWorkspace, Workspace } from 'store/workspace/models';
import { WorkspaceStore } from 'store/workspace/store';

const stamp = '2026-09-02T12:00:00.000Z';

// Payloads mirror server/internal/api/wire_contract_test.go exactly —
// keep the two files in step.
const member = (
  over: Record<string, unknown> = {},
): Record<string, unknown> => ({
  id: 'm1',
  createdAt: stamp,
  updatedAt: stamp,
  role: 'USER',
  status: 'ACTIVE',
  userId: 'u1',
  workspaceId: 'w1',
  teamIds: [],
  settings: {},
  ...over,
});

const issue = (
  over: Record<string, unknown> = {},
): Record<string, unknown> => ({
  id: 'i1',
  createdAt: stamp,
  updatedAt: stamp,
  title: 'T',
  number: 7,
  description: null,
  priority: null,
  dueDate: null,
  sortOrder: 3,
  estimate: 0,
  teamId: 't1',
  createdById: 'u1',
  assigneeId: null,
  labelIds: [],
  parentId: null,
  stateId: 's1',
  subscriberIds: [],
  children: [],
  agentPaused: false,
  relations: [],
  ...over,
});

describe('safePriorityIndex (render total over the permissive wire domain)', () => {
  it('passes the display domain through untouched', () => {
    for (const v of [0, 1, 2, 3, 4]) {
      assert.equal(safePriorityIndex(v), v);
    }
  });
  it('degrades everything else to no-priority', () => {
    for (const v of [5, -1, 0.5, NaN, null, undefined, '3', true] as never[]) {
      assert.equal(safePriorityIndex(v as number | null | undefined), 0);
    }
  });
});

describe('UsersOnWorkspace model', () => {
  it('accepts every role the server can emit', () => {
    for (const role of ['ADMIN', 'USER', 'AGENT']) {
      const node = UsersOnWorkspace.create(member({ role }) as never);
      assert.equal(node.role, role);
    }
  });
  it('accepts every status the server can emit (plus undefined)', () => {
    for (const status of ['INVITED', 'ACTIVE', 'SUSPENDED', undefined] as never[]) {
      UsersOnWorkspace.create(member({ status }) as never);
    }
  });
  it('rejects out-of-vocabulary roles (the server must map via clientRole)', () => {
    assert.throws(() => UsersOnWorkspace.create(member({ role: 'OWNER' }) as never));
  });
  it('rejects a degraded payload (the crash class fixed in the save handler)', () => {
    assert.throws(() => UsersOnWorkspace.create({ id: 'm1' } as never));
  });
});

describe('WorkspaceStore D-record handling (the sync crash regression)', () => {
  it('crashes on a minimal {id} upsert — so the handler must special-case D', () => {
    const store = WorkspaceStore.create({
      workspace: undefined,
      usersOnWorkspaces: [member() as never],
    });
    // Pre-fix saveWorkspaceData built this from a D record and upserted it.
    assert.throws(() =>
      store.updateUsers({ id: 'm1' } as never, 'm1'),
    );
  });
  it('deletes cleanly by id (the fixed path)', () => {
    const store = WorkspaceStore.create({
      workspace: undefined,
      usersOnWorkspaces: [member() as never],
    });
    store.deleteUser('m1');
    assert.equal(store.usersOnWorkspaces.length, 0);
  });
});

describe('Issue model', () => {
  it('accepts the full server contract payload', () => {
    const node = Issue.create(issue() as never);
    assert.equal(node.stateId, 's1');
    assert.deepEqual(node.labelIds, []);
  });
  it('accepts out-of-domain priority (5, -1): the client renders total', () => {
    assert.equal(Issue.create(issue({ priority: 5 }) as never).priority, 5);
    assert.equal(Issue.create(issue({ priority: -1 }) as never).priority, -1);
  });
  it('requires its non-nullable fields (no defaults exist)', () => {
    assert.throws(
      () =>
        Issue.create(
          { ...issue(), stateId: null, teamId: null } as never,
        ),
    );
    assert.throws(() => Issue.create({ ...issue(), labelIds: null } as never));
  });
  it('accepts the D1 agentPaused escalation flag (both values)', () => {
    assert.equal(
      Issue.create(issue({ agentPaused: true }) as never).agentPaused,
      true,
    );
    assert.equal(Issue.create(issue() as never).agentPaused, false);
  });
  it('defaults agentPaused on pre-D1 payloads (outbox retention)', () => {
    const { agentPaused: _omitted, ...legacy } = issue();
    assert.equal(Issue.create(legacy as never).agentPaused, false);
  });

  // v1.1: the denormalized relations array (server/internal/api/
  // issue_relation.go relationListSQL — the reader's perspective).
  const relation = (over: Record<string, unknown> = {}) => ({
    id: 'r1',
    createdAt: stamp,
    updatedAt: stamp,
    issueId: 'i1',
    createdById: 'u1',
    relatedIssueId: 'i2',
    type: 'BLOCKS',
    ...over,
  });

  it('accepts the server relations payload (reader-side rewrite)', () => {
    const node = Issue.create(
      issue({
        relations: [
          relation(),
          relation({ id: 'r2', relatedIssueId: 'i3', type: 'BLOCKED' }),
        ],
      }) as never,
    );
    assert.equal(node.relations.length, 2);
    assert.equal(node.relations[0].type, 'BLOCKS');
    assert.equal(node.relations[1].relatedIssueId, 'i3');
  });
  it('accepts a null createdById (the denormalized subselect emits null)', () => {
    const node = Issue.create(
      issue({ relations: [relation({ createdById: null })] }) as never,
    );
    assert.equal(node.relations[0].createdById, null);
  });
  it('defaults relations on pre-v1.1 payloads (outbox retention)', () => {
    const { relations: _omitted, ...legacy } = issue();
    assert.deepEqual(Issue.create(legacy as never).relations, []);
  });
});

describe('IssueHistory model', () => {
  it('accepts a D1 handoff row (action + summary)', () => {
    const node = IssueHistory.create({
      id: 'h7',
      createdAt: stamp,
      updatedAt: stamp,
      userId: 'a1',
      issueId: 'i1',
      action: 'handoff',
      summary: 'found the bug',
      addedLabelIds: [],
      removedLabelIds: [],
      fromPriority: null,
      toPriority: null,
      fromStateId: null,
      toStateId: null,
      fromEstimate: null,
      toEstimate: null,
      fromAssigneeId: 'a1',
      toAssigneeId: 'a2',
      fromParentId: null,
      toParentId: null,
      relationChanges: null,
      sourceMetadata: null,
    } as never);
    assert.equal(node.action, 'handoff');
    assert.equal(node.summary, 'found the bug');
  });
  it('accepts a fully-null transition (every field present, null)', () => {
    const node = IssueHistory.create({
      id: 'h1',
      createdAt: stamp,
      updatedAt: stamp,
      userId: 'u1',
      issueId: 'i1',
      addedLabelIds: [],
      removedLabelIds: [],
      fromPriority: null,
      toPriority: null,
      fromStateId: null,
      toStateId: null,
      fromEstimate: null,
      toEstimate: null,
      fromAssigneeId: null,
      toAssigneeId: null,
      fromParentId: null,
      toParentId: null,
      relationChanges: null,
      sourceMetadata: null,
    } as never);
    assert.equal(node.addedLabelIds.length, 0);
  });
  it('accepts out-of-domain priorities', () => {
    IssueHistory.create({
      id: 'h2',
      createdAt: stamp,
      updatedAt: stamp,
      userId: null,
      issueId: null,
      addedLabelIds: [],
      removedLabelIds: [],
      fromPriority: null,
      toPriority: 5,
      fromStateId: null,
      toStateId: null,
      fromEstimate: null,
      toEstimate: null,
      fromAssigneeId: null,
      toAssigneeId: null,
      fromParentId: null,
      toParentId: null,
      relationChanges: null,
      sourceMetadata: null,
    } as never);
  });
  it('rejects a degraded payload (required label arrays missing)', () => {
    assert.throws(
      () =>
        IssueHistory.create({ id: 'h3', createdAt: stamp, updatedAt: stamp } as never),
    );
  });
});

describe('Comment model', () => {
  it('accepts an empty-string body (the server never sends null)', () => {
    const node = Comment.create({
      id: 'c1',
      createdAt: stamp,
      updatedAt: stamp,
      body: '',
      userId: 'u1',
      issueId: 'i1',
      parentId: null,
      sourceMetadata: null,
    } as never);
    assert.equal(node.body, '');
  });
  it('rejects a null body (the client union is string|undefined)', () => {
    assert.throws(
      () =>
        Comment.create({
          id: 'c2',
          createdAt: stamp,
          updatedAt: stamp,
          body: null,
          userId: 'u1',
          issueId: 'i1',
          parentId: null,
          sourceMetadata: null,
        } as never),
    );
  });
});

describe('Team model', () => {
  it('accepts an empty preferences object', () => {
    Team.create({
      id: 't1',
      createdAt: stamp,
      updatedAt: stamp,
      name: 'Engineering',
      identifier: 'ENG',
      workspaceId: 'w1',
      currentCycle: null,
      preferences: {},
    } as never);
  });
  it('rejects a null preferences object', () => {
    assert.throws(
      () =>
        Team.create({
          id: 't2',
          createdAt: stamp,
          updatedAt: stamp,
          name: 'Engineering',
          identifier: 'ENG',
          workspaceId: 'w1',
          currentCycle: null,
          preferences: null,
        } as never),
    );
  });
});

describe('Workflow model', () => {
  it('accepts all six client categories', () => {
    for (const category of [
      'BACKLOG',
      'UNSTARTED',
      'STARTED',
      'COMPLETED',
      'CANCELED',
      'TRIAGE',
    ]) {
      Workflow.create({
        id: 'wf1',
        createdAt: stamp,
        updatedAt: stamp,
        name: 'N',
        description: null,
        position: 0,
        color: '#8884d8',
        category: category as never,
        teamId: 't1',
      } as never);
    }
  });
  it('rejects legacy lowercase categories (the server normalizes)', () => {
    assert.throws(
      () =>
        Workflow.create({
          id: 'wf2',
          createdAt: stamp,
          updatedAt: stamp,
          name: 'N',
          description: null,
          position: 0,
          color: '#8884d8',
          category: 'todo',
          teamId: 't1',
        } as never),
    );
  });
});

describe('Label model', () => {
  it('requires a color (the client model has no default)', () => {
    Label.create({
      id: 'l1',
      createdAt: stamp,
      updatedAt: stamp,
      name: 'Bug',
      color: '#f00',
      description: null,
      workspaceId: 'w1',
      teamId: null,
      groupId: null,
    } as never);
    assert.throws(
      () =>
        Label.create({
          id: 'l2',
          createdAt: stamp,
          updatedAt: stamp,
          name: 'Bug',
          color: null,
          description: null,
          workspaceId: 'w1',
          teamId: null,
          groupId: null,
        } as never),
    );
  });
});

describe('View model', () => {
  it('accepts empty filters with all required fields', () => {
    View.create({
      id: 'v1',
      createdAt: stamp,
      updatedAt: stamp,
      name: 'My view',
      description: '',
      filters: {},
      isBookmarked: false,
      workspaceId: 'w1',
      teamId: null,
      createdById: 'u1',
    } as never);
  });
});

describe('Workspace model', () => {
  it('accepts the server payload and a null preferences', () => {
    const node = Workspace.create({
      id: 'w1',
      createdAt: stamp,
      updatedAt: stamp,
      name: 'Acme',
      slug: 'acme',
      preferences: null,
    } as never);
    assert.equal(node.slug, 'acme');
  });
});

// --- SwarmActivity: the in-flight work signal (spec cs:swarm:activity)
// Payloads mirror server/internal/api/swarm_activity_test.go exactly —
// keep the two files in step.
const signal = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
  id: 'a1',
  agentId: 'a1',
  agentName: 'scout',
  issueId: 'i1',
  issueNumber: 29,
  issuePrefix: 'ENG',
  phase: 'deciding',
  since: stamp,
  ...over,
});

describe('SwarmActivity model', () => {
  it('accepts the exact 8-key wire shape the server emits', () => {
    const node = SwarmActivity.create(signal() as never);
    assert.equal(node.agentId, 'a1');
    assert.equal(node.issueNumber, 29);
    assert.equal(node.phase, 'deciding');
  });
  it('accepts a future, unknown phase (degrades, never crashes validation)', () => {
    // The OWNER-role crash class: a value outside the client vocabulary
    // must not throw during model creation. The client renders it as a
    // plain-text phase instead.
    const node = SwarmActivity.create(signal({ phase: 'dreaming' }) as never);
    assert.equal(node.phase, 'dreaming');
  });
  it('rejects a degraded payload (missing required fields)', () => {
    assert.throws(() => SwarmActivity.create({ id: 'a1' } as never));
  });
});

describe('SwarmActivityStore (one entry per agent)', () => {
  it('upserts by agent id and resolves the signal for an issue', () => {
    const store = SwarmActivityStore.create({ activities: {} });
    store.upsert(signal() as never);
    assert.ok(store.activities.has('a1'));
    const found = store.forIssue('i1');
    assert.ok(found && found.agentName === 'scout');
    assert.equal(store.forIssue('i2'), undefined);
  });
  it('isFresh tracks the TTL (fresh now, stale after it lapses)', () => {
    const store = SwarmActivityStore.create({ activities: {} });
    const fresh = signal({ since: new Date().toISOString() }) as never;
    const stale = signal({
      id: 'a2',
      agentId: 'a2',
      since: new Date(Date.now() - 2 * swarmActivityTTL).toISOString(),
    }) as never;
    store.upsert(fresh);
    store.upsert(stale);
    assert.ok(store.isFresh(store.activities.get('a1')!));
    assert.ok(!store.isFresh(store.activities.get('a2')!));
    store.expireStale();
    assert.equal(store.activities.size, 1); // the stale one is dropped
    assert.ok(store.activities.has('a1'));
  });
  it('removes by id (the DELETE record carries only the id)', () => {
    const store = SwarmActivityStore.create({ activities: {} });
    store.upsert(signal() as never);
    store.remove('a1');
    assert.equal(store.activities.size, 0);
    store.remove('a1'); // idempotent: a double delete is a no-op
  });
});

describe('saveSwarmActivityData (the sync handler)', () => {
  it('routes U to upsert and the id-only D to remove', async () => {
    const store = SwarmActivityStore.create({ activities: {} });
    await saveSwarmActivityData(
      [
        {
          data: signal() as never,
          modelName: 'SwarmActivity',
          modelId: 'a1',
          action: 'U',
          workspaceId: 'w1',
          sequenceId: '1',
        } as never,
        {
          data: { id: 'a1' } as never,
          modelName: 'SwarmActivity',
          modelId: 'a1',
          action: 'D',
          workspaceId: 'w1',
          sequenceId: '2',
        } as never,
      ],
      store,
    );
    assert.equal(store.activities.size, 0);
  });
});
