// Learning workspace: search + memory list + knowledge graph + autolearn log.
// Uses vis-network for graph rendering (vendored ESM standalone build).

import { rpc, on, off } from '../rpc.js';
import { el, debounce, createSelect, toast, fmtTime, confirmDialog } from '../ui.js';
import { resolvedFontFamily } from '../font-preferences.js';
// A learning job's LLM run is persisted as a background conversation, so the
// log renders it with the Agent view's own transcript renderer. Reusing it
// beats keeping a parallel renderer here: the two used to drift, and the
// drift is invisible until one of them renders a message wrong.
import { renderConversation } from './agent/render.js';
import { DataSet, Network } from '../../vendor/vis-network/vis-network.esm.min.js';

const state = {
  results: [],
  network: null,
  nodes: null,
  edges: null,
  memoryCount: 0,
  edgeCount: 0,
  logEntries: [],
  logLoaded: false,
  runningJobs: 0,
  catalogLoaded: false,
  experiences: [],
  records: [],
  selectedRecordId: null,
  selectedExperienceId: null,
  learningEventHandlers: null, // cleanup funcs for memory/skill/job listeners
  graphRefreshTimer: null, // debounce timer coalescing background graph refreshes
  userMemory: null,
  userLoaded: false,
  agentMemory: null,
  agentLoaded: false,
};

const MEMORY_EDITORS = Object.freeze({
  user: Object.freeze({
    textareaId: 'learning-user-memory',
    reloadId: 'learning-user-reload',
    saveId: 'learning-user-save',
    statusId: 'learning-user-status',
    countId: 'learning-user-count',
    tier: 'user',
    memoryKey: 'userMemory',
    loadedKey: 'userLoaded',
    updateMethod: 'memory.user.update',
    label: 'User memory',
    capMessage: 'User memory cannot exceed 4000 characters.',
    loadError: 'User memory could not be loaded.',
    savedMessage: 'User memory saved.',
  }),
  agent: Object.freeze({
    textareaId: 'learning-agent-memory',
    reloadId: 'learning-agent-reload',
    saveId: 'learning-agent-save',
    statusId: 'learning-agent-status',
    countId: 'learning-agent-count',
    tier: 'agent',
    memoryKey: 'agentMemory',
    loadedKey: 'agentLoaded',
    updateMethod: 'memory.agent.update',
    label: 'Soul memory',
    capMessage: 'Soul memory cannot exceed 4000 characters.',
    loadError: 'Soul memory could not be loaded.',
    savedMessage: 'Soul memory saved.',
  }),
});

export async function initLearning() {
  const input = document.getElementById('learning-search-input');
  const kindFilter = document.getElementById('learning-kind-filter');
  const searchBtn = document.getElementById('learning-search-btn');
  const refreshBtn = document.getElementById('learning-graph-refresh');
  const fitBtn = document.getElementById('learning-graph-fit');
  const logRefreshBtn = document.getElementById('learning-log-refresh');

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
  // A manual refresh is also an explicit re-layout request. Background
  // refreshes keep established positions, but this control must be able to
  // repair a cramped layout instead of pinning the same bad coordinates.
  refreshBtn.addEventListener('click', () => loadGraph({ preservePositions: false }));
  fitBtn.addEventListener('click', () => {
    fitGraphToView(state.network, state.nodes, 300);
  });
  logRefreshBtn.addEventListener('click', () => loadLog());
  initMemoryDocumentEditor(MEMORY_EDITORS.user);
  initMemoryDocumentEditor(MEMORY_EDITORS.agent);

  initTabs();
  initSplitter();
  initLearningUpdateListeners();
  initCatalogActions();

  await loadStats();
  initGraph();
  // Initial search: empty query lists all skills + records so the results
  // pane is populated immediately (backend returns an unfiltered listing
  // for empty queries). The graph loads in parallel. The learning log
  // loads lazily on first tab switch to keep init light.
  await Promise.all([doSearch(), loadGraph({ preservePositions: false })]);
}

// Tab switching between the memory document editors, memory graph, and Learning log.
// The log is fetched lazily on first open so switching back and forth does
// not spam the trajectory file read.
function initTabs() {
  const tabs = document.querySelectorAll('[data-learning-tab]');
  const panels = document.querySelectorAll('[data-learning-panel]');
  for (const tab of tabs) {
    tab.addEventListener('click', () => {
      const target = tab.dataset.learningTab;
      tabs.forEach((t) => {
        const active = t === tab;
        t.classList.toggle('active', active);
        t.setAttribute('aria-selected', String(active));
      });
      panels.forEach((panel) => {
        panel.hidden = panel.dataset.learningPanel !== target;
      });
      if (target === 'about' && !state.userLoaded) void loadStats();
      if (target === 'agent' && !state.agentLoaded) void loadStats();
      if (target === 'experience') {
        void loadCatalog();
      }
      if (target === 'log') {
        loadLog();
        // The graph canvas was possibly hidden while the other panel was
        // active; re-fit after layout settles.
        setTimeout(() => { if (state.network) state.network.fit({ animation: { duration: 200 } }); }, 60);
      }
    });
  }
}

// A single debounced graph refresh coalesces bursts (a job that writes
// several records, or job.done landing right after memory.updated) so the
// layout is not restarted repeatedly.
let graphRefreshTimer = null;
function scheduleGraphRefresh() {
  if (graphRefreshTimer) clearTimeout(graphRefreshTimer);
  graphRefreshTimer = setTimeout(() => {
    graphRefreshTimer = null;
    loadGraph();
  }, 300);
}

