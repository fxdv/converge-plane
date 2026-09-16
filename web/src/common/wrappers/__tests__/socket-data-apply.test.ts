// Sync apply pipeline (saveSocketData / saveLiveSocketData) — the client's
// single write path for every synced model. The wire->model seams are
// pinned in wire-contract.test.ts; this file pins the PIPELINE around them:
// batch grouping, handler routing for all synced models, the per-model
// failure boundary, and the dedupe guard composed with the apply (SWR-51).
//
// Harness note: node has no IndexedDB, so the tests initialize the real
// database object (the constructor is inert until a transaction opens) and
// stub the table put/delete methods the save handlers call — the handlers
// read the tables off the module's convergeDatabase binding at call time,
// so the stubs stand in for the object-cache writes without a browser.
//
// The tab high-water is module state shared by every test in this file
// (one process): each test seeds its own baseline and the ranges ascend in
// declaration order, the same pattern as the dedupe tests in
// wire-contract.test.ts.

import { before, describe, it } from 'node:test';
import assert from 'node:assert/strict';

import type { SyncActionRecord } from 'common/types';

import * as database from 'store/database';
import { MODELS } from 'store/models';

import { CommentsStore } from 'store/comments/store';
import { IssueHistoryStore } from 'store/issue-history/store';
import { IssuesStore } from 'store/issues/store';
import { LabelsStore } from 'store/labels/store';
import { ProjectsStore } from 'store/projects/store';
import { SwarmActivityStore } from 'store/swarm-activity/store';
import { TeamsStore } from 'store/teams/store';
import { ViewsStore } from 'store/views/store';
import { WorkflowsStore } from 'store/workflows/store';
import { WorkspaceStore } from 'store/workspace/store';

import {
  saveLiveSocketData,
  saveSocketData,
  seedTabHighWater,
} from 'common/wrappers/socket-data-util';

const stamp = '2026-09-02T12:00:00.000Z';

