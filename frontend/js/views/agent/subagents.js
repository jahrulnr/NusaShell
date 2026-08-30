/* ACP subagent live UI: dock chips, right drawer, peek popup. */

import { rpc, on } from '../../rpc.js';
import { el, fmtTime } from '../../ui.js';
import { updateScrollPin } from '../../agent-ui.js';
import { renderMarkdown } from '../../markdown.js';
import { incrementalRender } from '../../incremental-render.js';
import { highlightCode } from '../../highlight-render.js';
import { attachZoomButtons } from '../../media-zoom.js';
import { renderToolJob, reasoningDisclosure, setReasoningSource, setToolTerminalOutput } from './render.js';

const LIVE = new Set(['starting', 'running']);
const RECENT_MS = 2 * 60 * 1000;

const state = {
  runs: new Map(),
  conversationId: '',
  drawerRunId: '',
  popupRunId: '',
  runLoads: new Map(),
  runLoadErrors: new Map(),
  bound: false,
};

// Agent name cache: agent_id → name. Hydrated lazily from acp.agents.list
// so delegation cards can show "Devin" instead of "acp_4aa7732594cbfd1d".
const agentNames = new Map();
let agentNamesLoading = false;

async function ensureAgentNames() {
  if (agentNames.size > 0 || agentNamesLoading) return;
  agentNamesLoading = true;
  try {
    const res = await rpc('acp.agents.list', {});
    for (const a of res.agents ?? []) {
      if (a.id && a.name) agentNames.set(a.id, a.name);
    }
  } catch {
    /* backend may not be ready */
  } finally {
    agentNamesLoading = false;
  }
}

export function agentNameForId(id) {
  if (!id) return '';
  return agentNames.get(id) || '';
}

export function bindSubagents({ getActiveConversationId } = {}) {
  if (state.bound) return;
  state.bound = true;
  state.getActiveConversationId = getActiveConversationId || (() => '');

  document.getElementById('acp-dock-toggle')?.addEventListener('click', () => {
    const toggle = document.getElementById('acp-dock-toggle');
    const list = document.getElementById('acp-dock-list');
    const open = toggle.getAttribute('aria-expanded') === 'true';
    toggle.setAttribute('aria-expanded', String(!open));
    list.hidden = open;
  });
  document.getElementById('acp-dock-open-drawer')?.addEventListener('click', () => openDrawer(firstVisibleRunId()));
  document.getElementById('acp-drawer-close')?.addEventListener('click', closeDrawer);
  document.getElementById('acp-drawer-overlay')?.addEventListener('click', closeDrawer);
  document.getElementById('acp-popup-close')?.addEventListener('click', closePopup);
  document.getElementById('acp-popup-overlay')?.addEventListener('click', (event) => {
    if (event.target.id === 'acp-popup-overlay') closePopup();
  });
  document.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return;
    if (!document.getElementById('acp-popup-overlay')?.hidden) {
      closePopup();
      return;
    }
    if (!document.getElementById('acp-drawer')?.hidden) closeDrawer();
  });

  on('acp.run.started', (payload) => upsertRun(payload?.run));
  on('acp.run.updated', (payload) => upsertRun(payload?.run));
  on('acp.run.done', (payload) => upsertRun(payload?.run));
  on('acp.session.mode_changed', (payload) => {
    const run = state.runs.get(payload?.run_id);
    if (run) {
      run.current_mode_id = payload.mode_id;
      renderAll();
    }
  });

  void ensureAgentNames();
  void hydrate();
}

export function setSubagentConversation(id) {
  state.conversationId = id || '';
  void hydrate();
  renderDock();
  if (!document.getElementById('acp-drawer')?.hidden) {
    // A drawer showing a run from the previous room must not follow the
    // user across rooms — reset to the new room's runs instead.
    const run = state.runs.get(state.drawerRunId);
    if (!run || (id && run.conversation_id !== id)) state.drawerRunId = '';
    renderDrawer();
  }
}

export function closeAcpOverlays() {
  closePopup();
  closeDrawer();
}

