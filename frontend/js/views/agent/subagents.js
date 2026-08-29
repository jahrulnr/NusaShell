/* ACP subagent live UI: dock chips, right drawer, peek popup. */

import { rpc, on } from '../../rpc.js';
import { el } from '../../ui.js';
import { incrementalRender } from '../../incremental-render.js';
import { highlightCode } from '../../highlight-render.js';
import { attachZoomButtons } from '../../media-zoom.js';

const LIVE = new Set(['starting', 'running']);
const RECENT_MS = 2 * 60 * 1000;

const state = {
  runs: new Map(),
  conversationId: '',
  drawerRunId: '',
  popupRunId: '',
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
    content.replaceChildren(el('p', { class: 'acp-empty', text: state.drawerRunId ? 'Loading transcript…' : 'No subagents in this conversation. The parent agent can spawn them with the subagent tool.' }));
    return;
  }
  state.drawerRunId = selected.id;
  const existing = content.querySelector(`.acp-run-panel[data-run-id="${selected.id}"]`);
  if (!existing) {
    content.replaceChildren(buildRunPanel(selected));
  } else {
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
    if (!item.parentNode) list.append(item);
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
    el('div', { class: 'acp-transcript', 'aria-label': 'Subagent transcript' }),
  );

  if (run.error) {
    panel.append(el('p', { class: 'acp-run-error', text: run.error }));
  }
  syncTranscript(panel, run.transcript || []);
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
  syncTranscriptWithFollow(panel, run.transcript || []);
}

function syncTranscriptWithFollow(panel, transcript) {
  const box = panel.querySelector('.acp-transcript');
  if (!box) return;
  const scroller = box.closest('.acp-run-content, .acp-popup');
  const atBottom = !scroller || scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 60;
  syncTranscript(panel, transcript);
  if (atBottom && scroller) scroller.scrollTop = scroller.scrollHeight;
}

export function syncTranscript(panel, transcript) {
  const box = panel?.querySelector('.acp-transcript');
  if (!box) return;
  const { chunks, usage } = normalizeTranscript(transcript);
  syncUsagePill(panel, usage);
  if (!chunks.length) {
    box.replaceChildren(el('p', { class: 'acp-empty', text: 'Waiting for the first update…' }));
    return;
  }
  box.querySelector('.acp-empty')?.remove();
  const lines = [...box.querySelectorAll(':scope > .acp-transcript-line')];
  for (let index = 0; index < chunks.length; index++) {
    const chunk = chunks[index];
    const key = transcriptChunkKey(chunk, index);
    let line = lines[index];
    if (line?.dataset.transcriptKey !== key) {
      const replacement = transcriptLine(chunk, key);
      if (line) line.replaceWith(replacement);
      else box.append(replacement);
      line = replacement;
    } else {
      updateTranscriptLine(line, chunk);
    }
  }
  for (const line of lines.slice(chunks.length)) line.remove();
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
  const line = el('div', {
    class: `acp-transcript-line is-${chunk.kind || 'text'}`,
    'data-transcript-key': key,
  });
  if (chunk.kind === 'tool' || chunk.kind === 'thought') {
    line.append(el('span', { class: 'acp-transcript-kind' }));
  }
  line.append(el('div', { class: 'acp-transcript-text' }));
  updateTranscriptLine(line, chunk);
  return line;
}

function updateTranscriptLine(line, chunk) {
  if (chunk.kind === 'tool') {
    line.querySelector('.acp-transcript-kind').textContent = chunk.tool_kind || 'tool';
    line.querySelector('.acp-transcript-text').textContent = `${chunk.tool_title || chunk.tool_id || ''} ${chunk.tool_status || ''}`.trim();
    return;
  }
  if (chunk.kind === 'thought') {
    line.querySelector('.acp-transcript-kind').textContent = 'think';
  }
  const body = line.querySelector('.acp-transcript-text');
  const text = chunk.text || chunk.kind || '';
  if (chunk.kind === 'thought' || chunk.kind === 'text') {
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
