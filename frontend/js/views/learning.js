// Learning workspace: search + memory list + knowledge graph + autolearn log.
// Uses vis-network for graph rendering (vendored ESM standalone build).

import { rpc, on, off } from '../rpc.js';
import { el, debounce, createSelect, toast, fmtTime } from '../ui.js';
import { renderMarkdown } from '../markdown.js';
// Reuse the Agent view's thinking/tool components so the review activity
// reads exactly like a live agent conversation (minus the user side).
import { reasoningDisclosure, renderToolCallCard, setToolTerminalStatus, toolTerminalMeta } from './agent/render.js';
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
  runningReviews: new Set(), // conversation IDs with in-flight reviews
  reviewEventHandlers: null, // cleanup funcs for event listeners
  learningEventHandlers: null, // cleanup funcs for memory/skill update listeners
  memoryRefreshTimer: null, // debounce timer for memory.updated refresh
  skillRefreshTimer: null, // debounce timer for skill.updated refresh
};

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
  refreshBtn.addEventListener('click', () => loadGraph());
  fitBtn.addEventListener('click', () => {
    if (state.network) state.network.fit({ animation: { duration: 300 } });
  });
  logRefreshBtn.addEventListener('click', () => loadLog());

  initTabs();
  initSplitter();
  initReviewEventListeners();
  initLearningUpdateListeners();

  await loadStats();
  initGraph();
  // Initial search: empty query lists all skills + memories so the results
  // pane is populated immediately (backend returns an unfiltered listing
  // for empty queries). The graph loads in parallel. The learning log
  // loads lazily on first tab switch to keep init light.
  await Promise.all([doSearch(), loadGraph()]);
}

// Tab switching between "Memory & Graph" and "Learning log". The log is
// fetched lazily on first open so switching back and forth does not spam
// the trajectory file read.
function initTabs() {
  const tabs = document.querySelectorAll('[data-learning-tab]');
  for (const tab of tabs) {
    tab.addEventListener('click', () => {
      const target = tab.dataset.learningTab;
      tabs.forEach((t) => {
        const active = t === tab;
        t.classList.toggle('active', active);
        t.setAttribute('aria-selected', String(active));
      });
      document.getElementById('learning-panel-memory').hidden = target !== 'memory';
      document.getElementById('learning-panel-log').hidden = target !== 'log';
      if (target === 'log') {
        loadLog();
        // The graph canvas was possibly hidden while the other panel was
        // active; re-fit after layout settles.
        setTimeout(() => { if (state.network) state.network.fit({ animation: { duration: 200 } }); }, 60);
      }
    });
  }
}

// Real-time review status: when a review starts, show a "running"
// indicator at the top of the log. When it finishes (done or error),
// refresh the log so the final status appears from the trajectory.
function initReviewEventListeners() {
  if (state.reviewEventHandlers) return;
  const onStarted = (payload) => {
    const convId = payload?.conversation_id;
    if (convId) state.runningReviews.add(convId);
    showRunningIndicator();
  };
  const onDone = (payload) => {
    const convId = payload?.conversation_id;
    if (convId) state.runningReviews.delete(convId);
    if (state.runningReviews.size === 0) hideRunningIndicator();
    if (state.logLoaded) loadLog();
    // A review may have promoted/demoted memory or saved skills —
    // refresh the memory & graph panes too.
    loadStats();
    loadGraph();
    doSearch();
  };
  const onError = (payload) => {
    const convId = payload?.conversation_id;
    if (convId) state.runningReviews.delete(convId);
    if (state.runningReviews.size === 0) hideRunningIndicator();
    if (state.logLoaded) loadLog();
  };
  on('learning.review.started', onStarted);
  on('learning.review.done', onDone);
  on('learning.review.error', onError);
  state.reviewEventHandlers = [
    () => off('learning.review.started', onStarted),
    () => off('learning.review.done', onDone),
    () => off('learning.review.error', onError),
  ];
}

