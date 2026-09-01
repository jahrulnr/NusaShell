// Automation workspace: workflows, runs, schedules, events.

import { rpc, on } from '../rpc.js';
import { el, toast, dialog, confirmDialog } from '../ui.js';

const state = {
  tab: 'workflows',
  workflows: [],
  runs: [],
  schedules: [],
  events: [],
  selected: null,
  blockedProvider: '',
};

export async function initAutomation() {
  document.getElementById('automation-tabs').addEventListener('click', (e) => {
    const btn = e.target.closest('[data-auto-tab]');
    if (!btn) return;
    setTab(btn.dataset.autoTab);
    refresh().catch((err) => toast(err.message || String(err), 'error'));
  });
  document.getElementById('automation-new-btn').addEventListener('click', () => createWizard());
  document.getElementById('automation-enable-provider-btn').addEventListener('click', enableBlockedProvider);
  on('automation.run.created', () => refresh().catch(() => {}));
  on('automation.run.completed', () => refresh().catch(() => {}));
  on('automation.run.failed', () => refresh().catch(() => {}));
  on('automation.run.waiting', () => refresh().catch(() => {}));
  on('automation.run.blocked', () => refresh().catch(() => {}));
  await refresh();
}

export async function refresh() {
  try {
    const [{ workflows }, { runs }, { schedules }, { events }] = await Promise.all([
      rpc('automation.list'),
      rpc('automation.runs.list'),
      rpc('automation.schedules'),
      rpc('automation.events'),
    ]);
    state.workflows = workflows || [];
    state.runs = runs || [];
    state.schedules = schedules || [];
    state.events = events || [];
  } catch (err) {
    toast(err.message || 'Automation RPC failed', 'error');
    return;
  }
  renderStats();
  renderList();
  if (state.selected) selectCurrent();
}

function setTab(tab) {
  state.tab = tab;
  state.selected = null;
  document.querySelectorAll('[data-auto-tab]').forEach((btn) => {
    const on = btn.dataset.autoTab === tab;
    btn.classList.toggle('active', on);
    btn.setAttribute('aria-selected', String(on));
  });
  const titles = { workflows: 'Workflows', runs: 'Runs', schedules: 'Schedules', events: 'Events' };
  document.getElementById('automation-list-title').textContent = titles[tab];
  document.getElementById('automation-detail').innerHTML = '';
  document.getElementById('automation-detail').append(
    el('div', { class: 'automation-empty' },
      el('strong', { text: 'No selection' }),
      el('span', { text: 'Pick a row to inspect status, DAG, or wake time.' }),
    ),
  );
  document.getElementById('automation-detail-actions').innerHTML = '';
}

function renderStats() {
  const runnable = state.workflows.filter((w) => w.availability === 'runnable').length;
  const blocked = state.workflows.filter((w) => w.availability === 'blocked').length;
  const waiting = state.runs.filter((r) => r.status === 'waiting').length;
  document.getElementById('automation-stat-runnable').textContent = `${runnable} runnable`;
  document.getElementById('automation-stat-blocked').textContent = `${blocked} blocked`;
  document.getElementById('automation-stat-waiting').textContent = `${waiting} waiting`;
  const firstBlocked = state.workflows.find((w) => w.availability === 'blocked');
  const banner = document.getElementById('automation-blocked-banner');
  if (firstBlocked) {
    banner.hidden = false;
    document.getElementById('automation-blocked-text').textContent = firstBlocked.blocked_reason || `"${firstBlocked.name}" is blocked.`;
    state.blockedProvider = (firstBlocked.blocked_reason || '').split(' ').pop();
  } else {
    banner.hidden = true;
    state.blockedProvider = '';
  }
}

