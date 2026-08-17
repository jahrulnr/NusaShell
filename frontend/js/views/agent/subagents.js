/* ACP subagent live UI: dock chips, right drawer, peek popup, permission overlay. */

import { rpc, on } from '../../rpc.js';
import { el, toast, confirmDialog } from '../../ui.js';

const LIVE = new Set(['starting', 'running', 'waiting_permission']);
const RECENT_MS = 2 * 60 * 1000;

const state = {
  runs: new Map(),
  conversationId: '',
  drawerRunId: '',
  popupRunId: '',
  permission: null,
  bound: false,
};

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
  document.getElementById('acp-permission-allow')?.addEventListener('click', () => decidePermission('allow_once'));
  document.getElementById('acp-permission-allow-session')?.addEventListener('click', () => decidePermission('allow_session'));
  document.getElementById('acp-permission-deny')?.addEventListener('click', () => decidePermission('deny'));
  document.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return;
    if (!document.getElementById('acp-permission-overlay')?.hidden) return;
    if (!document.getElementById('acp-popup-overlay')?.hidden) {
      closePopup();
      return;
    }
    if (!document.getElementById('acp-drawer')?.hidden) closeDrawer();
  });

  on('acp.run.started', (payload) => upsertRun(payload?.run));
  on('acp.run.updated', (payload) => upsertRun(payload?.run));
  on('acp.run.done', (payload) => upsertRun(payload?.run));
  on('acp.permission.requested', (payload) => {
    const run = state.runs.get(payload?.run_id);
    queuePermission(payload?.run_id, payload?.permission, run);
  });
  on('acp.permission.decided', (payload) => {
    if (state.permission?.id === payload?.id) {
      state.permission = null;
      renderPermission();
    }
  });
  on('acp.session.mode_changed', (payload) => {
    const run = state.runs.get(payload?.run_id);
    if (run) {
      run.current_mode_id = payload.mode_id;
      renderAll();
    }
  });

  void hydrate();
}

export function setSubagentConversation(id) {
  state.conversationId = id || '';
  renderDock();
  if (!document.getElementById('acp-drawer')?.hidden) renderDrawer();
}

export function closeAcpOverlays({ keepPermission = true } = {}) {
  closePopup();
  closeDrawer();
  if (!keepPermission) {
    state.permission = null;
    renderPermission();
  }
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
  if (run.pending_permission) {
    queuePermission(run.id, run.pending_permission, run);
  }
  if (!silent) renderAll();
}

function visibleRuns() {
  const now = Date.now();
  const active = state.getActiveConversationId?.() || state.conversationId;
  return [...state.runs.values()]
    .filter((run) => {
      if (LIVE.has(run.status)) return true;
      if (active && run.conversation_id === active) {
        const ended = Date.parse(run.ended_at || run.updated_at || '') || 0;
        return ended && now - ended < RECENT_MS;
      }
      return false;
    })
    .sort((a, b) => Date.parse(b.updated_at || b.started_at || 0) - Date.parse(a.updated_at || a.started_at || 0));
}

function firstVisibleRunId() {
  return visibleRuns()[0]?.id || [...state.runs.keys()][0] || '';
}

