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
  GRAPH_NODE_MAX_SIZE,
  GRAPH_NODE_MIN_SIZE,
  GRAPH_PALETTE,
  spaceGraphPositions,
  freezeGraphLayout,
  relayoutGraph,
  keepGraphPositions,
  sizeGraphNodesByRelations,
  graphNodeSizeAtScale,
  bindGraphZoomSizing,
  positionGraphByRelations,
  graphEdgeWidth,
  fitGraphToView,
} from '../js/views/learning.js';

function distance(a, b) {
  return Math.hypot(a.x - b.x, a.y - b.y);
}

test('GRAPH_PALETTE uses a restrained archipelago palette for every graph role', () => {
  assert.deepEqual(GRAPH_PALETTE, {
    ocean: '#79bdd2',
    oceanBorder: '#27758d',
    deepOcean: '#1d5a70',
    earth: '#c38c5e',
    earthBorder: '#895833',
    leaf: '#68b982',
    leafBorder: '#3f7656',
    mangrove: '#3f8a71',
    sand: '#d6b36c',
  });
  assert.equal(new Set([
    GRAPH_PALETTE.ocean,
    GRAPH_PALETTE.earth,
    GRAPH_PALETTE.leaf,
  ]).size, 3, 'node categories remain distinguishable');
});

test('graphEdgeWidth keeps dense graph lines thin while preserving weight order', () => {
  assert.equal(graphEdgeWidth(0), 0.35);
  assert.ok(graphEdgeWidth(0.5) < graphEdgeWidth(1));
  assert.equal(graphEdgeWidth(1), 1.1);
  assert.ok(graphEdgeWidth(1) < 1.5, 'even the strongest edge stays below the old minimum width');
});

test('positionGraphByRelations places hubs inward and isolates at the perimeter', () => {
  const nodes = [
    { id: 'hub', relationCount: 4 },
    { id: 'branch', relationCount: 2 },
    { id: 'isolated', relationCount: 0 },
  ];
  const out = positionGraphByRelations(nodes, {
    hub: { x: 100, y: 0 },
    branch: { x: 0, y: 100 },
    isolated: { x: 20, y: 0 },
  });
  const center = { x: 40, y: 100 / 3 };

  assert.ok(distance(out.hub, center) < distance(out.branch, center));
  assert.ok(distance(out.branch, center) < distance(out.isolated, center));
});

test('positionGraphByRelations keeps the peripheral ring compact', () => {
  const radius = 100;
  const nodes = [
    { id: 'hub', relationCount: 8 },
    { id: 'branch', relationCount: 3 },
    { id: 'isolated', relationCount: 0 },
  ];
  const out = positionGraphByRelations(nodes, {
    hub: { x: radius, y: 0 },
    branch: { x: -radius / 2, y: radius * Math.sqrt(3) / 2 },
    isolated: { x: -radius / 2, y: -radius * Math.sqrt(3) / 2 },
  });
  const center = { x: 0, y: 0 };
  const hubRadius = distance(out.hub, center);
  const isolatedRadius = distance(out.isolated, center);

  assert.ok(isolatedRadius <= radius, 'peripheral nodes stay inside the natural graph radius');
  assert.ok(isolatedRadius / hubRadius < 1.8, 'centrality remains visible without splitting the graph apart');
});

test('positionGraphByRelations leaves a pinned refresh layout untouched', () => {
  const positions = { hub: { x: 10, y: 20 }, leaf: { x: 30, y: 40 } };
  const out = positionGraphByRelations([
    { id: 'hub', relationCount: 5, fixed: { x: true, y: true } },
    { id: 'leaf', relationCount: 0 },
  ], positions);

  assert.deepEqual(out, positions);
});

test('fitGraphToView reapplies relation sizing after programmatic fit', () => {
  const records = [{ id: 'hub', relationSize: GRAPH_NODE_MAX_SIZE, size: GRAPH_NODE_MAX_SIZE }];
  const network = {
    fit: () => {},
    getScale: () => 0.5,
  };
  const nodes = {
    get: () => records.map((node) => ({ ...node })),
    update: (updates) => updates.forEach((update) => Object.assign(records[0], update)),
  };

  fitGraphToView(network, nodes, 0);

  assert.equal(records[0].size, 43);
});

test('sizeGraphNodesByRelations makes nodes with more unique relations larger', () => {
  const nodes = [
    { id: 'hub' },
    { id: 'branch' },
    { id: 'leaf' },
    { id: 'single' },
    { id: 'isolated' },
  ];
  const edges = [
    { from: 'hub', to: 'branch' },
    { from: 'hub', to: 'leaf' },
    { from: 'hub', to: 'single' },
    { from: 'branch', to: 'leaf' },
    // Repeated edges describe another relation type, not another neighbour.
    { from: 'hub', to: 'branch' },
  ];

  const sized = sizeGraphNodesByRelations(nodes, edges);
  const byID = Object.fromEntries(sized.map((node) => [node.id, node]));

  assert.equal(byID.hub.relationCount, 3);
  assert.equal(byID.branch.relationCount, 2);
  assert.equal(byID.leaf.relationCount, 2);
  assert.equal(byID.single.relationCount, 1);
  assert.equal(byID.isolated.relationCount, 0);
  assert.ok(byID.hub.size > byID.branch.size);
  assert.equal(byID.branch.size, byID.leaf.size);
  assert.ok(byID.branch.size > byID.single.size);
  assert.ok(byID.single.size > byID.isolated.size);
  assert.equal(byID.hub.size, GRAPH_NODE_MAX_SIZE);
  assert.equal(byID.isolated.size, GRAPH_NODE_MIN_SIZE);
});

test('graphNodeSizeAtScale preserves relation-size differences while zoomed out', () => {
  const scale = 0.5;
  const leaf = graphNodeSizeAtScale(GRAPH_NODE_MIN_SIZE, scale);
  const branch = graphNodeSizeAtScale(16, scale);
  const hub = graphNodeSizeAtScale(GRAPH_NODE_MAX_SIZE, scale);

  assert.equal(leaf * scale, GRAPH_NODE_MIN_SIZE * scale);
  assert.equal((branch - leaf) * scale, (16 - GRAPH_NODE_MIN_SIZE) * 0.75);
  assert.equal((hub - leaf) * scale, (GRAPH_NODE_MAX_SIZE - GRAPH_NODE_MIN_SIZE) * 0.75);
});

test('graphNodeSizeAtScale leaves natural node sizes unchanged at normal and close zoom', () => {
  assert.equal(graphNodeSizeAtScale(16, 1), 16);
  assert.equal(graphNodeSizeAtScale(16, 2), 16);
});

test('bindGraphZoomSizing reapplies relation sizes whenever the viewport zooms', () => {
  let zoomHandler;
  const records = [
    { id: 'leaf', relationSize: GRAPH_NODE_MIN_SIZE, size: GRAPH_NODE_MIN_SIZE },
    { id: 'hub', relationSize: GRAPH_NODE_MAX_SIZE, size: GRAPH_NODE_MAX_SIZE },
  ];
  const network = {
    on: (event, handler) => { if (event === 'zoom') zoomHandler = handler; },
  };
  const nodes = {
    get: () => records.map((node) => ({ ...node })),
    update: (updates) => updates.forEach((update) => Object.assign(
      records.find((node) => node.id === update.id),
      update,
    )),
  };

  bindGraphZoomSizing(network, nodes);
  zoomHandler({ scale: 0.5 });

  assert.equal(records[0].size, GRAPH_NODE_MIN_SIZE);
  assert.equal(records[1].size, 43);
});

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