async function hydrate() {
  try {
    const res = await rpc('acp.runs.list', {});
    for (const run of res.runs ?? []) upsertRun(run, { silent: true });
    renderAll();
  } catch {
    /* backend may not be ready */
  }
}

// A room can render a persisted subagent card before the global run list has
// hydrated (for example while the backend WebSocket is reconnecting). Load the
// exact run on demand instead of making a card depend on process-local cache.
function ensureRun(runId) {
  if (!runId || state.runs.has(runId)) return Promise.resolve(state.runs.get(runId));
  if (state.runLoads.has(runId)) return state.runLoads.get(runId);
  const request = rpc('acp.runs.get', { id: runId })
    .then((result) => {
      const run = result?.run || result;
      if (!run?.id) throw new Error('ACP run response did not include an id');
      upsertRun(run);
      return run;
    })
    .catch((error) => {
      state.runLoadErrors.set(runId, error);
      renderAll();
      return null;
    })
    .finally(() => state.runLoads.delete(runId));
  state.runLoads.set(runId, request);
  return request;
}

function upsertRun(run, { silent } = {}) {
  if (!run?.id) return;
  state.runLoadErrors.delete(run.id);
  state.runs.set(run.id, run);
  pruneRuns();
  if (!silent) renderAll();
}

// pruneRuns drops terminal runs that fell out of the recent window and are
// not part of the active conversation.
// Without this the maps grow for the lifetime of the session even though
// nothing can ever render those runs again.
function pruneRuns() {
  const now = Date.now();
  const active = activeConversationId();
  for (const [id, run] of state.runs) {
    if (LIVE.has(run.status)) continue;
    if (active && run.conversation_id === active) continue;
    const ended = Date.parse(run.ended_at || run.updated_at || '') || 0;
    if (!ended || now - ended > RECENT_MS) {
      state.runs.delete(id);
    }
  }
}

function activeConversationId() {
  return state.getActiveConversationId?.() || state.conversationId;
}

// Dock chips: live runs of the active conversation plus runs that settled
// within RECENT_MS. Newest runs are shown first so the active/latest work is
// immediately reachable on a tall mobile drawer.
function visibleRuns() {
  const now = Date.now();
  const active = activeConversationId();
  return sortRunsNewestFirst([...state.runs.values()]
    .filter((run) => {
      if (active && run.conversation_id !== active) return false;
      if (LIVE.has(run.status)) return true;
      const ended = Date.parse(run.ended_at || run.updated_at || '') || 0;
      return ended && now - ended < RECENT_MS;
    }));
}

// Every run of the active conversation, settled included — the drawer's
// switcher and the "open transcript" fallback must reach completed runs
// as long as the conversation exists.
function conversationRuns() {
  const active = activeConversationId();
  return sortRunsNewestFirst([...state.runs.values()]
    .filter((run) => !active || run.conversation_id === active)
  );
}

function startedAtMs(run) {
  return Date.parse(run.started_at || run.created_at || run.updated_at || '') || 0;
}

// sortRunsNewestFirst also handles old persisted ACP records without a
// timestamp. The index tie-breaker preserves the newest insertion at the top
// for those records, matching the backend's historical ascending list order.
export function sortRunsNewestFirst(runs) {
  return (runs || [])
    .map((run, index) => ({ run, index, startedAt: startedAtMs(run) }))
    .sort((a, b) => b.startedAt - a.startedAt || b.index - a.index)
    .map(({ run }) => run);
}

export function firstVisibleRunId() {
  const visible = visibleRuns();
  if (visible.length) return visible[0].id;
  // Fall back to any run of the active conversation (settled, not recent)
  // so a completed subagent card can still open its transcript.
  return conversationRuns()[0]?.id || '';
}

function renderAll() {
  renderDock();
  if (!document.getElementById('acp-drawer')?.hidden) renderDrawer();
  if (!document.getElementById('acp-popup-overlay')?.hidden) renderPopup();
}