function renderAll() {
  renderDock();
  if (!document.getElementById('acp-drawer')?.hidden) renderDrawer();
  if (!document.getElementById('acp-popup-overlay')?.hidden) renderPopup();
  renderPermission();
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
  list.replaceChildren(...runs.map((run) => {
    const chip = el('button', {
      class: `acp-dock-chip is-${run.status}`,
      type: 'button',
      role: 'listitem',
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
  }));
}

function openDrawer(runId) {
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
  const runs = visibleRuns();
  const selected = state.runs.get(state.drawerRunId) || runs[0];
  document.getElementById('acp-drawer-title').textContent = selected ? (selected.agent_name || 'Subagent') : 'Subagents';
  document.getElementById('acp-drawer-subtitle').textContent = selected
    ? `${statusLabel(selected.status)} · ${shortPath(selected.workspace)}`
    : 'No live ACP sessions';
  const body = document.getElementById('acp-drawer-body');
  if (!selected) {
    body.replaceChildren(el('p', { class: 'acp-empty', text: 'No subagents running. The parent agent can spawn them with the subagent tool.' }));
    return;
  }
  state.drawerRunId = selected.id;
  body.replaceChildren(
    runSwitcher(runs, selected.id, (id) => { state.drawerRunId = id; renderDrawer(); }),
    runPanel(selected, { compact: false }),
  );
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
  body.replaceChildren(runPanel(run, { compact: true }));
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

function runPanel(run, { compact }) {
  const live = LIVE.has(run.status);
  const modes = run.available_modes || [];
  const panel = el('div', { class: 'acp-run-panel' },
    el('div', { class: 'acp-run-meta' },
      el('span', { class: `acp-status-pill is-${run.status}`, text: statusLabel(run.status) }),
      el('span', { class: `acp-risk-pill is-${run.risk_tier || 'read_only'}`, text: riskLabel(run.risk_tier) }),
      run.current_model_id ? el('span', { class: 'acp-model-pill', text: modelLabel(run) }) : null,
    ),
    el('div', { class: 'acp-run-workspace', title: run.workspace || '' },
      el('span', { text: 'Workspace' }),
      el('code', { text: run.workspace || '—' }),
    ),
    modes.length ? modeRow(run, modes) : null,
    el('div', { class: 'acp-transcript', 'aria-label': 'Subagent transcript' },
      ...(run.transcript || []).length
        ? (run.transcript || []).slice(-80).map(transcriptLine)
        : [el('p', { class: 'acp-empty', text: 'Waiting for the first update…' })],
    ),
  );

  if (live) {
    const steer = el('form', { class: 'acp-steer-form' },
      el('textarea', {
        rows: compact ? 2 : 3,
        placeholder: 'Steer this subagent…',
        'aria-label': 'Steer instruction',
      }),
      el('button', { class: 'mini-btn', type: 'submit', text: 'Steer' }),
    );
    steer.addEventListener('submit', async (event) => {
      event.preventDefault();
      const text = steer.querySelector('textarea').value.trim();
      if (!text) return;
      try {
        await rpc('acp.runs.steer', { id: run.id, text });
        steer.querySelector('textarea').value = '';
        toast('Steer queued', 'success');
      } catch (err) {
        toast(err.message, 'error');
      }
    });
    panel.append(steer);

    const actions = el('div', { class: 'acp-run-actions' },
      el('button', { class: 'mini-btn ghost', type: 'button', text: 'Promote edits', dataset: { action: 'edit' } }),
      el('button', { class: 'mini-btn ghost', type: 'button', text: 'Bypass prompts', dataset: { action: 'bypass' } }),
      el('button', { class: 'mini-btn danger', type: 'button', text: 'Stop', dataset: { action: 'stop' } }),
    );
    actions.addEventListener('click', async (event) => {
      const action = event.target?.dataset?.action;
      if (action === 'stop') {
        const ok = await confirmDialog('Stop subagent', `Cancel “${run.agent_name}”? Pending permissions are denied.`, 'Stop');
        if (!ok) return;
        try { await rpc('acp.runs.stop', { id: run.id }); } catch (err) { toast(err.message, 'error'); }
      }
      if (action === 'edit') {
        const ok = await confirmDialog('Allow edits', 'Promote this session to auto-approve file edits inside the bound workspace. Shell still asks.', 'Promote');
        if (!ok) return;
        try { await rpc('acp.runs.promote', { id: run.id, tier: 'edit_confirmed' }); } catch (err) { toast(err.message, 'error'); }
      }
      if (action === 'bypass') {
        const ok = await confirmDialog('Bypass all prompts', 'This auto-approves every tool for this session, including shell. Only do this when you trust the agent and the workspace.', 'Bypass');
        if (!ok) return;
        try { await rpc('acp.runs.promote', { id: run.id, tier: 'bypass' }); } catch (err) { toast(err.message, 'error'); }
      }
    });
    panel.append(actions);
  } else if (run.error) {
    panel.append(el('p', { class: 'acp-run-error', text: run.error }));
  }
  return panel;
}

function modeRow(run, modes) {
  const row = el('div', { class: 'acp-mode-row', 'aria-label': 'Session mode' });
  for (const mode of modes) {
    const btn = el('button', {
      class: `acp-mode-chip${mode.id === run.current_mode_id ? ' is-active' : ''}`,
      type: 'button',
      title: mode.description || mode.name,
      text: mode.name || mode.id,
    });
    btn.addEventListener('click', async () => {
      if (mode.id === run.current_mode_id) return;
      try {
        await rpc('acp.runs.set-mode', { id: run.id, mode_id: mode.id });
      } catch (err) {
        toast(err.message, 'error');
      }
    });
    row.append(btn);
  }
  return row;
}

function transcriptLine(chunk) {
  if (chunk.kind === 'tool') {
    return el('div', { class: 'acp-transcript-line is-tool' },
      el('span', { class: 'acp-transcript-kind', text: chunk.tool_kind || 'tool' }),
      el('span', { text: `${chunk.tool_title || chunk.tool_id || ''} ${chunk.tool_status || ''}`.trim() }),
    );
  }
  return el('div', { class: `acp-transcript-line is-${chunk.kind || 'text'}` },
    chunk.kind === 'thought' ? el('span', { class: 'acp-transcript-kind', text: 'think' }) : null,
    el('span', { text: chunk.text || chunk.kind || '' }),
  );
}

function queuePermission(runId, permission, run) {
  if (!permission?.id) return;
  if (state.permission?.id === permission.id) return;
  state.permission = { ...permission, run_id: runId, agent_name: run?.agent_name };
  renderPermission();
}

function renderPermission() {
  const overlay = document.getElementById('acp-permission-overlay');
  if (!overlay) return;
  const perm = state.permission;
  overlay.hidden = !perm;
  if (!perm) return;
  document.getElementById('acp-permission-title').textContent = perm.tool_title || 'Tool permission';
  const body = document.getElementById('acp-permission-body');
  body.replaceChildren(
    el('p', { text: `${perm.agent_name || 'ACP subagent'} wants to run a ${perm.tool_kind || 'tool'}. Timeout denies.` }),
    ...(perm.paths || []).length
      ? [el('ul', { class: 'acp-permission-paths' }, ...(perm.paths.map((p) => el('li', {}, el('code', { text: p })))))]
      : [],
  );
}

async function decidePermission(outcome) {
  const perm = state.permission;
  if (!perm) return;
  const option = (perm.options || []).find((o) => {
    if (outcome === 'deny') return o.kind === 'reject_once' || o.kind === 'reject_always';
    if (outcome === 'allow_session') return o.kind === 'allow_always' || o.kind === 'allow_once';
    return o.kind === 'allow_once';
  });
  try {
    await rpc('acp.permission.decide', {
      run_id: perm.run_id,
      id: perm.id,
      option_id: option?.id || '',
      outcome,
    });
    state.permission = null;
    renderPermission();
  } catch (err) {
    toast(err.message, 'error');
  }
}

function statusLabel(status) {
  switch (status) {
    case 'starting': return 'starting';
    case 'running': return 'running';
    case 'waiting_permission': return 'needs you';
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