// Real-time memory & skill updates: when a tool or review agent mutates
// memory or skills, the backend emits memory.updated / skill.updated so
// the Learning tab can refresh its stats, search results, and graph
// without polling. A short debounce coalesces bursts (e.g. a review that
// saves several fragments in quick succession).
function initLearningUpdateListeners() {
  if (state.learningEventHandlers) return;
  const onMemoryUpdated = () => {
    if (state.memoryRefreshTimer) clearTimeout(state.memoryRefreshTimer);
    state.memoryRefreshTimer = setTimeout(() => {
      state.memoryRefreshTimer = null;
      loadStats();
      loadGraph();
      doSearch();
    }, 300);
  };
  const onSkillUpdated = () => {
    if (state.skillRefreshTimer) clearTimeout(state.skillRefreshTimer);
    state.skillRefreshTimer = setTimeout(() => {
      state.skillRefreshTimer = null;
      loadGraph();
      doSearch();
    }, 300);
  };
  on('memory.updated', onMemoryUpdated);
  on('skill.updated', onSkillUpdated);
  state.learningEventHandlers = [
    () => off('memory.updated', onMemoryUpdated),
    () => off('skill.updated', onSkillUpdated),
  ];
}

function showRunningIndicator() {
  const logEl = document.getElementById('learning-log');
  if (!logEl) return;
  let indicator = document.getElementById('learning-log-running');
  if (!indicator) {
    indicator = el('div', { id: 'learning-log-running', class: 'learning-log-running' }, [
      el('span', { class: 'learning-log-running-dot' }),
      el('span', { text: 'Autolearn running…' }),
    ]);
    logEl.insertBefore(indicator, logEl.firstChild);
  }
}

function hideRunningIndicator() {
  const indicator = document.getElementById('learning-log-running');
  if (indicator) indicator.remove();
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
      el('span', { text: 'Autolearn runs in the background after enough turns and records what it saved here.' }),
    ]));
    return;
  }
  for (const entry of state.logEntries) {
    logEl.appendChild(renderLogEntry(entry));
  }
}

export function renderLogEntry(entry) {
  const headChildren = [
    el('span', { class: `learning-log-type learning-type-${entry.type}`, text: typeLabel(entry.type) }),
  ];
  // Status badge: review entries carry a status (done|error|skipped). Other
  // event types (prune, decay, etc.) do not have a status.
  if (entry.status) {
    headChildren.push(el('span', {
      class: `learning-log-status learning-log-status-${entry.status}`,
      text: entry.status,
      title: entry.status === 'error' && entry.error ? entry.error : '',
    }));
  }
  headChildren.push(el('span', { class: 'learning-log-time', text: fmtTime(entry.ts) }));
  const head = el('div', { class: 'learning-log-entry-head' }, headChildren);

  const parts = [head];

  // Error message: shown when the review failed so the user can see
  // why without opening the transcript.
  if (entry.status === 'error' && entry.error) {
    parts.push(el('div', { class: 'learning-log-error', text: entry.error }));
  }
  if (entry.status === 'skipped') {
    const reason = entry.detail?.reason || 'deferred';
    const messages = {
      already_running: 'Coalesced: another review is already running.',
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
      parts.push(el('div', { class: 'learning-log-skipped', text: `Reviews run on the override model “${d.resolved}”.` }));
    } else {
      const bare = String(d.requested || '').split(':').pop();
      const hint = bare ? ` (configured override: ${bare})` : '';
      parts.push(el('div', { class: 'learning-log-error', text: `Review model override failed — reviews fell back to the conversation model${hint}.` }));
    }
    return el('div', { class: 'learning-log-entry' }, parts);
  }

  // Source conversation as a compact "Source <title>" line. The raw
  // conversation id and the replayed transcript text do not belong in the
  // activity digest — the log is about what the review agent did.
  if (entry.conversation_id) {
    parts.push(el('div', { class: 'learning-log-conv' }, [
      el('span', { class: 'learning-log-conv-label', text: 'Source' }),
      el('span', { class: 'learning-log-conv-title', text: entry.conversation_title || 'Untitled conversation' }),
    ]));
  }

  // Saved outcomes: one compact line per mutation so the feed shows what
  // the review actually stored. A finished review with zero mutations says
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

  // "View review details" button: expands an activity digest of what the
  // background review agent actually did. Only review entries have a
  // review_id; other event types (prune, decay, etc.) have no run record.
  if (entry.review_id) {
    const transcriptRow = el('div', { class: 'learning-log-transcript-row' }, [
      el('button', {
        class: 'learning-log-open',
        type: 'button',
        text: 'View review details',
        title: 'Show the background review agent activity',
      }),
    ]);
    const btn = transcriptRow.querySelector('.learning-log-open');
    btn.addEventListener('click', () => toggleTranscript(entry.review_id, btn));
    parts.push(transcriptRow);
  }

  return el('div', { class: 'learning-log-entry' }, parts);
}

