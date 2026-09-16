import { runInAction } from 'mobx';

import type { SyncActionRecord } from 'common/types';

import { convergeDatabase } from 'store/database';
import { saveCommentsData } from 'store/comments';
import { saveIssueHistoryData } from 'store/issue-history';
import { saveIssuesData } from 'store/issues';
import { saveLabelData } from 'store/labels';
import { saveProjectData } from 'store/projects';
import { MODELS } from 'store/models';
import { saveSwarmActivityData } from 'store/swarm-activity';
import { saveTeamData } from 'store/teams';
import { saveViewData } from 'store/views';
import { saveWorkflowData } from 'store/workflows';
import { saveWorkspaceData } from 'store/workspace';

// One save handler per synced model: routes a record batch to that model's
// cache write + store update. Module-level because the bootstrap prune
// reuses the exact same handlers for its synthetic DELETE records — one
// code path for "this row is gone" whether the server said so or the
// snapshot reconciliation found out.
// eslint-disable-next-line @typescript-eslint/ban-types
const SAVE_HANDLERS: Record<string, Function> = {
  [MODELS.Label]: saveLabelData,
  [MODELS.Team]: saveTeamData,
  [MODELS.Workflow]: saveWorkflowData,
  [MODELS.Workspace]: saveWorkspaceData,
  [MODELS.UsersOnWorkspaces]: saveWorkspaceData,
  [MODELS.Issue]: saveIssuesData,
  [MODELS.IssueHistory]: saveIssueHistoryData,
  [MODELS.IssueComment]: saveCommentsData,
  [MODELS.View]: saveViewData,
  [MODELS.Project]: saveProjectData,
  [MODELS.SwarmActivity]: saveSwarmActivityData,
};

// ---------------------------------------------------------------------------
// Per-tab realtime cursor (SWR-51)
//
// The sync watermark in localStorage is shared by every tab of the same
// browser (localStorage is per-origin). Two tabs writing the same key can
// roll it backwards, and a tab that re-seeds from it can skip records its
// own cache never applied — the desync the audit flagged. The shared key
// stays as the cross-tab floor (the bootstrap-skip seed), but each tab
// additionally tracks its own applied high-water in memory: it never
// advances below what this tab has applied, and it never fetches from a
// cursor another tab moved. Writes to the shared key are max-only.
// ---------------------------------------------------------------------------

// The module's one tab-scoped state: the highest outbox sequence this
// tab has applied for its workspace (0 = nothing yet).
let appliedSeq = 0;

// Seeds (or raises, never lowers) the tab's cursor from the shared
// watermark. Called at wrapper mount and after every full bootstrap.
export function seedTabHighWater(stored: string | null): number {
  const n = stored ? Number(stored) : 0;
  if (Number.isFinite(n) && n > appliedSeq) {
    appliedSeq = n;
  }
  return appliedSeq;
}

export function tabHighWater(): number {
  return appliedSeq;
}

// The live-record idempotency guard (SWR-51): an SSE record and the
// reconnection delta can both deliver the same record (the stream drops
// between delivery and the watermark write), and a shared watermark
// rolled back by another tab can re-deliver an old window over newer
// state — re-applying a stale CREATE resurrects a row a newer DELETE
// removed. The server's sequence is monotonic per workspace, so "newer
// than everything this tab applied" is the dedupe condition. Only live
// records (SSE/delta — real outbox sequences) pass through this; the
// bootstrap snapshot carries a local 1..N counter, not the watermark, so
// it applies unconditionally.
export function dedupeLiveRecords(
  data: SyncActionRecord[],
): SyncActionRecord[] {
  if (!Array.isArray(data)) {
    return [];
  }
  const fresh: SyncActionRecord[] = [];
  for (const record of data) {
    const seq = Number(record.sequenceId);
    if (!Number.isFinite(seq) || seq <= 0) {
      fresh.push(record); // synthetic or unknown sequence: apply
      continue;
    }
    if (seq > appliedSeq) {
      appliedSeq = seq;
      fresh.push(record);
    }
    // else: this tab already applied newer state for this workspace;
    // the record is a duplicate or a rewind — drop it.
  }
  return fresh;
}

