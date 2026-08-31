// Regression pin for the Learning knowledge graph physics behavior
// (frontend/js/views/learning.js, vendored vis-network 9.1.10).
//
// Background: loadGraph() used to "re-enable" physics via
// setOptions({ physics: { enabled: true, stabilization: {...} } }). With the
// network created empty (initGraph), the initial stabilization completes on
// zero nodes, so the stabilizationIterationsDone freeze handler never runs.
// Every subsequent refresh then restarted an UNBOUNDED simulation: nodes kept
// moving until natural convergence (seconds, or never on denser graphs) —
// the "nodes jitter while idle" bug.
//
// These tests pin the contract the fix relies on:
//   - network.stabilize(N) always completes and fires
//     stabilizationIterationsDone, so the freeze handler applies (still graph).
//   - the old setOptions re-enable never fires that event and keeps nodes
//     moving (documenting why it was removed).
//   - refresh with kept positions + stabilize() yields a static graph.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';
import { setTimeout as delay } from 'node:timers/promises';

const dom = new JSDOM('<!doctype html><html><body><div id="graph" style="width:800px;height:600px"></div></body></html>', {
  url: 'http://localhost/',
  pretendToBeVisual: true,
});
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
globalThis.Element = dom.window.Element;
globalThis.Node = dom.window.Node;
globalThis.SVGElement = dom.window.SVGElement;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);

// No-op canvas 2D context: JSDOM returns null from getContext without the
// `canvas` package. vis-network only needs the context to draw; physics is
// independent of rendering, so a stub keeps the simulation observable.
const ctxStub = new Proxy(
  { canvas: {}, measureText: () => ({ width: 10 }) },
  {
    get(target, prop) {
      if (prop in target) return target[prop];
      if (typeof prop === 'string') return () => {};
      return undefined;
    },
    set() { return true; },
  },
);
dom.window.HTMLCanvasElement.prototype.getContext = () => ctxStub;

const { DataSet, Network } = await import('../vendor/vis-network/vis-network.esm.min.js');

// Physics options mirror frontend/js/views/learning.js initGraph().
const options = {
  nodes: { shape: 'dot', size: 16, font: { size: 12 }, borderWidth: 2 },
  edges: { width: 1.5, color: { color: '#30363d' }, smooth: { type: 'continuous', roundness: 0.5 } },
  physics: {
    enabled: true,
    solver: 'forceAtlas2Based',
    forceAtlas2Based: {
      gravitationalConstant: -26,
      centralGravity: 0.1,
      springLength: 120,
      springConstant: 0.04,
      damping: 0.4,
      avoidOverlap: 0.5,
    },
    maxVelocity: 50,
    timestep: 0.5,
    stabilization: {
      enabled: true,
      iterations: 80,
      updateInterval: 25,
      onlyDynamicEdges: false,
      fit: true,
    },
  },
  interaction: { hover: true, tooltipDelay: 200, navigationButtons: false, keyboard: false },
};

function fixture(offset = 0) {
  const nodes = [];
  for (let i = 0; i < 25; i++) nodes.push({ id: `n${offset + i}`, label: `node ${offset + i}` });
  const edges = [];
  for (let i = 0; i < 30; i++) {
    const from = `n${offset + (i % 25)}`;
    const to = `n${offset + ((i * 7 + 3) % 25)}`;
    if (from !== to) edges.push({ id: `e${offset + i}`, from, to });
  }
  return { nodes, edges };
}

// Freeze handler copied from learning.js initGraph(): disable physics and
// release layout pins (positions stay put because physics is already off).
function installFreezeHandler(network, nodes) {
  network.on('stabilizationIterationsDone', () => {
    network.setOptions({ physics: false });
    for (const node of nodes.get()) {
      if (node.fixed) nodes.update({ id: node.id, fixed: { x: false, y: false } });
    }
  });
}

function maxDelta(a, b) {
  let max = 0;
  for (const id of Object.keys(a)) {
    if (b[id]) {
      const d = Math.hypot(a[id].x - b[id].x, a[id].y - b[id].y);
      if (d > max) max = d;
    }
  }
  return max;
}

