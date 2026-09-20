// The project rail's pure logic (spec cs:ui:projects-rail): the
// focus-lens card classification and the rail visibility rule. Pure by
// construction (no React import) so the wire-contract harness — which
// compiles .ts files only — can pin it.

import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { projectFocusKind, railStackVisible } from '../project-focus';

describe('projectFocusKind', () => {
  it('reports none when no project is focused, whatever the card holds', () => {
    assert.equal(projectFocusKind([], null), 'none');
    assert.equal(projectFocusKind(undefined, null), 'none');
    assert.equal(projectFocusKind(null, null), 'none');
    assert.equal(projectFocusKind(['p1'], undefined), 'none');
    assert.equal(projectFocusKind(['p1', 'p2'], null), 'none');
  });

  it('reports match for a card of the focused project', () => {
    assert.equal(projectFocusKind(['p1'], 'p1'), 'match');
    // v2's multi-project shape: membership is an includes-lookup.
    assert.equal(projectFocusKind(['p1', 'p2'], 'p2'), 'match');
  });

  it('reports dim for every card that is not of the focused project', () => {
    assert.equal(projectFocusKind(['p2'], 'p1'), 'dim');
    assert.equal(projectFocusKind([], 'p1'), 'dim');
    assert.equal(projectFocusKind(undefined, 'p1'), 'dim');
    assert.equal(projectFocusKind(null, 'p1'), 'dim');
  });
});

describe('railStackVisible', () => {
  it('shows an empty project at once (it must appear to be filled)', () => {
    assert.equal(railStackVisible(0, 0), true);
  });

  it('shows a project whose view keeps at least one card', () => {
    assert.equal(railStackVisible(3, 3), true);
    assert.equal(railStackVisible(1, 7), true);
  });

  it('hides a project only when the view filters out every card', () => {
    assert.equal(railStackVisible(0, 5), false);
  });
});