// Saves the data from the socket and call explicitly functions from individual models
export async function saveSocketData(
  data: SyncActionRecord[],
  // eslint-disable-next-line @typescript-eslint/ban-types
  MODEL_STORE_MAP: Record<string, any>,
): Promise<void> {
  // Defensive: a server may serialize an empty record list as null;
  // never let that take the app down.
  if (!Array.isArray(data)) {
    return;
  }

  await runInAction(async () => {
    // Pre-initialize the accumulator object with known model names
    const groupedRecords: Record<string, SyncActionRecord[]> = Object.values(
      MODELS,
    ).reduce(
      (acc, model) => {
        acc[model] = [];
        return acc;
      },
      {} as Record<string, SyncActionRecord[]>,
    );

    // Use for...of instead of reduce for better performance with large arrays
    for (const record of data) {
      if (groupedRecords[record.modelName]) {
        groupedRecords[record.modelName].push(record);
      }
    }

    // Process records using the handler map. One bad record must not
    // reject the whole batch (both callers await this): a row that no
    // longer matches its model fails that model only — the rest of the
    // batch applies, and the next full bootstrap re-applies the row.
    // The prune pass logs failures the same way.
    return Promise.all(
      Object.entries(groupedRecords)
        .map(([modelName, records]) => {
          if (records.length === 0) {
            return null;
          }
          const handler = SAVE_HANDLERS[modelName];
          if (!handler) {
            return null;
          }
          return handler(records, MODEL_STORE_MAP[modelName]).catch(
            (err: unknown): null => {
              console.warn(`[converge] sync: ${modelName} apply failed`, err);
              return null;
            },
          );
        })
        .filter(Boolean),
    );
  });
}

// Live records only (SSE message, reconnection delta): the idempotency
// guard runs before the same apply path. Bootstrap never uses this — its
// snapshot sequences are a local counter, and the snapshot upsert is the
// full-state reconciliation the prune that follows depends on.
export async function saveLiveSocketData(
  data: SyncActionRecord[],
  // eslint-disable-next-line @typescript-eslint/ban-types
  MODEL_STORE_MAP: Record<string, any>,
): Promise<void> {
  await saveSocketData(dedupeLiveRecords(data), MODEL_STORE_MAP);
}

// ---------------------------------------------------------------------------
// Bootstrap reconciliation (the prune pass)
//
// The bootstrap snapshot is the server's full tenant object set, but the
// client's upsert-only apply can never remove rows: anything deleted
// server-side without the client ever receiving a DELETE record (an
// operator's SQL fix, a dropped stream record, a trimmed outbox window)
// stays in the IndexedDB cache and the MST store forever, resurrecting on
// every load. This pass is the self-heal: after the snapshot is upserted,
// every local row that is inside the snapshot's domain and absent from the
// snapshot is deleted (cache row + store node, via the same D handler a
// real DELETE record routes through).
//
// The domain is what makes this deletion-safe. Bootstrap collectors are
// scoped to one workspace, but the local cache is a UNION across every
// workspace this browser has opened. A row belonging to another workspace
// is legitimate cache, not residue, so the prune only touches rows inside
// the current workspace's domain. Delta and stream never prune (they are
// partial by design); the snapshot is the only authoritative set.
// ---------------------------------------------------------------------------

// The snapshot's domain: what the bootstrap collectors for this workspace
// may return. teamIds/issueIds are the local cache's projection of the
// workspace (live rows plus any stale rows); used as superset-safe filters.
export interface PruneDomain {
  workspaceId: string;
  teamIds: ReadonlySet<string>;
  issueIds: ReadonlySet<string>;
}