// toggleTranscript fetches and renders the review agent's activity digest
// (its tool steps + outcome + conclusion) inline below the log entry.
// Toggles open/closed on repeated clicks.
async function toggleTranscript(reviewID, btn) {
  const entry = btn.closest('.learning-log-entry');
  if (!entry) return;
  const existing = entry.querySelector('.learning-log-activity');
  if (existing) {
    existing.remove();
    btn.textContent = 'View review details';
    return;
  }
  btn.textContent = 'Loading…';
  btn.disabled = true;
  try {
    const res = await rpc('learning.review.transcript', { id: reviewID });
    const view = renderTranscript(res);
    entry.appendChild(view);
    btn.textContent = 'Hide review details';
  } catch (e) {
    toast(e.message || 'Failed to load review details.', 'error', 4000);
    btn.textContent = 'View review details';
  } finally {
    btn.disabled = false;
  }
}

// oneLine collapses whitespace and truncates a snippet so log rows stay
// oneLine collapses whitespace and truncates a snippet so log rows stay
// scannable. Narration and conclusions go through this; full tool inputs
// and outputs live inside their collapsible terminal cards instead.
function oneLine(text, max) {
  const clean = (text || '').replace(/\s+/g, ' ').trim();
  if (!clean) return '';
  return clean.length > max ? `${clean.slice(0, max - 1)}…` : clean;
}

// renderTranscript renders what the background review agent did as an
// agent-style flow — thinking disclosures, terminal-style tool cards, and
// interstitial narration — exactly like the Agent view conversation, minus
// the user side: the replayed transcript message (role "user") is never
// rendered. This answers "what did the agent do and store?".
export function renderTranscript(transcript) {
  const view = el('div', { class: 'learning-log-activity' });
  // Banner: makes it obvious this is background review agent activity.
  view.appendChild(el('div', { class: 'learning-log-activity-banner' }, [
    el('span', { class: 'learning-log-activity-badge', text: 'Background review agent' }),
    el('span', { class: 'learning-log-activity-meta', text: transcript.model || '' }),
    el('span', { class: 'learning-log-activity-meta', text: fmtTime(transcript.created_at) }),
  ]));

  const items = collectAgentFlow(transcript.messages || []);
  if (items.length === 0) {
    view.appendChild(el('div', { class: 'learning-log-detail', text: 'No steps recorded.' }));
    return view;
  }

  const detailStep = document.createElement("div")
  detailStep.className = "learning-log-activity-step"
  for (const item of items) {
    detailStep.appendChild(item);
  }
  view.appendChild(detailStep)
  return view;
}