function renderDock() {
  const dock = document.getElementById('acp-dock');
  const list = document.getElementById('acp-dock-list');
  const title = document.getElementById('acp-dock-title');
  const meta = document.getElementById('acp-dock-meta');
  if (!dock || !list) return;
  const runs = visibleRuns();
  dock.hidden = runs.length === 0;
  const live = runs.filter((r) => LIVE.has(r.status)).length;
  title.textContent = `${runs.length} subagent${runs.length === 1 ? '' : 's'}`;
  meta.textContent = live ? `${live} live` : 'settled';
  // Patch in place: existing chips keep their DOM node (hover/focus and
  // listeners), while append() moves each node into the requested
  // newest-first position. This matters when a newly-started run arrives
  // after older chips already exist.
  const seen = new Set();
  for (const run of runs) {
    seen.add(run.id);
    let chip = list.querySelector(`[data-run-id="${run.id}"]`);
    if (!chip) {
      chip = buildDockChip(run);
    } else {
      updateDockChip(chip, run);
    }
    list.append(chip);
  }
  for (const chip of [...list.querySelectorAll('[data-run-id]')]) {
    if (!seen.has(chip.dataset.runId)) chip.remove();
  }
}

function buildDockChip(run) {
  const chip = el('button', {
    class: `acp-dock-chip is-${run.status}`,
    type: 'button',
    role: 'listitem',
    'data-run-id': run.id,
    title: `${run.agent_name || 'ACP'} · ${statusLabel(run.status)}`,
  },
    el('span', { class: 'acp-dock-chip-pulse', 'aria-hidden': 'true' }),
    el('span', { class: 'acp-dock-chip-name', text: run.agent_name || 'ACP' }),
    el('span', { class: 'acp-dock-chip-status', text: statusLabel(run.status) }),
  );
  chip.addEventListener('click', () => openDrawer(run.id));
  const peek = el('button', { class: 'acp-dock-chip-peek', type: 'button', title: 'Peek in popup', 'aria-label': 'Peek', text: '↗' });
  peek.addEventListener('click', (event) => {
    event.stopPropagation();
    openPopup(run.id);
  });
  chip.append(peek);
  return chip;
}

function updateDockChip(chip, run) {
  chip.className = `acp-dock-chip is-${run.status}`;
  chip.title = `${run.agent_name || 'ACP'} · ${statusLabel(run.status)}`;
  const status = chip.querySelector('.acp-dock-chip-status');
  if (status && status.textContent !== statusLabel(run.status)) {
    status.textContent = statusLabel(run.status);
  }
}

export function openDrawer(runId) {
  state.drawerRunId = runId || firstVisibleRunId();
  state.runLoadErrors.delete(state.drawerRunId);
  const drawer = document.getElementById('acp-drawer');
  const overlay = document.getElementById('acp-drawer-overlay');
  drawer.hidden = false;
  drawer.removeAttribute('inert');
  drawer.setAttribute('aria-hidden', 'false');
  drawer.classList.add('active');
  overlay.classList.add('active');
  overlay.setAttribute('aria-hidden', 'false');
  renderDrawer();
  if (state.drawerRunId && !state.runs.has(state.drawerRunId)) void ensureRun(state.drawerRunId);
  document.getElementById('acp-drawer-close')?.focus();
}

function closeDrawer() {
  const drawer = document.getElementById('acp-drawer');
  const overlay = document.getElementById('acp-drawer-overlay');
  drawer.classList.remove('active');
  overlay.classList.remove('active');
  overlay.setAttribute('aria-hidden', 'true');
  drawer.setAttribute('aria-hidden', 'true');
  drawer.setAttribute('inert', '');
  drawer.hidden = true;
  state.drawerRunId = '';
}

function openPopup(runId) {
  state.popupRunId = runId;
  state.runLoadErrors.delete(runId);
  const overlay = document.getElementById('acp-popup-overlay');
  overlay.hidden = false;
  renderPopup();
  if (runId && !state.runs.has(runId)) void ensureRun(runId);
  document.getElementById('acp-popup-close')?.focus();
}

function closePopup() {
  document.getElementById('acp-popup-overlay').hidden = true;
  state.popupRunId = '';
}

