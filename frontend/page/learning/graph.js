import { DataSet, Network } from '../../vendor/vis-network/vis-network.esm.min.js';
import {
  GRAPH_EDGE_LIMIT,
  GRAPH_LAYOUT_ITERATIONS,
  GRAPH_PALETTE,
  bindGraphZoomSizing,
  fitGraphToView,
  freezeGraphLayout,
  graphEdgeWidth,
  graphVisNodeFromDTO,
  keepGraphPositions,
  relayoutGraph,
  selectGraphEdges,
  sizeGraphNodesByRelations,
} from '../../js/views/learning.js';

function graphFont() {
  return "'IBM Plex Sans', 'Noto Color Emoji', 'Noto Sans Symbols 2', sans-serif";
}

function graphOptions() {
  return {
    nodes: {
      shape: 'dot',
      size: 16,
      font: { size: 12, color: '#c9d1d9', face: graphFont() },
      borderWidth: 2,
    },
    edges: {
      width: 0.6,
      color: { color: '#30363d', highlight: '#6ee0c4', hover: '#8b949e' },
      smooth: { type: 'continuous', roundness: 0.5 },
      font: { size: 10, color: '#8b949e', face: graphFont() },
    },
    groups: {
      skill: { color: { background: GRAPH_PALETTE.ocean, border: GRAPH_PALETTE.oceanBorder }, size: 20 },
      memory: { color: { background: GRAPH_PALETTE.earth, border: GRAPH_PALETTE.earthBorder }, size: 14 },
      'memory-user': { color: { background: GRAPH_PALETTE.leaf, border: GRAPH_PALETTE.leafBorder }, size: 16, shape: 'square' },
    },
    physics: {
      enabled: true,
      solver: 'forceAtlas2Based',
      forceAtlas2Based: {
        gravitationalConstant: -26,
        centralGravity: 0.1,
        springLength: 180,
        springConstant: 0.04,
        damping: 0.4,
        avoidOverlap: 1,
      },
      maxVelocity: 12,
      timestep: 0.5,
      stabilization: {
        enabled: true,
        iterations: GRAPH_LAYOUT_ITERATIONS,
        updateInterval: 25,
        onlyDynamicEdges: false,
        fit: true,
      },
    },
    interaction: {
      hover: true,
      tooltipDelay: 200,
      navigationButtons: false,
      keyboard: false,
    },
  };
}

function graphEdgesFromDTO(edges = []) {
  const colors = {
    related: GRAPH_PALETTE.deepOcean,
    used_with: GRAPH_PALETTE.mangrove,
    derived_from: GRAPH_PALETTE.sand,
  };
  return (Array.isArray(edges) ? edges : []).map((edge, index) => ({
    id: `edge_${index}`,
    from: edge.from,
    to: edge.to,
    width: graphEdgeWidth(Number(edge.weight)),
    color: { color: colors[edge.type] || '#4b504b', highlight: '#6ee0c4', hover: '#8af0d4' },
    title: `${edge.type} (${(Number(edge.weight) * 100).toFixed(0)}%)`,
  }));
}

export function createLearningGraph(container, { onSelect = () => {} } = {}) {
  if (!container) return null;
  const probe = document.createElement('canvas');
  if (!probe.getContext || !probe.getContext('2d')) return null;

  const nodes = new DataSet([]);
  const edges = new DataSet([]);
  const network = new Network(container, { nodes, edges }, graphOptions());
  network.on('click', (params) => {
    const selected = network.getSelectedNodes?.()?.[0] || params.nodes?.[0] || null;
    onSelect(selected ? nodes.get(selected) : null);
  });
  network.on('stabilizationIterationsDone', () => freezeGraphLayout(network, nodes));
  bindGraphZoomSizing(network, nodes);

  // The backend returns the complete relationship set; the rendered graph is
  // bounded to GRAPH_EDGE_LIMIT (2000) strongest edges so the physics engine
  // stays usable. Returns the rendered/total counts so the view can surface
  // the "N of M edges" statistic.
  function update(nodeDTO = [], edgeDTO = [], { preservePositions = true, fit = false } = {}) {
    const prevPositions = preservePositions ? network.getPositions() : {};
    const renderedEdges = selectGraphEdges(edgeDTO, GRAPH_EDGE_LIMIT);
    const visNodes = keepGraphPositions(
      sizeGraphNodesByRelations(nodeDTO, renderedEdges).map(graphVisNodeFromDTO),
      prevPositions,
    );
    const visEdges = graphEdgesFromDTO(renderedEdges);
    nodes.clear();
    edges.clear();
    if (visNodes.length) nodes.add(visNodes);
    if (visEdges.length) edges.add(visEdges);
    if (!visNodes.length) return { nodeCount: 0, edgeCount: 0, totalEdges: (edgeDTO || []).length };
    relayoutGraph(network);
    // Auto-fit only on explicit reloads or a fresh layout — background
    // refreshes keep the user's zoom and pan.
    if (fit || !preservePositions) {
      requestAnimationFrame(() => {
        setTimeout(() => fitGraphToView(network, nodes, 350), 50);
      });
    }
    return { nodeCount: visNodes.length, edgeCount: visEdges.length, totalEdges: (edgeDTO || []).length };
  }

  function fit() {
    fitGraphToView(network, nodes, 250);
  }

  function select(id) {
    if (id) network.selectNodes([id]);
    else network.unselectAll();
  }

  function destroy() {
    network.destroy();
  }

  return { update, fit, select, destroy, network, nodes, edges };
}
