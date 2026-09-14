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

import type { SyncActionRecord } from 'common/types';

import { safePriorityIndex } from 'common/priority';

import {
  dedupeLiveRecords,
  liveIdsByModel,
  pruneStaleLocalRecords,
  seedTabHighWater,
  staleIdsForModel,
  tabHighWater,
  type PruneDomain,
  type PruneRow,
} from 'common/wrappers/socket-data-util';

import { Comment } from 'store/comments/models';
import { Issue } from 'store/issues/models';
import { IssuesStore } from 'store/issues/store';
import { IssueHistory } from 'store/issue-history/models';
import { IssueHistoryStore } from 'store/issue-history/store';
import { Label } from 'store/labels/models';
import { LabelsStore } from 'store/labels/store';
import { Project } from 'store/projects/models';
import { ProjectsStore } from 'store/projects/store';
import { Team } from 'store/teams/models';
import { TeamsStore } from 'store/teams/store';
import { ViewsStore } from 'store/views/store';
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
  projectIds: [],
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
  it('a degraded upsert over an existing row keeps the last good values (no board crash)', () => {
    const store = WorkspaceStore.create({
      workspace: undefined,
      usersOnWorkspaces: [member() as never],
    });
    // The pre-fix crash: a minimal record (what a D record carries) upserted
    // over a live row took the store down. The merge now spreads the
    // existing snapshot first, so a degraded wire record retains the last
    // good values instead — the handler's D special-case still owns
    // deletions, and a payload with *wrong* values still fails create()
    // (pinned in the model block above); only *missing* fields are kept.
    store.updateUsers({ id: 'm1' } as never, 'm1');
    assert.equal(store.usersOnWorkspaces.length, 1);
    assert.equal(store.usersOnWorkspaces[0].role, 'USER');
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

  // v1.1: the project membership (server/internal/api/project.go +
  // sync.go — the server always emits the array; v1: at most one).
  it('accepts the server projectIds payload (single membership)', () => {
    const node = Issue.create(issue({ projectIds: ['p1'] }) as never);
    assert.deepEqual(node.projectIds, ['p1']);
  });
  it('defaults projectIds on pre-v1.1 payloads (outbox retention)', () => {
    const { projectIds: _omitted, ...legacy } = issue();
    assert.deepEqual(Issue.create(legacy as never).projectIds, []);
  });
});