function renderDrawer() {
  const runs = conversationRuns();
  // When a specific run was requested (card/dock click) but its record
  // hasn't hydrated yet, show a loading state instead of silently
  // switching to a different run.
  const selected = state.runs.get(state.drawerRunId) || (state.drawerRunId ? undefined : runs[0]);
  document.getElementById('acp-drawer-title').textContent = selected ? (selected.agent_name || 'Subagent') : 'Subagents';
  document.getElementById('acp-drawer-subtitle').textContent = selected
    ? `${statusLabel(selected.status)} · ${shortPath(selected.workspace)}`
    : 'No subagents in this conversation';
  const body = document.getElementById('acp-drawer-body');
  let shell = body.querySelector('.acp-drawer-shell');
  if (!shell) {
    const list = el('div', { class: 'agent-conversation-list acp-run-list', role: 'list' });
    list.addEventListener('click', (event) => {
      const item = event.target.closest('[data-run-id]');
      if (!item) return;
      state.drawerRunId = item.dataset.runId;
      renderDrawer();
    });
    shell = el('div', { class: 'acp-drawer-shell' },
      el('aside', { class: 'agent-conversations acp-run-sidebar', 'aria-label': 'Subagent runs' },
        el('div', { class: 'agent-conversations-head' },
          el('div', {},
            el('span', { class: 'agent-panel-label', text: 'Subagents' }),
            el('span', { class: 'agent-conversation-count acp-run-count' }),
          ),
        ),
        list,
      ),
      el('div', { class: 'acp-run-content' }),
    );
    body.replaceChildren(shell);
  }
  renderRunSidebar(shell.querySelector('.acp-run-list'), runs, selected?.id || '');
  const content = shell.querySelector('.acp-run-content');
  if (!selected) {
    const text = state.drawerRunId
      ? (state.runLoadErrors.has(state.drawerRunId) ? 'Unable to load this transcript. Click the card again to retry.' : 'Loading transcript…')
      : 'No subagents in this conversation. The parent agent can spawn them with the subagent tool.';
    content.replaceChildren(el('p', { class: 'acp-empty', text }));
    if (state.drawerRunId && !state.runLoadErrors.has(state.drawerRunId)) void ensureRun(state.drawerRunId);
    return;
  }
  state.drawerRunId = selected.id;
  const existing = content.querySelector(`.acp-run-panel[data-run-id="${selected.id}"]`);
  if (!existing) {
    mountRunPanel(content, selected);
  } else {
    bindTranscriptFollow(existing);
    patchRunPanel(existing, selected);
  }
}

function renderRunSidebar(list, runs, selectedId) {
  const count = list.closest('.acp-run-sidebar')?.querySelector('.acp-run-count');
  if (count) count.textContent = `${runs.length} run${runs.length === 1 ? '' : 's'}`;
  if (!runs.length) {
    list.replaceChildren(el('div', { class: 'agent-conversation-empty', text: 'No subagent runs yet.' }));
    return;
  }
  const existing = new Map(
    [...list.querySelectorAll('[data-run-id]')].map((node) => [node.dataset.runId, node]),
  );
  const seen = new Set();
  for (const run of runs) {
    seen.add(run.id);
    const item = existing.get(run.id) || buildRunSidebarItem(run);
    updateRunSidebarItem(item, run, selectedId);
    // append() preserves the item and its focus/listener state while moving
    // it into the newest-first order on every refresh.
    list.append(item);
  }
  for (const [id, item] of existing) {
    if (!seen.has(id)) item.remove();
  }
}

function buildRunSidebarItem(run) {
  return el('div', {
    class: `agent-conversation-item is-${run.status}`,
    role: 'listitem',
    'data-run-id': run.id,
  },
    el('button', { class: 'agent-conversation-open', type: 'button' },
      el('span', { class: 'agent-conversation-title' }),
      el('span', { class: 'agent-conversation-time' }),
    ),
  );
}

function updateRunSidebarItem(item, run, selectedId) {
  const live = LIVE.has(run.status);
  item.className = `agent-conversation-item is-${run.status}${run.id === selectedId ? ' is-active' : ''}${live ? ' is-running' : ''}`;
  item.querySelector('.agent-conversation-title').textContent = run.agent_name || 'ACP';
  const time = item.querySelector('.agent-conversation-time');
  time.textContent = `${statusLabel(run.status)} · ${shortPath(run.workspace)}`;
  if (live) {
    time.append(el('span', { class: 'agent-conversation-dot', 'aria-hidden': 'true' }));
  }
}