// The minimum of a local row the prune reads: the id plus the scoping key
// the model's in-domain test switches on.
export interface PruneRow {
  id: string;
  workspaceId?: string | null;
  teamId?: string | null;
  issueId?: string | null;
}

// Model -> live id set. Every synced model gets an entry; an empty set is
// itself a truth (the server says zero rows), so every in-domain local row
// is stale. D records count as not-live; ids come from modelId (the sync
// key, always present).
export function liveIdsByModel(
  records: SyncActionRecord[],
): Map<string, Set<string>> {
  const live = new Map<string, Set<string>>();
  for (const model of Object.values(MODELS)) {
    live.set(model, new Set<string>());
  }
  if (!Array.isArray(records)) {
    return live;
  }
  for (const record of records) {
    if (record.action === 'D' || !record.modelId) {
      continue;
    }
    let set = live.get(record.modelName);
    if (!set) {
      set = new Set<string>();
      live.set(record.modelName, set);
    }
    set.add(record.modelId);
  }
  return live;
}

// Is this local row inside the snapshot's domain? Rows of other workspaces
// are out of domain (cache, not residue). The Workspace model is never
// pruned: the snapshot knows exactly one workspace (the current one, which
// it re-upserts) while the cache keeps the union the workspace picker
// relies on.
function inDomain(
  modelName: string,
  domain: PruneDomain,
  row: PruneRow,
): boolean {
  switch (modelName) {
    case MODELS.UsersOnWorkspaces:
    case MODELS.Team:
    case MODELS.Label:
    case MODELS.Project:
    case MODELS.View:
      return row.workspaceId === domain.workspaceId;
    case MODELS.Workflow:
    case MODELS.Issue:
      // Scoped to the workspace through their team.
      return row.teamId != null && domain.teamIds.has(row.teamId);
    case MODELS.IssueComment:
    case MODELS.IssueHistory:
      return row.issueId != null && domain.issueIds.has(row.issueId);
    case MODELS.SwarmActivity:
      // In-memory session store: the snapshot replays the runtime's live
      // signal set in full; anything else is a ghost (a crashed worker).
      return true;
    default:
      return false; // Workspace + unknown models: the cache is untouched
  }
}

// The pure deletion decision: which in-domain local rows are stale.
// Exported so the wire-contract harness pins the policy (a wrong row
// deleted here is data loss the user cannot recover).
export function staleIdsForModel(
  modelName: string,
  domain: PruneDomain,
  localRows: ReadonlyArray<PruneRow>,
  live: ReadonlyMap<string, ReadonlySet<string>>,
): string[] {
  const liveSet = live.get(modelName);
  if (!liveSet) {
    return []; // a model the client does not sync: never delete
  }
  const stale: string[] = [];
  for (const row of localRows) {
    if (inDomain(modelName, domain, row) && !liveSet.has(row.id)) {
      stale.push(row.id);
    }
  }
  return stale;
}

