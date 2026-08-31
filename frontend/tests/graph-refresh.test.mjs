// Unit tests for the Learning knowledge graph refresh helpers
// (frontend/js/views/learning.js). These pin the bounded-layout contract:
// refresh uses network.stabilize() (which always completes and lets the
// freeze handler apply) and keeps existing node positions so the graph
// stays still while idle.
import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  GRAPH_LAYOUT_ITERATIONS,
  GRAPH_NODE_GAP,
  spaceGraphPositions,
  freezeGraphLayout,
  relayoutGraph,
  keepGraphPositions,
} from '../js/views/learning.js';

function distance(a, b) {
  return Math.hypot(a.x - b.x, a.y - b.y);
}

test('spaceGraphPositions separates overlapping nodes by their radii plus a visible gap', () => {
  const input = { a: { x: 0, y: 0 }, b: { x: 0, y: 0 } };
  const out = spaceGraphPositions([
    { id: 'a', size: 16 },
    { id: 'b', size: 20 },
  ], input);

  assert.ok(distance(out.a, out.b) >= 16 + 20 + GRAPH_NODE_GAP - 0.01);
  assert.deepEqual(input, { a: { x: 0, y: 0 }, b: { x: 0, y: 0 } }, 'layout input must not be mutated');
});

test('spaceGraphPositions resolves a dense cluster without moving pinned nodes', () => {
  const nodes = [
    { id: 'anchor', size: 16, fixed: { x: true, y: true } },
    ...Array.from({ length: 8 }, (_, i) => ({ id: `n${i}`, size: 16 })),
  ];
  const positions = Object.fromEntries(nodes.map((node) => [node.id, { x: 0, y: 0 }]));
  const out = spaceGraphPositions(nodes, positions);

  assert.deepEqual(out.anchor, { x: 0, y: 0 });
  for (let i = 0; i < nodes.length; i++) {
    for (let j = i + 1; j < nodes.length; j++) {
      assert.ok(
        distance(out[nodes[i].id], out[nodes[j].id]) >= 32 + GRAPH_NODE_GAP - 0.05,
        `${nodes[i].id} and ${nodes[j].id} must not overlap`,
      );
    }
  }
});

test('freezeGraphLayout applies separated positions before disabling physics', () => {
  const calls = [];
  const records = [
    { id: 'a', size: 16 },
    { id: 'b', size: 16, fixed: { x: true, y: true } },
  ];
  const nodes = {
    get: () => records,
    update: (value) => calls.push(['update', value]),
  };
  const network = {
    getPositions: () => ({ a: { x: 0, y: 0 }, b: { x: 0, y: 0 } }),
    moveNode: (id, x, y) => calls.push(['moveNode', id, x, y]),
    setOptions: (value) => calls.push(['setOptions', value]),
  };

  freezeGraphLayout(network, nodes);

  assert.equal(calls.filter(([name]) => name === 'moveNode').length, 2);
  assert.deepEqual(calls.find(([name]) => name === 'setOptions'), ['setOptions', { physics: false }]);
  assert.deepEqual(calls.at(-1), ['update', { id: 'b', fixed: { x: false, y: false } }]);
});

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