function renderPopup() {
  const run = state.runs.get(state.popupRunId);
  const title = document.getElementById('acp-popup-title');
  const body = document.getElementById('acp-popup-body');
  if (!run) {
    title.textContent = 'Subagent';
    body.replaceChildren(el('p', {
      class: 'acp-empty',
      text: state.runLoadErrors.has(state.popupRunId)
        ? 'Unable to load this transcript. Close and try again.'
        : 'Loading transcript…',
    }));
    if (state.popupRunId && !state.runLoadErrors.has(state.popupRunId)) void ensureRun(state.popupRunId);
    return;
  }
  title.textContent = run.agent_name || 'Subagent';
  // Same run: patch in place so the popup transcript doesn't reset.
  const existing = body.querySelector(`.acp-run-panel[data-run-id="${run.id}"]`);
  if (!existing) {
    mountRunPanel(body, run);
  } else {
    bindTranscriptFollow(existing);
    patchRunPanel(existing, run);
  }
}

function buildRunPanel(run) {
  const panel = el('div', { class: 'acp-run-panel', 'data-run-id': run.id },
    el('div', { class: 'acp-run-meta' },
      el('span', { class: `acp-status-pill is-${run.status}`, text: statusLabel(run.status) }),
      el('span', { class: `acp-risk-pill is-${run.risk_tier || 'read_only'}`, text: riskLabel(run.risk_tier) }),
      run.current_model_id ? el('span', { class: 'acp-model-pill', text: modelLabel(run) }) : null,
    ),
    el('div', { class: 'acp-run-workspace', title: run.workspace || '' },
      el('span', { text: 'Workspace' }),
      el('code', { text: run.workspace || '—' }),
    ),
    el('div', {
      class: 'agent-thread acp-transcript',
      role: 'log',
      'aria-label': 'Subagent transcript',
    }),
  );

  if (run.error) {
    panel.append(el('p', { class: 'acp-run-error', text: run.error }));
  }
  syncTranscript(panel, run.transcript || [], run.prompt, run.started_at);
  return panel;
}

// A freshly opened panel always starts pinned to the live tail; the user can
// scroll up from there (which releases the follow via updateScrollPin).
function mountRunPanel(container, run) {
  const panel = buildRunPanel(run);
  container.replaceChildren(panel);
  bindTranscriptFollow(panel);
  const scroller = transcriptScroller(panel);
  if (scroller) scroller.scrollTop = scroller.scrollHeight;
  return panel;
}

// patchRunPanel updates a live panel in place: status pill + model pill +
// error, then patches the transcript in place. Auto-follows the bottom only
// when the user is already there — reading earlier output is never interrupted.
function patchRunPanel(panel, run) {
  const pill = panel.querySelector('.acp-status-pill');
  if (pill) {
    pill.className = `acp-status-pill is-${run.status}`;
    pill.textContent = statusLabel(run.status);
  }
  const modelPill = panel.querySelector('.acp-model-pill');
  if (run.current_model_id) {
    if (modelPill) {
      modelPill.textContent = modelLabel(run);
    } else {
      panel.querySelector('.acp-run-meta')?.append(el('span', { class: 'acp-model-pill', text: modelLabel(run) }));
    }
  }
  const errEl = panel.querySelector('.acp-run-error');
  if (run.error && !errEl) {
    panel.append(el('p', { class: 'acp-run-error', text: run.error }));
  }
  syncTranscriptWithFollow(panel, run.transcript || [], run.prompt, run.started_at);
}

// Transcript follow state lives on the scrolling element (drawer content or
// popup), keyed by WeakMap so panels can come and go. The decision is
// direction-aware (updateScrollPin): only a real upward user scroll releases
// the follow. Measuring the bottom distance before a patch — the old
// approach — read post-growth geometry and silently killed the follow while
// a subagent streamed tool output.
const followStates = new WeakMap();