function renderList() {
  const list = document.getElementById('automation-list');
  list.innerHTML = '';
  let rows = [];
  if (state.tab === 'workflows') {
    rows = state.workflows.map((w) => ({
      id: w.id,
      title: w.name || w.id,
      meta: (w.triggers || []).map((t) => t.kind).join(', ') || 'manual',
      pill: w.availability || (w.enabled ? 'runnable' : 'disabled'),
      item: w,
    }));
  } else if (state.tab === 'runs') {
    rows = state.runs.map((r) => ({
      id: r.id,
      title: r.name || r.id,
      meta: r.status + (r.wake_at ? ` · wake ${r.wake_at}` : ''),
      pill: r.status,
      item: r,
    }));
  } else if (state.tab === 'schedules') {
    rows = (state.schedules || []).map((s) => ({
      id: s.id,
      title: s.workflow_id,
      meta: `${s.kind} · ${s.next_run_at || ''}`,
      pill: s.status,
      item: s,
    }));
  } else {
    rows = (state.events || []).map((e) => ({
      id: e.id,
      title: e.type,
      meta: e.subject || e.source || '',
      pill: 'event',
      item: e,
    }));
  }
  document.getElementById('automation-list-count').textContent = String(rows.length);
  if (!rows.length) {
    list.append(el('div', { class: 'automation-empty' },
      el('strong', { text: 'Nothing here yet' }),
      el('span', { text: state.tab === 'workflows' ? 'Create an Automation workflow or discover pipeline files.' : 'Waiting for activity.' }),
    ));
    return;
  }
  for (const row of rows) {
    const btn = el('button', { class: 'automation-row', type: 'button' },
      el('span', { class: 'automation-row-title', text: row.title }),
      el('span', { class: `automation-pill ${row.pill}`, text: row.pill }),
      el('span', { class: 'automation-row-meta', text: row.meta }),
    );
    if (state.selected && state.selected.id === row.id) btn.classList.add('active');
    btn.addEventListener('click', () => selectItem(row.item));
    list.append(btn);
  }
}

function selectCurrent() {
  const id = state.selected?.id;
  if (!id) return;
  const pool = state.tab === 'workflows' ? state.workflows
    : state.tab === 'runs' ? state.runs
      : state.tab === 'schedules' ? state.schedules
        : state.events;
  const found = (pool || []).find((x) => x.id === id);
  if (found) selectItem(found);
}

function selectItem(item) {
  state.selected = item;
  renderList();
  const detail = document.getElementById('automation-detail');
  const actions = document.getElementById('automation-detail-actions');
  detail.innerHTML = '';
  actions.innerHTML = '';
  document.getElementById('automation-detail-title').textContent = item.name || item.type || item.id;
  if (state.tab === 'workflows') renderWorkflowDetail(item, detail, actions);
  else if (state.tab === 'runs') renderRunDetail(item, detail, actions);
  else if (state.tab === 'schedules') {
    detail.append(el('p', { text: `Next fire ${item.next_run_at || '—'} (${item.kind}, ${item.status})` }));
  } else {
    detail.append(el('pre', { class: 'automation-yaml', text: JSON.stringify(item, null, 2) }));
  }
}

function renderWorkflowDetail(w, detail, actions) {
  detail.append(
    el('p', { text: w.blocked_reason || `Availability: ${w.availability || 'runnable'}` }),
    el('div', { class: 'automation-dag' }, ...(w.jobs || []).map((j) =>
      el('div', { class: 'automation-job-card' },
        el('h3', { text: j.name || j.id }),
        el('div', { class: 'automation-step', text: (j.needs || []).length ? `needs ${j.needs.join(', ')}` : 'no needs' }),
        ...(j.steps || []).map((s) => el('div', { class: 'automation-step', text: s.run || s.uses || s.name || s.id })),
      ),
    )),
  );
  const runBtn = el('button', { class: 'mini-btn', type: 'button', text: 'Run' });
  const invalid = w.availability === 'invalid';
  runBtn.disabled = invalid;
  runBtn.addEventListener('click', async () => {
    try {
      await rpc('automation.run', { id: w.id });
      toast(`Started ${w.name}`, 'success');
      setTab('runs');
      await refresh();
    } catch (err) {
      toast(err.message || String(err), 'error');
    }
  });
  const toggle = el('button', { class: 'mini-btn ghost', type: 'button', text: w.enabled ? 'Disable' : 'Enable' });
  if (invalid && !w.enabled) toggle.disabled = true;
  toggle.addEventListener('click', async () => {
    try {
      await rpc(w.enabled ? 'automation.disable' : 'automation.enable', { id: w.id });
      await refresh();
    } catch (err) {
      toast(err.message || String(err), 'error');
    }
  });
  const del = el('button', { class: 'mini-btn ghost danger', type: 'button', text: 'Delete' });
  del.addEventListener('click', async () => {
    const ok = await confirmDialog('Delete automation', `"${w.name}" will be removed.`, 'Delete');
    if (!ok) return;
    try {
      await rpc('automation.delete', { id: w.id });
      state.selected = null;
      await refresh();
    } catch (err) {
      toast(err.message || String(err), 'error');
    }
  });
  actions.append(runBtn, toggle, del);
}

