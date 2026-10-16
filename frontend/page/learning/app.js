import { rpc as defaultRPC, on as defaultOn } from '../../js/rpc.js';
import { bindTablistKeyboard, confirmDialog, debounce, el, toast } from '../../js/ui.js';
import { renderConversation } from '../../js/views/agent/render.js';
import { createLearningGraph } from './graph.js';

import {
  formatLearningDate,
  isMemoryRecord,
  learningTypeLabel,
  memoryEntryBody,
  memoryTierLabel,
  oneLine,
  outcomeLabel,
  sortExperiencesNewestFirst,
  summarizeSignals,
} from './model.js';

const EXPERIENCE_PAGE_SIZE = 25;
const LEARNING_LOG_PAGE_SIZE = 50;
const DOCUMENT_CHAR_CAP = 4000;
const VIEWS = new Set(['overview', 'memory', 'experiences', 'activity']);

const DOCUMENTS = Object.freeze({
  user: Object.freeze({
    textareaId: 'learning-user-memory',
    reloadId: 'learning-user-reload',
    saveId: 'learning-user-save',
    statusId: 'learning-user-status',
    countId: 'learning-user-count',
    updateMethod: 'memory.user.update',
    label: 'About you',
  }),
  agent: Object.freeze({
    textareaId: 'learning-agent-memory',
    reloadId: 'learning-agent-reload',
    saveId: 'learning-agent-save',
    statusId: 'learning-agent-status',
    countId: 'learning-agent-count',
    updateMethod: 'memory.agent.update',
    label: 'About agent',
  }),
});

function resultList(response, ...keys) {
  for (const key of keys) {
    if (Array.isArray(response?.[key])) return response[key];
  }
  return Array.isArray(response) ? response : [];
}

function graphNodeKind(node) {
  const tier = String(node?.tier || '').toLowerCase();
  if (tier === 'user' || tier === 'agent') return 'profile';
  return node?.kind === 'skill' ? 'skill' : 'record';
}

function rawDetailValue(detail, key) {
  const raw = detail?.[key];
  if (raw == null) return '';
  if (typeof raw !== 'string') return String(raw);
  try {
    const parsed = JSON.parse(raw);
    return typeof parsed === 'string' ? parsed : JSON.stringify(parsed);
  } catch {
    return raw;
  }
}

async function fetchLearningTranscript(rpcClient, conversationId) {
  const response = await rpcClient('agent.conversations.get', { id: conversationId });
  return Array.isArray(response?.messages) ? response.messages : [];
}

function nextFrame(callback) {
  if (typeof requestAnimationFrame === 'function') requestAnimationFrame(callback);
  else setTimeout(callback, 0);
}

// Keep the transcript inline with the activity entry so users can inspect the
// exact background-agent run without losing their place in the learning feed.
// The loader is injectable because the controller uses the same RPC seam as
// the rest of this prototype and because a missing/pruned transcript is a
// recoverable display state, not a reason to break the whole activity page.
export async function toggleLearningTranscript(button, load) {
  const entry = button?.closest?.('.learning-log-entry');
  if (!entry) return;
  const existing = entry.querySelector('.learning-log-conversation-panel');
  if (existing) {
    existing.remove();
    button.textContent = 'View agent log';
    button.setAttribute('aria-expanded', 'false');
    return;
  }
  const conversationId = button.dataset.learningConversationId || '';
  if (!conversationId) return;
  const idleLabel = button.textContent;
  button.textContent = 'Loading…';
  button.disabled = true;
  button.setAttribute('aria-busy', 'true');
  const panel = el('div', {
    class: 'learning-log-conversation-panel',
    role: 'region',
    'aria-label': 'Background agent conversation',
  });
  try {
    const transcriptLoader = load || ((id) => fetchLearningTranscript(defaultRPC, id));
    const messages = await transcriptLoader(conversationId);
    const transcript = renderConversation(messages);
    if (transcript.childNodes.length) panel.append(transcript);
    else panel.append(el('p', { class: 'learning-log-conversation-empty', text: 'No messages were persisted for this learning run.' }));
    entry.append(panel);
    button.textContent = 'Hide agent log';
    button.setAttribute('aria-expanded', 'true');
  } catch (error) {
    panel.append(el('p', { class: 'learning-log-conversation-error', text: error?.message ? `Agent log unavailable: ${error.message}` : 'Agent log unavailable.' }));
    entry.append(panel);
    button.textContent = idleLabel;
    button.setAttribute('aria-expanded', 'false');
  } finally {
    button.disabled = false;
    button.removeAttribute('aria-busy');
  }
}