function transcriptScroller(panel) {
  return panel?.querySelector('.acp-transcript')?.closest('.acp-run-content, .acp-popup') || null;
}

function bindTranscriptFollow(panel) {
  const scroller = transcriptScroller(panel);
  if (!scroller || followStates.has(scroller)) return;
  const follow = { pinned: true };
  followStates.set(scroller, follow);
  scroller.addEventListener('scroll', () => {
    updateScrollPin(follow, scroller);
  }, { passive: true });
}

// followTranscriptBottom scrolls the transcript to its newest output unless
// the user has scrolled up to read. Scrolling back to the bottom re-arms it.
function followTranscript(panel) {
  const scroller = transcriptScroller(panel);
  if (!scroller) return;
  const follow = followStates.get(scroller);
  if (follow && !follow.pinned) return;
  scroller.scrollTop = scroller.scrollHeight;
}

function syncTranscriptWithFollow(panel, transcript, prompt, promptAt) {
  syncTranscript(panel, transcript, prompt, promptAt);
  followTranscript(panel);
}

export function syncTranscript(panel, transcript, initialPrompt = '', initialPromptAt = '') {
  const box = panel?.querySelector('.acp-transcript');
  if (!box) return;
  const { chunks, usage } = normalizeTranscript(transcript);
  syncUsagePill(panel, usage);
  const segments = transcriptSegments(chunks, initialPrompt, initialPromptAt);
  if (!segments.length) {
    box.replaceChildren(el('p', { class: 'acp-empty', text: 'Waiting for the first update…' }));
    return;
  }
  box.querySelector('.acp-empty')?.remove();
  const messages = [...box.querySelectorAll(':scope > .agent-message')];
  for (let index = 0; index < segments.length; index++) {
    const segment = segments[index];
    let message = messages[index];
    if (message?.dataset.transcriptSegmentKey !== segment.key) {
      const replacement = transcriptMessage(segment);
      if (message) message.replaceWith(replacement);
      else box.append(replacement);
      message = replacement;
    } else {
      updateTranscriptMessage(message, segment);
    }
  }
  for (const message of messages.slice(segments.length)) message.remove();
}

// ACP exposes the initial delegation as run.prompt and follow-up steering as
// transcript chunks. Split the transcript at each prompt so every prompt is
// a real user message and the assistant output after it gets its own normal
// agent bubble, matching the main conversation structure.
function transcriptSegments(chunks, initialPrompt, initialPromptAt) {
  const segments = [];
  const prompt = typeof initialPrompt === 'string' ? initialPrompt.trim() : '';
  if (prompt) segments.push({
    kind: 'prompt',
    key: 'prompt:initial',
    text: initialPrompt,
    at: initialPromptAt,
  });

  let assistantChunks = [];
  let assistantIndex = 0;
  let promptIndex = 0;
  const flushAssistant = () => {
    if (!assistantChunks.length) return;
    segments.push({ kind: 'assistant', key: `assistant:${assistantIndex++}`, chunks: assistantChunks });
    assistantChunks = [];
  };

  for (const [index, chunk] of chunks.entries()) {
    if (chunk.kind === 'prompt') {
      flushAssistant();
      segments.push({
        kind: 'prompt',
        key: `prompt:${++promptIndex}`,
        text: chunk.text || '',
        at: chunk.at,
      });
      continue;
    }
    assistantChunks.push({ chunk, index });
  }
  flushAssistant();
  return segments;
}

function transcriptMessage(segment) {
  if (segment.kind === 'prompt') return transcriptPromptMessage(segment);
  const message = el('div', {
    class: 'agent-message assistant acp-run-message',
    'data-transcript-segment-key': segment.key,
  }, el('div', { class: 'agent-bubble acp-transcript-body' }));
  syncAssistantMessage(message, segment.chunks);
  return message;
}

function transcriptPromptMessage(segment) {
  const body = el('div', { class: 'agent-bubble-text' });
  renderPromptContent(body, segment.text);
  const message = el('div', {
    class: 'agent-message acp-prompt-message',
    'data-transcript-segment-key': segment.key,
  }, el('div', { class: 'agent-bubble' }, body));
  if (segment.at) {
    message.append(el('div', { class: 'agent-message-meta', text: fmtTime(segment.at) }));
  }
  return message;
}