// Real-time learning status: jobs refresh the catalog, log, search, and
// graph. Experience recording only needs the experience list. Memory
// updates refresh documents, records, search, and graph.
function initLearningUpdateListeners() {
  if (state.learningEventHandlers) return;
  const onMemoryUpdated = () => {
    loadStats();
    scheduleGraphRefresh();
    doSearch();
  };
  const onSkillUpdated = () => {
    scheduleGraphRefresh();
    doSearch();
  };
  const onExperienceRecorded = () => {
    void loadExperiences();
  };
  const onJobStarted = () => {
    state.runningJobs += 1;
    showRunningIndicator();
    if (state.logLoaded) loadLog();
  };
  const onJobDone = () => {
    state.runningJobs = Math.max(0, state.runningJobs - 1);
    if (state.runningJobs === 0) hideRunningIndicator();
    if (state.logLoaded) loadLog();
    loadStats();
    void loadExperiences();
    scheduleGraphRefresh();
    doSearch();
  };
  const onJobError = () => {
    state.runningJobs = Math.max(0, state.runningJobs - 1);
    if (state.runningJobs === 0) hideRunningIndicator();
    if (state.logLoaded) loadLog();
  };
  on('memory.updated', onMemoryUpdated);
  on('skill.updated', onSkillUpdated);
  on('experience.recorded', onExperienceRecorded);
  on('learning.job.started', onJobStarted);
  on('learning.job.done', onJobDone);
  on('learning.job.error', onJobError);
  state.learningEventHandlers = [
    () => off('memory.updated', onMemoryUpdated),
    () => off('skill.updated', onSkillUpdated),
    () => off('experience.recorded', onExperienceRecorded),
    () => off('learning.job.started', onJobStarted),
    () => off('learning.job.done', onJobDone),
    () => off('learning.job.error', onJobError),
    () => { if (graphRefreshTimer) clearTimeout(graphRefreshTimer); },
  ];
}

function initMemoryDocumentEditor(config) {
  const textarea = document.getElementById(config.textareaId);
  const reloadBtn = document.getElementById(config.reloadId);
  const saveBtn = document.getElementById(config.saveId);
  if (!textarea || !reloadBtn || !saveBtn) return;
  textarea.addEventListener('input', () => {
    textarea.dataset.dirty = String(textarea.value !== (textarea.dataset.savedContent || ''));
    updateMemoryDocumentMeta(config, textarea);
  });
  // Reload is an explicit user action: it discards the current draft and
  // reads the persisted document value again. No native confirm dialog is
  // used because the shell keeps destructive confirmation in its own UI.
  reloadBtn.addEventListener('click', () => { void loadMemoryDocument(config); });
  saveBtn.addEventListener('click', () => { void saveMemoryDocument(config); });
  updateMemoryDocumentMeta(config, textarea);
}

function updateMemoryDocumentMeta(config, textarea) {
  const count = document.getElementById(config.countId);
  if (count) count.textContent = `${textarea.value.length} / 4000 characters`;
  const save = document.getElementById(config.saveId);
  if (save) save.disabled = textarea.dataset.dirty !== 'true';
}

function setMemoryDocumentEditor(config, entry, { preserveDirty = true } = {}) {
  const textarea = document.getElementById(config.textareaId);
  if (!textarea) return;
  if (preserveDirty && textarea.dataset.dirty === 'true') return;
  const content = String(entry?.content || '');
  textarea.value = content;
  textarea.dataset.savedContent = content;
  textarea.dataset.dirty = 'false';
  state[config.memoryKey] = entry || null;
  state[config.loadedKey] = true;
  const status = document.getElementById(config.statusId);
  if (status) status.textContent = entry?.content ? 'Saved locally' : 'Empty';
  updateMemoryDocumentMeta(config, textarea);
}

async function loadMemoryDocument(config) {
  const status = document.getElementById(config.statusId);
  if (status) status.textContent = 'Loading…';
  try {
    const { entries } = await rpc('memory.list');
    const entry = (entries || []).find((item) => item.tier === config.tier) || null;
    setMemoryDocumentEditor(config, entry, { preserveDirty: false });
  } catch (e) {
    state[config.loadedKey] = false;
    if (status) status.textContent = 'Unavailable';
    toast(e.message || config.loadError, 'error');
  }
}

async function saveMemoryDocument(config) {
  const textarea = document.getElementById(config.textareaId);
  const saveBtn = document.getElementById(config.saveId);
  const status = document.getElementById(config.statusId);
  if (!textarea || !saveBtn) return;
  if (textarea.value.length > 4000) {
    toast(config.capMessage, 'error');
    return;
  }
  saveBtn.disabled = true;
  if (status) status.textContent = 'Saving…';
  try {
    const result = await rpc(config.updateMethod, { content: textarea.value });
    setMemoryDocumentEditor(config, result.entry || { tier: config.tier, content: textarea.value }, { preserveDirty: false });
    toast(config.savedMessage, 'success');
  } catch (e) {
    if (status) status.textContent = 'Unsaved changes';
    updateMemoryDocumentMeta(config, textarea);
    toast(e.message || `${config.label} could not be saved.`, 'error');
  }
}

function showRunningIndicator() {
  const logEl = document.getElementById('learning-log');
  if (!logEl) return;
  let indicator = document.getElementById('learning-log-running');
  if (!indicator) {
    indicator = el('div', { id: 'learning-log-running', class: 'learning-log-running' }, [
      el('span', { class: 'learning-log-running-dot' }),
      el('span', { text: 'Learning job running…' }),
    ]);
    logEl.insertBefore(indicator, logEl.firstChild);
  }
}

function hideRunningIndicator() {
  const indicator = document.getElementById('learning-log-running');
  if (indicator) indicator.remove();
}

function isDocumentTier(tier) {
  const value = String(tier || '').toLowerCase();
  return value === 'user' || value === 'agent';
}

function isMemoryRecord(entry) {
  if (!entry) return false;
  if (isDocumentTier(entry.tier)) return false;
  const tier = String(entry.tier || '').toLowerCase();
  return tier === 'record' || tier === 'records' || entry.body != null || entry.type != null;
}

function listFrom(res, ...keys) {
  for (const key of keys) {
    if (Array.isArray(res?.[key])) return res[key];
  }
  if (Array.isArray(res)) return res;
  return [];
}

function initCatalogActions() {
  const retireBtn = document.getElementById('learning-record-retire');
  if (retireBtn) {
    retireBtn.addEventListener('click', () => {
      if (state.selectedRecordId) void retireRecord(state.selectedRecordId);
    });
  }
}

