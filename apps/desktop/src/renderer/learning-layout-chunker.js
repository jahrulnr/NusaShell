/**
 * Chunked ForceAtlas2 + Noverlap layout for the Learning graph.
 *
 * The synchronous `forceAtlas2.assign(graph, ...)` / `noverlap.assign(graph, ...)`
 * helpers run the whole layout in one main-thread block — hundreds of ms for a
 * large graph. To keep the UI responsive the low-level primitives are instead
 * driven in chunks across frames:
 *
 *   1. Build the persistent byte arrays ONCE (`graphToByteArrays`), which seeds
 *      velocities at 0 and captures mass.
 *   2. Run `iterate(...)` in small chunks on the SAME matrices — velocities,
 *      mass and convergence carry across chunks.
 *   3. Write positions back once with `assignLayoutChanges`.
 *
 * The high-level `assign` helper must NOT be re-invoked per chunk — it rebuilds
 * fresh matrices and zeroes dx/dy on every call, which restarts the physics
 * (nodes drift incorrectly / never converge).
 */

import fa2Helpers from "graphology-layout-forceatlas2/helpers.js";
import fa2Iterate from "graphology-layout-forceatlas2/iterate.js";
import noverlapHelpers from "graphology-layout-noverlap/helpers.js";
import noverlapIterate from "graphology-layout-noverlap/iterate.js";

const FA2_DEFAULT_ITERATIONS = 240;
const FA2_BIG_GRAPH_ITERATIONS = 80;
/**
 * FA2 nodes per iteration are O(order + edges); ~60 iterations per frame keeps
 * a few-hundred-node graph well under a single frame budget.
 */
const FA2_DEFAULT_CHUNK = 60;
const NOVERLAP_DEFAULT_ITERATIONS = 40;
const NOVERLAP_DEFAULT_CHUNK = 20;

/**
 * Mirror the settings used by the Learning graph's synchronous layout but
 * scaled to the given order.
 */
export function fa2SettingsForOrder(order) {
  return {
    linLogMode: false,
    outboundAttractionDistribution: true,
    gravity: 0.8,
    scalingRatio: order < 200 ? 8 : 14,
    strongGravityMode: false,
    slowDown: 5,
    barnesHutOptimize: order > 50,
    barnesHutTheta: 0.5,
    edgeWeightInfluence: 0,
    adjustSizes: true,
  };
}

export function noverlapSettings() {
  return { margin: 4, ratio: 1.1, speed: 3, gridSize: 20 };
}

/**
 * Bounded layout plan for a graph: number of FA2 iterations (graph gated) and
 * the matching settings. Mirror of the `order < 200 ? 240 : 80` heuristic.
 *
 * @param {{ order: number, size: number }} graph
 * @returns {{ iterations: number, settings: object, noverlapIterations: number }}
 */
export function computeFaLayoutSettings({ order, size }) {
  const big = size === 0 ? order >= 200 : order >= 200;
  const iterations = order > 1 ? (big ? FA2_BIG_GRAPH_ITERATIONS : FA2_DEFAULT_ITERATIONS) : 0;
  return {
    iterations,
    settings: fa2SettingsForOrder(order),
    noverlapIterations: order > 1 ? NOVERLAP_DEFAULT_ITERATIONS : 0,
  };
}

/**
 * Build the persistent FA2 byte arrays for a graph. Call ONCE before chunking —
 * never inside the chunk loop (that would zero velocities per chunk).
 */
export function createFALayoutState(graph, { iterations = FA2_DEFAULT_ITERATIONS, settings } = {}) {
  const nodeMatrices = fa2Helpers.graphToByteArrays(graph, () => 1);
  return {
    matrices: nodeMatrices,
    settings: settings ?? fa2SettingsForOrder(graph.order),
    remaining: Math.max(0, iterations),
    applied: false,
  };
}

/**
 * Run up to `iterations` FA2 steps on the persisted matrices and decrement the
 * remaining counter. Does NOT write back to the graph.
 */
export function runNextFaChunk(state, nodeCount, iterations = 1) {
  void nodeCount;
  if (state.remaining <= 0) {
    state.remaining = 0;
    return state;
  }
  const count = Math.max(0, Math.min(iterations, state.remaining));
  for (let i = 0; i < count; i += 1) {
    fa2Iterate(state.settings, state.matrices.nodes, state.matrices.edges);
  }
  state.remaining -= count;
  return state;
}

