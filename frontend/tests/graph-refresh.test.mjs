// Unit tests for the Learning knowledge graph refresh helpers
// (frontend/js/views/learning.js). These pin the bounded-layout contract:
// refresh uses network.stabilize() (which always completes and lets the
// freeze handler apply) and keeps existing node positions so the graph
// stays still while idle.
import assert from 'node:assert/strict';
import { test } from 'node:test';

import { GRAPH_LAYOUT_ITERATIONS, relayoutGraph, keepGraphPositions } from '../js/views/learning.js';

test('relayoutGraph drives a bounded stabilize and never touches setOptions physics', () => {
  const calls = [];
  const network = {
    stabilize: (iterations) => calls.push(['stabilize', iterations]),
    setOptions: (opts) => calls.push(['setOptions', opts]),
  };
  relayoutGraph(network);
  assert.deepEqual(calls, [['stabilize', GRAPH_LAYOUT_ITERATIONS]]);
  assert.equal(GRAPH_LAYOUT_ITERATIONS, 80, 'refresh layout must be bounded so the freeze handler applies');
});

test('relayoutGraph tolerates a missing network (headless/test environments)', () => {
  relayoutGraph(null);
  relayoutGraph(undefined);
});

test('keepGraphPositions keeps existing positions pinned and leaves new nodes positionless', () => {
  const prev = { a: { x: 12, y: -34 } };
  const out = keepGraphPositions([{ id: 'a' }, { id: 'b' }], prev);
  assert.deepEqual(out, [
    { id: 'a', x: 12, y: -34, fixed: { x: true, y: true } },
    { id: 'b' },
  ]);
});

test('keepGraphPositions preserves extra node fields when merging positions', () => {
  const prev = { a: { x: 1, y: 2 } };
  const out = keepGraphPositions([{ id: 'a', label: 'A', group: 'memory', size: 14 }], prev);
  assert.deepEqual(out, [{ id: 'a', label: 'A', group: 'memory', size: 14, x: 1, y: 2, fixed: { x: true, y: true } }]);
});