test('bounded stabilize() fires stabilizationIterationsDone and freezes the graph', async () => {
  const nodes = new DataSet([]);
  const edges = new DataSet([]);
  const network = new Network(document.getElementById('graph'), { nodes, edges }, options);
  installFreezeHandler(network, nodes);
  let fired = 0;
  network.on('stabilizationIterationsDone', () => { fired += 1; });

  const data = fixture();
  nodes.add(data.nodes);
  edges.add(data.edges);
  network.stabilize(80);

  const deadline = Date.now() + 3000;
  while (fired === 0 && Date.now() < deadline) await delay(50);
  assert.ok(fired >= 1, 'stabilize() must complete and fire stabilizationIterationsDone');

  // After the freeze handler runs, positions must be static.
  await delay(300);
  const before = network.getPositions();
  await delay(300);
  const after = network.getPositions();
  assert.ok(maxDelta(before, after) < 0.1, 'graph must be frozen after stabilization');
  network.destroy();
});

test('old setOptions physics re-enable never fires stabilizationIterationsDone and keeps nodes moving', async () => {
  const nodes = new DataSet([]);
  const edges = new DataSet([]);
  const network = new Network(document.getElementById('graph'), { nodes, edges }, options);
  installFreezeHandler(network, nodes);
  let fired = 0;
  network.on('stabilizationIterationsDone', () => { fired += 1; });

  // First load: same flow as the old code — setOptions re-enable + data push.
  nodes.add(fixture().nodes);
  edges.add(fixture().edges);
  network.setOptions({
    physics: {
      enabled: true,
      stabilization: { enabled: true, iterations: 200, updateInterval: 25, onlyDynamicEdges: false, fit: true },
    },
  });
  await delay(600);
  assert.equal(fired, 0, 'setOptions re-enable must not fire stabilizationIterationsDone (unbounded simulation)');

  // Jitter evidence: nodes are still moving while nothing is being done.
  const before = network.getPositions();
  await delay(250);
  const after = network.getPositions();
  assert.ok(maxDelta(before, after) > 0.5, 'nodes must be moving while idle (the jitter bug)');
  network.destroy();
});

test('refresh with kept positions + stabilize() keeps unchanged nodes still and freezes', async () => {
  const nodes = new DataSet([]);
  const edges = new DataSet([]);
  const network = new Network(document.getElementById('graph'), { nodes, edges }, options);
  installFreezeHandler(network, nodes);

  // Initial layout.
  const data = fixture(0);
  nodes.add(data.nodes);
  edges.add(data.edges);
  network.stabilize(80);
  await delay(500);
  const prevPos = network.getPositions();

  // Refresh: existing ids keep x/y, plus new nodes; then bounded stabilize().
  const keptNodes = fixture(0).nodes.map((n) => {
    const p = prevPos[n.id];
    return p ? { ...n, x: p.x, y: p.y, fixed: { x: true, y: true } } : n;
  });
  const newNodes = [25, 26, 27].map((i) => ({ id: `n${i}`, label: `node ${i}` }));
  const freshEdges = [
    ...fixture(0).edges,
    ...newNodes.map((n, i) => ({ id: `new-e${i}`, from: n.id, to: `n${i * 7}` })),
  ];
  nodes.clear();
  edges.clear();
  nodes.add([...keptNodes, ...newNodes]);
  edges.add(freshEdges);
  network.stabilize(80);
  await delay(500);

  const after = network.getPositions();
  for (const id of Object.keys(prevPos)) {
    assert.ok(
      Math.hypot(after[id].x - prevPos[id].x, after[id].y - prevPos[id].y) < 0.1,
      `existing node ${id} must keep its position across a refresh`,
    );
  }

  // And the graph ends frozen.
  const before2 = network.getPositions();
  await delay(300);
  const after2 = network.getPositions();
  assert.ok(maxDelta(before2, after2) < 0.1, 'graph must be frozen after the refresh layout');
  network.destroy();
});