/**
 * Write the final FA2 positions back to the graph (idempotent).
 */
export function applyFaLayout(graph, state) {
  if (state.applied) return;
  fa2Helpers.assignLayoutChanges(graph, state.matrices.nodes);
  state.applied = true;
}

/**
 * Run a complete FA2 layout in chunks synchronously. Kept for tests / small
 * graphs and as a reference implementation for the rAF-driven path.
 *
 * @returns {{ remaining: number, matrices: object, nodesMatrix: Float32Array }}
 */
export function forceAtlas2Chunked(graph, { iterations = FA2_DEFAULT_ITERATIONS, chunkSize = FA2_DEFAULT_CHUNK, settings } = {}) {
  const state = createFALayoutState(graph, { iterations, settings });
  while (state.remaining > 0) {
    runNextFaChunk(state, graph.order, chunkSize);
  }
  applyFaLayout(graph, state);
  return {
    remaining: state.remaining,
    matrices: state.matrices,
    nodesMatrix: state.matrices.nodes,
  };
}

/**
 * Build the Noverlap (anti-collision) matrix once.
 */
export function createNoverlapState(graph, { maxIterations = NOVERLAP_DEFAULT_ITERATIONS, settings, inputReducer } = {}) {
  return {
    matrix: noverlapHelpers.graphToByteArray(graph, inputReducer),
    settings: settings ?? noverlapSettings(),
    remaining: Math.max(0, maxIterations),
    applied: false,
  };
}

/**
 * Run up to `options.iterations` Noverlap steps; stops early when converged.
 */
export function runNextNoverlapChunk(state, iterations = 1) {
  if (state.remaining <= 0) return state;
  const count = Math.max(0, Math.min(iterations, state.remaining));
  let converged = false;
  for (let i = 0; i < count && !converged; i += 1) {
    converged = noverlapIterate(state.settings, state.matrix).converged;
    state.remaining -= 1;
  }
  return state;
}

/**
 * Write the final Noverlap positions back to the graph (idempotent).
 */
export function applyNoverlapLayout(graph, state) {
  if (state.applied) return;
  noverlapHelpers.assignLayoutChanges(graph, state.matrix);
  state.applied = true;
}

/**
 * Run a complete Noverlap layout in chunks synchronously.
 */
export function noverlapChunked(graph, { maxIterations = NOVERLAP_DEFAULT_ITERATIONS, chunkSize = NOVERLAP_DEFAULT_CHUNK, settings } = {}) {
  const state = createNoverlapState(graph, { maxIterations, settings });
  while (state.remaining > 0) {
    runNextNoverlapChunk(state, chunkSize);
  }
  applyNoverlapLayout(graph, state);
  return { remaining: state.remaining, matrix: state.matrix };
}

/**
 * High-level entry point used by LearningSigmaGraph.mount: chunked FA2 followed
 * by chunked Noverlap, runnable synchronously (tests/small graphs) or driven by
 * requestAnimationFrame to keep the main thread free.
 *
 * @param {object} graph Graphology-like graph (order/size/forEachNode/forEachEdge...)
 * @param {object} [options] { iterations, settings, onFrame }
 * @returns {{ state: object, noverlapState: object, plan: object }}
 */
export function runFaLayoutChunked(graph, options = {}) {
  const plan = computeFaLayoutSettings(graph);
  const state = createFALayoutState(graph, {
    iterations: options.iterations ?? plan.iterations,
    settings: options.settings,
  });
  const noverlapState = createNoverlapState(graph, {
    maxIterations: plan.noverlapIterations,
  });
  return { state, noverlapState, plan };
}

/**
 * Convenience wrapper mirroring the Learning graph layout: build the
 * chunked FA2 + Noverlap state for a graph and return a single object with
 * both states plus the plan, for the mount path to pump via rAF.
 *
 * @param {object} graph
 * @param {object} [options]
 * @returns {{ state: object, noverlapState: object, plan: object }}
 */
export function layoutGraphChunked(graph, options = {}) {
  return runFaLayoutChunked(graph, options);
}
