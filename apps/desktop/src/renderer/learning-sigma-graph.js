import Graph from "graphology";
import Sigma from "sigma";
import { EdgeArrowProgram } from "sigma/rendering";
import { EdgeCurvedArrowProgram } from "@sigma/edge-curve";
import forceAtlas2 from "graphology-layout-forceatlas2";
import noverlap from "graphology-layout-noverlap";

const ZOOM_FAR = 0.6;
const ZOOM_MID = 0.3;
const ZOOM_NEAR = 0.12;

function hash(value) {
  let result = 2166136261;
  for (const char of String(value)) {
    result ^= char.charCodeAt(0);
    result = Math.imul(result, 16777619);
  }
  return result >>> 0;
}

function compactOrphans(graph) {
  const connected = [];
  const orphans = [];
  graph.forEachNode((id) => (graph.degree(id) === 0 ? orphans : connected).push(id));
  if (!orphans.length) return;

  const center = connected.reduce((sum, id) => ({
    x: sum.x + graph.getNodeAttribute(id, "x"),
    y: sum.y + graph.getNodeAttribute(id, "y"),
  }), { x: 0, y: 0 });
  if (connected.length) {
    center.x /= connected.length;
    center.y /= connected.length;
  }
  const radius = connected.length
    ? Math.min(Math.max(...connected.map((id) => Math.hypot(
      graph.getNodeAttribute(id, "x") - center.x,
      graph.getNodeAttribute(id, "y") - center.y,
    )), 40) * 1.15, 260)
    : 140;
  orphans.forEach((id, index) => {
    const angle = orphans.length === 1 ? 0 : (index / orphans.length) * Math.PI * 2 - Math.PI / 2;
    graph.mergeNodeAttributes(id, {
      x: center.x + Math.cos(angle) * radius,
      y: center.y + Math.sin(angle) * radius,
    });
  });
}

function neighborhood(graph, active) {
  const nodes = new Set(active ? [active] : []);
  const edges = new Set();
  if (!active || !graph.hasNode(active)) return { nodes, edges };
  let frontier = [active];
  for (let hop = 0; hop < 2; hop++) {
    const next = [];
    for (const id of frontier) {
      graph.forEachEdge(id, (edge, _attrs, source, target) => {
        edges.add(edge);
        const other = source === id ? target : source;
        if (!nodes.has(other)) {
          nodes.add(other);
          next.push(other);
        }
      });
    }
    frontier = next;
  }
  return { nodes, edges };
}

export class LearningSigmaGraph {
  constructor(container, { onNodeSelect, onZoom } = {}) {
    this.container = container;
    this.onNodeSelect = onNodeSelect;
    this.onZoom = onZoom;
    this.sigma = null;
    this.graph = null;
    this.selectedNodeId = null;
    this.scrubberValue = 100;
    this.cameraRatio = 1;
    this.hoveredNode = null;
    this.pendingMount = null;
    this.containerObserver = null;
    // Sigma can synchronously emit an internal update while a setting or
    // refresh is being applied. Prevent that callback from re-entering the
    // reducer setup and recursively refreshing forever.
    this.applyingReducers = false;
  }

  mount(nodes, edges) {
    if (!this._containerHasDimensions()) {
      this.pendingMount = { nodes, edges };
      this._watchContainer();
      return false;
    }

    this.pendingMount = null;
    this.containerObserver?.disconnect();
    this.containerObserver = null;
    this.destroy();
    this.graph = new Graph({ multi: false, type: "directed" });
    const nodeIds = new Set(nodes.map((node) => node.id));
    const center = { x: 0, y: 0 };
    for (const node of nodes) {
      const seed = hash(node.id) / 0xffffffff;
      const angle = seed * Math.PI * 2;
      const radius = 60 + (hash(`${node.id}:radius`) % 100);
      this.graph.addNode(node.id, {
        label: node.label,
        x: Math.cos(angle) * radius,
        y: Math.sin(angle) * radius,
        size: this.nodeSize(0),
        color: node.kind === "memory" ? "#c4a7ff" : node.state === "stale" ? "#f0b35b" : "#b7f36b",
        kind: node.kind,
        state: node.state,
        timestamp: node.timestamp ?? 0,
        degree: 0,
      });
    }
    edges.forEach((edge, index) => {
      if (!nodeIds.has(edge.source) || !nodeIds.has(edge.target)) return;
      if (this.graph.hasEdge(edge.source, edge.target)) return;
      this.graph.addEdgeWithKey(`${edge.source}→${edge.target}#${index}`, edge.source, edge.target, {
        type: "curvedArrow",
        size: 1,
        color: "#526070",
      });
    });
    this.graph.forEachNode((id) => {
      this.graph.setNodeAttribute(id, "degree", this.graph.degree(id));
      this.graph.setNodeAttribute(id, "size", this.nodeSize(this.graph.degree(id)));
    });

    if (this.graph.order > 1) {
      forceAtlas2.assign(this.graph, {
        iterations: this.graph.order < 200 ? 240 : 80,
        settings: {
          linLogMode: false,
          outboundAttractionDistribution: true,
          gravity: 0.8,
          scalingRatio: this.graph.order < 200 ? 8 : 14,
          strongGravityMode: false,
          slowDown: 5,
          barnesHutOptimize: this.graph.order > 50,
          barnesHutTheta: 0.5,
          edgeWeightInfluence: 0,
          adjustSizes: true,
        },
      });
      compactOrphans(this.graph);
      noverlap.assign(this.graph, { maxIterations: 40, settings: { margin: 4, ratio: 1.1, speed: 3, gridSize: 20 } });
    }

    this.sigma = new Sigma(this.graph, this.container, {
      renderLabels: true,
      labelRenderedSizeThreshold: 10,
      labelDensity: this.graph.order <= 12 ? 0.2 : 0.04,
      labelGridCellSize: 180,
      labelColor: { color: "#c6d0db" },
      labelFont: "IBM Plex Sans, sans-serif",
      labelSize: 12,
      defaultEdgeColor: "#526070",
      defaultEdgeType: "curvedArrow",
      edgeProgramClasses: { arrow: EdgeArrowProgram, curvedArrow: EdgeCurvedArrowProgram },
      minCameraRatio: 0.02,
      maxCameraRatio: 8,
      zIndex: true,
      defaultDrawNodeHover: () => undefined,
    });
    this._bindEvents();
    this._applyReducers();
    this.sigma.getCamera().animatedReset({ duration: 0 });
    this._emitZoom();
    return true;
  }