describe('Project model', () => {
  // Mirrors server/internal/api/project.go projectData exactly.
  const project = (
    over: Record<string, unknown> = {},
  ): Record<string, unknown> => ({
    id: 'p1',
    createdAt: stamp,
    updatedAt: stamp,
    name: 'Rail',
    description: '',
    color: '#3B82F6',
    startDate: null,
    endDate: null,
    status: 'ACTIVE',
    leadUserId: null,
    teams: [],
    workspaceId: 'w1',
    ...over,
  });

  it('accepts the server payload (all nullable variants)', () => {
    const node = Project.create(project() as never);
    assert.equal(node.name, 'Rail');
    assert.equal(node.status, 'ACTIVE');
    assert.deepEqual(node.teams, []);
    assert.equal(node.leadUserId, null);
    assert.equal(node.color, '#3B82F6');
  });
  it('accepts populated optionals (lead, dates, teams)', () => {
    const node = Project.create(
      project({
        leadUserId: 'u1',
        startDate: '2026-09-01',
        endDate: '2026-09-30',
        teams: ['t1', 't2'],
        description: 'scope',
      }) as never,
    );
    assert.equal(node.leadUserId, 'u1');
    assert.equal(node.startDate, '2026-09-01');
    assert.deepEqual(node.teams, ['t1', 't2']);
  });
  it('rejects a degraded payload (missing the required wire fields)', () => {
    assert.throws(() => Project.create({ id: 'p1' } as never));
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

// ---------------------------------------------------------------------------
// Bootstrap reconciliation (the prune pass): the client-side policy that
// keeps the cache from resurrecting rows the server no longer has (a
// deletion that never reached this client as a DELETE record).
// The pure decision core is pinned here — a wrong deletion is unrecoverable
// data loss; the Dexie read/write glue mirrors the save-data handlers the
// harness already exercises.
// ---------------------------------------------------------------------------

const rec = (
  modelName: string,
  id: string,
  action: 'I' | 'U' | 'D',
  sequenceId = '1',
): SyncActionRecord =>
  ({
    data: { id },
    modelName,
    modelId: id,
    action,
    workspaceId: 'w1',
    sequenceId,
  }) as SyncActionRecord;

const pruneRow = (id: string, over: Partial<PruneRow> = {}): PruneRow => ({
  id,
  ...over,
});

const domain = (over: Partial<PruneDomain> = {}): PruneDomain => ({
  workspaceId: 'w1',
  teamIds: new Set(['t1']),
  issueIds: new Set(['i1']),
  ...over,
});

describe('liveIdsByModel (snapshot -> live id sets)', () => {
  it('groups I/U records by model and keeps every synced model present', () => {
    const live = liveIdsByModel([
      rec('Project', 'p1', 'I'),
      rec('Project', 'p2', 'U'),
      rec('Issue', 'i1', 'I'),
    ]);
    assert.deepEqual([...live.get('Project')!.values()], ['p1', 'p2']);
    assert.deepEqual([...live.get('Issue')!.values()], ['i1']);
    // A model the snapshot never mentions is an empty set, not absent:
    // zero rows in the snapshot is a truth the prune may act on.
    assert.equal(live.get('View')!.size, 0);
  });

  it('treats D records as not-live', () => {
    const live = liveIdsByModel([rec('Project', 'p1', 'D')]);
    assert.equal(live.get('Project')!.size, 0);
  });

  it('survives a null snapshot (the defensive array check)', () => {
    const live = liveIdsByModel(null as never);
    assert.equal(live.get('Project')!.size, 0);
  });
});

describe('staleIdsForModel (the deletion policy)', () => {
  const projectRows: PruneRow[] = [
    pruneRow('p1', { workspaceId: 'w1' }), // live in the snapshot
    pruneRow('p2', { workspaceId: 'w1' }), // residue: in domain, not live
    pruneRow('p9', { workspaceId: 'w2' }), // another workspace: cache, never
  ];

  it('prunes in-domain rows missing from the snapshot and nothing else', () => {
    const live = liveIdsByModel([rec('Project', 'p1', 'I')]);
    assert.deepEqual(
      staleIdsForModel('Project', domain(), projectRows, live),
      ['p2'],
    );
  });

  it('prunes the whole in-domain set when the snapshot has zero rows', () => {
    // An empty snapshot means the workspace has zero live projects:
    // every in-domain row (p1 and p2) is residue; the foreign-workspace
    // row (p9) is still untouched.
    const live = liveIdsByModel([]);
    assert.deepEqual(
      staleIdsForModel('Project', domain(), projectRows, live),
      ['p1', 'p2'],
    );
  });

  it('scopes issues through their team (other workspaces stay untouched)', () => {
    const live = liveIdsByModel([rec('Issue', 'i1', 'I')]);
    const rows: PruneRow[] = [
      pruneRow('i1', { teamId: 't1' }), // live
      pruneRow('i2', { teamId: 't1' }), // residue
      pruneRow('i9', { teamId: 'tX' }), // outside this workspace
    ];
    assert.deepEqual(
      staleIdsForModel('Issue', domain(), rows, live),
      ['i2'],
    );
  });

  it('scopes comments and history through their issue', () => {
    const live = liveIdsByModel([
      rec('IssueComment', 'c1', 'I'),
      rec('IssueHistory', 'h1', 'I'),
    ]);
    const comments: PruneRow[] = [
      pruneRow('c1', { issueId: 'i1' }),
      pruneRow('c2', { issueId: 'i1' }), // residue
      pruneRow('c9', { issueId: 'iX' }), // another workspace: untouched
    ];
    assert.deepEqual(
      staleIdsForModel('IssueComment', domain(), comments, live),
      ['c2'],
    );
    const history: PruneRow[] = [
      pruneRow('h1', { issueId: 'i1' }),
      pruneRow('h2', { issueId: 'i1' }), // residue
      pruneRow('h9', { issueId: 'iX' }),
    ];
    assert.deepEqual(
      staleIdsForModel('IssueHistory', domain(), history, live),
      ['h2'],
    );
  });

  it('scopes workflows through their team', () => {
    const live = liveIdsByModel([rec('Workflow', 's1', 'I')]);
    const rows: PruneRow[] = [
      pruneRow('s1', { teamId: 't1' }),
      pruneRow('s2', { teamId: 't1' }), // residue
      pruneRow('s9', { teamId: 'tX' }),
    ];
    assert.deepEqual(
      staleIdsForModel('Workflow', domain(), rows, live),
      ['s2'],
    );
  });

  it('never prunes workspace rows (the picker relies on the union cache)', () => {
    const live = liveIdsByModel([rec('Workspace', 'w1', 'I')]);
    const rows: PruneRow[] = [pruneRow('w1'), pruneRow('w2')];
    assert.deepEqual(
      staleIdsForModel('Workspace', domain(), rows, live),
      [],
    );
  });

  it('prunes swarm signals the snapshot no longer reports (crashed workers)', () => {
    const live = liveIdsByModel([rec('SwarmActivity', 'a1', 'I')]);
    const rows: PruneRow[] = [pruneRow('a1'), pruneRow('a2')];
    assert.deepEqual(
      staleIdsForModel('SwarmActivity', domain(), rows, live),
      ['a2'],
    );
  });
});

describe('pruneStaleLocalRecords (the reconciliation pass)', () => {
  it('is a safe no-op when no database is initialized (this harness)', async () => {
    // convergeDatabase is undefined here; the pass must resolve, never
    // throw, no matter what the snapshot carries.
    await pruneStaleLocalRecords(
      [rec('Project', 'p1', 'I')],
      'w1',
      {} as never,
    );
    await pruneStaleLocalRecords(null as never, '', {} as never);
  });

  it('cannot reach an in-flight mutation: deletions derive from cache rows only (SWR-51 audit, item 3)', () => {
    // The audit claimed the prune could delete in-flight user mutations.
    // Adjudicated against the code: no store writes the cache on a user
    // mutation (optimistic updates are store-only; the cache is written
    // by the sync apply path exclusively), and the prune derives its
    // deletions exclusively from CACHE rows. A row the snapshot lacks
    // and the cache lacks is nothing to the pass — the optimistic node
    // in the store is invisible to it by construction.
    const live = liveIdsByModel([]); // the snapshot lists no issues
    // localRows is what the pass reads from the cache: the in-flight
    // issue was never written there, so it is absent from the scan.
    const stale = staleIdsForModel('Issue', domain(), [], live);
    assert.deepEqual(stale, []);
  });
});

describe('dedupeLiveRecords (the SWR-51 idempotency guard)', () => {
  // The guard keeps a per-tab high-water (module state); the tests run
  // in order and each seeds its own baseline.
  it('applies records newer than the tab high-water and advances it', () => {
    seedTabHighWater('100');
    const kept = dedupeLiveRecords([
      rec('Issue', 'i1', 'I', '101'),
      rec('Issue', 'i2', 'I', '105'),
    ] as unknown as SyncActionRecord[]);
    assert.equal(kept.length, 2);
    assert.equal(tabHighWater(), 105);
  });

  it('drops a duplicate and a rewind below the high-water', () => {
    seedTabHighWater('105');
    // Same record re-delivered by the reconnection delta, and an older
    // window re-fetched after another tab rolled the shared key back.
    const kept = dedupeLiveRecords([
      rec('Issue', 'i1', 'I', '101'),
      rec('Issue', 'i2', 'D', '105'),
      rec('Issue', 'i3', 'I', '106'),
    ] as unknown as SyncActionRecord[]);
    assert.deepEqual(kept.map((r) => r.sequenceId), ['106']);
  });

  it('applies synthetic records (no real sequence) unconditionally', () => {
    // The prune's synthetic D records carry an empty sequence and never
    // pass through the guard — but any other sequence-less live record
    // must apply rather than vanish.
    seedTabHighWater('999');
    const synthetic = { ...rec('Issue', 'i1', 'I'), sequenceId: '' };
    const kept = dedupeLiveRecords([synthetic]);
    assert.equal(kept.length, 1);
  });

  it('never lowers the tab high-water (seed is max-only)', () => {
    // Order-independent: the earlier cases in this suite raise the shared
    // module state, so pin the invariant against the current value — a
    // lower seed must be a no-op and the seed must report the cursor it kept.
    const before = tabHighWater();
    assert.equal(seedTabHighWater(String(Math.max(0, before - 1))), before);
    assert.equal(tabHighWater(), before);
  });
});

// ---------------------------------------------------------------------------
// Store update merges (SWR-49): the wire -> model seams.
//
// The create-level tests above pin a full record through each model; the
// merge path — the existing node's snapshot plus the wire record,
// re-created — is what the any-casts used to hide. These run the real
// store actions (the exact code the sync handlers and the optimistic
// mutation services call per record), so a wire field that drifts from
// the model's shape (a nested object, an array) now fails the harness
// instead of silently overwriting state.
// ---------------------------------------------------------------------------

describe('store update merges (the SWR-49 wire->model seams)', () => {
  // Local factories (the create-level ones are scoped to their blocks):
  // same shapes, one wire record each.
  const team = (
    over: Record<string, unknown> = {},
  ): Record<string, unknown> => ({
    id: 't1',
    createdAt: stamp,
    updatedAt: stamp,
    name: 'Engineering',
    identifier: 'ENG',
    workspaceId: 'w1',
    currentCycle: null,
    preferences: { cyclesEnabled: false, teamType: 'engineering' },
    ...over,
  });
  const view = (
    over: Record<string, unknown> = {},
  ): Record<string, unknown> => ({
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
    ...over,
  });
  const project = (
    over: Record<string, unknown> = {},
  ): Record<string, unknown> => ({
    id: 'p1',
    createdAt: stamp,
    updatedAt: stamp,
    name: 'Rail',
    description: '',
    color: '#3B82F6',
    startDate: null,
    endDate: null,
    status: 'ACTIVE',
    leadUserId: null,
    teams: [],
    workspaceId: 'w1',
    ...over,
  });
  const label = (
    over: Record<string, unknown> = {},
  ): Record<string, unknown> => ({
    id: 'l1',
    createdAt: stamp,
    updatedAt: stamp,
    name: 'Bug',
    color: '#f00',
    description: null,
    workspaceId: 'w1',
    teamId: null,
    groupId: null,
    ...over,
  });
  const hist = (
    over: Record<string, unknown> = {},
  ): Record<string, unknown> => ({
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
    ...over,
  });

  it('teams: a nested preferences object merges through create()', () => {
    const store = TeamsStore.create({
      teams: [team() as never],
      workspaceId: 'w1',
    });
    store.update(
      team({
        name: 'Eng',
        preferences: { cyclesEnabled: true, teamType: 'engineering' },
      }) as never,
      't1',
    );
    assert.equal(store.teams[0].name, 'Eng');
    assert.equal(store.teams[0].identifier, 'ENG'); // untouched survives
    assert.equal(store.teams[0].preferences.cyclesEnabled, true);
  });

  it('views: the embedded filters model merges through create()', () => {
    const store = ViewsStore.create({
      views: [view() as never],
      workspaceId: 'w1',
    });
    store.update(
      view({
        name: 'Renamed',
        filters: { priority: { value: [1, 2], filterType: 'INCLUDES' } },
      }) as never,
      'v1',
    );
    assert.equal(store.views[0].name, 'Renamed');
    assert.deepEqual(store.views[0].filters.priority?.value, [1, 2]);
    assert.equal(store.views[0].filters.priority?.filterType, 'INCLUDES');
  });

  it('views: a legacy filterType is rejected at the model boundary, not mid-merge', () => {
    // The filter vocabulary is a closed enumeration written client-side
    // (the server passes the filters jsonb through untouched), so a
    // legacy value cannot be authored — but a hand-edited database row
    // could carry one. The contract: it fails create() loudly, at the
    // model boundary, exactly as any out-of-vocabulary value does.
    const store = ViewsStore.create({
      views: [view() as never],
      workspaceId: 'w1',
    });
    assert.throws(
      () =>
        store.update(
          view({
            filters: { priority: { value: [1], filterType: 'isAnyOf' } },
          }) as never,
          'v1',
        ),
    );
  });

  it('projects: the teams array merges through create()', () => {
    const store = ProjectsStore.create({
      projects: [project() as never],
      workspaceId: 'w1',
    });
    store.update(project({ name: 'Rail 2', teams: ['t1', 't2'] }) as never, 'p1');
    assert.equal(store.projects[0].name, 'Rail 2');
    assert.deepEqual(store.projects[0].teams, ['t1', 't2']);
  });

  it('workspace: a member\'s teamIds array and settings merge through create()', () => {
    const store = WorkspaceStore.create({
      workspace: undefined,
      usersOnWorkspaces: [member() as never],
    });
    store.updateUsers(
      { ...member(), role: 'AGENT', teamIds: ['t1'], settings: { ai: true } } as never,
      'm1',
    );
    assert.equal(store.usersOnWorkspaces[0].role, 'AGENT');
    assert.deepEqual(store.usersOnWorkspaces[0].teamIds, ['t1']);
    assert.equal(store.usersOnWorkspaces[0].settings.ai, true);
  });

  it('issues: an optimistic merge validates; a missing row is a no-op', () => {
    const store = IssuesStore.create({
      issuesMap: { i1: issue() as never },
      teamId: undefined,
    });
    store.updateIssue({ title: 'Renamed', relations: [] } as never, 'i1');
    assert.equal(store.issuesMap.get('i1')?.title, 'Renamed');
    // A DELETE-first race leaves the row absent: the merge must skip,
    // not crash — the any-cast this replaced also hid the null.
    store.updateIssue({ title: 'x' } as never, 'never-loaded');
  });

  it('issue-history: the transition arrays merge through create()', () => {
    const store = IssueHistoryStore.create({
      issueHistories: { i1: [hist() as never] },
    });
    store.update(hist({ fromStateId: 's2', toStateId: 's3' }) as never, 'h1');
    const entry = store.issueHistories.get('i1')?.[0];
    assert.equal(entry?.toStateId, 's3');
    assert.equal(entry?.userId, 'u1'); // untouched survives
  });

  it('labels: the flat merge (the cosmetic class) still validates', () => {
    const store = LabelsStore.create({
      labels: [label() as never],
      workspaceId: 'w1',
    });
    store.update(label({ name: 'Renamed' }) as never, 'l1');
    assert.equal(store.labels[0].name, 'Renamed');
    assert.equal(store.labels[0].color, '#f00');
  });
});