export function createLearningPage(root = document, { rpcClient = defaultRPC, onEvent = defaultOn, graphFactory = createLearningGraph } = {}) {
  const state = {
    view: 'overview',
    loading: true,
    syncError: null,
    entries: [],
    records: [],
    userEntry: null,
    agentEntry: null,
    experiences: [],
    experiencesPage: 0,
    experiencesTotal: 0,
    selectedExperienceId: null,
    graphNodes: [],
    graphEdges: [],
    selectedGraphNodeId: null,
    results: [],
    searchQuery: '',
    searchKind: '',
    searchLoaded: false,
    selectedResultId: null,
    activityLoaded: false,
    logEntries: [],
    logPage: 0,
    logCursors: [0],
    logHasMore: false,
    jobs: [],
  };
  let initialized = false;
  let refreshToken = 0;
  let memoryToken = 0;
  let graphToken = 0;
  let graphRequest = null;
  let learningGraph = null;
  let experienceToken = 0;
  let searchToken = 0;
  let activityToken = 0;
  let refreshInFlight = false;
  const eventUnsubscribers = [];

  const find = (id) => root?.querySelector?.(`#${id}`);
  const pageNode = () => root?.nodeType === 1 ? root : root?.querySelector?.('#learning-view');
  const isMobile = () => Number(window.innerWidth) <= 720;

  // Mobile list -> detail -> Back panes for the Memory atlas and Experiences
  // tabs. The attribute lives on each tab panel so list-only chrome (search,
  // kind filter, tools, pagination) can hide while a detail is open; it is
  // inert on desktop widths where both panes stay side by side.
  function setMobilePane(view, pane) {
    const panel = find(`learning-panel-${view}`);
    const page = pageNode();
    if (!panel || !page) return;
    const previous = panel.dataset.mobilePane || 'list';
    if (pane === 'detail' && previous === 'list') {
      panel.dataset.mobileListScroll = String(page.scrollTop || 0);
    }
    panel.dataset.mobilePane = pane;
    if (pane === 'detail' && previous !== 'detail') {
      page.scrollTop = 0;
      return;
    }
    if (pane === 'list' && previous !== 'list') {
      const top = Number(panel.dataset.mobileListScroll) || 0;
      nextFrame(() => {
        if (panel.dataset.mobilePane === 'list') page.scrollTop = top;
      });
    }
  }

  function focusMobileControl(selector) {
    if (!isMobile()) return;
    nextFrame(() => root.querySelector(selector)?.focus?.({ preventScroll: true }));
  }

  // The shared index.html belongs to another workstream, so Back controls are
  // mounted through this controller into the stable layout containers.
  function ensureMobileBack(view, { id, label, layoutSelector, focusSelector }) {
    const panel = find(`learning-panel-${view}`);
    const layout = panel?.querySelector(layoutSelector);
    if (!layout || find(id)) return;
    const back = el('button', {
      class: 'learning-mobile-back',
      id,
      type: 'button',
      'aria-label': `Back to ${label}`,
    }, [
      el('i', { dataset: { lucide: 'arrow-left' }, 'aria-hidden': 'true' }),
      el('span', { text: label }),
    ]);
    back.addEventListener('click', () => {
      setMobilePane(view, 'list');
      focusMobileControl(focusSelector);
    });
    layout.prepend(back);
  }

  // On mobile the always-in-context editors collapse into compact entry
  // points. The textarea and its dirty flags stay in the DOM, so an unsaved
  // draft survives disclosure toggles and pane changes untouched.
  function initDocumentDisclosures() {
    for (const card of root.querySelectorAll('.learning-document-card')) {
      if (!card.dataset.editorOpen) card.dataset.editorOpen = 'false';
      const header = card.querySelector('.learning-document-header');
      if (!header || card.querySelector('.learning-doc-toggle')) continue;
      const toggle = el('button', {
        class: 'learning-doc-toggle',
        type: 'button',
        'aria-expanded': 'false',
        'aria-label': 'Toggle memory editor',
      }, [
        el('i', { dataset: { lucide: 'pen-line' }, 'aria-hidden': 'true' }),
        el('span', { text: 'Edit' }),
      ]);
      toggle.addEventListener('click', () => {
        const open = card.dataset.editorOpen !== 'true';
        card.dataset.editorOpen = String(open);
        toggle.setAttribute('aria-expanded', String(open));
        toggle.querySelector('span').textContent = open ? 'Done' : 'Edit';
        if (open) card.querySelector('textarea')?.focus();
      });
      header.append(toggle);
    }
  }

  // The knowledge map stays reachable on mobile behind a preview disclosure;
  // revealing it refits the graph into the now-visible frame.
  function initMapDisclosure() {
    const card = root.querySelector('.learning-map-card');
    if (!card || card.querySelector('.learning-map-toggle')) return;
    if (!card.dataset.mapOpen) card.dataset.mapOpen = 'false';
    const toggle = el('button', {
      class: 'learning-map-toggle',
      type: 'button',
      'aria-expanded': 'false',
      'aria-label': 'Toggle knowledge map preview',
    }, [
      el('i', { dataset: { lucide: 'orbit' }, 'aria-hidden': 'true' }),
      el('span', { text: 'Show map' }),
    ]);
    toggle.addEventListener('click', () => {
      const open = card.dataset.mapOpen !== 'true';
      card.dataset.mapOpen = String(open);
      toggle.setAttribute('aria-expanded', String(open));
      toggle.querySelector('span').textContent = open ? 'Hide map' : 'Show map';
      if (open) nextFrame(() => {
        learningGraph?.network?.redraw?.();
        learningGraph?.fit?.();
      });
    });
    card.querySelector('.learning-card-header')?.append(toggle);
  }

  function initMobileWorkbench() {
    for (const view of ['memory', 'experiences']) {
      const panel = find(`learning-panel-${view}`);
      if (panel && !panel.dataset.mobilePane) panel.dataset.mobilePane = 'list';
    }
    ensureMobileBack('memory', {
      id: 'learning-atlas-back',
      label: 'Atlas',
      layoutSelector: '.learning-atlas-layout',
      focusSelector: '.learning-result-item.is-selected',
    });
    ensureMobileBack('experiences', {
      id: 'learning-experience-back',
      label: 'Episodes',
      layoutSelector: '.learning-experiences-layout',
      focusSelector: '.learning-experience-item.is-selected',
    });
    initDocumentDisclosures();
    initMapDisclosure();
  }

  function setSyncState(copy, tone = '') {
    const node = find('learning-sync-state');
    if (!node) return;
    node.textContent = copy;
    node.dataset.state = tone;
  }

  function setRefreshBusy(id, busy) {
    const button = find(id);
    if (!button) return;
    button.setAttribute('aria-busy', String(busy));
    button.disabled = busy;
  }

  function setDocumentStatus(config, text, tone = '') {
    const node = find(config.statusId);
    if (!node) return;
    node.textContent = text;
    node.dataset.state = tone;
  }

  function updateDocumentMeta(config) {
    const textarea = find(config.textareaId);
    if (!textarea) return;
    const overLimit = textarea.value.length > DOCUMENT_CHAR_CAP;
    const count = find(config.countId);
    if (count) {
      count.textContent = `${textarea.value.length} / ${DOCUMENT_CHAR_CAP}`;
      count.dataset.state = overLimit ? 'over' : '';
    }
    const save = find(config.saveId);
    if (save) save.disabled = textarea.dataset.dirty !== 'true' || overLimit;
  }

  function setDocumentEditor(config, entry, { preserveDirty = true } = {}) {
    const textarea = find(config.textareaId);
    if (!textarea) return;
    if (preserveDirty && textarea.dataset.dirty === 'true') {
      updateDocumentMeta(config);
      return;
    }
    const content = String(entry?.content || '');
    textarea.value = content;
    textarea.dataset.savedContent = content;
    textarea.dataset.dirty = 'false';
    if (config === DOCUMENTS.user) state.userEntry = entry || null;
    if (config === DOCUMENTS.agent) state.agentEntry = entry || null;
    const overLimit = content.length > DOCUMENT_CHAR_CAP;
    setDocumentStatus(config, overLimit ? 'Over limit' : entry?.content ? 'Synced' : 'Empty', overLimit ? 'error' : entry?.content ? 'saved' : '');
    updateDocumentMeta(config);
  }

  async function loadMemory() {
    const token = ++memoryToken;
    setDocumentStatus(DOCUMENTS.user, 'Loading…');
    setDocumentStatus(DOCUMENTS.agent, 'Loading…');
    try {
      const response = await rpcClient('memory.list');
    if (token !== memoryToken) return;
      state.entries = resultList(response, 'entries');
      state.records = state.entries.filter(isMemoryRecord);
      state.userEntry = state.entries.find((entry) => entry.tier === 'user') || null;
      state.agentEntry = state.entries.find((entry) => entry.tier === 'agent') || null;
      setDocumentEditor(DOCUMENTS.user, state.userEntry);
      setDocumentEditor(DOCUMENTS.agent, state.agentEntry);
      paintGraphStats();
      renderSelection();
    } catch (error) {
      if (token !== memoryToken) return;
      setDocumentStatus(DOCUMENTS.user, 'Unavailable', 'error');
      setDocumentStatus(DOCUMENTS.agent, 'Unavailable', 'error');
      state.syncError = error;
      renderMemoryError(error);
    }
  }

  async function reloadDocument(config) {
    setDocumentStatus(config, 'Reloading…');
    await loadMemory();
  }

  async function saveDocument(config) {
    const textarea = find(config.textareaId);
    const save = find(config.saveId);
    if (!textarea || textarea.value.length > DOCUMENT_CHAR_CAP) {
      toast(`${config.label} cannot exceed ${DOCUMENT_CHAR_CAP} characters.`, 'error');
      return;
    }
    if (save) save.disabled = true;
    setDocumentStatus(config, 'Saving…');
    try {
      const response = await rpcClient(config.updateMethod, { content: textarea.value });
      setDocumentEditor(config, response?.entry || { tier: config === DOCUMENTS.user ? 'user' : 'agent', content: textarea.value }, { preserveDirty: false });
      setDocumentStatus(config, 'Saved', 'saved');
      toast(`${config.label} saved.`, 'success', 2200);
    } catch (error) {
      setDocumentStatus(config, 'Unsaved changes', 'error');
      updateDocumentMeta(config);
      toast(error.message || `${config.label} could not be saved.`, 'error');
    }
  }

  function renderMemoryError(error) {
    for (const id of ['learning-results', 'learning-selection']) {
      const node = find(id);
      if (!node || id !== 'learning-results') continue;
      node.replaceChildren(el('p', { class: 'learning-empty learning-error' }, [
        el('strong', { text: 'Memory unavailable' }),
        el('span', { text: error?.message || 'The memory store could not be reached.' }),
      ]));
    }
  }

  function renderGraph() {
    const empty = find('learning-graph-empty');
    if (!empty) return;
    empty.hidden = state.graphNodes.length > 0;
    renderNodeInspector();
  }

  function selectedGraphNode() {
    return state.graphNodes.find((node) => node.id === state.selectedGraphNodeId) || null;
  }

  function selectGraphNode(id) {
    state.selectedGraphNodeId = id || null;
    learningGraph?.select?.(state.selectedGraphNodeId);
    renderNodeInspector();
  }

  function renderNodeInspector() {
    const inspector = find('learning-node-inspector');
    if (!inspector) return;
    const node = selectedGraphNode();
    inspector.replaceChildren();
    inspector.hidden = !node;
    if (!node) return;
    const kind = graphNodeKind(node);
    const entry = node.kind === 'memory' ? state.entries.find((item) => item.id === node.id) : null;
    const body = memoryEntryBody(entry) || node.name || node.id;
    inspector.append(
      el('div', { class: 'learning-node-inspector-head' }, [
        el('strong', { text: oneLine(node.name || node.id, 80) }),
        el('span', { text: kind === 'profile' ? memoryTierLabel(node.tier) : kind }),
      ]),
      el('p', { text: oneLine(body, 180) }),
    );
    const actions = el('div', { class: 'learning-node-inspector-actions' });
    if (node.kind === 'memory') {
      const open = el('button', { class: 'learning-quiet-button', type: 'button', text: 'Inspect in atlas' });
      open.addEventListener('click', () => {
        switchView('memory');
        selectResult({ ...(entry || node), id: node.id, kind: 'memory', content: body, name: node.name || body });
      });
      actions.append(open);
    }
    inspector.append(actions);
  }

  function paintGraphStats(metrics) {
    const memoryStat = find('learning-stat-memory');
    if (memoryStat) {
      const count = metrics?.memoryCount ?? state.records.length;
      memoryStat.textContent = `${count} record${count === 1 ? '' : 's'}`;
    }
    const edgesStat = find('learning-stat-edges');
    if (edgesStat && metrics) {
      edgesStat.textContent = metrics.edgeCount < metrics.totalEdges
        ? `${metrics.edgeCount} of ${metrics.totalEdges} edges`
        : `${metrics.edgeCount} edge${metrics.edgeCount === 1 ? '' : 's'}`;
    }
  }

  function loadGraph({ fit = false } = {}) {
    if (graphRequest) return graphRequest;
    const token = ++graphToken;
    const empty = find('learning-graph-empty');
    if (!state.graphNodes.length && empty) {
      empty.hidden = false;
      empty.replaceChildren(
        el('i', { dataset: { lucide: 'orbit' }, 'aria-hidden': 'true' }),
        el('strong', { text: 'Building map…' }),
        el('span', { text: 'Connecting the skills and memory that are already in reach.' }),
      );
      window.lucide?.createIcons({ attrs: { 'stroke-width': 1.7 } });
    }
    const request = (async () => {
      try {
        const response = await rpcClient('learning.graph');
        if (token !== graphToken) return;
        const hadNodes = state.graphNodes.length > 0;
        state.graphNodes = resultList(response, 'nodes');
        state.graphEdges = resultList(response, 'edges');
        if (state.selectedGraphNodeId && !state.graphNodes.some((node) => node.id === state.selectedGraphNodeId)) state.selectedGraphNodeId = null;
        const metrics = learningGraph?.update(state.graphNodes, state.graphEdges, {
          preservePositions: hadNodes,
          fit,
        });
        if (metrics) paintGraphStats(metrics);
        renderGraph();
      } catch (error) {
        if (token !== graphToken) return;
        state.graphNodes = [];
        state.graphEdges = [];
        const graphEmpty = find('learning-graph-empty');
        if (graphEmpty) {
          graphEmpty.hidden = false;
          graphEmpty.replaceChildren(el('i', { dataset: { lucide: 'cloud-off' }, 'aria-hidden': 'true' }), el('strong', { text: 'Map unavailable' }), el('span', { text: error.message || 'Could not load learning connections.' }));
          window.lucide?.createIcons({ attrs: { 'stroke-width': 1.7 } });
        }
        renderNodeInspector();
      } finally {
        if (graphRequest === request) graphRequest = null;
      }
    })();
    graphRequest = request;
    return request;
  }

  function setTabState(view) {
    for (const tab of root.querySelectorAll('[data-learning-tab]')) {
      const active = tab.dataset.learningTab === view;
      tab.classList.toggle('is-active', active);
      tab.setAttribute('aria-selected', String(active));
      tab.tabIndex = active ? 0 : -1;
    }
    for (const panel of root.querySelectorAll('[data-learning-panel]')) {
      panel.hidden = panel.dataset.learningPanel !== view;
    }
  }

  function switchView(view) {
    const next = VIEWS.has(view) ? view : 'overview';
    state.view = next;
    setTabState(next);
    if (next === 'overview') {
      renderGraph();
      learningGraph?.fit?.();
    }
    if (next === 'memory' && !state.searchLoaded) void loadSearch();
    if (next === 'experiences' && !state.experiences.length) void loadExperiences();
    if (next === 'activity' && !state.activityLoaded) void loadActivity();
  }

  function activate() {
    if (refreshInFlight) return;
    if (!state.entries.length && !state.experiences.length && !state.graphNodes.length) {
      void refresh();
      return;
    }
    if (state.graphNodes.length) {
      learningGraph?.update(state.graphNodes, state.graphEdges);
      learningGraph?.fit?.();
      renderGraph();
      return;
    }
    void loadGraph();
  }

  function renderResults() {
    const list = find('learning-results');
    const count = find('learning-results-count');
    const hint = find('learning-results-hint');
    if (!list) return;
    list.replaceChildren();
    if (count) count.textContent = `${state.results.length} result${state.results.length === 1 ? '' : 's'}`;
    if (hint) hint.textContent = state.searchQuery ? `Matching “${oneLine(state.searchQuery, 42)}”` : 'Everything in reach';
    if (!state.results.length) {
      list.append(el('p', { class: 'learning-empty' }, [
        el('strong', { text: state.searchQuery ? 'Nothing matched' : 'No learning items yet' }),
        el('span', { text: state.searchQuery ? 'Try fewer words or switch the filter.' : 'Skills and structured memory will appear here.' }),
      ]));
      return;
    }
    for (const item of state.results) {
      const body = item.content || item.body || memoryEntryBody(item);
      const kind = item.kind === 'memory' ? 'memory' : 'skill';
      const row = el('button', {
        class: `learning-result-item${item.id === state.selectedResultId ? ' is-selected' : ''}`,
        type: 'button',
        dataset: { resultId: item.id, kind },
        'aria-label': `${item.name || body || item.id} — ${kind}`,
      });
      const mark = el('span', { class: 'learning-result-mark', text: kind === 'memory' ? 'M' : 'S', 'aria-hidden': 'true' });
      const copy = el('span', { class: 'learning-result-copy' });
      copy.append(
        el('strong', { text: item.name || body || item.id }),
        el('p', { text: body || 'No description available.' }),
      );
      const meta = el('span', { class: 'learning-result-meta' });
      if (kind === 'memory') meta.append(el('span', { text: memoryTierLabel(item.tier || 'record') }));
      if (item.type) meta.append(el('span', { text: item.type }));
      copy.append(meta);
      const score = Number(item.score) > 0 ? `${Math.round(Number(item.score) * 100)}%` : '';
      row.append(mark, copy, el('span', { class: 'learning-result-score', text: score }));
      row.addEventListener('click', () => selectResult(item));
      list.append(row);
    }
  }

  function selectResult(item) {
    if (!item?.id) return;
    state.selectedResultId = item.id;
    if (state.graphNodes.some((node) => node.id === item.id)) state.selectedGraphNodeId = item.id;
    setMobilePane('memory', 'detail');
    renderResults();
    renderSelection(item);
    learningGraph?.select?.(state.selectedGraphNodeId);
    renderGraph();
    focusMobileControl('#learning-atlas-back');
  }

  function selectedRecord() {
    return state.records.find((record) => record.id === state.selectedResultId) || null;
  }

  function renderSelection(item = null) {
    const selection = find('learning-selection');
    if (!selection) return;
    const selected = item || state.results.find((entry) => entry.id === state.selectedResultId) || selectedRecord();
    selection.replaceChildren();
    if (!selected) {
      setMobilePane('memory', 'list');
      selection.append(el('div', { class: 'learning-selection-empty' }, [
        el('i', { dataset: { lucide: 'scan-search' }, 'aria-hidden': 'true' }),
        el('strong', { text: 'Select a result' }),
        el('span', { text: 'The full record and its scope will appear here.' }),
      ]));
      return;
    }
    const source = state.entries.find((entry) => entry.id === selected.id);
    const merged = { ...source, ...selected };
    const kind = merged.kind === 'skill' ? 'skill' : 'memory';
    const body = merged.content || merged.body || memoryEntryBody(merged) || 'No content available.';
    const header = el('div', { class: 'learning-selection-header' }, [
      el('div', { class: 'learning-selection-heading' }, [
        el('span', { class: 'learning-card-kicker', text: kind === 'skill' ? 'Capability' : memoryTierLabel(merged.tier || 'record') }),
        el('h3', { text: merged.name || oneLine(body, 80) || merged.id }),
        el('small', { text: merged.id }),
      ]),
      el('span', { class: 'learning-type-badge', text: kind }),
    ]);
    selection.append(header, el('div', { class: 'learning-selection-body', text: body }));
    if (kind === 'memory') {
      const facts = el('div', { class: 'learning-selection-facts' });
      for (const [label, value] of [
        ['Status', merged.status || 'active'],
        ['Scope', merged.scope?.project || merged.project || merged.scope?.level || 'general'],
        ['Updated', formatLearningDate(merged.updated_at || merged.created_at)],
        ['Source', merged.source || 'learner'],
      ]) facts.append(el('div', {}, [el('span', { text: label }), el('strong', { text: value })]));
      selection.append(facts);
    }
    const actions = el('div', { class: 'learning-selection-actions' });
    if (kind === 'memory' && (merged.tier || 'record') === 'record') {
      const remove = el('button', { class: 'learning-danger-button', type: 'button', text: 'Delete record' });
      remove.addEventListener('click', () => { void deleteRecord(merged.id); });
      actions.append(remove);
    }
    if (kind === 'skill') {
      const open = el('button', { class: 'learning-quiet-button', type: 'button', text: 'Open Skills' });
      open.addEventListener('click', () => { location.hash = '#skills'; });
      actions.append(open);
    }
    if (actions.childElementCount) selection.append(actions);
  }

  async function loadSearch() {
    const token = ++searchToken;
    const list = find('learning-results');
    if (list) list.replaceChildren(el('p', { class: 'learning-empty' }, [el('strong', { text: 'Searching…' }), el('span', { text: 'Matching the atlas against the current query.' })]));
    try {
      const response = await rpcClient('learning.search', { query: state.searchQuery, kind: state.searchKind, limit: 20 });
      if (token !== searchToken) return;
      state.results = resultList(response, 'items');
      state.searchLoaded = true;
      renderResults();
    } catch (error) {
      if (token !== searchToken) return;
      state.results = [];
      state.searchLoaded = false;
      if (list) list.replaceChildren(el('p', { class: 'learning-empty learning-error' }, [el('strong', { text: 'Search unavailable' }), el('span', { text: error.message || 'Could not search learning.' })]));
    }
  }

  function renderRecentExperiences() {
    const list = find('learning-recent-list');
    if (!list) return;
    list.replaceChildren();
    if (!state.experiences.length) {
      list.append(el('p', { class: 'learning-empty' }, [el('strong', { text: 'No episodes yet' }), el('span', { text: 'Finish a conversation to give the learner something to reflect on.' })]));
      return;
    }
    for (const experience of state.experiences.slice(0, 3)) {
      const outcome = outcomeLabel(experience);
      const button = el('button', {
        class: 'learning-recent-item',
        type: 'button',
        dataset: { outcome },
        'aria-label': `Open ${experience.goal || 'untitled episode'}`,
      });
      button.append(
        el('i', { class: 'learning-recent-dot', 'aria-hidden': 'true' }),
        el('span', { class: 'learning-recent-copy' }, [el('strong', { text: experience.goal || 'Untitled episode' }), el('small', { text: summarizeSignals(experience.signals).join(' · ') || 'No special signal' })]),
        el('span', { class: 'learning-recent-time', text: formatLearningDate(experience.timestamp || experience.created_at) }),
      );
      button.addEventListener('click', () => {
        state.selectedExperienceId = experience.id;
        switchView('experiences');
        void openExperience(experience.id);
      });
      list.append(button);
    }
  }

  function renderExperiences() {
    const list = find('learning-experience-list');
    if (!list) return;
    list.replaceChildren();
    if (!state.experiences.length) {
      list.append(el('p', { class: 'learning-empty' }, [el('strong', { text: 'No episodes yet' }), el('span', { text: 'Completed conversations will show their goals and learning signals here.' })]));
    } else {
      for (const experience of state.experiences) {
        const outcome = outcomeLabel(experience);
        const item = el('button', {
          class: `learning-experience-item${experience.id === state.selectedExperienceId ? ' is-selected' : ''}`,
          type: 'button',
          dataset: { outcome },
          'aria-label': `${experience.goal || 'Untitled episode'} — ${outcome}`,
        });
        item.append(
          el('i', { class: 'learning-experience-dot', 'aria-hidden': 'true' }),
          el('span', { class: 'learning-experience-copy' }, [el('strong', { text: experience.goal || 'Untitled episode' }), el('small', { text: `${formatLearningDate(experience.timestamp || experience.created_at)} · ${summarizeSignals(experience.signals).join(' · ') || 'No special signal'}` })]),
          el('span', { class: `learning-outcome learning-outcome-${outcome}`, text: outcome }),
        );
        item.addEventListener('click', () => { void openExperience(experience.id); });
        list.append(item);
      }
    }
    updateExperiencePagination();
    renderRecentExperiences();
  }

  function updateExperiencePagination() {
    const pages = Math.max(1, Math.ceil(state.experiencesTotal / EXPERIENCE_PAGE_SIZE));
    const page = find('learning-experience-page');
    const prev = find('learning-experience-prev');
    const next = find('learning-experience-next');
    const count = find('learning-experiences-count');
    if (page) page.textContent = `Page ${state.experiencesPage + 1} of ${pages}`;
    if (prev) prev.disabled = state.experiencesPage <= 0;
    if (next) next.disabled = state.experiencesPage + 1 >= pages;
    if (count) count.textContent = `${state.experiencesTotal} episode${state.experiencesTotal === 1 ? '' : 's'}`;
  }

  async function loadExperiences() {
    const token = ++experienceToken;
    try {
      const response = await rpcClient('experience.list', { offset: state.experiencesPage * EXPERIENCE_PAGE_SIZE, limit: EXPERIENCE_PAGE_SIZE });
      if (token !== experienceToken) return;
      state.experiences = sortExperiencesNewestFirst(resultList(response, 'experiences', 'entries', 'items'));
      state.experiencesTotal = Number.isFinite(Number(response?.total)) ? Number(response.total) : state.experiences.length;
      renderExperiences();
    } catch (error) {
      if (token !== experienceToken) return;
      state.experiences = [];
      state.experiencesTotal = 0;
      const list = find('learning-experience-list');
      if (list) list.replaceChildren(el('p', { class: 'learning-empty learning-error' }, [el('strong', { text: 'Experiences unavailable' }), el('span', { text: error.message || 'Could not load recorded episodes.' })]));
    }
  }

  async function openExperience(id) {
    if (!id) return;
    // Click-to-deselect is a desktop gesture: both panes stay visible there.
    // On mobile the list is hidden while a detail is open, so a tap on the
    // still-selected episode after Back is a reopen request, not a toggle.
    if (!isMobile() && state.selectedExperienceId === id && find('learning-experience-detail')?.dataset.loadedId === id) {
      state.selectedExperienceId = null;
      renderExperiences();
      renderExperienceDetail(null);
      return;
    }
    state.selectedExperienceId = id;
    setMobilePane('experiences', 'detail');
    focusMobileControl('#learning-experience-back');
    renderExperiences();
    renderExperienceDetail({ goal: 'Loading episode…' });
    try {
      const response = await rpcClient('experience.get', { id });
      const detail = response?.experience || response?.entry || response;
      const index = state.experiences.findIndex((entry) => entry.id === id);
      if (index >= 0) state.experiences[index] = { ...state.experiences[index], ...detail };
      renderExperiences();
      renderExperienceDetail(detail);
    } catch (error) {
      toast(error.message || 'Could not load episode.', 'error');
    }
  }

  function renderExperienceDetail(experience) {
    const panel = find('learning-experience-detail');
    if (!panel) return;
    panel.replaceChildren();
    delete panel.dataset.loadedId;
    if (!experience) {
      setMobilePane('experiences', 'list');
      panel.append(el('div', { class: 'learning-selection-empty' }, [el('i', { dataset: { lucide: 'route' }, 'aria-hidden': 'true' }), el('strong', { text: 'Choose an episode' }), el('span', { text: 'Review what NusaShell noticed and tried during a past conversation.' })]));
      return;
    }
    if (experience.id) panel.dataset.loadedId = experience.id;
    const outcome = outcomeLabel(experience);
    const header = el('header', { class: 'learning-detail-header' }, [
      el('span', { class: 'learning-card-kicker', text: `Learning record · ${outcome} · ${formatLearningDate(experience.timestamp || experience.created_at)}` }),
      el('h3', { text: experience.goal || 'Untitled episode' }),
      experience.scope?.project ? el('p', { text: `Project · ${experience.scope.project}` }) : null,
    ]);
    panel.append(header);
    panel.append(el('p', { class: 'learning-detail-intro', text: 'A compact record of what NusaShell noticed and tried during this conversation. It helps the learner decide what is worth remembering for later.' }));
    const signals = summarizeSignals(experience.signals);
    if (signals.length) {
      panel.append(el('section', { class: 'learning-detail-section' }, [el('h4', { text: 'What it noticed' }), el('div', { class: 'learning-signal-list' }, signals.map((signal) => el('span', { class: 'learning-signal-chip', text: signal })))]));
    }
    const actions = Array.isArray(experience.actions) ? experience.actions : [];
    if (actions.length) {
      panel.append(el('section', { class: 'learning-detail-section' }, [el('h4', { text: 'What it tried' }), el('ul', { class: 'learning-action-list' }, actions.slice(0, 10).map((action) => el('li', { class: action.failed ? 'is-failed' : '', text: action.name || action })))]));
    }
    const observations = Array.isArray(experience.observations) ? experience.observations : [];
    if (observations.length) panel.append(el('section', { class: 'learning-detail-section' }, [el('h4', { text: 'What it learned' }), el('p', { text: observations.join('\n') })]));
    if (experience.id) {
      const footer = el('div', { class: 'learning-selection-actions' });
      const remove = el('button', { class: 'learning-danger-button', type: 'button', text: 'Delete episode' });
      remove.addEventListener('click', () => { void deleteExperience(experience.id); });
      footer.append(remove);
      panel.append(footer);
    }
  }

  // confirmDialog traps focus without restoring it; capture the invoker so a
  // cancelled delete lands back on its button and a completed delete parks on
  // the active tab landmark.
  async function deleteExperience(id) {
    const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const ok = await confirmDialog('Delete this episode?', 'The recorded experience and any learning jobs queued from it are removed permanently.', 'Delete');
    if (!ok) {
      if (trigger?.isConnected) trigger.focus();
      return;
    }
    try {
      await rpcClient('experience.delete', { id });
      state.selectedExperienceId = null;
      setMobilePane('experiences', 'list');
      if (state.experiences.length === 1 && state.experiencesPage > 0) state.experiencesPage -= 1;
      toast('Episode deleted.', 'success', 2200);
      await loadExperiences();
      find('learning-tab-experiences')?.focus();
    } catch (error) {
      toast(error.message || 'Could not delete episode.', 'error');
    }
  }

  async function deleteRecord(id) {
    const record = state.records.find((entry) => entry.id === id);
    if (!record) return;
    const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const ok = await confirmDialog('Delete this record?', 'It will leave memory retrieval and the knowledge map permanently.', 'Delete');
    if (!ok) {
      if (trigger?.isConnected) trigger.focus();
      return;
    }
    try {
      await rpcClient('memory.delete', { id });
      state.selectedResultId = null;
      state.selectedGraphNodeId = null;
      toast('Memory record deleted.', 'success', 2200);
      await Promise.all([loadMemory(), loadGraph(), loadSearch()]);
      find('learning-tab-memory')?.focus();
    } catch (error) {
      toast(error.message || 'Could not delete memory record.', 'error');
    }
  }

  function renderJobs() {
    const running = find('learning-running-pill');
    const active = state.jobs.some((job) => ['running', 'queued'].includes(String(job.status || '').toLowerCase()));
    if (running) running.hidden = !active;
  }

  function renderLog() {
    const list = find('learning-log');
    const count = find('learning-log-count');
    if (!list) return;
    list.replaceChildren();
    if (count) count.textContent = `${state.logEntries.length} event${state.logEntries.length === 1 ? '' : 's'}`;
    if (!state.logEntries.length) {
      list.append(el('p', { class: 'learning-empty' }, [el('strong', { text: 'No learning activity yet' }), el('span', { text: 'The learner will record extraction, consolidation, and graph work here.' })]));
      return;
    }
    for (const entry of state.logEntries) {
      const head = el('div', { class: 'learning-log-entry-head' }, [
        el('span', { class: 'learning-log-type', text: learningTypeLabel(entry.type) }),
        entry.status ? el('span', { class: `learning-log-status learning-log-status-${entry.status}`, text: entry.status }) : null,
        el('span', { class: 'learning-log-time', text: formatLearningDate(entry.ts) }),
      ]);
      const parts = [head];
      if (entry.conversation_id) parts.push(el('div', { class: 'learning-log-source' }, [el('span', { text: 'Source' }), el('span', { text: entry.conversation_title || 'Untitled conversation' })]));
      if (entry.status === 'error') parts.push(el('div', { class: 'learning-log-error', text: 'Background learning failed during automatic processing.' }));
      if (entry.mutations?.length) parts.push(el('div', { class: 'learning-log-outcomes' }, entry.mutations.map((mutation) => el('div', { class: 'learning-log-outcome', text: oneLine(mutation.snippet || mutation.tool || mutation.kind || 'saved', 180) }))));
      else if (entry.status === 'done') parts.push(el('div', { class: 'learning-log-nothing', text: 'Nothing to save.' }));
      const metadata = [];
      const kind = rawDetailValue(entry.detail, 'kind');
      if (kind) metadata.push(el('span', { class: 'learning-log-meta-item learning-log-meta-kind', text: `kind: ${kind}` }));
      const model = String(entry.model || entry.model_id || '').trim();
      if (model) metadata.push(el('span', { class: 'learning-log-meta-item learning-log-meta-model', text: `model: ${model}`, title: model }));
      if (metadata.length) parts.push(el('div', { class: 'learning-log-meta' }, metadata));
      const detail = Object.entries(entry.detail || {})
        .filter(([key]) => !['job_id', 'kind', 'model'].includes(key))
        .map(([key]) => `${key}: ${rawDetailValue(entry.detail, key)}`)
        .join('\n');
      if (detail) parts.push(el('div', { class: 'learning-log-detail', text: detail }));
      const actions = [];
      const conversationId = entry.llm_conversation_id || entry.llmConversationId || '';
      if (conversationId) {
        const button = el('button', {
          class: 'learning-log-open learning-log-conversation-button',
          type: 'button',
          text: 'View agent log',
          title: 'Show the background agent conversation',
          'aria-expanded': 'false',
        });
        button.dataset.learningConversationId = conversationId;
        button.addEventListener('click', () => {
          void toggleLearningTranscript(button, (id) => fetchLearningTranscript(rpcClient, id));
        });
        actions.push(button);
      }
      const jobID = rawDetailValue(entry.detail, 'job_id');
      if (jobID) {
        const remove = el('button', { class: 'learning-log-open learning-log-delete-button', type: 'button', text: 'Delete entry' });
        remove.addEventListener('click', () => { void deleteLogEntry(jobID); });
        actions.push(remove);
      }
      if (actions.length) parts.push(el('div', { class: 'learning-log-entry-actions' }, actions));
      list.append(el('article', { class: 'learning-log-entry', role: 'listitem' }, parts));
    }
  }

  function updateLogPagination() {
    const prev = find('learning-log-prev');
    const next = find('learning-log-next');
    const page = find('learning-log-page');
    if (prev) prev.disabled = state.logPage <= 0;
    if (next) next.disabled = !state.logHasMore;
    if (page) page.textContent = `Page ${state.logPage + 1}`;
  }

  async function loadActivity({ page = 0, cursor = 0 } = {}) {
    const token = ++activityToken;
    const list = find('learning-log');
    if (list) list.replaceChildren(el('p', { class: 'learning-empty' }, [el('strong', { text: 'Loading activity…' }), el('span', { text: 'Reading one bounded page of the learning trail.' })]));
    try {
      const [logResponse, jobsResponse] = await Promise.all([
        rpcClient('learning.log', { limit: LEARNING_LOG_PAGE_SIZE, cursor }),
        rpcClient('learning.jobs.list'),
      ]);
      if (token !== activityToken) return;
      state.activityLoaded = true;
      state.logEntries = resultList(logResponse, 'entries');
      state.logPage = page;
      state.logHasMore = Boolean(logResponse?.has_more && logResponse?.next_cursor);
      state.logCursors[page] = cursor;
      if (state.logHasMore) state.logCursors[page + 1] = logResponse.next_cursor;
      state.logCursors.length = page + 1 + (state.logHasMore ? 1 : 0);
      state.jobs = resultList(jobsResponse, 'jobs');
      renderJobs();
      renderLog();
      updateLogPagination();
    } catch (error) {
      if (token !== activityToken) return;
      state.jobs = [];
      const log = find('learning-log');
      if (log) log.replaceChildren(el('p', { class: 'learning-empty learning-error' }, [el('strong', { text: 'Activity unavailable' }), el('span', { text: error.message || 'Could not load the learning trail.' })]));
      renderJobs();
      updateLogPagination();
    }
  }

  async function deleteLogEntry(jobID) {
    const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const ok = await confirmDialog('Delete this activity entry?', 'The job row, learning events, and background transcript are removed permanently.', 'Delete');
    if (!ok) {
      if (trigger?.isConnected) trigger.focus();
      return;
    }
    try {
      await rpcClient('learning.log.delete', { job_id: jobID });
      toast('Activity entry deleted.', 'success', 2200);
      await loadActivity();
      find('learning-tab-activity')?.focus();
    } catch (error) {
      toast(error.message || 'Could not delete activity entry.', 'error');
    }
  }

  async function refresh() {
    if (refreshInFlight) return;
    refreshInFlight = true;
    setRefreshBusy('learning-refresh', true);
    const token = ++refreshToken;
    try {
      state.loading = true;
      state.syncError = null;
      setSyncState('Syncing…');
      const base = Promise.all([loadMemory(), loadExperiences()]);
      const background = [loadGraph()];
      if (state.searchLoaded || state.view === 'memory') background.push(loadSearch());
      if (state.activityLoaded || state.view === 'activity') background.push(loadActivity({ page: state.logPage, cursor: state.logCursors[state.logPage] || 0 }));
      await base;
      if (token !== refreshToken) return;
      await Promise.all(background);
      if (token !== refreshToken) return;
    } finally {
      refreshInFlight = false;
      state.loading = false;
      setRefreshBusy('learning-refresh', false);
      setSyncState(state.syncError ? 'Sync failed' : 'Synced', state.syncError ? 'error' : 'ready');
    }
  }

  function initDocumentEditor(config) {
    const textarea = find(config.textareaId);
    if (!textarea) return;
    textarea.addEventListener('input', () => {
      textarea.dataset.dirty = String(textarea.value !== (textarea.dataset.savedContent || ''));
      setDocumentStatus(config, textarea.dataset.dirty === 'true' ? 'Unsaved changes' : 'Synced', textarea.dataset.dirty === 'true' ? 'dirty' : 'saved');
      updateDocumentMeta(config);
    });
    find(config.reloadId)?.addEventListener('click', () => { void reloadDocument(config); });
    find(config.saveId)?.addEventListener('click', () => { void saveDocument(config); });
    updateDocumentMeta(config);
  }

  // The stats chips live in the map-card legend (production keeps them in the
  // graph toolbar); they are created here so index.html stays the shell's
  // stable markup contract.
  function ensureGraphStats() {
    const legend = root.querySelector('.learning-legend');
    if (!legend || find('learning-stat-memory')) return;
    legend.append(
      el('span', { class: 'learning-stat', id: 'learning-stat-memory', text: '0 records' }),
      el('span', { class: 'learning-stat', id: 'learning-stat-edges', text: '0 edges' }),
    );
  }

  // Draggable splitter between the atlas results pane and the selection
  // inspector. Mirrors production initSplitter(): persists the results-pane
  // width (px) under nushell:learning-split and stays out of the stacked
  // layout below 860px.
  function initAtlasSplitter() {
    const layout = root.querySelector('.learning-atlas-layout');
    const resultsPane = layout?.querySelector('.learning-results-panel');
    const selection = find('learning-selection');
    if (!layout || !resultsPane || !selection || find('learning-splitter')) return;
    const STORAGE_KEY = 'nushell:learning-split';
    const SPLITTER_W = 6;
    const isNarrow = () => typeof window.matchMedia === 'function'
      ? window.matchMedia('(max-width: 860px)').matches
      : window.innerWidth <= 860;
    const splitter = el('div', {
      class: 'learning-splitter',
      id: 'learning-splitter',
      role: 'separator',
      'aria-orientation': 'vertical',
      'aria-label': 'Resize results pane',
      title: 'Drag to resize',
      tabindex: '0',
      style: 'cursor: col-resize',
    });
    resultsPane.after(splitter);
    const clearDesktopWidth = () => {
      layout.style.removeProperty('grid-template-columns');
      splitter.hidden = true;
    };
    const applyWidth = (width) => {
      if (isNarrow()) {
        clearDesktopWidth();
        return;
      }
      splitter.hidden = false;
      layout.style.gridTemplateColumns = `${width}px ${SPLITTER_W}px minmax(0, 1.32fr)`;
    };
    const saved = Number.parseInt(localStorage.getItem(STORAGE_KEY) || '', 10);
    const applySavedWidth = () => {
      if (Number.isFinite(saved) && saved > 120 && saved < window.innerWidth - 200) applyWidth(saved);
      else if (isNarrow()) clearDesktopWidth();
      else {
        splitter.hidden = false;
        layout.style.gridTemplateColumns = `minmax(250px, .68fr) ${SPLITTER_W}px minmax(0, 1.32fr)`;
      }
    };
    applySavedWidth();
    window.addEventListener('resize', applySavedWidth, { passive: true });
    let dragging = false;
    let startX = 0;
    let startW = 0;
    splitter.addEventListener('mousedown', (event) => {
      dragging = true;
      startX = event.clientX;
      startW = resultsPane.getBoundingClientRect().width;
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
      event.preventDefault();
    });
    document.addEventListener('mousemove', (event) => {
      if (!dragging) return;
      const width = Math.max(180, Math.min(window.innerWidth - 200, startW + event.clientX - startX));
      applyWidth(width);
    });
    document.addEventListener('mouseup', () => {
      if (!dragging) return;
      dragging = false;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      localStorage.setItem(STORAGE_KEY, String(Math.round(resultsPane.getBoundingClientRect().width)));
    });
    splitter.addEventListener('keydown', (event) => {
      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
      event.preventDefault();
      const delta = event.key === 'ArrowRight' ? 16 : -16;
      const width = Math.max(180, Math.min(window.innerWidth - 200, resultsPane.getBoundingClientRect().width + delta));
      applyWidth(width);
      localStorage.setItem(STORAGE_KEY, String(Math.round(resultsPane.getBoundingClientRect().width)));
    });
  }

  function init() {
    if (initialized) return controller;
    initialized = true;
    initDocumentEditor(DOCUMENTS.user);
    initDocumentEditor(DOCUMENTS.agent);
    initMobileWorkbench();
    ensureGraphStats();
    initAtlasSplitter();
    learningGraph = graphFactory(find('learning-graph-canvas'), {
      onSelect: (node) => {
        state.selectedGraphNodeId = node?.id || null;
        renderNodeInspector();
      },
    });
    const searchForm = find('learning-search-form');
    const search = find('learning-search-input');
    searchForm?.addEventListener('submit', (event) => {
      event.preventDefault();
      state.searchQuery = search?.value.trim() || '';
      void loadSearch();
    });
    search?.addEventListener('input', debounce(() => {
      state.searchQuery = search.value.trim();
      void loadSearch();
    }, 180));
    for (const button of root.querySelectorAll('[data-learning-kind]')) {
      button.addEventListener('click', () => {
        state.searchKind = button.dataset.learningKind || '';
        for (const candidate of root.querySelectorAll('[data-learning-kind]')) {
          const active = candidate === button;
          candidate.classList.toggle('is-active', active);
          candidate.setAttribute('aria-pressed', String(active));
        }
        void loadSearch();
      });
    }
    for (const tab of root.querySelectorAll('[data-learning-tab]')) tab.addEventListener('click', () => switchView(tab.dataset.learningTab));
    bindTablistKeyboard(root.querySelector('.learning-tabs'));
    find('learning-refresh')?.addEventListener('click', () => { void refresh(); });
    find('learning-open-atlas')?.addEventListener('click', () => switchView('memory'));
    find('learning-open-experiences')?.addEventListener('click', () => switchView('experiences'));
    find('learning-experience-prev')?.addEventListener('click', () => {
      if (state.experiencesPage <= 0) return;
      state.experiencesPage -= 1;
      void loadExperiences();
    });
    find('learning-experience-next')?.addEventListener('click', () => {
      const pages = Math.max(1, Math.ceil(state.experiencesTotal / EXPERIENCE_PAGE_SIZE));
      if (state.experiencesPage + 1 >= pages) return;
      state.experiencesPage += 1;
      void loadExperiences();
    });
    find('learning-log-prev')?.addEventListener('click', () => {
      if (state.logPage <= 0) return;
      void loadActivity({ page: state.logPage - 1, cursor: state.logCursors[state.logPage - 1] || 0 });
    });
    find('learning-log-next')?.addEventListener('click', () => {
      if (!state.logHasMore) return;
      void loadActivity({ page: state.logPage + 1, cursor: state.logCursors[state.logPage + 1] || 0 });
    });

    const subscribe = (eventName, handler) => {
      const unsubscribe = onEvent(eventName, handler);
      if (typeof unsubscribe === 'function') eventUnsubscribers.push(unsubscribe);
    };
    subscribe('memory.updated', () => { void loadMemory(); void loadGraph(); if (state.searchLoaded) void loadSearch(); });
    subscribe('skill.updated', () => { void loadGraph(); if (state.searchLoaded) void loadSearch(); });
    subscribe('experience.recorded', () => { void loadExperiences(); });
    subscribe('experience.deleted', () => { void loadExperiences(); });
    subscribe('learning.job.started', () => { if (state.activityLoaded) void loadActivity({ page: state.logPage, cursor: state.logCursors[state.logPage] || 0 }); });
    subscribe('learning.job.done', () => { void loadGraph(); void loadMemory(); void loadExperiences(); if (state.activityLoaded) void loadActivity({ page: state.logPage, cursor: state.logCursors[state.logPage] || 0 }); });
    subscribe('learning.job.error', () => { if (state.activityLoaded) void loadActivity({ page: state.logPage, cursor: state.logCursors[state.logPage] || 0 }); });
    // The edge builder runs async after learning.graph reads: its completion
    // event is the only signal that refreshes the map with the new edges.
    subscribe('learning.graph.updated', () => { void loadGraph(); });
    setTabState(state.view);
    renderNodeInspector();
    renderRecentExperiences();
    renderExperiences();
    renderSelection();
    renderJobs();
    return controller;
  }

  function destroy() {
    for (const unsubscribe of eventUnsubscribers.splice(0)) unsubscribe();
    learningGraph?.destroy?.();
    learningGraph = null;
    initialized = false;
  }

  const controller = { init, activate, refresh, switchView, selectResult, selectGraphNode, destroy, state };
  return controller;
}