async function loadCatalog() {
  state.catalogLoaded = true;
  await Promise.all([loadExperiences(), loadStats()]);
}

async function loadExperiences() {
  const listEl = document.getElementById('learning-experience-list');
  const countEl = document.getElementById('learning-experience-count');
  if (!listEl) return;
  try {
    const res = await rpc('experience.list');
    state.experiences = listFrom(res, 'experiences', 'entries', 'items');
    renderExperiences();
    if (countEl) countEl.textContent = String(state.experiences.length);
  } catch (e) {
    state.experiences = [];
    listEl.innerHTML = '';
    listEl.appendChild(el('div', { class: 'learning-empty' }, [
      el('strong', { text: 'Experience unavailable' }),
      el('span', { text: e.message || 'Could not load experience.list.' }),
    ]));
    if (countEl) countEl.textContent = '0';
  }
}

function signalChips(signals) {
  if (!signals || typeof signals !== 'object') return [];
  const chips = [];
  const push = (ok, label) => { if (ok) chips.push(el('span', { class: 'learning-signal', text: label })); };
  push(signals.explicit_teaching, 'teaching');
  push(Number(signals.user_corrections) > 0, `corrections ${signals.user_corrections}`);
  push(Number(signals.retries) > 0, `retries ${signals.retries}`);
  push(Number(signals.failed_actions) > 0, `failed ${signals.failed_actions}`);
  push(signals.verified_success, 'verified');
  push(signals.novel_workflow, 'novel');
  push(signals.root_cause_recovered, 'recovered');
  return chips;
}

function renderExperiences() {
  const listEl = document.getElementById('learning-experience-list');
  if (!listEl) return;
  listEl.innerHTML = '';
  if (state.experiences.length === 0) {
    listEl.appendChild(el('div', { class: 'learning-empty' }, [
      el('strong', { text: 'No experience yet' }),
      el('span', { text: 'Completed conversations record a goal, outcome, and signals here. Episodes are read-only.' }),
    ]));
    return;
  }
  for (const exp of state.experiences) {
    const selected = exp.id === state.selectedExperienceId;
    const outcome = exp.outcome?.status || (typeof exp.outcome === 'string' ? exp.outcome : 'unknown');
    const card = el('button', {
      class: `learning-catalog-card${selected ? ' active' : ''}`,
      type: 'button',
      'data-id': exp.id,
    }, [
      el('div', { class: 'learning-catalog-card-head' }, [
        el('span', { class: `learning-outcome learning-outcome-${outcome}`, text: String(outcome) }),
        el('span', { class: 'learning-catalog-time', text: fmtTime(exp.timestamp || exp.created_at) }),
      ]),
      el('div', { class: 'learning-catalog-title', text: exp.goal || 'Untitled episode' }),
      el('div', { class: 'learning-catalog-signals' }, signalChips(exp.signals)),
    ]);
    card.addEventListener('click', () => { void openExperience(exp.id); });
    listEl.appendChild(card);
    if (selected && exp._detail) {
      listEl.appendChild(renderExperienceDetail(exp._detail));
    }
  }
}

function renderExperienceDetail(exp) {
  const observations = (exp.observations || []).slice(0, 4);
  const actions = (exp.actions || []).slice(0, 6);
  return el('div', { class: 'learning-experience-detail' }, [
    exp.scope?.project ? el('div', { class: 'learning-catalog-meta', text: `Project ${exp.scope.project}` }) : null,
    actions.length ? el('div', { class: 'learning-catalog-meta', text: `Actions: ${actions.map((a) => a.name || a).join(' → ')}` }) : null,
    observations.length ? el('div', { class: 'learning-catalog-meta', text: observations.join(' · ') }) : null,
  ]);
}

async function openExperience(id) {
  if (state.selectedExperienceId === id) {
    state.selectedExperienceId = null;
    renderExperiences();
    return;
  }
  state.selectedExperienceId = id;
  renderExperiences();
  try {
    const res = await rpc('experience.get', { id });
    const detail = res.experience || res.entry || res;
    const idx = state.experiences.findIndex((item) => item.id === id);
    if (idx >= 0) state.experiences[idx] = { ...state.experiences[idx], ...detail, _detail: detail };
    renderExperiences();
  } catch (e) {
    toast(e.message || 'Could not load experience.', 'error');
  }
}

function recordBody(entry) {
  return entry.body || [entry.subject, entry.predicate, entry.object].filter(Boolean).join(' ') || entry.content || '';
}

function renderRecords() {
  const listEl = document.getElementById('learning-records-list');
  const retireBtn = document.getElementById('learning-record-retire');
  if (!listEl) return;
  listEl.innerHTML = '';
  const records = state.records;
  if (records.length === 0) {
    listEl.appendChild(el('div', { class: 'learning-empty' }, [
      el('strong', { text: 'No memory records' }),
      el('span', { text: 'Structured facts, preferences, and constraints appear here. Retire a record to keep it off retrieval without deleting the audit trail.' }),
    ]));
    if (retireBtn) retireBtn.disabled = true;
    return;
  }
  for (const rec of records) {
    const selected = rec.id === state.selectedRecordId;
    const status = rec.status || 'learned';
    const retired = status === 'retired';
    const card = el('button', {
      class: `learning-catalog-card${selected ? ' active' : ''}${retired ? ' retired' : ''}`,
      type: 'button',
      'data-id': rec.id,
    }, [
      el('div', { class: 'learning-catalog-card-head' }, [
        rec.type ? el('span', { class: 'learning-result-kind learning-kind-memory', text: rec.type }) : null,
        el('span', { class: `learning-record-status learning-record-status-${status}`, text: status }),
        el('span', { class: 'learning-catalog-time', text: fmtTime(rec.updated_at || rec.created_at) }),
      ]),
      el('div', { class: 'learning-catalog-title', text: recordBody(rec) || rec.id }),
      rec.scope?.level || rec.scope?.project
        ? el('div', { class: 'learning-catalog-meta', text: [rec.scope.level, rec.scope.project, rec.scope.repo].filter(Boolean).join(' · ') })
        : null,
    ]);
    card.addEventListener('click', () => {
      state.selectedRecordId = rec.id;
      renderRecords();
    });
    listEl.appendChild(card);
  }
  if (retireBtn) {
    const selected = records.find((r) => r.id === state.selectedRecordId);
    retireBtn.disabled = !selected || selected.status === 'retired';
  }
}

