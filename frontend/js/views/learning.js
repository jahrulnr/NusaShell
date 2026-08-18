// Learning workspace: search + memory list + knowledge graph.
// Uses vis-network for graph rendering (vendored ESM standalone build).

import { rpc } from '../rpc.js';
import { el, debounce, createSelect, toast } from '../ui.js';
import { DataSet, Network } from '../../vendor/vis-network/vis-network.esm.min.js';

const state = {
  results: [],
  network: null,
  nodes: null,
  edges: null,
  memoryCount: 0,
  edgeCount: 0,
};

export async function initLearning() {
  const input = document.getElementById('learning-search-input');
  const kindFilter = document.getElementById('learning-kind-filter');
  const searchBtn = document.getElementById('learning-search-btn');
  const refreshBtn = document.getElementById('learning-graph-refresh');
  const fitBtn = document.getElementById('learning-graph-fit');

  input.addEventListener('input', debounce(() => doSearch(), 200));
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') doSearch();
  });
  createSelect(kindFilter, {
    data: [
      { text: 'All', value: '' },
      { text: 'Skills', value: 'skills' },
      { text: 'Memory', value: 'memory' },
    ],
    value: '',
    placeholder: 'All',
    onChange: () => doSearch(),
  });
  searchBtn.addEventListener('click', () => doSearch());
  refreshBtn.addEventListener('click', () => loadGraph());
  fitBtn.addEventListener('click', () => {
    if (state.network) state.network.fit({ animation: { duration: 300 } });
  });

  initSplitter();

  await loadStats();
  initGraph();
  // Initial search: empty query lists all skills + memories so the results
  // pane is populated immediately (backend returns an unfiltered listing
  // for empty queries). The graph loads in parallel.
  await Promise.all([doSearch(), loadGraph()]);
}

// Draggable splitter between results pane and graph pane. Persists the
// results-pane width (px) to localStorage so the user's preference survives
// reloads. The graph pane fills the remaining space (1fr).
function initSplitter() {
  const workspace = document.getElementById('learning-workspace');
  const resultsPane = document.getElementById('learning-results-pane');
  const splitter = document.getElementById('learning-splitter');
  if (!workspace || !resultsPane || !splitter) return;

  const STORAGE_KEY = 'nushell:learning-split';
  const SPLITTER_W = 6;
  const applyWidth = (w) => {
    workspace.style.gridTemplateColumns = `${w}px ${SPLITTER_W}px minmax(0, 1fr)`;
  };

  const saved = Number.parseInt(localStorage.getItem(STORAGE_KEY) || '', 10);
  if (saved > 120 && saved < window.innerWidth - 200) {
    applyWidth(saved);
  }

  let dragging = false;
  let startX = 0;
  let startW = 0;

  splitter.addEventListener('mousedown', (e) => {
    dragging = true;
    startX = e.clientX;
    startW = resultsPane.getBoundingClientRect().width;
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
    e.preventDefault();
  });
  document.addEventListener('mousemove', (e) => {
    if (!dragging) return;
    const delta = e.clientX - startX;
    const w = Math.max(180, Math.min(window.innerWidth - 200, startW + delta));
    applyWidth(w);
  });
  document.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    const w = resultsPane.getBoundingClientRect().width;
    localStorage.setItem(STORAGE_KEY, String(Math.round(w)));
    // Resize the graph to fit the new container.
    if (state.network) state.network.redraw();
  });
}

export async function refresh() {
  await loadStats();
  await loadGraph();
}

async function loadStats() {
  try {
    const { entries } = await rpc('memory.list');
    state.memoryCount = entries.length;
    document.getElementById('learning-stat-memory').textContent =
      `${state.memoryCount} memor${state.memoryCount === 1 ? 'y' : 'ies'}`;
  } catch (e) {
    // memory.list might fail if store not initialized
  }
  // Edge count: we don't have a direct RPC, infer from graph load
}