function renderRunDetail(run, detail, actions) {
  if (run.status === 'waiting') {
    detail.append(el('p', { text: `Waiting until ${run.wake_at || 'an event'}. The runner is free.` }));
  }
  if (run.blocked_reason) {
    detail.append(el('p', { text: run.blocked_reason }));
  }
  detail.append(el('div', { class: 'automation-dag' }, ...(run.jobs || []).map((j) =>
    el('div', { class: 'automation-job-card' },
      el('h3', { text: j.name || j.id }),
      el('span', { class: `automation-pill ${j.status}`, text: j.status }),
      ...(j.steps || []).map((s) => {
        const stepEl = el('div', { class: 'automation-step', text: `${s.name || s.id}: ${s.status}` });
        if (s.output) {
          stepEl.append(el('pre', { class: 'automation-step-output', text: s.output }));
        }
        return stepEl;
      }),
    ),
  )));
  if (run.status === 'running' || run.status === 'queued' || run.status === 'waiting') {
    const hasAgentStep = (run.jobs || []).some(j => (j.steps || []).some(s => s.status === 'running' && s.output !== undefined));
    if (hasAgentStep) {
      const steer = el('button', { class: 'mini-btn ghost', type: 'button', text: 'Steer' });
      steer.addEventListener('click', async () => {
        const text = await dialog({ input: true, placeholder: 'Additional instructions for the running agent step…' });
        if (text) {
          await rpc('automation.runs.steer', { id: run.id, text });
          await refresh();
        }
      });
      actions.append(steer);
    }
    const cancel = el('button', { class: 'mini-btn ghost danger', type: 'button', text: 'Cancel' });
    cancel.addEventListener('click', async () => {
      await rpc('automation.runs.cancel', { id: run.id });
      await refresh();
    });
    actions.append(cancel);
  }
  if (run.status === 'failed') {
    const retry = el('button', { class: 'mini-btn', type: 'button', text: 'Retry' });
    retry.addEventListener('click', async () => {
      await rpc('automation.runs.retry', { id: run.id });
      await refresh();
    });
    actions.append(retry);
  }
}

async function enableBlockedProvider() {
  const caps = await rpc('automation.capabilities');
  const disabled = (caps.capabilities || []).find((c) => c.status === 'disabled');
  if (!disabled) {
    toast('No disabled provider found', 'info');
    return;
  }
  try {
    await rpc('automation.provider.disable', { id: disabled.provider, enabled: true });
    toast(`Enabled ${disabled.provider}`, 'success');
    await refresh();
  } catch (err) {
    toast(err.message || String(err), 'error');
  }
}

async function createWizard() {
  const result = await dialog({
    title: 'New automation',
    message: 'NusaShell owns the schedule. Pick a trigger family and describe the jobs in YAML.',
    fields: [
      { name: 'name', label: 'Name', value: 'reminder' },
      {
        name: 'family', label: 'Trigger', tag: 'select',
        options: [
          { value: 'once', label: 'Once' },
          { value: 'every', label: 'Every' },
          { value: 'when', label: 'When' },
          { value: 'manual', label: 'Manual' },
        ],
        value: 'once',
      },
      { name: 'at', label: 'Time (RFC3339) or cron/interval or event', placeholder: '2026-08-18T09:00:00Z' },
      {
        name: 'yaml', label: 'Jobs YAML', tag: 'textarea', rows: 8,
        value: 'jobs:\n  run:\n    steps:\n      - run: echo hello\n',
      },
    ],
    actions: [
      { label: 'Cancel', value: null },
      { label: 'Create', value: 'create', primary: true },
    ],
  });
  if (result.value !== 'create') return;
  const name = result.fields.name || 'automation';
  const family = result.fields.family || 'manual';
  const extra = result.fields.at || '';
  let trigger = 'triggers:\n  manual: true\n';
  if (family === 'once') {
    trigger = `triggers:\n  - once:\n      at: ${extra || new Date(Date.now() + 3600000).toISOString()}\n`;
  } else if (family === 'every') {
    trigger = extra.includes(' ')
      ? `triggers:\n  - every:\n      cron: "${extra}"\n`
      : `triggers:\n  - every:\n      interval: ${extra || '1h'}\n`;
  } else if (family === 'when') {
    trigger = `triggers:\n  - when:\n      event: ${extra || 'email.received'}\n`;
  }
  const yaml = `name: ${name}\n${trigger}${result.fields.yaml || ''}`;
  try {
    const validated = await rpc('automation.validate', { yaml });
    if (validated.verdict === 'INVALID') {
      toast(validated.issues?.[0]?.message || 'Invalid workflow', 'error');
      return;
    }
    await rpc('automation.save', { yaml, enabled: true });
    toast(`Saved ${name}`, 'success');
    await refresh();
  } catch (err) {
    toast(err.message || String(err), 'error');
  }
}
