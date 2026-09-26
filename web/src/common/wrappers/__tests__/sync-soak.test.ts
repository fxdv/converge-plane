// Sync soak (Phase 1 exit criterion: injected drops lose nothing). A
// seeded simulation drives the real stream cursor (acceptStreamRecord,
// acceptStreamHead, acceptDeltaBatch) against a model of the server:
// gap-free sequences, skip markers for other members' inbox rows, a delta
// complete up to the watermark it reports, and a heartbeat carrying the
// committed head. Meanwhile it injects what realtime delivery can suffer:
// lost publishes, duplicates, reordering, disconnects, and slow or failed
// deltas whose snapshot predates stream records applied before them. The
// client side mirrors socket-data-sync.tsx: the gap grace timer, the
// single-flight reconcile with one queued rerun, and reconcile on
// reconnect.
//
// The property: once the faults stop, the tab has applied every visible
// record exactly once, in sequence order, and never a skip marker.
//
// Cursor state is module state shared by the file (one process): every
// run seeds a baseline above everything before it.

import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import type { SyncActionRecord } from 'common/types';
import {
  acceptDeltaBatch,
  acceptStreamHead,
  acceptStreamRecord,
  hasSequenceGap,
  seedTabHighWater,
  tabHighWater,
} from 'common/wrappers/socket-data-util';

// Ticks of 100ms: the client's 1.5s gap grace, the server's 15s heartbeat,
// the stream's `retry: 2000`, and deltas taking up to 3s.
const GRACE = 15;
const HEARTBEAT = 150;
const RECONNECT = 20;
const DELTA_LATENCY = 30;

// mulberry32: small and seedable, so a failing seed replays exactly.
function prng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface Options {
  steps: number; // ticks with commits and faults; then the system settles
  pCommit: number;
  pHidden: number; // another member's notification: a skip marker here
  pLost: number; // a publish that never reaches this tab
  pDuplicate: number;
  pDisconnect: number;
  pDeltaFail: number;
  window: number; // delivery picks among the oldest `window` in flight
  heartbeat: boolean;
}

interface Faults {
  lost: number;
  duplicated: number;
  reordered: number;
  disconnects: number;
  deltas: number;
  deltaFailures: number;
  staleSnapshots: number; // deltas applied after newer stream records
}

interface Outcome {
  applied: number[];
  visible: number[];
  appliedSkip: boolean;
  head: number;
}

const FAULTY: Options = {
  steps: 1500,
  pCommit: 0.3,
  pHidden: 0.15,
  pLost: 0.05,
  pDuplicate: 0.05,
  pDisconnect: 0.004,
  pDeltaFail: 0.15,
  window: 4,
  heartbeat: true,
};

function newFaults(): Faults {
  return {
    lost: 0,
    duplicated: 0,
    reordered: 0,
    disconnects: 0,
    deltas: 0,
    deltaFailures: 0,
    staleSnapshots: 0,
  };
}

function soak(seed: number, base: number, o: Options, f: Faults): Outcome {
  const rand = prng(seed);
  seedTabHighWater(String(base));

  // Server: the committed log and what each view of it returns.
  let head = base;
  const hidden = new Set<number>();
  const record = (seq: number): SyncActionRecord => ({
    data: { seq },
    modelName: 'Issue',
    modelId: `issue-${seq}`,
    action: 'U',
    workspaceId: 'ws',
    sequenceId: String(seq),
  });
  const streamed = (seq: number): SyncActionRecord =>
    hidden.has(seq)
      ? ({ sequenceId: String(seq), skip: true } as SyncActionRecord)
      : record(seq);
  const delta = (after: number) => {
    const syncActions: SyncActionRecord[] = [];
    for (let s = after + 1; s <= head; s++) {
      if (!hidden.has(s)) {
        syncActions.push(record(s));
      }
    }
    return { syncActions, lastSequenceId: String(head) };
  };

  // Client: the stream connection and socket-data-sync.tsx's control flow.
  const applied: number[] = [];
  let appliedSkip = false;
  const apply = (ready: SyncActionRecord[]) => {
    for (const r of ready) {
      appliedSkip ||= r.skip === true;
      applied.push(Number(r.sequenceId));
    }
  };

  let connected = true;
  let reconnectAt = 0;
  let inFlight: SyncActionRecord[] = [];
  let gapTimerAt = -1;
  let reconciling = false;
  let rerun = false;
  let deltaDueAt = 0;
  let deltaResp: ReturnType<typeof delta> | null = null;
  let deltaHead = 0;

  const scheduleGapRepair = (now: number) => {
    if (gapTimerAt < 0) {
      gapTimerAt = now + GRACE;
    }
  };
  const reconcile = (now: number, faulting: boolean) => {
    if (reconciling) {
      rerun = true;
      return;
    }
    reconciling = true;
    f.deltas++;
    deltaResp =
      faulting && rand() < o.pDeltaFail ? null : delta(tabHighWater());
    deltaHead = head;
    deltaDueAt = now + Math.floor(rand() * DELTA_LATENCY);
  };
  const finishDelta = (now: number, faulting: boolean) => {
    if (deltaResp) {
      if (tabHighWater() > deltaHead) {
        f.staleSnapshots++;
      }
      apply(acceptDeltaBatch(deltaResp.syncActions, deltaResp.lastSequenceId));
    } else {
      f.deltaFailures++;
    }
    reconciling = false;
    deltaResp = null;
    if (rerun) {
      rerun = false;
      reconcile(now, faulting);
    } else if (hasSequenceGap()) {
      scheduleGapRepair(now);
    }
  };
  const onMessage = (rec: SyncActionRecord, now: number) => {
    const { ready, gap } = acceptStreamRecord(rec);
    apply(ready);
    if (gap) {
      scheduleGapRepair(now);
    } else {
      gapTimerAt = -1;
    }
  };

  // Settled: nothing in flight or pending, and a heartbeat has had its say
  // since the last commit.
  const settled = (now: number) =>
    connected &&
    inFlight.length === 0 &&
    !reconciling &&
    gapTimerAt < 0 &&
    now > o.steps + HEARTBEAT;

  for (let now = 1; now <= o.steps + 20 * HEARTBEAT; now++) {
    const faulting = now <= o.steps;
    if (!faulting && settled(now)) {
      break;
    }

    if (faulting && rand() < o.pCommit) {
      head++;
      if (rand() < o.pHidden) {
        hidden.add(head);
      }
      if (!connected || rand() < o.pLost) {
        f.lost++;
      } else {
        inFlight.push(streamed(head));
      }
    }

    if (faulting && connected && rand() < o.pDisconnect) {
      connected = false;
      f.lost += inFlight.length;
      inFlight = [];
      reconnectAt = now + RECONNECT;
      f.disconnects++;
    } else if (!connected && now >= reconnectAt) {
      // The server subscribes before the client sees the stream open, so
      // the reconnect delta's snapshot postdates the subscription.
      connected = true;
      reconcile(now, faulting);
    }

    if (connected && inFlight.length > 0 && rand() < 0.6) {
      const i = Math.floor(rand() * Math.min(o.window, inFlight.length));
      if (i > 0) {
        f.reordered++;
      }
      const [rec] = inFlight.splice(i, 1);
      if (faulting && rand() < o.pDuplicate) {
        inFlight.splice(Math.floor(rand() * (inFlight.length + 1)), 0, rec);
        f.duplicated++;
      }
      onMessage(rec, now);
    }

    if (reconciling && now >= deltaDueAt) {
      finishDelta(now, faulting);
    }
    if (gapTimerAt >= 0 && now >= gapTimerAt) {
      gapTimerAt = -1;
      reconcile(now, faulting);
    }
    if (o.heartbeat && connected && now % HEARTBEAT === 0) {
      if (acceptStreamHead(String(head))) {
        scheduleGapRepair(now);
      }
    }
  }

  const visible: number[] = [];
  for (let s = base + 1; s <= head; s++) {
    if (!hidden.has(s)) {
      visible.push(s);
    }
  }
  return { applied, visible, appliedSkip, head };
}