// --- Wire payloads: the full contract shape per model --------------------
// The same shapes wire-contract.test.ts pins at the model boundary.
const workspace = (
  over: Record<string, unknown> = {},
): Record<string, unknown> => ({
  id: 'w1',
  createdAt: stamp,
  updatedAt: stamp,
  name: 'Acme',
  slug: 'acme',
  preferences: null,
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
const workflow = (
  over: Record<string, unknown> = {},
): Record<string, unknown> => ({
  id: 's1',
  createdAt: stamp,
  updatedAt: stamp,
  name: 'In Progress',
  description: null,
  position: 2,
  color: '#4484d5',
  category: 'STARTED',
  teamId: 't1',
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
const history = (
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
const comment = (
  over: Record<string, unknown> = {},
): Record<string, unknown> => ({
  id: 'c1',
  createdAt: stamp,
  updatedAt: stamp,
  userId: 'u1',
  issueId: 'i1',
  body: 'hi',
  parentId: null,
  sourceMetadata: null,
  ...over,
});
const signal = (
  over: Record<string, unknown> = {},
): Record<string, unknown> => ({
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

const rec = (
  modelName: string,
  id: string,
  action: 'I' | 'U' | 'D',
  data: Record<string, unknown>,
  sequenceId = '',
): SyncActionRecord =>
  ({
    data: { ...data, id },
    modelName,
    modelId: id,
    action,
    workspaceId: 'w1',
    sequenceId,
  }) as SyncActionRecord;

// --- The object-cache stand-in -------------------------------------------
// Dexie table property names (the constructor binds camelCase properties
// to the MODELS-named tables); SwarmActivity is in-memory, no table.
const TABLE_PROPS: Record<string, string> = {
  [MODELS.Workspace]: 'workspaces',
  [MODELS.Label]: 'labels',
  [MODELS.Team]: 'teams',
  [MODELS.Workflow]: 'workflows',
  [MODELS.Issue]: 'issues',
  [MODELS.UsersOnWorkspaces]: 'usersOnWorkspaces',
  [MODELS.IssueHistory]: 'issueHistory',
  [MODELS.IssueComment]: 'comments',
  [MODELS.View]: 'views',
  [MODELS.Project]: 'projects',
};

interface TableStub {
  puts: Record<string, unknown>[];
  dels: string[];
}
const tableStubs = new Map<string, TableStub>();

before(() => {
  database.initDatabase(0);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const db = database.convergeDatabase as unknown as Record<string, any>;
  for (const model of Object.values(MODELS)) {
    const prop = TABLE_PROPS[model];
    if (!prop) {
      continue; // in-memory model: nothing to stub
    }
    const stub: TableStub = { puts: [], dels: [] };
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const table: any = db[prop];
    table.put = (value: Record<string, unknown>) => {
      stub.puts.push(value);
      return Promise.resolve(value.id);
    };
    table.delete = (key: unknown) => {
      stub.dels.push(String(key));
      return Promise.resolve();
    };
    tableStubs.set(model, stub);
  }
});

const allWrites = (): { puts: number; dels: number } => {
  let puts = 0;
  let dels = 0;
  for (const stub of tableStubs.values()) {
    puts += stub.puts.length;
    dels += stub.dels.length;
  }
  return { puts, dels };
};

// The app's MODEL_STORE_MAP (socket-data-sync.tsx / bootstrap-data.tsx):
// UsersOnWorkspaces rides the workspace store, as in production. The
// map is keyed exactly as the apply path receives it (model name ->
// store instance), so the tests exercise the real routing.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function storeMap(): Record<string, any> {
  const workspaceStore = WorkspaceStore.create({
    workspace: undefined,
    usersOnWorkspaces: [],
  } as never);
  return {
    [MODELS.Workspace]: workspaceStore,
    [MODELS.UsersOnWorkspaces]: workspaceStore,
    [MODELS.Team]: TeamsStore.create({ teams: [], workspaceId: 'w1' } as never),
    [MODELS.Label]: LabelsStore.create({ labels: [], workspaceId: 'w1' } as never),
    [MODELS.Project]: ProjectsStore.create({ projects: [], workspaceId: 'w1' } as never),
    [MODELS.Workflow]: WorkflowsStore.create({
      workflows: {},
      workflowsByTeamId: {},
    } as never),
    [MODELS.Issue]: IssuesStore.create({
      issuesMap: {},
      teamId: undefined,
    } as never),
    [MODELS.IssueHistory]: IssueHistoryStore.create({
      issueHistories: {},
    } as never),
    [MODELS.IssueComment]: CommentsStore.create({ comments: {} } as never),
    [MODELS.View]: ViewsStore.create({ views: [], workspaceId: 'w1' } as never),
    [MODELS.SwarmActivity]: SwarmActivityStore.create({ activities: {} } as never),
  };
}

describe('saveSocketData (the sync apply pipeline)', () => {
  it('resolves without writing on a null or non-array payload', async () => {
    const map = storeMap();
    await saveSocketData(null as never, map);
    await saveSocketData(undefined as never, map);
    assert.deepEqual(allWrites(), { puts: 0, dels: 0 });
  });

  it('ignores records for models the client does not sync (forked residue)', async () => {
    const map = storeMap();
    await saveSocketData(
      [
        rec('Integration', 'x1', 'I', { id: 'x1', workspaceId: 'w1' }, '1'),
        rec('Notification', 'x2', 'U', { id: 'x2', workspaceId: 'w1' }, '2'),
      ],
      map,
    );
    assert.deepEqual(allWrites(), { puts: 0, dels: 0 });
  });

  it('routes every synced model to its store and its cache table', async () => {
    const map = storeMap();
    await saveSocketData(
      [
        rec(MODELS.Workspace, 'w1', 'I', workspace(), '1'),
        rec(MODELS.UsersOnWorkspaces, 'm1', 'I', member(), '2'),
        rec(MODELS.Team, 't1', 'I', team(), '3'),
        rec(MODELS.Label, 'l1', 'I', label(), '4'),
        rec(MODELS.Project, 'p1', 'I', project(), '5'),
        rec(MODELS.Workflow, 's1', 'I', workflow(), '6'),
        rec(MODELS.Issue, 'i1', 'I', issue(), '7'),
        rec(MODELS.IssueHistory, 'h1', 'I', history(), '8'),
        rec(MODELS.IssueComment, 'c1', 'I', comment(), '9'),
        rec(MODELS.View, 'v1', 'I', view(), '10'),
        rec(MODELS.SwarmActivity, 'a1', 'I', signal(), '11'),
      ],
      map,
    );

    const ws = map[MODELS.Workspace];
    assert.equal(ws.workspace?.slug, 'acme');
    assert.equal(ws.usersOnWorkspaces.length, 1); // membership rides the same store
    assert.equal(map[MODELS.Team].teams[0].identifier, 'ENG');
    assert.equal(map[MODELS.Label].labels[0].name, 'Bug');
    assert.equal(map[MODELS.Project].projects[0].name, 'Rail');
    assert.equal(map[MODELS.Workflow].workflows.get('s1')?.name, 'In Progress');
    // Materialize to a plain array before comparing: a failing assertion
    // would otherwise inspect the mobx Proxy (workflowsByTeamId's value is
    // an ObservableArray), which the node harness turns into an unbounded
    // allocation loop — and a loose == on two object refs could never pass.
    assert.deepEqual(
      [...(map[MODELS.Workflow].workflowsByTeamId.get('t1') ?? [])],
      ['s1'],
    );
    assert.equal(map[MODELS.Issue].issuesMap.get('i1')?.title, 'T');
    assert.equal(map[MODELS.IssueHistory].issueHistories.get('i1')?.length, 1);
    assert.equal(map[MODELS.IssueComment].comments.get('i1')?.length, 1);
    assert.equal(map[MODELS.View].views[0].name, 'My view');
    assert.equal(map[MODELS.SwarmActivity].activities.get('a1')?.phase, 'deciding');

    // Every table-backed model got its cache row; swarm is in-memory.
    for (const model of Object.keys(TABLE_PROPS)) {
      assert.equal(
        tableStubs.get(model)!.puts.length,
        1,
        `expected one cache put for ${model}`,
      );
    }
    assert.equal(tableStubs.get(MODELS.SwarmActivity), undefined);
  });

  it('D records remove the cache row and the store node by id', async () => {
    const map = storeMap();
    // Live rows first (the handlers only remove by id on D).
    await saveSocketData(
      [
        rec(MODELS.Label, 'l1', 'I', label(), '1'),
        rec(MODELS.Issue, 'i1', 'I', issue(), '2'),
        rec(MODELS.UsersOnWorkspaces, 'm1', 'I', member(), '3'),
        rec(MODELS.IssueHistory, 'h1', 'I', history(), '4'),
        rec(MODELS.IssueComment, 'c1', 'I', comment(), '5'),
      ],
      map,
    );
    // The sync contract: a DELETE carries only the id.
    await saveSocketData(
      [
        rec(MODELS.Label, 'l1', 'D', { id: 'l1' }, '6'),
        rec(MODELS.Issue, 'i1', 'D', { id: 'i1' }, '7'),
        rec(MODELS.UsersOnWorkspaces, 'm1', 'D', { id: 'm1' }, '8'),
        rec(MODELS.IssueHistory, 'h1', 'D', { id: 'h1' }, '9'),
        rec(MODELS.IssueComment, 'c1', 'D', { id: 'c1' }, '10'),
      ],
      map,
    );

    assert.equal(map[MODELS.Label].labels.length, 0);
    assert.equal(map[MODELS.Issue].issuesMap.size, 0);
    assert.equal(map[MODELS.Workspace].usersOnWorkspaces.length, 0);
    // The history store removes the issue key once its array is empty
    // (same contract as comments): the key's absence is the deletion.
    // .has() keeps a primitive on both sides — a failing assertion must
    // never be left to inspect the mobx Proxy.
    assert.equal(map[MODELS.IssueHistory].issueHistories.has('i1'), false);
    assert.equal(map[MODELS.IssueComment].comments.size, 0);
    assert.deepEqual(tableStubs.get(MODELS.Label)!.dels, ['l1']);
    assert.deepEqual(tableStubs.get(MODELS.Issue)!.dels, ['i1']);
    assert.deepEqual(tableStubs.get(MODELS.UsersOnWorkspaces)!.dels, ['m1']);
    assert.deepEqual(tableStubs.get(MODELS.IssueHistory)!.dels, ['h1']);
    assert.deepEqual(tableStubs.get(MODELS.IssueComment)!.dels, ['c1']);
  });

  it('a degraded record fails its own model only (the batch survives)', async () => {
    // The wire drifted: the label row no longer matches its model. The
    // apply must not reject the whole batch — the other models still
    // apply, and the warning lands where the prune pass warns.
    const map = storeMap();
    const warnings: unknown[][] = [];
    const originalWarn = console.warn;
    console.warn = (...args: unknown[]) => {
      warnings.push(args);
    };
    try {
      await assert.doesNotReject(
        saveSocketData(
          [
            // name/color missing: create() validation fails inside the
            // label handler (the degraded-payload crash class).
            rec(MODELS.Label, 'l9', 'I', { id: 'l9' } as never, '1'),
            rec(MODELS.Team, 't1', 'I', team(), '2'),
          ],
          map,
        ),
      );
    } finally {
      console.warn = originalWarn;
    }
    assert.equal(map[MODELS.Team].teams.length, 1);
    assert.equal(map[MODELS.Label].labels.length, 0);
    assert.ok(
      warnings.some((args) =>
        String(args[0]).includes('Label apply failed'),
      ),
      'the failed model must be reported',
    );
  });

  it('U records merge through the store on the apply path (SWR-49)', async () => {
    const map = storeMap();
    await saveSocketData([rec(MODELS.Team, 't1', 'I', team(), '1')], map);
    await saveSocketData(
      [
        rec(
          MODELS.Team,
          't1',
          'U',
          team({ name: 'Eng', preferences: { cyclesEnabled: true, teamType: 'engineering' } }),
          '2',
        ),
      ],
      map,
    );
    const t = map[MODELS.Team].teams[0];
    assert.equal(t.name, 'Eng');
    assert.equal(t.identifier, 'ENG'); // untouched survives the merge
    assert.equal(t.preferences.cyclesEnabled, true);
  });
});

describe('saveLiveSocketData (the dedupe guard composed with the apply)', () => {
  // Cursor ranges ascend in declaration order (shared module state).
  it('a re-delivered stale CREATE cannot resurrect a deleted row', async () => {
    seedTabHighWater('1000');
    const map = storeMap();
    await saveLiveSocketData([rec(MODELS.Issue, 'i1', 'I', issue(), '1001')], map);
    assert.equal(map[MODELS.Issue].issuesMap.size, 1);
    await saveLiveSocketData(
      [rec(MODELS.Issue, 'i1', 'D', { id: 'i1' }, '1002')],
      map,
    );
    assert.equal(map[MODELS.Issue].issuesMap.size, 0);
    // The stream drops between delivery and the watermark write, then
    // re-delivers the old CREATE over the newer DELETE: it must be deduped
    // before the apply, never reaching the handler.
    const putsBefore = tableStubs.get(MODELS.Issue)!.puts.length;
    await saveLiveSocketData([rec(MODELS.Issue, 'i1', 'I', issue(), '1001')], map);
    assert.equal(map[MODELS.Issue].issuesMap.size, 0); // no resurrection
    assert.equal(tableStubs.get(MODELS.Issue)!.puts.length, putsBefore);
  });

  it('applies a fresh batch above the high-water and advances it', async () => {
    seedTabHighWater('2000');
    const map = storeMap();
    await saveLiveSocketData(
      [
        rec(MODELS.Issue, 'i2', 'I', issue({ id: 'i2', title: 'B' }), '2001'),
        rec(MODELS.Label, 'l2', 'I', label({ id: 'l2', name: 'Perf' }), '2002'),
        rec(MODELS.Issue, 'i3', 'I', issue({ id: 'i3', title: 'C' }), '2003'),
      ],
      map,
    );
    assert.deepEqual(
      [...map[MODELS.Issue].issuesMap.keys()].sort(),
      ['i2', 'i3'],
    );
    assert.equal(map[MODELS.Label].labels[0].name, 'Perf');
  });

  it('synthetic records (an empty sequence) apply unconditionally', async () => {
    seedTabHighWater('3000');
    const map = storeMap();
    await saveLiveSocketData(
      [rec(MODELS.Issue, 'i4', 'I', issue({ id: 'i4', title: 'D' }), '')],
      map,
    );
    assert.equal(map[MODELS.Issue].issuesMap.get('i4')?.title, 'D');
  });
});