// The local rows inside the domain, per model. Read path only — deletion
// goes through the model's D handler.
async function localRowsForModel(
  modelName: string,
  domain: PruneDomain,
  // eslint-disable-next-line @typescript-eslint/ban-types
  MODEL_STORE_MAP: Record<string, any>,
): Promise<PruneRow[]> {
  const db = convergeDatabase;
  switch (modelName) {
    case MODELS.Workspace:
      return [];
    case MODELS.UsersOnWorkspaces:
      return db.usersOnWorkspaces
        .where('workspaceId')
        .equals(domain.workspaceId)
        .toArray();
    case MODELS.Team:
      return db.teams
        .where('workspaceId')
        .equals(domain.workspaceId)
        .toArray();
    case MODELS.Label:
      return db.labels
        .where('workspaceId')
        .equals(domain.workspaceId)
        .toArray();
    case MODELS.Project:
      return db.projects
        .where('workspaceId')
        .equals(domain.workspaceId)
        .toArray();
    case MODELS.View:
      return db.views
        .where('workspaceId')
        .equals(domain.workspaceId)
        .toArray();
    case MODELS.Workflow:
      return domain.teamIds.size
        ? db.workflows.where('teamId').anyOf([...domain.teamIds]).toArray()
        : [];
    case MODELS.Issue:
      return domain.teamIds.size
        ? db.issues.where('teamId').anyOf([...domain.teamIds]).toArray()
        : [];
    case MODELS.IssueComment:
      return domain.issueIds.size
        ? db.comments.where('issueId').anyOf([...domain.issueIds]).toArray()
        : [];
    case MODELS.IssueHistory:
      return domain.issueIds.size
        ? db.issueHistory
            .where('issueId')
            .anyOf([...domain.issueIds])
            .toArray()
        : [];
    case MODELS.SwarmActivity:
      // In-memory only (no cache table): the whole store is the local set.
      return Array.from(
        MODEL_STORE_MAP[MODELS.SwarmActivity]?.activities.values() ?? [],
      );
    default:
      return [];
  }
}

// Reconciles the local cache + stores against a bootstrap snapshot. Called
// from the bootstrap handler only (never delta/stream). Self-contained: it
// never throws — a failed pass simply leaves residue for the next full
// bootstrap, while the UI always works from what the snapshot upserted.
export async function pruneStaleLocalRecords(
  snapshot: SyncActionRecord[],
  workspaceId: string,
  // eslint-disable-next-line @typescript-eslint/ban-types
  MODEL_STORE_MAP: Record<string, any>,
): Promise<void> {
  if (!convergeDatabase || !workspaceId || !Array.isArray(snapshot)) {
    return;
  }

  try {
    const live = liveIdsByModel(snapshot);

    // Read phase: the domain, computed before any row is removed (the
    // issue/comment/workflow domains derive from the team and issue rows).
    const teams = await convergeDatabase.teams
      .where('workspaceId')
      .equals(workspaceId)
      .toArray();
    const teamIds = new Set(teams.map((t) => t.id));
    const issues = teamIds.size
      ? await convergeDatabase.issues
          .where('teamId')
          .anyOf([...teamIds])
          .toArray()
      : [];
    const issueIds = new Set(issues.map((i) => i.id));
    const domain: PruneDomain = { workspaceId, teamIds, issueIds };

    // Decision phase.
    const results = await Promise.all(
      Object.values(MODELS).map(async (model) => {
        const rows = await localRowsForModel(model, domain, MODEL_STORE_MAP);
        return [model, staleIdsForModel(model, domain, rows, live)] as const;
      }),
    );

    const stale: SyncActionRecord[] = [];
    for (const [model, ids] of results) {
      for (const id of ids) {
        stale.push({
          // A DELETE carries only the id (sync contract); each model's D
          // handler resolves the removal by id.
          data: { id },
          modelName: model,
          modelId: id,
          action: 'D',
          workspaceId,
          sequenceId: '',
        });
      }
    }
    if (stale.length === 0) {
      return;
    }

    // Write phase: the same handlers a real DELETE record routes through
    // (cache row + store node per model). One model failing must not
    // cancel the others.
    const grouped = new Map<string, SyncActionRecord[]>();
    for (const record of stale) {
      const batch = grouped.get(record.modelName);
      if (batch) {
        batch.push(record);
      } else {
        grouped.set(record.modelName, [record]);
      }
    }
    await Promise.all(
      [...grouped.entries()].map(([modelName, records]) => {
        const handler = SAVE_HANDLERS[modelName];
        if (!handler) {
          return null;
        }
        return handler(records, MODEL_STORE_MAP[modelName]).catch((
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          err: any,
        ) => {
          console.warn(`[converge] prune: ${modelName} delete failed`, err);
        });
      }),
    );
  } catch (err) {
    console.warn('[converge] bootstrap prune skipped', err);
  }
}