async function doSearch() {
  const input = document.getElementById('learning-search-input');
  const kindFilter = document.getElementById('learning-kind-filter');
  const query = input.value.trim();
  const kind = kindFilter.value;

  const resultsEl = document.getElementById('learning-results');
  const countEl = document.getElementById('learning-results-count');

  resultsEl.innerHTML = '';
  resultsEl.appendChild(el('div', { class: 'learning-searching' }, [el('span', { text: 'Searching…' })]));

  try {
    const res = await rpc('learning.search', { query, kind, limit: 20 });
    state.results = res.items || [];
    renderResults();
    countEl.textContent = String(state.results.length);
  } catch (e) {
    resultsEl.innerHTML = '';
    resultsEl.appendChild(el('div', { class: 'learning-empty' }, [
      el('strong', { text: 'Search failed' }),
      el('span', { text: e.message || 'Unknown error' }),
    ]));
    countEl.textContent = '0';
  }
}

function renderResults() {
  const resultsEl = document.getElementById('learning-results');
  resultsEl.innerHTML = '';
  if (state.results.length === 0) {
    resultsEl.appendChild(el('div', { class: 'learning-empty' }, [
      el('strong', { text: 'No results' }),
      el('span', { text: 'Try a different query or kind filter.' }),
    ]));
    return;
  }
  for (const item of state.results) {
    const isLong = item.content && item.content.length > 200;
    const contentEl = item.content
      ? el('div', { class: 'learning-result-content collapsed', text: item.content })
      : null;
    if (isLong && contentEl) {
      contentEl.title = 'Click to expand/collapse';
      contentEl.addEventListener('click', (e) => {
        e.stopPropagation();
        contentEl.classList.toggle('collapsed');
        contentEl.classList.toggle('expanded');
      });
    }
    const headerRight = el('div', { class: 'learning-result-header-right' }, [
      el('span', { class: 'learning-result-score', text: scoreLabel(item.score) }),
    ]);
    if (item.kind === 'memory') {
      const delBtn = el('button', {
        class: 'learning-result-delete',
        type: 'button',
        title: 'Delete memory',
        'aria-label': 'Delete memory',
        text: '×',
      });
      delBtn.addEventListener('click', (e) => deleteMemory(item.id, e));
      headerRight.appendChild(delBtn);
    }
    const card = el('div', { class: 'learning-result-card', 'data-id': item.id, 'data-kind': item.kind }, [
      el('div', { class: 'learning-result-header' }, [
        el('span', { class: `learning-result-kind learning-kind-${item.kind}`, text: item.kind }),
        headerRight,
      ]),
      item.name ? el('div', { class: 'learning-result-name', text: item.name }) : null,
      contentEl,
    ]);
    card.addEventListener('click', () => focusNode(item.id));
    resultsEl.appendChild(card);
  }
}

async function deleteMemory(id, event) {
  event.stopPropagation();
  const ok = await confirmDelete(id);
  if (!ok) return;
  try {
    await rpc('memory.delete', { id });
    toast('Memory deleted.', 'success', 2000);
    // Remove from local state + re-render without a full reload.
    state.results = state.results.filter((r) => !(r.id === id && r.kind === 'memory'));
    renderResults();
    document.getElementById('learning-results-count').textContent = String(state.results.length);
    // Refresh stats + graph in the background.
    loadStats();
    loadGraph();
  } catch (e) {
    toast(e.message || 'Failed to delete memory.', 'error', 4000);
  }
}

// Inline confirmation: a small popover asking "Delete this memory?" with
// confirm/cancel. Avoids native confirm() per the frontend style rules.
function confirmDelete(id) {
  // Synchronous-style confirmation via a transient inline dialog. Returns
  // true only if the user clicks confirm. We use a simple approach: render
  // a modal-like overlay and resolve on click.
  return new Promise((resolve) => {
    const overlay = el('div', { class: 'learning-confirm-overlay' }, [
      el('div', { class: 'learning-confirm-dialog' }, [
        el('strong', { text: 'Delete this memory?' }),
        el('span', { text: 'This cannot be undone.' }),
        el('div', { class: 'learning-confirm-actions' }, [
          el('button', { class: 'mini-btn', type: 'button', text: 'Cancel' }),
          el('button', { class: 'mini-btn danger', type: 'button', text: 'Delete' }),
        ]),
      ]),
    ]);
    document.body.appendChild(overlay);
    const [cancelBtn, deleteBtn] = overlay.querySelectorAll('button');
    const close = (result) => { overlay.remove(); resolve(result); };
    cancelBtn.addEventListener('click', () => close(false));
    deleteBtn.addEventListener('click', () => close(true));
    overlay.addEventListener('click', (e) => { if (e.target === overlay) close(false); });
  });
}