function updateTranscriptMessage(message, segment) {
  if (segment.kind === 'prompt') {
    const body = message.querySelector(':scope > .agent-bubble > div');
    if (body) renderPromptContent(body, segment.text);
    const meta = message.querySelector(':scope > .agent-message-meta');
    if (segment.at && meta) meta.textContent = fmtTime(segment.at);
    return;
  }
  syncAssistantMessage(message, segment.chunks);
}

function renderPromptContent(body, text) {
  if (!body) return;
  const raw = typeof text === 'string' ? text : '';
  body.innerHTML = raw ? renderMarkdown(raw) : '';
  if (!body.innerHTML && raw) body.textContent = raw;
}

function syncAssistantMessage(message, entries) {
  const body = message.querySelector(':scope > .acp-transcript-body');
  if (!body) return;
  const lines = [...body.querySelectorAll(':scope > .agent-round')];
  for (let index = 0; index < entries.length; index++) {
    const { chunk, index: sourceIndex } = entries[index];
    const key = transcriptChunkKey(chunk, sourceIndex ?? index);
    let line = lines[index];
    if (line?.dataset.transcriptKey !== key) {
      const replacement = transcriptLine(chunk, key);
      if (line) line.replaceWith(replacement);
      else body.append(replacement);
    } else {
      updateTranscriptLine(line, chunk);
    }
  }
  for (const line of lines.slice(entries.length)) line.remove();
}

function normalizeTranscript(transcript) {
  const chunks = [];
  const toolIndexes = new Map();
  let usage = '';
  for (const original of transcript || []) {
    const chunk = { ...original };
    if (chunk.kind === 'usage') {
      usage = chunk.text || usage;
      continue;
    }
    if (chunk.kind === 'tool' && chunk.tool_id) {
      const existingIndex = toolIndexes.get(chunk.tool_id);
      if (existingIndex !== undefined) {
        chunks[existingIndex] = { ...chunks[existingIndex], ...withoutEmptyValues(chunk) };
        continue;
      }
      toolIndexes.set(chunk.tool_id, chunks.length);
    }
    const previous = chunks.at(-1);
    if ((chunk.kind === 'text' || chunk.kind === 'thought') && previous?.kind === chunk.kind) {
      previous.text = (previous.text || '') + (chunk.text || '');
      continue;
    }
    chunks.push(chunk);
  }
  return { chunks, usage };
}

function withoutEmptyValues(chunk) {
  return Object.fromEntries(Object.entries(chunk).filter(([, value]) => value !== '' && value != null));
}

function syncUsagePill(panel, usage) {
  const meta = panel.querySelector('.acp-run-meta');
  if (!meta) return;
  let pill = meta.querySelector('.acp-usage-pill');
  if (!usage) {
    pill?.remove();
    return;
  }
  if (!pill) {
    pill = el('span', { class: 'acp-usage-pill' });
    meta.append(pill);
  }
  pill.textContent = usage;
}

function transcriptChunkKey(chunk, index) {
  if (chunk.kind === 'tool' && chunk.tool_id) return `tool:${chunk.tool_id}`;
  return `${chunk.kind || 'text'}:${index}`;
}

function transcriptLine(chunk, key) {
  const kind = chunk.kind || 'text';
  const line = el('div', {
    class: `agent-round acp-transcript-round is-${kind}`,
    'data-transcript-key': key,
  });
  if (kind === 'thought') line.append(reasoningDisclosure(chunk.text || ''));
  else if (kind === 'tool') line.append(el('div', { class: 'agent-tool-stack' }, acpToolCard(chunk)));
  else line.append(el('div', { class: 'agent-bubble-text' }));
  updateTranscriptLine(line, chunk);
  return line;
}