// collectAgentFlow converts the review agent's message history into the
// Agent view's visual language: a thinking disclosure per assistant round,
// one tool call card per call (paired with its tool result), short
// narration notes between rounds, and the final conclusion line.
// Replayed user messages are dropped — this view shows agent work only.
function collectAgentFlow(messages) {
  const items = [];
  const cardsById = new Map();
  let lastCard = null;
  let narration = '';

  const flushNarration = () => {
    const text = narration.trim();
    if (text) {
      const note = el('div', { class: 'learning-log-note' });
      note.innerHTML = renderMarkdown(text);
      items.push(note);
    }
    narration = '';
  };

  const applyResult = (card, content) => {
    if (!card) return;
    const output = String(content || '');
    const failed = /^error:/i.test(output.trim());
    const status = failed ? 'fail' : 'ok';
    const panel = card.querySelector('.agent-tool-terminal-output');
    if (panel) panel.textContent = output.length > 12000 ? `${output.slice(0, 12000)}\n… (truncated)` : (output || 'ok');
    // Refresh the summary-line meta that was rendered as "Running".
    const meta = card.querySelector('.agent-tool-terminal-meta');
    if (meta && card._toolName) {
      meta.textContent = toolTerminalMeta({ name: card._toolName, args: card._toolArgs, status });
    }
    setToolTerminalStatus(card, status);
  };

  for (const msg of messages) {
    // Tool results merge into their call card. Never rendered standalone.
    if (msg.role === 'tool' && msg.tool_result) {
      const id = msg.tool_result.tool_call_id;
      applyResult((id && cardsById.get(id)) || lastCard, msg.tool_result.content);
      continue;
    }
    // The replayed source-conversation transcript (role "user") stays hidden.
    if (msg.role !== 'assistant') continue;

    if (msg.reasoning?.trim()) {
      flushNarration();
      items.push(reasoningDisclosure(msg.reasoning));
    }
    if (msg.tool_calls?.length) {
      // Spoken text alongside tool calls reads as narration for the steps.
      if (typeof msg.content === 'string' && msg.content.trim()) narration = msg.content;
      flushNarration();
      for (const call of msg.tool_calls) {
        const card = renderToolCallCard({ id: call.id, name: call.name, args: call.args ?? '', status: 'running', output: '' });
        if (call.id) cardsById.set(call.id, card);
        lastCard = card;
        items.push(card);
      }
      continue;
    }
    if (typeof msg.content === 'string' && msg.content.trim()) {
      // Terminal text response: the review's verdict.
      flushNarration();
      const conclusion = el('div', { class: 'learning-log-conclusion' });
      conclusion.innerHTML = renderMarkdown(msg.content);
      items.push(conclusion);
    }
  }
  // Narration captured just before an interrupted loop end still shows.
  flushNarration();
  return items;
}

function typeLabel(type) {
  switch (type) {
    case 'review': return 'Autolearn review';
    case 'extract': return 'Extraction';
    case 'edge_build': return 'Edge build';
    case 'consolidate': return 'Consolidation';
    case 'decay': return 'Decay';
    case 'prune': return 'Prune';
    case 'review_model': return 'Review model';
    default: return type.replace(/_/g, ' ');
  }
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
    const score = item.score && item.score > 0 ? `★ ${(item.score * 100).toFixed(1)}%` : '';
    const headerRight = el('div', { class: 'learning-result-header-right' }, [
      el('span', { class: 'learning-result-score', text: score }),
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
        item.kind === 'memory' && item.tier
          ? el('span', { class: `learning-result-tier learning-tier-${item.tier}`, text: item.tier, title: item.tier === 'primary' ? 'Primary memory' : 'Fragment' })
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
      // Primary memory is one document (not per-fact), so it gets a
      // distinct shape + color instead of masquerading as a fragment.
      'memory-primary': { color: { background: '#f85149', border: '#b62324' }, size: 16, shape: 'square' },
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

    // Degree centrality: a node's size grows with how many edges touch
    // it, so well-connected hubs (frequently-relevant memories, used
    // skills) read as larger than isolated leaves — a neuron-style map
    // instead of uniform dots.
    const degree = new Map();
    for (const e of edges || []) {
      degree.set(e.from, (degree.get(e.from) || 0) + 1);
      degree.set(e.to, (degree.get(e.to) || 0) + 1);
    }
    const nodeSize = (kind, deg) => {
      if (kind === 'skill') return Math.round(14 + Math.sqrt(deg) * 4);
      if (kind === 'memory-primary') return 16; // document node: fixed
      return Math.round(10 + Math.sqrt(deg) * 3.5);
    };

    const newNodes = (nodes || []).map((n) => {
      const group = n.kind === 'memory' && n.tier === 'primary' ? 'memory-primary' : n.kind;
      return {
        id: n.id,
        label: n.name || n.id,
        group,
        size: nodeSize(group, degree.get(n.id) || 0),
        title: group === 'memory-primary' ? `Primary memory: ${n.name || n.id}` : (n.name || n.id),
      };
    });

    const edgeColors = { related: '#1f6feb', used_with: '#6ee0c4', derived_from: '#c1a6ff' };
    const newEdges = (edges || []).map((e, i) => ({
      id: `edge_${i}`,
      from: e.from,
      to: e.to,
      width: Math.max(1.5, e.weight * 4),
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
    const memCount = newNodes.filter((n) => n.group === 'memory' || n.group === 'memory-primary').length;
    document.getElementById('learning-stat-memory').textContent =
      `${memCount} memor${memCount === 1 ? 'y' : 'ies'}`;
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