async function retireRecord(id) {
  const rec = state.records.find((r) => r.id === id);
  if (!rec || rec.status === 'retired') return;
  const ok = await confirmDialog('Retire this record?', 'It stays on disk for audit but is excluded from search and agent context.', 'Retire');
  if (!ok) return;
  try {
    await rpc('memory.retire', { id });
    toast('Memory record retired.', 'success', 2000);
    state.selectedRecordId = null;
    await loadStats();
    doSearch();
    loadGraph();
  } catch (e) {
    toast(e.message || 'Failed to retire memory record.', 'error', 4000);
  }
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
  const isNarrow = () => typeof window !== 'undefined'
    && (typeof window.matchMedia === 'function'
      ? window.matchMedia('(max-width: 900px)').matches
      : window.innerWidth <= 900);
  const clearDesktopWidth = () => workspace.style.removeProperty('grid-template-columns');
  const applyWidth = (w) => {
    if (isNarrow()) {
      clearDesktopWidth();
      return;
    }
    workspace.style.gridTemplateColumns = `${w}px ${SPLITTER_W}px minmax(0, 1fr)`;
  };

  const saved = Number.parseInt(localStorage.getItem(STORAGE_KEY) || '', 10);
  const applySavedWidth = () => {
    if (saved > 120 && saved < window.innerWidth - 200) applyWidth(saved);
    else if (isNarrow()) clearDesktopWidth();
  };
  applySavedWidth();
  window.addEventListener('resize', applySavedWidth, { passive: true });

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
  await loadExperiences();
  await loadGraph();
  // Refresh the log too — but only if it has been opened at least once
  // (lazy-loading keeps init light).
  if (state.logLoaded) loadLog();
}

// ---- Learning log (autolearn trajectory) ----

async function loadLog() {
  const logEl = document.getElementById('learning-log');
  if (!logEl) return;
  const countEl = document.getElementById('learning-log-count');
  state.logLoaded = true;
  logEl.innerHTML = '';
  logEl.appendChild(el('div', { class: 'learning-empty' }, [
    el('strong', { text: 'Loading…' }),
    el('span', { text: 'Reading the autolearn trajectory.' }),
  ]));
  try {
    const res = await rpc('learning.log', { limit: 200 });
    state.logEntries = res.entries || [];
    renderLog();
    countEl.textContent = `${state.logEntries.length} event${state.logEntries.length === 1 ? '' : 's'}`;
  } catch (e) {
    logEl.innerHTML = '';
    logEl.appendChild(el('div', { class: 'learning-empty' }, [
      el('strong', { text: 'Log unavailable' }),
      el('span', { text: e.message || 'Unknown error' }),
    ]));
    countEl.textContent = '0 events';
  }
}

function renderLog() {
  const logEl = document.getElementById('learning-log');
  logEl.innerHTML = '';
  if (state.logEntries.length === 0) {
    logEl.appendChild(el('div', { class: 'learning-empty' }, [
      el('strong', { text: 'No learning activity yet' }),
      el('span', { text: 'Signal-based learning jobs record consolidations and skill updates here.' }),
    ]));
    return;
  }
  for (const entry of state.logEntries) {
    logEl.appendChild(renderLogEntry(entry));
  }
}

// fetchLLMTranscript loads the messages of the background conversation that
// holds a learning job's LLM run.
async function fetchLLMTranscript(convID) {
  const res = await rpc('agent.conversations.get', { id: convID });
  return res.messages || [];
}

// toggleLearningTranscript loads the background conversation holding a
// learning job's LLM run and renders it inline below the log entry. It is a
// persisted agent transcript, so the Agent view's renderer draws it: the
// packet the model was shown, its tool rounds, and its final answer. That
// packet is the useful part — it is what explains why a job saved what it
// saved. Repeated clicks collapse the panel again.
//
// load is the fetch seam; tests inject a stub so the click path is testable
// without a server.
export async function toggleLearningTranscript(btn, load = fetchLLMTranscript) {
  const entry = btn.closest('.learning-log-entry');
  if (!entry) return;
  const existing = entry.querySelector('.learning-log-activity');
  if (existing) {
    existing.remove();
    btn.textContent = 'View LLM log';
    return;
  }
  const convID = btn.dataset.llmConversationId || '';
  if (!convID) return;
  const label = btn.textContent;
  btn.textContent = 'Loading…';
  btn.disabled = true;
  const panel = el('div', { class: 'learning-log-activity' });
  try {
    panel.appendChild(renderConversation(await load(convID)));
    entry.appendChild(panel);
    btn.textContent = 'Hide LLM log';
  } catch (e) {
    // A missing transcript is not exceptional: conversations can be pruned
    // while the job itself still happened. Say so instead of failing
    // silently, which is exactly how the old button behaved.
    panel.appendChild(el('div', { class: 'learning-log-error' }, [
      el('span', { text: e?.message ? `LLM log unavailable: ${e.message}` : 'LLM log unavailable.' }),
    ]));
    entry.appendChild(panel);
    btn.textContent = label;
  } finally {
    btn.disabled = false;
  }
}