function updateTranscriptLine(line, chunk) {
  if (chunk.kind === 'tool') {
    const card = line.querySelector('.agent-tool-terminal, .agent-tool-event');
    if (!card) return;
    const status = normalizeAcpToolStatus(chunk.tool_status);
    const name = chunk.tool_kind || chunk.tool_id || chunk.tool_title || 'tool';
    const action = chunk.tool_title || chunk.tool_kind || chunk.tool_id || 'Tool';
    // setToolTerminalOutput repaints summary/result/raw; setToolTerminalStatus
    // inside it recomputes the title from the tool name, so the ACP-provided
    // title is applied after.
    setToolTerminalOutput(card, chunk.text || '', status, acpToolMeta(chunk, status));
    const terminalTitle = card.querySelector('.agent-tool-terminal-title');
    if (terminalTitle) terminalTitle.textContent = name;
    const terminalAction = card.querySelector('.agent-tool-terminal-action');
    if (terminalAction) terminalAction.textContent = chunk.tool_title || name;
    const eventTitle = card.querySelector('.agent-tool-event-title');
    if (eventTitle) eventTitle.textContent = chunk.tool_title || name;
    return;
  }
  if (chunk.kind === 'thought') {
    setReasoningSource(line.querySelector('.agent-reasoning'), chunk.text || '');
    return;
  }
  const body = line.querySelector('.agent-bubble-text');
  if (!body) return;
  const text = chunk.text || chunk.kind || '';
  if (chunk.kind === 'text' || chunk.kind === 'plan' || chunk.kind === 'status') {
    const previous = body._transcriptRaw || '';
    if (previous && !text.startsWith(previous)) body.replaceChildren();
    body._transcriptRaw = text;
    const changed = incrementalRender(body, text);
    for (const node of changed) {
      void highlightCode(node);
      attachZoomButtons(node);
    }
    return;
  }
  body.textContent = text;
}

function acpToolCard(chunk) {
  const status = normalizeAcpToolStatus(chunk.tool_status);
  const actionText = chunk.tool_title || chunk.tool_kind || chunk.tool_id || 'Tool';
  const card = renderToolJob({
    name: chunk.tool_kind || chunk.tool_title || 'tool',
    args: {},
    status,
    output: chunk.text || '',
    presentation: {
      variant: 'terminal',
      action: actionText,
      request: '',
      result: {
        format: 'terminal',
        summary: acpToolMeta(chunk, status),
        text: chunk.text || '',
      },
    },
  });
  const action = card.querySelector('.agent-tool-terminal-action');
  if (action) action.textContent = chunk.tool_title || chunk.tool_kind || 'Tool';
  const title = card.querySelector('.agent-tool-terminal-title');
  if (title) title.textContent = chunk.tool_kind || chunk.tool_id || chunk.tool_title || 'tool';
  if (chunk.tool_id) card.dataset.acpToolId = chunk.tool_id;
  return card;
}

function normalizeAcpToolStatus(status) {
  switch (status) {
    case 'completed':
    case 'success':
    case 'ok':
      return 'ok';
    case 'failed':
    case 'error':
    case 'fail':
    case 'cancelled':
    case 'interrupted':
      return 'fail';
    default:
      return 'running';
  }
}

function acpToolMeta(chunk, status) {
  if (chunk.tool_status) return chunk.tool_status.replaceAll('_', ' ');
  if (status === 'running') return 'Running';
  if (status === 'fail') return 'Failed';
  return 'Completed';
}

function statusLabel(status) {
  switch (status) {
    case 'starting': return 'starting';
    case 'running': return 'running';
    case 'completed': return 'done';
    case 'failed': return 'failed';
    case 'cancelled': return 'stopped';
    default: return status || 'idle';
  }
}

function riskLabel(tier) {
  switch (tier) {
    case 'bypass': return 'bypass';
    case 'edit_confirmed': return 'edits ok';
    default: return 'read-only';
  }
}

function modelLabel(run) {
  const status = run.model_selection_status;
  if (status === 'rejected') return `${run.current_model_id} (rejected)`;
  if (status === 'unverified') return `${run.current_model_id} (unverified)`;
  return run.current_model_id;
}

function shortPath(path) {
  if (!path) return 'no workspace';
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts.slice(-2).join('/') || path;
}
