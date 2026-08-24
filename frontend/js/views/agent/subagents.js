/* ACP subagent live UI: dock chips, right drawer, peek popup. */

import { rpc, on } from '../../rpc.js';
import { el } from '../../ui.js';
import { renderMarkdown } from '../../markdown.js';

const LIVE = new Set(['starting', 'running']);
const RECENT_MS = 2 * 60 * 1000;

const state = {
  runs: new Map(),
  conversationId: '',
  drawerRunId: '',
  popupRunId: '',
  bound: false,
  // runId -> number of transcript chunks already rendered into the DOM.
  // Lets live delta updates patch the panel in place instead of rebuilding
  // it (which reset scroll and made the view jump around).
  renderedChunks: new Map(),
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

function upsertRun(run, { silent } = {}) {
  if (!run?.id) return;
  state.runs.set(run.id, run);
  if (!silent) renderAll();
}

function activeConversationId() {
  return state.getActiveConversationId?.() || state.conversationId;
}

// Dock chips: live runs of the active conversation plus runs that settled
// within RECENT_MS. Sorted by start time (stable) — never by recency, so
// live delta updates cannot reshuffle chips under the cursor.
function visibleRuns() {
  const now = Date.now();
  const active = activeConversationId();
  return [...state.runs.values()]
    .filter((run) => {
      if (active && run.conversation_id !== active) return false;
      if (LIVE.has(run.status)) return true;
      const ended = Date.parse(run.ended_at || run.updated_at || '') || 0;
      return ended && now - ended < RECENT_MS;
    })
    .sort((a, b) => startedAtMs(a) - startedAtMs(b));
}

// Every run of the active conversation, settled included — the drawer's
// switcher and the "open transcript" fallback must reach completed runs
// as long as the conversation exists.
function conversationRuns() {
  const active = activeConversationId();
  return [...state.runs.values()]
    .filter((run) => !active || run.conversation_id === active)
    .sort((a, b) => startedAtMs(a) - startedAtMs(b));
}

function startedAtMs(run) {
  return Date.parse(run.started_at || run.updated_at || '') || 0;
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
  // position preserved across live deltas); only new runs are appended
  // and settled runs removed. Runs are appended in spawn order, so the
  // DOM order matches the stable sort without reshuffling.
  const seen = new Set();
  for (const run of runs) {
    seen.add(run.id);
    let chip = list.querySelector(`[data-run-id="${run.id}"]`);
    if (!chip) {
      chip = buildDockChip(run);
      list.append(chip);
    } else {
      updateDockChip(chip, run);
    }
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
  const drawer = document.getElementById('acp-drawer');
  const overlay = document.getElementById('acp-drawer-overlay');
  drawer.hidden = false;
  drawer.removeAttribute('inert');
  drawer.setAttribute('aria-hidden', 'false');
  drawer.classList.add('active');
  overlay.classList.add('active');
  overlay.setAttribute('aria-hidden', 'false');
  renderDrawer();
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
  const overlay = document.getElementById('acp-popup-overlay');
  overlay.hidden = false;
  renderPopup();
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
  if (!selected) {
    body.replaceChildren(el('p', { class: 'acp-empty', text: state.drawerRunId ? 'Loading transcript…' : 'No subagents in this conversation. The parent agent can spawn them with the subagent tool.' }));
    return;
  }
  state.drawerRunId = selected.id;
  const switcher = runSwitcher(runs, selected.id, (id) => { state.drawerRunId = id; renderDrawer(); });
  // Same run: patch in place (status pill + transcript deltas) so the
  // scroll position survives live updates. Different run: rebuild.
  const existing = body.querySelector(`.acp-run-panel[data-run-id="${selected.id}"]`);
  if (!existing) {
    body.replaceChildren(switcher, buildRunPanel(selected));
  } else {
    const prevSwitcher = body.querySelector('.acp-run-switcher');
    if (prevSwitcher) prevSwitcher.replaceWith(switcher);
    patchRunPanel(existing, selected);
  }
}

function renderPopup() {
  const run = state.runs.get(state.popupRunId);
  const title = document.getElementById('acp-popup-title');
  const body = document.getElementById('acp-popup-body');
  if (!run) {
    title.textContent = 'Subagent';
    body.replaceChildren(el('p', { class: 'acp-empty', text: 'This subagent is no longer available.' }));
    return;
  }
  title.textContent = run.agent_name || 'Subagent';
  // Same run: patch in place so the popup transcript doesn't reset.
  const existing = body.querySelector(`.acp-run-panel[data-run-id="${run.id}"]`);
  if (!existing) {
    body.replaceChildren(buildRunPanel(run));
  } else {
    patchRunPanel(existing, run);
  }
}

function runSwitcher(runs, selectedId, onSelect) {
  if (runs.length < 2) return el('div');
  const row = el('div', { class: 'acp-run-switcher' });
  for (const run of runs) {
    const btn = el('button', {
      class: `acp-run-switch${run.id === selectedId ? ' is-active' : ''} is-${run.status}`,
      type: 'button',
      text: run.agent_name || 'ACP',
    });
    btn.addEventListener('click', () => onSelect(run.id));
    row.append(btn);
  }
  return row;
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
    el('div', { class: 'acp-transcript', 'aria-label': 'Subagent transcript' },
      ...(run.transcript || []).length
        ? (run.transcript || []).map(transcriptLine)
        : [el('p', { class: 'acp-empty', text: 'Waiting for the first update…' })],
    ),
  );

  if (run.error) {
    panel.append(el('p', { class: 'acp-run-error', text: run.error }));
  }
  state.renderedChunks.set(run.id, (run.transcript || []).length);
  return panel;
}

// patchRunPanel updates a live panel in place: status pill + model pill +
// error, then appends only the transcript chunks that arrived since the
// last render. Auto-follows the bottom only when the user is already at
// the bottom — reading earlier output is never interrupted.
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
  appendTranscriptDeltas(panel, run);
}