// Each run's sequences start above every earlier run's, in any test.
let nextBase = 1_000_000;
function takeBase(): number {
  nextBase += 1_000_000;
  return nextBase;
}

describe('sync soak: injected faults lose nothing', () => {
  it('applies every visible record exactly once, in order, across 300 seeded runs', () => {
    const faults = newFaults();
    for (let seed = 1; seed <= 300; seed++) {
      const out = soak(seed, takeBase(), FAULTY, faults);
      const where = `seed ${seed}`;

      for (let i = 1; i < out.applied.length; i++) {
        assert.ok(
          out.applied[i] > out.applied[i - 1],
          `${where}: applied ${out.applied[i]} after ${out.applied[i - 1]}`,
        );
      }
      assert.deepEqual(out.applied, out.visible, `${where}: applied set`);
      assert.equal(out.appliedSkip, false, `${where}: applied a skip marker`);
      assert.equal(tabHighWater(), out.head, `${where}: cursor short of head`);
      assert.equal(hasSequenceGap(), false, `${where}: gap left open`);
    }

    // The run must actually have exercised every fault it claims to survive.
    for (const [name, count] of Object.entries(faults)) {
      assert.ok(count > 0, `fault never injected: ${name}`);
    }
  });

  it('without the heartbeat, a lost final record goes unnoticed', () => {
    // The control: the same faults with the head heartbeat off leave some
    // runs short of head, so the property above rests on the heartbeat
    // and not on luck.
    const faults = newFaults();
    let short = 0;
    for (let seed = 1; seed <= 300; seed++) {
      const out = soak(
        seed,
        takeBase(),
        { ...FAULTY, heartbeat: false },
        faults,
      );
      if (tabHighWater() < out.head) {
        short++;
      }
    }
    assert.ok(short > 0, 'expected trailing losses without the heartbeat');
  });

  it('recovers a lone lost record from the next heartbeat', () => {
    const base = takeBase();
    seedTabHighWater(String(base));
    // Record base+1 was committed and its publish lost: no later record
    // arrives to reveal the hole, so only the head can.
    assert.equal(hasSequenceGap(), false);
    assert.equal(acceptStreamHead(String(base + 1)), true);
    assert.equal(hasSequenceGap(), true);

    const repaired = acceptDeltaBatch(
      [
        {
          data: {},
          modelName: 'Issue',
          modelId: 'issue-lone',
          action: 'U',
          workspaceId: 'ws',
          sequenceId: String(base + 1),
        },
      ],
      String(base + 1),
    );
    assert.deepEqual(
      repaired.map((r) => r.sequenceId),
      [String(base + 1)],
    );
    assert.equal(hasSequenceGap(), false);
  });

  it('ignores a malformed or older head', () => {
    const base = takeBase();
    seedTabHighWater(String(base));
    assert.equal(acceptStreamHead(undefined), false);
    assert.equal(acceptStreamHead('not-a-number'), false);
    assert.equal(acceptStreamHead(String(base - 5)), false);
    assert.equal(acceptStreamHead(String(base)), false);
  });
});
