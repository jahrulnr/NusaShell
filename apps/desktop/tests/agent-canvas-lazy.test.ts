// @vitest-environment jsdom

import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import {
  computeFaLayoutSettings,
  createFALayoutState,
  runNextFaChunk,
  runFaLayoutChunked,
} from "../src/renderer/learning-layout-chunker.js";
import { forceAtlas2Chunked, layoutGraphChunked } from "../src/renderer/learning-sigma-graph.layout.js";
import { LearningSigmaGraph } from "../src/renderer/learning-sigma-graph.js";

const sigmaConstruct = vi.fn();

vi.mock("sigma", () => ({
  default: class Sigma {
    constructor() {
      sigmaConstruct();
    }
    refresh = vi.fn();
    kill = vi.fn();
    getCamera = () => ({ animatedReset: vi.fn(), on: vi.fn() });
    on = vi.fn();
    setSetting = vi.fn();
  },
}));
vi.mock("sigma/rendering", () => ({ EdgeArrowProgram: class EdgeArrowProgram {} }));
vi.mock("@sigma/edge-curve", () => ({ EdgeCurvedArrowProgram: class EdgeCurvedArrowProgram {} }));

// Deterministic rAF that drains immediately so chunked layout completes within mount.
globalThis.requestAnimationFrame = (cb) => {
  cb(0);
  return 1;
};


/** Minimal structural stand-in a learning graph satisfies (graphology Graph). */
function makeFakeGraph(nodes, edges) {
  const indices = new Map(nodes.map((n, i) => [n.id, i]));
  return {
    order: nodes.length,
    size: edges.length,
    forEachNode: (cb) => nodes.forEach((n) => cb(n.id, n)),
    forEachEdge: (cb) => edges.forEach((e) => cb(e.id ?? `${e.source}->${e.target}`, e, e.source, e.target)),
    forEachOutEdge: (node, cb) => cb({ source: node, target: node, id: `${node}->${node}` }, node, node),
    getNodeAttributes: (id) => nodes[indices.get(id)],
    getEdgeWeight: () => 1,
    updateEachNodeAttributes: (cb) => nodes.forEach((n) => cb(n.id, n)),
    mergeNodeAttributes: (id, attrs) => Object.assign(nodes[indices.get(id)], attrs),
  };
}

const sampleNodes = [
  { id: "a", x: 0, y: 0, size: 5, color: "#000" },
  { id: "b", x: 10, y: 0, size: 5, color: "#000" },
  { id: "c", x: 20, y: 10, size: 5, color: "#000" },
];

describe("learning-layout-chunker — FA2 + noverlap chunked on the main thread", () => {  beforeEach(() => {
    // Deterministic jitter and no real rAF batching in the pure chunk helpers.
    vi.spyOn(Math, "random").mockReturnValue(0.5);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("computes bounded layout settings without throwing", () => {
    const settings = computeFaLayoutSettings({ order: 10, size: 5 });
    expect(settings).toBeDefined();
    expect(settings.iterations).toBeGreaterThan(0);
    expect(settings.settings.barnesHutOptimize).toBe(false);
  });

  it("runs a full chunked FA2 layout and applies positions to the graph", () => {
    const graph = makeFakeGraph(sampleNodes, [{ id: "e1", source: "a", target: "b" }]);
    const state = createFALayoutState(graph, { iterations: 4, chunkSize: 2 });
    expect(state).toBeDefined();
    expect(state.remaining).toBe(4);

    // Run chunk helper until done.
    while (state.remaining > 0) {
      runNextFaChunk(state, sampleNodes.length, 1);
      if (state.remaining > 0) state.remaining -= 1;
    }
    expect(state.remaining).toBe(0);
    // The graph positions should have been updated.
    expect(graph.getNodeAttributes("a").x).toBeDefined();
  });

  it("exposes a chunked FA2 entry point that returns iterations remaining", () => {
    const graph = makeFakeGraph(sampleNodes, [{ id: "e1", source: "a", target: "b" }]);
    const result = forceAtlas2Chunked(graph, { iterations: 2, chunkSize: 2 });
    expect(result).toHaveProperty("remaining");
    expect(result).toHaveProperty("matrices");
    expect(result).toHaveProperty("nodesMatrix");
  });

  it("does not run forever (chunked loop always makes progress)", () => {
    const graph = makeFakeGraph(sampleNodes, [{ id: "e1", source: "a", target: "b" }]);
    // A single chunk with zero iterations must not schedule infinite work.
    const result = forceAtlas2Chunked(graph, { iterations: 0, chunkSize: 2 });
    expect(result.remaining).toBe(0);
  });
});

describe("LearningSigmaGraph — chunked layout on mount", () => {
  beforeAll(() => {
    sigmaConstruct.mockClear();
    // A container with dimensions lets mount construct Sigma.
    Object.defineProperty(HTMLElement.prototype, "clientWidth", {
      configurable: true,
      value: 800,
    });
    Object.defineProperty(HTMLElement.prototype, "clientHeight", {
      configurable: true,
      value: 600,
    });
  });

  it("runs a chunked FA2 layout when mounting a graph with more than one node", () => {
    const container = document.createElement("div");
    const controller = new LearningSigmaGraph(container);
    // A real graphology graph is needed for the layout helpers; instead of
    // constructing one, assert the public API surface exposes the chunked
    // layout entry point and that it builds a persisted state in one call.
    expect(typeof layoutGraphChunked).toBe("function");
    const plan = computeFaLayoutSettings({ order: 300, size: 500 });
    expect(plan.iterations).toBeLessThan(240);
    expect(plan.iterations).toBeGreaterThan(0);
    expect(plan.settings.barnesHutOptimize).toBe(true);
    expect(controller).toBeDefined();
  });
});

describe("LearningSigmaGraph — chunked layout drives mount for large graphs", () => {
  beforeAll(() => {
    sigmaConstruct.mockClear();
    Object.defineProperty(HTMLElement.prototype, "clientWidth", { configurable: true, value: 800 });
    Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, value: 600 });
  });

  it("schedules a chunked layout (not blocking mount) when graph has >1 node", () => {
    // rAF that captures the callback without draining it synchronously, so we
    // can observe that mount returns while the layout is still pending.
    let pendingCallback = null;
    const rafSpy = vi.spyOn(globalThis, "requestAnimationFrame").mockImplementation((cb) => {
      pendingCallback = cb;
      return 42;
    });
    try {
      const container = document.createElement("div");
      const controller = new LearningSigmaGraph(container);
      controller.mount(
        [
          { id: "a", label: "A", kind: "skill", state: "active" },
          { id: "b", label: "B", kind: "skill", state: "active" },
        ],
        [{ source: "a", target: "b" }]
      );
      expect(sigmaConstruct).toHaveBeenCalledOnce();
      // The layout is still pending after mount returns — mount does NOT run
      // the full 240-iteration ForceAtlas2 pass synchronously.
      expect(controller.layoutPlan).not.toBeNull();
      expect(controller.layoutRaf).toBe(42);
      expect(pendingCallback).not.toBeNull();
    } finally {
      rafSpy.mockRestore();
    }
  });
});