// appendTranscriptDeltas appends chunks that are not yet in the DOM and
// keeps the view pinned to the bottom only while the user is already
// there (same auto-follow behavior as the conversation thread). The
// transcript flows inside the drawer/popup body, so the scrollable
// ancestor is the follow target.
function appendTranscriptDeltas(panel, run) {
  const box = panel.querySelector('.acp-transcript');
  if (!box) return;
  const chunks = run.transcript || [];
  const rendered = state.renderedChunks.get(run.id) || 0;
  if (chunks.length <= rendered) return;
  const empty = box.querySelector('.acp-empty');
  if (empty) empty.remove();
  const scroller = box.closest('.drawer-body, .acp-popup');
  const atBottom = !scroller || scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 60;
  const frag = document.createDocumentFragment();
  for (const c of chunks.slice(rendered)) frag.append(transcriptLine(c));
  box.append(frag);
  state.renderedChunks.set(run.id, chunks.length);
  if (atBottom && scroller) scroller.scrollTop = scroller.scrollHeight;
}

function transcriptLine(chunk) {
  if (chunk.kind === 'tool') {
    return el('div', { class: 'acp-transcript-line is-tool' },
      el('span', { class: 'acp-transcript-kind', text: chunk.tool_kind || 'tool' }),
      el('span', { text: `${chunk.tool_title || chunk.tool_id || ''} ${chunk.tool_status || ''}`.trim() }),
    );
  }
  const line = el('div', { class: `acp-transcript-line is-${chunk.kind || 'text'}` });
  if (chunk.kind === 'thought') line.append(el('span', { class: 'acp-transcript-kind', text: 'think' }));
  const body = el('div', { class: 'acp-transcript-text' });
  const text = chunk.text || chunk.kind || '';
  if (chunk.kind === 'thought' || chunk.kind === 'text') {
    body.innerHTML = renderMarkdown(text);
  } else {
    body.textContent = text;
  }
  line.append(body);
  return line;
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