export function renderLogEntry(entry) {
  const headChildren = [
    el('span', { class: `learning-log-type learning-type-${entry.type}`, text: typeLabel(entry.type) }),
  ];
	// Status badge: job entries may carry a status (done|error|skipped).
  if (entry.status) {
    headChildren.push(el('span', {
      class: `learning-log-status learning-log-status-${entry.status}`,
      text: entry.status,
    }));
  }
  headChildren.push(el('span', { class: 'learning-log-time', text: fmtTime(entry.ts) }));
  const head = el('div', { class: 'learning-log-entry-head' }, headChildren);

  const parts = [head];

  // Job failures are automatic/background work. Keep the Learning log
  // concise without leaking a provider's verbose error body; the raw
  // diagnostic remains in the backend log/trajectory for diagnosis.
  if (entry.status === 'error') {
    const label = entry.type === 'review' ? 'review' : 'learning job';
    parts.push(el('div', { class: 'learning-log-error', text: `Background ${label} failed during automatic processing.` }));
  }
  if (entry.status === 'skipped') {
    const reason = entry.detail?.reason || 'deferred';
    const messages = {
      already_running: 'Coalesced: another learning job is already running.',
      cooldown_active: 'Deferred: retry cooldown is active.',
    };
    parts.push(el('div', { class: 'learning-log-skipped', text: messages[reason] || `Deferred: ${reason}.` }));
  }

  // Review-model resolution: one short human sentence instead of the raw
  // detail dump. "requested" is a provider-scoped id (prov_xxx:model) that
  // means nothing to users, so it is reduced to the bare model name.
  if (entry.type === 'review_model') {
    const d = entry.detail || {};
    // Note: handleLearningLog lifts detail.status into entry.Status, so the
    // outcome is read from entry.status, not d.status.
    if (entry.status === 'ok' && d.resolved) {
      parts.push(el('div', { class: 'learning-log-skipped', text: `Learning jobs run on the override model “${d.resolved}”.` }));
    } else {
      const bare = String(d.requested || '').split(':').pop();
      const hint = bare ? ` (configured override: ${bare})` : '';
      parts.push(el('div', { class: 'learning-log-error', text: `Learning-job model override failed — jobs fell back to the conversation model${hint}.` }));
    }
    return el('div', { class: 'learning-log-entry' }, parts);
  }

  // Source conversation as a compact "Source <title>" line. The raw
  // conversation id and the replayed transcript text do not belong in the
  // activity digest — the log is about what learning jobs stored.
  if (entry.conversation_id) {
    parts.push(el('div', { class: 'learning-log-conv' }, [
      el('span', { class: 'learning-log-conv-label', text: 'Source' }),
      el('span', { class: 'learning-log-conv-title', text: entry.conversation_title || 'Untitled conversation' }),
    ]));
  }

  // Jobs that ran a model expose the conversation holding its transcript, so
  // the entry can open that exact LLM log. Entries recorded before the
  // transcript existed (legacy review ids) have nothing to open: they render
  // no button rather than a dead one.
  const llmConversationID = entry.llm_conversation_id || '';
  if (llmConversationID) {
    const btn = el('button', {
      class: 'learning-log-open',
      type: 'button',
      text: 'View LLM log',
      title: 'Show the background model transcript',
    });
    btn.dataset.llmConversationId = llmConversationID;
    btn.addEventListener('click', () => { void toggleLearningTranscript(btn); });
    parts.push(el('div', { class: 'learning-log-transcript-row' }, [btn]));
  }

  // Saved outcomes: one compact line per mutation so the feed shows what
  // the job actually stored. A finished job with zero mutations says
  // so explicitly instead of leaving a silent gap.
  if (entry.mutations && entry.mutations.length > 0) {
    const saves = el('div', { class: 'learning-log-saves' });
    for (const m of entry.mutations) {
      saves.appendChild(el('div', { class: 'learning-log-outcome-row' }, [
        el('span', { class: `learning-mut-kind learning-mut-kind-${m.kind}`, text: m.kind }),
        el('span', { class: 'learning-log-save-snippet', text: oneLine(m.snippet || m.tool || 'saved', 180) }),
      ]));
    }
    parts.push(saves);
  } else if (entry.status === 'done') {
    parts.push(el('div', { class: 'learning-log-nothing', text: 'Nothing to save.' }));
  }

  // Per-type extras (e.g. prune/decay counts, consolidation merges).
  const extras = detailText(entry);
  if (extras) {
    parts.push(el('div', { class: 'learning-log-detail', text: extras }));
  }

  return el('div', { class: 'learning-log-entry' }, parts);
}

function typeLabel(type) {
  switch (type) {
    case 'review': return 'Learning job';
    case 'extract': return 'Extraction';
    case 'edge_build': return 'Edge build';
    case 'consolidate': return 'Consolidation';
    case 'skill_evolve': return 'Skill evolved';
    case 'skill_promote': return 'Skill promoted';
    case 'decay': return 'Decay';
    case 'prune': return 'Prune';
    case 'review_model': return 'Learning model';
    default: return type.replace(/_/g, ' ');
  }
}

// oneLine collapses a string to a single trimmed line capped at maxLen.
function oneLine(s, maxLen = 180) {
  const line = String(s || '').replace(/\s+/g, ' ').trim();
  return line.length > maxLen ? `${line.slice(0, maxLen)}…` : line;
}

// detailText renders non-conversation detail fields (numbers, counts) so
// the log entry stays scannable. The conversation and mutations fields
// are rendered separately.
function detailText(entry) {
  if (!entry.detail) return '';
  const lines = [];
  for (const [key, value] of Object.entries(entry.detail)) {
    if (key === 'conversation' || key === 'mutations') continue;
    if (value !== null && typeof value === 'object') {
      for (const [k, v] of Object.entries(value)) {
        lines.push(`${k}: ${v}`);
      }
    } else {
      lines.push(`${key}: ${value}`);
    }
  }
  return lines.join('\n');
}