  _containerHasDimensions() {
    if (!this.container) return false;
    const rect = this.container.getBoundingClientRect?.();
    const width = rect?.width || this.container.clientWidth;
    const height = rect?.height || this.container.clientHeight;
    return width > 0 && height > 0;
  }

  _watchContainer() {
    if (!this.container || this.containerObserver || typeof ResizeObserver === "undefined") return;
    this.containerObserver = new ResizeObserver(() => {
      if (!this.pendingMount || !this._containerHasDimensions()) return;
      const { nodes, edges } = this.pendingMount;
      this.mount(nodes, edges);
    });
    this.containerObserver.observe(this.container);
  }

  _bindEvents() {
    this.sigma.on("clickNode", ({ node }) => {
      const selected = node === this.selectedNodeId ? null : node;
      this.selectedNodeId = selected;
      this._applyReducers();
      this.onNodeSelect?.(selected);
    });
    this.sigma.on("clickStage", () => {
      this.selectedNodeId = null;
      this._applyReducers();
      this.onNodeSelect?.(null);
    });
    this.sigma.on("enterNode", ({ node }) => {
      this.hoveredNode = node;
      this._applyReducers();
    });
    this.sigma.on("leaveNode", () => {
      this.hoveredNode = null;
      this._applyReducers();
    });
    this.sigma.getCamera().on("updated", () => {
      this.cameraRatio = this.sigma.getCamera().ratio;
      this._applyReducers();
      this._emitZoom();
    });
  }

  _emitZoom() {
    this.onZoom?.(Math.round((1 / this.cameraRatio) * 100));
  }

  _applyReducers() {
    if (!this.sigma || !this.graph) return;
    if (this.applyingReducers) return;
    this.applyingReducers = true;

    try {
      const active = this.selectedNodeId || this.hoveredNode;
      const hood = neighborhood(this.graph, active);
      const smallGraph = this.graph.order <= 12;
      const cutoff = Math.max(...this.graph.mapNodes((_, attrs) => attrs.timestamp), 0) * (this.scrubberValue / 100);
      this.sigma.setSetting("nodeReducer", (id, data) => {
        const degree = this.graph.degree(id);
        const next = { ...data };
        if ((data.timestamp ?? 0) > cutoff) next.hidden = true;
        if (active && !hood.nodes.has(id)) next.color = "#29313c";
        if (active && hood.nodes.has(id)) next.zIndex = id === active ? 4 : 3;
        if (!smallGraph && !active && this.cameraRatio > ZOOM_FAR && degree < 2) next.hidden = true;
        if (!smallGraph && !active && this.cameraRatio > ZOOM_MID && degree < 1) next.hidden = true;
        if (id === active) { next.forceLabel = true; next.highlighted = true; }
        return next;
      });
      this.sigma.setSetting("edgeReducer", (edge, data) => {
        const next = { ...data };
        if (active) {
          if (!hood.edges.has(edge)) next.hidden = true;
          else { next.color = "#d7f59a"; next.size = 2; }
          return next;
        }
        const source = this.graph.source(edge);
        const target = this.graph.target(edge);
        if (this.cameraRatio > ZOOM_FAR) next.hidden = true;
        else if (this.cameraRatio > ZOOM_MID && (this.graph.degree(source) < 3 || this.graph.degree(target) < 3)) next.hidden = true;
        return next;
      });
      this.sigma.setSetting("labelRenderedSizeThreshold", smallGraph ? 8 : this.cameraRatio > ZOOM_FAR ? 18 : this.cameraRatio > ZOOM_MID ? 12 : 6);
      this.sigma.setSetting("labelDensity", smallGraph ? 0.3 : this.cameraRatio > ZOOM_FAR ? 0.02 : this.cameraRatio > ZOOM_MID ? 0.08 : 0.2);
      this.sigma.refresh({ skipIndexation: true });
    } finally {
      this.applyingReducers = false;
    }
  }

  nodeSize(degree) {
    return Math.min(12, 5 + Math.log2((degree ?? 0) + 1) * 1.8);
  }

  setSelected(nodeId) {
    this.selectedNodeId = nodeId;
    this._applyReducers();
  }

  setScrubber(value) {
    this.scrubberValue = value;
    this._applyReducers();
  }

  resize() {
    this.sigma?.resize();
    this.sigma?.refresh();
  }

  applyReducers() {
    this._applyReducers();
  }

  zoomIn() { this.sigma?.getCamera().animatedZoom({ duration: 200 }); }
  zoomOut() { this.sigma?.getCamera().animatedUnzoom({ duration: 200 }); }
  resetView() { this.sigma?.getCamera().animatedReset({ duration: 300 }); }

  destroy() {
    this.sigma?.kill();
    this.sigma = null;
    this.graph = null;
    this.pendingMount = null;
    this.containerObserver?.disconnect();
    this.containerObserver = null;
    if (this.container) this.container.textContent = "";
  }
}
