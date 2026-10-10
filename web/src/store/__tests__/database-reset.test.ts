// resetDatabase clears the sync watermark the wrappers actually write
// (lastSequenceId_<hash>, the hash the database name carries) — never a
// bare 'lastSequenceId' key nothing reads. Node has no IndexedDB, so the
// instance's delete is stubbed (the constructor is inert until a
// transaction opens) and localStorage is a Map-backed stand-in.

import assert from 'node:assert/strict';
import { before, describe, it } from 'node:test';

import * as database from 'store/database';

const store = new Map<string, string>();

before(() => {
  (globalThis as unknown as { localStorage: Storage }).localStorage = {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, String(v)),
    removeItem: (k: string) => void store.delete(k),
    clear: () => store.clear(),
    key: (i: number) => [...store.keys()][i] ?? null,
    get length() {
      return store.size;
    },
  } as Storage;
});

describe('resetDatabase', () => {
  it('removes the hashed watermark of the open cache and deletes it', async () => {
    database.initDatabase(42);
    let deleted = 0;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (database.convergeDatabase as any).delete = () => {
      deleted += 1;
      return Promise.resolve();
    };
    store.set(database.lastSequenceIdKey(42), '17');
    store.set(database.lastSequenceIdKey(7), '99'); // another workspace's cache

    await database.resetDatabase();

    assert.equal(store.has('lastSequenceId_42'), false);
    assert.equal(store.get('lastSequenceId_7'), '99');
    assert.equal(deleted, 1);
  });

  it('keys the watermark by the database hash', () => {
    assert.equal(database.lastSequenceIdKey(123), 'lastSequenceId_123');
  });
});