async function loadStats() {
  try {
    const { entries } = await rpc('memory.list');
    const all = entries || [];
    const records = all.filter(isMemoryRecord);
    state.records = records;
    state.memoryCount = records.length;
    document.getElementById('learning-stat-memory').textContent =
      `${state.memoryCount} record${state.memoryCount === 1 ? '' : 's'}`;
    const user = all.find((entry) => entry.tier === 'user') || null;
    const agent = all.find((entry) => entry.tier === 'agent') || null;
    setMemoryDocumentEditor(MEMORY_EDITORS.user, user);
    setMemoryDocumentEditor(MEMORY_EDITORS.agent, agent);
    renderRecords();
  } catch (e) {
    // memory.list might fail if store not initialized
    state.userLoaded = false;
    state.agentLoaded = false;
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

function memoryTierLabel(tier) {
  if (tier === 'user') return 'User memory';
  if (tier === 'agent') return 'Soul memory';
  return 'Record';
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
    const text = item.content || item.body || '';
    const isLong = text && text.length > 200;
    const contentEl = text
      ? el('div', { class: 'learning-result-content collapsed', text })
      : null;
    if (isLong && contentEl) {
      contentEl.title = 'Click to expand/collapse';
      contentEl.addEventListener('click', (e) => {
        e.stopPropagation();
        contentEl.classList.toggle('collapsed');
        contentEl.classList.toggle('expanded');
      });
    }
    const score = item.score && item.score > 0 ? `★ ${(item.score * 100).toFixed(1)}%` : '';
    const headerRight = el('div', { class: 'learning-result-header-right' }, [
      el('span', { class: 'learning-result-score', text: score }),
    ]);
    const tier = item.kind === 'memory' ? (item.tier || 'record') : '';
    const displayTier = item.kind === 'memory' && !isDocumentTier(tier) ? 'record' : tier;
    const card = el('div', { class: 'learning-result-card', 'data-id': item.id, 'data-kind': item.kind }, [
      el('div', { class: 'learning-result-header' }, [
        el('span', { class: `learning-result-kind learning-kind-${item.kind}`, text: item.kind === 'memory' ? 'record' : item.kind }),
        item.kind === 'memory'
          ? el('span', { class: `learning-result-tier learning-tier-${displayTier}`, text: displayTier, title: memoryTierLabel(displayTier) })
          : null,
        headerRight,
      ]),
      item.name ? el('div', { class: 'learning-result-name', text: item.name }) : null,
      contentEl,
    ]);
    card.addEventListener('click', () => {
      if (state.network) {
        state.network.focus(item.id, { scale: 1.5, animation: { duration: 400 } });
        state.network.selectNodes([item.id]);
      }
    });
    resultsEl.appendChild(card);
  }
}

// Bounded re-layout for graph refreshes. network.stabilize() always
// completes and fires stabilizationIterationsDone, so the freeze handler in
// initGraph() applies and the graph ends still. (Re-enabling physics via
// setOptions({ physics: { enabled: true, ... } }) would restart an UNBOUNDED
// simulation that never fires that event — the "nodes jitter while idle" bug.)
export const GRAPH_LAYOUT_ITERATIONS = 80;
export const GRAPH_NODE_GAP = 12;
export const GRAPH_NODE_MIN_SIZE = 10;
export const GRAPH_NODE_MAX_SIZE = 32;
// A restrained archipelago palette. The three node colors share comparable
// lightness and saturation, while their hues map to sea, soil, and foliage.
export const GRAPH_PALETTE = Object.freeze({
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
const GRAPH_NODE_MIN_ZOOM_SCALE = 0.25;
const GRAPH_NODE_MIN_DETAIL_SCALE = 0.75;
const GRAPH_PROJECTION_BUFFER = 2;
export function relayoutGraph(network) {
  if (network) network.stabilize(GRAPH_LAYOUT_ITERATIONS);
}

// Scale every node against the most-connected node in the current graph.
// Relations are unique neighbours rather than raw edges: the same two nodes
// can have several edge types without pretending that they have more reach.
export function sizeGraphNodesByRelations(nodes, edges) {
  const nodeIDs = new Set((nodes || []).map((node) => node.id));
  const neighbours = new Map([...nodeIDs].map((id) => [id, new Set()]));
  for (const edge of edges || []) {
    if (!nodeIDs.has(edge.from) || !nodeIDs.has(edge.to) || edge.from === edge.to) continue;
    neighbours.get(edge.from).add(edge.to);
    neighbours.get(edge.to).add(edge.from);
  }
  const maxRelations = Math.max(0, ...[...neighbours.values()].map((related) => related.size));
  return (nodes || []).map((node) => {
    const relationCount = neighbours.get(node.id)?.size || 0;
    const ratio = maxRelations === 0 ? 0 : relationCount / maxRelations;
    return {
      ...node,
      relationCount,
      size: Math.round(GRAPH_NODE_MIN_SIZE + ratio * (GRAPH_NODE_MAX_SIZE - GRAPH_NODE_MIN_SIZE)),
    };
  });
}

// Canvas zoom multiplies small radius differences by the viewport scale. At
// low scales that turns several distinct relation sizes into the same few
// rasterized pixels. Keep the minimum node on the natural zoom curve, but
// compensate the relation-driven part so its screen-space difference remains
// visible. Close zoom (scale >= 1) keeps vis-network's natural sizing.
export function graphNodeSizeAtScale(baseSize, scale) {
  if (!Number.isFinite(baseSize) || baseSize <= GRAPH_NODE_MIN_SIZE) return GRAPH_NODE_MIN_SIZE;
  if (!Number.isFinite(scale) || scale >= 1) return baseSize;
  const effectiveScale = Math.max(scale, GRAPH_NODE_MIN_ZOOM_SCALE);
  const detailScale = Math.max(effectiveScale, GRAPH_NODE_MIN_DETAIL_SCALE);
  return GRAPH_NODE_MIN_SIZE + (baseSize - GRAPH_NODE_MIN_SIZE) * detailScale / effectiveScale;
}

export function bindGraphZoomSizing(network, nodes) {
  if (!network || !nodes) return;
  network.on('zoom', ({ scale }) => syncGraphNodeSizesForScale(nodes, scale));
}

function syncGraphNodeSizesForScale(nodes, scale) {
  if (!nodes) return;
  const updates = nodes.get()
    .filter((node) => Number.isFinite(node.relationSize))
    .map((node) => ({
      id: node.id,
      size: graphNodeSizeAtScale(node.relationSize, scale),
    }));
  if (updates.length > 0) nodes.update(updates);
}

// vis-network's animated fit does not emit its public zoom event. Reapply
// the screen-space relation sizing explicitly once the camera settles.
export function fitGraphToView(network, nodes, duration = 400) {
  if (!network) return;
  network.fit({ animation: duration > 0 ? { duration } : false });
  const sync = () => syncGraphNodeSizesForScale(nodes, network.getScale());
  if (duration > 0) setTimeout(sync, duration + 20);
  else sync();
}

export function graphEdgeWidth(weight) {
  const normalized = Number.isFinite(weight) ? Math.min(1, Math.max(0, weight)) : 0;
  return Math.max(0.35, normalized * 1.1);
}

function graphNodeIsFixed(node) {
  return node.fixed === true || (node.fixed?.x === true && node.fixed?.y === true);
}

function pairDirection(firstID, secondID) {
  // Coincident nodes have no vector to push along. Derive a stable angle
  // from their ids so a dense zero-position cluster fans out instead of all
  // pairs choosing the same axis.
  const key = `${firstID}\u0000${secondID}`;
  let hash = 2166136261;
  for (let i = 0; i < key.length; i++) {
    hash ^= key.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  const angle = ((hash >>> 0) / 0xffffffff) * Math.PI * 2;
  return { x: Math.cos(angle), y: Math.sin(angle) };
}

// Preserve the force layout's angular grouping, then remap distance from the
// graph center by degree centrality. Hubs move inward; low-degree and isolated
// nodes move toward the perimeter. Pinned refreshes deliberately skip this
// pass so background updates never make the established graph jump.
export function positionGraphByRelations(nodes, positions) {
  const points = {};
  for (const [id, point] of Object.entries(positions || {})) {
    points[id] = { x: point.x, y: point.y };
  }
  const active = (nodes || []).filter((node) => points[node.id]);
  if (active.length < 2 || active.some(graphNodeIsFixed)) return points;

  const center = active.reduce((sum, node) => ({
    x: sum.x + points[node.id].x / active.length,
    y: sum.y + points[node.id].y / active.length,
  }), { x: 0, y: 0 });
  const maxRelations = Math.max(0, ...active.map((node) => node.relationCount || 0));
  const maxRadius = Math.max(GRAPH_NODE_GAP * Math.sqrt(active.length), ...active.map((node) => (
    Math.hypot(points[node.id].x - center.x, points[node.id].y - center.y)
  )));

  for (const node of active) {
    const point = points[node.id];
    let dx = point.x - center.x;
    let dy = point.y - center.y;
    let radius = Math.hypot(dx, dy);
    if (radius < 0.001) {
      const direction = pairDirection(node.id, 'graph-center');
      dx = direction.x;
      dy = direction.y;
      radius = 1;
    }
    const centrality = maxRelations === 0
      ? 0
      : Math.sqrt(Math.max(0, node.relationCount || 0) / maxRelations);
    // Keep centrality as a readable bias, not a set of detached rings. The
    // outer target stays inside the force layout's natural radius, while a
    // small amount of the original radius preserves its organic grouping.
    const targetRadius = maxRadius * (0.58 + 0.32 * (1 - centrality));
    const adjustedRadius = radius * 0.1 + targetRadius * 0.9;
    point.x = center.x + dx / radius * adjustedRadius;
    point.y = center.y + dy / radius * adjustedRadius;
  }
  return points;
}

// Resolve the residual overlaps that a bounded physics run can leave behind.
// vis-network's avoidOverlap is a force preference, not a hard constraint;
// on dense graphs it may stop with circles still touching. This projection
// step makes node spacing an explicit layout invariant while respecting the
// positions pinned during background refreshes.
export function spaceGraphPositions(nodes, positions, gap = GRAPH_NODE_GAP) {
  const points = {};
  for (const [id, point] of Object.entries(positions || {})) {
    points[id] = { x: point.x, y: point.y };
  }
  const active = (nodes || []).filter((node) => points[node.id]);
  // Keep a small canvas-space buffer because vis-network performs its final
  // stabilization bookkeeping after emitting stabilizationIterationsDone.
  // The public invariant remains GRAPH_NODE_GAP after that sub-pixel drift.
  const projectedGap = gap + GRAPH_PROJECTION_BUFFER;
  const allFree = active.every((node) => !graphNodeIsFixed(node));
  // A full layout only needs a few passes to give coincident points distinct
  // directions; the uniform expansion below enforces the final gap. Pinned
  // refresh layouts cannot expand globally, so let their local relaxation
  // converge instead.
  const maxPasses = allFree ? 4 : 160;

  for (let pass = 0; pass < maxPasses; pass++) {
    let adjusted = false;
    for (let i = 0; i < active.length; i++) {
      const first = active[i];
      const firstPoint = points[first.id];
      for (let j = i + 1; j < active.length; j++) {
        const second = active[j];
        const secondPoint = points[second.id];
        let dx = secondPoint.x - firstPoint.x;
        let dy = secondPoint.y - firstPoint.y;
        let distance = Math.hypot(dx, dy);
        const required = (first.size || 16) + (second.size || 16) + projectedGap;
        if (distance >= required - 0.01) continue;

        const firstFixed = graphNodeIsFixed(first);
        const secondFixed = graphNodeIsFixed(second);
        if (firstFixed && secondFixed) continue;
        if (distance < 0.001) {
          const direction = pairDirection(first.id, second.id);
          dx = direction.x;
          dy = direction.y;
          distance = 1;
        }
        const push = required - distance + 0.01;
        const unitX = dx / distance;
        const unitY = dy / distance;
        const firstShare = firstFixed ? 0 : (secondFixed ? 1 : 0.5);
        const secondShare = secondFixed ? 0 : (firstFixed ? 1 : 0.5);
        firstPoint.x -= unitX * push * firstShare;
        firstPoint.y -= unitY * push * firstShare;
        secondPoint.x += unitX * push * secondShare;
        secondPoint.y += unitY * push * secondShare;
        adjusted = true;
      }
    }
    if (!adjusted) break;
  }

  // On a fresh/full layout no node is pinned. A final uniform expansion is
  // both cheaper and stricter than asking pairwise relaxation to converge to
  // sub-pixel precision: every pairwise distance grows by the same factor,
  // so the closest pair is guaranteed to satisfy the configured gap without
  // distorting the force-directed shape.
  if (active.length > 1 && allFree) {
    let scale = 1;
    for (let i = 0; i < active.length; i++) {
      for (let j = i + 1; j < active.length; j++) {
        const first = active[i];
        const second = active[j];
        const firstPoint = points[first.id];
        const secondPoint = points[second.id];
        const distance = Math.hypot(secondPoint.x - firstPoint.x, secondPoint.y - firstPoint.y);
        const required = (first.size || 16) + (second.size || 16) + projectedGap;
        if (distance > 0.001) scale = Math.max(scale, required / distance);
      }
    }
    if (scale > 1) {
      const center = active.reduce((sum, node) => ({
        x: sum.x + points[node.id].x / active.length,
        y: sum.y + points[node.id].y / active.length,
      }), { x: 0, y: 0 });
      for (const node of active) {
        const point = points[node.id];
        point.x = center.x + (point.x - center.x) * (scale + 0.001);
        point.y = center.y + (point.y - center.y) * (scale + 0.001);
      }
    }
  }
  return points;
}

export function freezeGraphLayout(network, nodes) {
  if (!network || !nodes) return;
  const records = nodes.get();
  const radialPositions = positionGraphByRelations(records, network.getPositions());
  const positions = spaceGraphPositions(records, radialPositions);
  network.setOptions({ physics: false });
  for (const [id, point] of Object.entries(positions)) {
    network.moveNode(id, point.x, point.y);
  }
  for (const node of records) {
    if (node.fixed) nodes.update({ id: node.id, fixed: { x: false, y: false } });
  }
}

// Keep unchanged nodes exactly where they are across refreshes: they get
// their current x/y back plus a temporary layout pin, so the graph stays
// still while idle and only new nodes are laid out. The freeze handler
// releases the pins once physics is off.
export function keepGraphPositions(nodes, prevPositions) {
  return (nodes || []).map((n) => {
    const p = prevPositions[n.id];
    return p ? { ...n, x: p.x, y: p.y, fixed: { x: true, y: true } } : n;
  });
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
  const graphFont = () => resolvedFontFamily() || "'IBM Plex Sans', 'Noto Color Emoji', 'Noto Sans Symbols 2', sans-serif";
  const options = {
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
      // User memory is one document (not per-fact), so it gets a
      // distinct shape + color instead of masquerading as a record.
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
  state.network = new Network(container, { nodes: state.nodes, edges: state.edges }, options);
  window.addEventListener('nusashell:font-change', () => {
    if (!state.network) return;
    const face = graphFont();
    state.network.setOptions({
      nodes: { font: { face } },
      edges: { font: { face } },
    });
  });
  bindGraphZoomSizing(state.network, state.nodes);
  // After a layout stabilizes, freeze the graph. Without this the physics
  // engine runs indefinitely (the "constant jitter / noise" bug when
  // loadGraph is re-triggered by WebSocket events like memory.updated or
  // skill.updated). loadGraph pushes new data then runs a bounded
  // stabilize(), so every refresh lays out briefly and freezes again here.
  // Layout pins on kept nodes are released while physics is already off, so
  // the graph stays exactly where it is.
  state.network.on('stabilizationIterationsDone', () => {
    freezeGraphLayout(state.network, state.nodes);
  });
}

async function loadGraph({ preservePositions = true } = {}) {
  if (!state.nodes) return;
  try {
    // Fetch pre-computed graph from backend (nodes + edges). Related edges
    // combine embedding, content/token overlap, and record metadata;
    // used-with edges come from learning nodes observed in one successful
    // agent or learning-job turn. Nothing is computed client-side.
    const { nodes, edges } = await rpc('learning.graph');

    // Keep current positions (pinned for the layout) so unchanged nodes
    // stay exactly where they are across refreshes; only new nodes are
    // laid out by the bounded stabilize() below.
    const prevPositions = preservePositions && state.network ? state.network.getPositions() : {};

    const newNodes = keepGraphPositions(sizeGraphNodesByRelations(nodes, edges).map((n) => {
      const group = n.kind === 'memory' && n.tier === 'user' ? 'memory-user' : n.kind;
      const relationLabel = `${n.relationCount} relation${n.relationCount === 1 ? '' : 's'}`;
      return {
        id: n.id,
        label: n.name || n.id,
        group,
        size: n.size,
        relationSize: n.size,
        relationCount: n.relationCount,
        title: group === 'memory-user'
          ? `User memory: ${n.name || n.id} • ${relationLabel}`
          : `${n.name || n.id} • ${relationLabel}`,
      };
    }), prevPositions);

    const edgeColors = {
      related: GRAPH_PALETTE.deepOcean,
      used_with: GRAPH_PALETTE.mangrove,
      derived_from: GRAPH_PALETTE.sand,
    };
    const newEdges = (edges || []).map((e, i) => ({
      id: `edge_${i}`,
      from: e.from,
      to: e.to,
      width: graphEdgeWidth(e.weight),
      color: { color: edgeColors[e.type] || '#4b504b', highlight: '#6ee0c4', hover: '#8af0d4' },
      title: `${e.type} (${(e.weight * 100).toFixed(0)}%)`,
    }));

    state.nodes.clear();
    state.edges.clear();
    state.nodes.add(newNodes);
    state.edges.add(newEdges);
    state.edgeCount = newEdges.length;
    document.getElementById('learning-stat-edges').textContent =
      `${state.edgeCount} edge${state.edgeCount === 1 ? '' : 's'}`;
    const memCount = newNodes.filter((n) => n.group === 'memory' || n.group === 'memory-user').length;
    document.getElementById('learning-stat-memory').textContent =
      `${memCount} record${memCount === 1 ? '' : 's'}`;
    // Bounded re-layout for the new/changed nodes, then the
    // stabilizationIterationsDone handler freezes the graph again.
    // Auto-fit after data load + render settled. Defer to next frame so
    // the container has its final dimensions (view switch, CSS layout).
    if (state.network && newNodes.length > 0) {
      relayoutGraph(state.network);
      requestAnimationFrame(() => {
        setTimeout(() => {
          fitGraphToView(state.network, state.nodes, 400);
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
