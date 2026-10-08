import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  BOARD_DEFAULT_ROW_HEIGHT,
  BOARD_OVERSCAN_ROW_COUNT,
  boardVirtualRowCount,
} from '../board-virtual-config';

/** OD-15 fixture: 10k issues, ~6 workflow columns (see board-profile.mjs). */
const FIXTURE_ISSUE_COUNT = 10_000;
const TYPICAL_COLUMN_COUNT = 6;

describe('board virtualization (OD-15 / Phase 3 CI)', () => {
  it('keeps overscan at 5 rows per column', () => {
    assert.equal(BOARD_OVERSCAN_ROW_COUNT, 5);
    assert.equal(BOARD_DEFAULT_ROW_HEIGHT, 100);
  });

  it('caps mounted rows per column on the 10k fixture', () => {
    const perColumn = Math.ceil(FIXTURE_ISSUE_COUNT / TYPICAL_COLUMN_COUNT);
    const mountedApprox = Math.min(perColumn, BOARD_OVERSCAN_ROW_COUNT * 2 + 1);
    assert.equal(mountedApprox, 11);
    assert.ok(mountedApprox < perColumn);
  });

  it('boardVirtualRowCount adds placeholder row when loading', () => {
    assert.equal(boardVirtualRowCount(10, false), 10);
    assert.equal(boardVirtualRowCount(10, true), 11);
  });
});