function scoreLabel(score) {
  if (!score || score <= 0) return '';
  return `★ ${(score * 100).toFixed(1)}%`;
}

function initGraph() {
  const container = document.getElementById('learning-graph');
  if (!container) return;
  // vis-network requires a canvas 2D context. In headless/test environments
  // (JSDOM without the `canvas` npm package), getContext() returns null and
  // the Network constructor throws. Guard so the rest of the view still works.
  const probe = document.createElement('canvas');
  if (!probe.getContext || !probe.getContext('2d')) return;
  state.nodes = new DataSet([]);
  state.edges = new DataSet([]);
  const options = {
    nodes: {
      shape: 'dot',
      size: 16,
      font: { size: 12, color: '#c9d1d9', face: 'Inter, system-ui, sans-serif' },
      borderWidth: 2,
    },
    edges: {
      width: 1.5,
      color: { color: '#30363d', highlight: '#58a6ff', hover: '#8b949e' },
      smooth: { type: 'continuous', roundness: 0.5 },
      font: { size: 10, color: '#8b949e', face: 'Inter, system-ui, sans-serif' },
    },
    groups: {
      skill: { color: { background: '#58a6ff', border: '#1f6feb' }, size: 20 },
      memory: { color: { background: '#f0883e', border: '#db6d28' }, size: 14 },
    },
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
        iterations: 200,
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
  state.network = new Network(container, { nodes: state.nodes, edges: state.edges }, options);
}

async function loadGraph() {
  if (!state.nodes) return;
  try {
    // Fetch pre-computed graph from backend (nodes + edges).
    // The backend builds edges via embedding similarity + token overlap,
    // so we don't need to compute anything client-side.
    const { nodes, edges } = await rpc('learning.graph');

    const newNodes = (nodes || []).map((n) => ({
      id: n.id,
      label: n.name || n.id,
      group: n.kind,
      title: n.name || n.id,
    }));

    const newEdges = (edges || []).map((e, i) => ({
      id: `edge_${i}`,
      from: e.from,
      to: e.to,
      width: Math.max(1.5, e.weight * 4),
      color: { color: edgeColor(e.type), highlight: '#c5f45d', hover: '#d5ff78' },
      title: `${e.type} (${(e.weight * 100).toFixed(0)}%)`,
    }));

    state.nodes.clear();
    state.edges.clear();
    state.nodes.add(newNodes);
    state.edges.add(newEdges);
    state.edgeCount = newEdges.length;
    document.getElementById('learning-stat-edges').textContent =
      `${state.edgeCount} edge${state.edgeCount === 1 ? '' : 's'}`;
    document.getElementById('learning-stat-memory').textContent =
      `${newNodes.filter((n) => n.group === 'memory').length} memor${newNodes.filter((n) => n.group === 'memory').length === 1 ? 'y' : 'ies'}`;
    // Auto-fit after data load + render settled. Defer to next frame so
    // the container has its final dimensions (view switch, CSS layout).
    if (state.network && newNodes.length > 0) {
      requestAnimationFrame(() => {
        setTimeout(() => {
          state.network.fit({ animation: { duration: 400 } });
        }, 50);
      });
    }
  } catch (e) {
    // Graph load failed — show empty state
    const container = document.getElementById('learning-graph');
    if (container) {
      container.innerHTML = '<div style="display:flex;align-items:center;justify-content:center;height:100%;color:var(--text-faint);font-size:12px;">Graph unavailable</div>';
    }
  }
}

function edgeColor(type) {
  switch (type) {
    case 'related': return '#1f6feb';
    case 'used_with': return '#c5f45d';
    case 'derived_from': return '#c1a6ff';
    default: return '#4b504b';
  }
}

function focusNode(id) {
  if (!state.network) return;
  state.network.focus(id, { scale: 1.5, animation: { duration: 400 } });
  state.network.selectNodes([id]);
}
