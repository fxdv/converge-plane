import type { IAnyType, SnapshotIn } from 'mobx-state-tree';

/**
 * Types a plain record (a wire record, a Dexie row, stored JSON) as a
 * model's input snapshot. The hand-written record interfaces are looser
 * than the models: optional where a model wants null, string where it
 * wants an enumeration. MST validates the snapshot when it lands in the
 * tree, and the wire-contract tests pin that server records pass.
 */
export function asSnapshot<T extends IAnyType>(record: object): SnapshotIn<T> {
  return record as SnapshotIn<T>;
}
