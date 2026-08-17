// ACP agent registry: spawn-only subprocess configs in the Providers view.

import { rpc } from '../rpc.js';
import { el, toast, dialog, confirmDialog } from '../ui.js';

let agents = [];
let detailId = null;
let envDrafts = new Map();

export async function initAcpProviders() {
  document.getElementById('add-acp-agent-btn')?.addEventListener('click', () => addAcpAgent());
  await refreshAcpProviders();
}

export async function refreshAcpProviders() {
  try {
    const res = await rpc('acp.agents.list');
    agents = res.agents ?? [];
  } catch (err) {
    toast(err.message, 'error');
    agents = [];
  }
  if (detailId && !agents.find((a) => a.id === detailId)) detailId = null;
  renderAcpRegistry();
}

export function acpDetailOpen() {
  return Boolean(detailId);
}

function setPane() {
  const llm = document.getElementById('provider-llm-section');
  const addProvider = document.getElementById('add-provider-btn');
  if (llm) llm.hidden = Boolean(detailId);
  if (addProvider) addProvider.hidden = Boolean(detailId);
}

function renderAcpRegistry() {
  const section = document.getElementById('provider-acp-section');
  const registry = document.getElementById('acp-agent-registry');
  const detail = document.getElementById('acp-agent-detail');
  const addBtn = document.getElementById('add-acp-agent-btn');
  if (!registry || !detail || !section) return;
  setPane();
  section.hidden = false;
  registry.style.display = detailId ? 'none' : '';
  detail.hidden = !detailId;
  if (addBtn) addBtn.hidden = Boolean(detailId);
  registry.innerHTML = '';
  if (!agents.length) {
    registry.append(el('div', { class: 'empty-state', style: 'grid-column:1/-1' },
      el('div', { class: 'empty-mark', text: '⌥' }),
      el('strong', { text: 'No ACP subagents' }),
      el('span', { text: 'Register a coding agent by command and args. It will not appear in the chat composer — the parent agent spawns it with the subagent tool.' }),
    ));
    return;
  }
  for (const agent of agents) {
    const card = el('div', { class: `provider-registry-card accent-acp${agent.id === detailId ? ' is-active' : ''}` },
      el('div', { class: 'provider-card-head' },
        el('div', { class: 'provider-mark', text: 'ACP' }),
        el('div', { style: 'min-width:0' },
          el('h2', { text: agent.name }),
          el('p', { text: [agent.command, ...(agent.args || [])].join(' ') }),
        ),
        el('label', { class: 'toggle provider-enabled', title: agent.enabled ? 'Disable agent' : 'Enable agent' },
          el('input', { type: 'checkbox', checked: agent.enabled !== false }),
          el('span', { class: 'toggle-slider' }),
        ),
      ),
      el('p', { text: agent.enabled === false
        ? 'Disabled — the parent agent cannot spawn this binary.'
        : 'Spawn-only. Probe to discover modes, models, and sign-in methods.' }),
      el('div', { class: 'provider-card-footer' },
        el('span', { class: `provider-status${agent.modes?.length ? ' configured' : ''}`, text: statusText(agent) }),
        el('div', { class: 'provider-card-actions' },
          el('button', { class: 'mini-btn ghost', type: 'button', text: 'Configure' }),
          el('button', { class: 'mini-btn danger', type: 'button', text: 'Delete' }),
        ),
      ),
    );
    card.querySelectorAll('button')[0].addEventListener('click', () => showDetail(agent.id));
    card.querySelectorAll('button')[1].addEventListener('click', async (e) => {
      e.stopPropagation();
      const ok = await confirmDialog('Delete ACP agent', `"${agent.name}" will be removed. Live runs keep their bound workspace until they stop.`, 'Delete');
      if (!ok) return;
      try {
        await rpc('acp.agents.delete', { id: agent.id });
        toast('ACP agent deleted', 'success');
        await refreshAcpProviders();
      } catch (err) { toast(err.message, 'error'); }
    });
    card.querySelector('.provider-enabled input').addEventListener('change', async (event) => {
      const toggle = event.currentTarget;
      toggle.disabled = true;
      try {
        await rpc('acp.agents.save', {
          id: agent.id,
          name: agent.name,
          command: agent.command,
          enabled: toggle.checked,
          preferred_mode_id: agent.preferred_mode_id,
          preferred_model_id: agent.preferred_model_id,
          default_workspace: agent.default_workspace,
          mode_risk_mappings: agent.mode_risk_mappings,
        });
        toast(toggle.checked ? 'ACP agent enabled' : 'ACP agent disabled', 'success');
        await refreshAcpProviders();
      } catch (err) {
        toggle.checked = !toggle.checked;
        toast(err.message, 'error');
      } finally {
        toggle.disabled = false;
      }
    });
    registry.append(card);
  }
  if (detailId) renderDetail(agents.find((a) => a.id === detailId));
}

function statusText(agent) {
  if (agent.enabled === false) return 'disabled';
  const modes = agent.modes?.length ?? 0;
  const models = agent.models?.length ?? 0;
  if (modes || models) return `${modes} modes · ${models} models`;
  if (agent.auth_methods?.length) return 'needs authenticate';
  return 'not probed';
}

function showDetail(id) {
  detailId = id;
  renderAcpRegistry();
}

function backToRegistry() {
  detailId = null;
  renderAcpRegistry();
}

function renderDetail(agent) {
  const detail = document.getElementById('acp-agent-detail');
  if (!agent || !detail) return;
  const envKeys = agent.env_keys || [];
  const draft = envDrafts.get(agent.id) || '';
  detail.innerHTML = '';
  detail.append(el('div', { class: 'provider-detail-head' },
    el('button', { class: 'provider-back', type: 'button', text: '← All ACP agents' }),
    el('span', { class: `provider-status${agent.enabled !== false ? ' configured' : ''}`, text: agent.enabled === false ? 'disabled' : 'spawn-only' }),
  ));
  detail.append(el('div', { class: 'provider-detail-card' },
    el('div', { class: 'provider-card-head' },
      el('div', { class: 'provider-mark accent-acp', text: 'ACP' }),
      el('div', { style: 'min-width:0' },
        el('h2', { text: agent.name }),
        el('p', { text: 'Command is immutable after save. Delete and recreate to change the binary.' }),
      ),
    ),
    el('form', { class: 'acp-agent-form', id: 'acp-agent-form' },
      field('Name', el('input', { name: 'name', value: agent.name, required: true })),
      field('Command', el('input', { name: 'command', value: agent.command, disabled: true, title: 'Immutable after registration' })),
      field('Arguments (space separated)', el('input', { name: 'args', value: (agent.args || []).join(' '), placeholder: 'agent acp' })),
      field(`Environment (KEY=VALUE). Stored keys: ${envKeys.join(', ') || 'none'}`, el('textarea', {
        name: 'env', rows: 4, placeholder: 'KEY=VALUE',
      })),
      field('Default workspace (optional absolute path)', el('input', {
        name: 'default_workspace', value: agent.default_workspace || '', placeholder: 'Leave empty to use the conversation workspace',
      })),
    ),
    el('div', { class: 'provider-detail-actions' },
      el('button', { class: 'mini-btn', type: 'button', id: 'acp-save-btn', text: 'Save' }),
      el('button', { class: 'mini-btn ghost', type: 'button', id: 'acp-probe-btn', text: 'Probe' }),
      el('button', { class: 'mini-btn ghost', type: 'button', id: 'acp-refresh-btn', text: 'Refresh catalog' }),
      el('button', { class: 'mini-btn danger', type: 'button', id: 'acp-delete-btn', text: 'Delete' }),
    ),
  ));

  const envInput = detail.querySelector('textarea[name="env"]');
  envInput.value = draft;
  envInput.addEventListener('input', () => envDrafts.set(agent.id, envInput.value));

  detail.querySelector('.provider-back').addEventListener('click', backToRegistry);
  detail.querySelector('#acp-save-btn').addEventListener('click', () => saveDetail(agent));
  detail.querySelector('#acp-probe-btn').addEventListener('click', () => probeAgent(agent.id));
  detail.querySelector('#acp-refresh-btn').addEventListener('click', () => refreshCatalog(agent.id));
  detail.querySelector('#acp-delete-btn').addEventListener('click', async () => {
    const ok = await confirmDialog('Delete ACP agent', `"${agent.name}" will be removed.`, 'Delete');
    if (!ok) return;
    try {
      await rpc('acp.agents.delete', { id: agent.id });
      toast('ACP agent deleted', 'success');
      backToRegistry();
      await refreshAcpProviders();
    } catch (err) { toast(err.message, 'error'); }
  });

  if (agent.auth_methods?.length) {
    detail.append(authCard(agent));
  }
  if (agent.modes?.length) {
    detail.append(modesCard(agent));
  }
  if (agent.models?.length) {
    detail.append(modelsCard(agent));
  }
}

function field(label, input) {
  return el('label', { class: 'acp-agent-field' }, el('span', { text: label }), input);
}

function parseEnv(text) {
  const env = {};
  for (const line of (text || '').split('\n')) {
    const separator = line.indexOf('=');
    if (separator > 0) env[line.slice(0, separator).trim()] = line.slice(separator + 1).trim();
  }
  return env;
}

async function saveDetail(agent) {
  const form = document.getElementById('acp-agent-form');
  const name = form.name.value.trim();
  const args = form.args.value.split(/\s+/).filter(Boolean);
  const envText = form.env.value;
  const env = parseEnv(envText);
  if (!name) {
    toast('Name is required', 'error');
    return;
  }
  try {
    const payload = {
      id: agent.id,
      name,
      command: agent.command,
      args,
      enabled: agent.enabled !== false,
      default_workspace: form.default_workspace.value.trim(),
      preferred_mode_id: agent.preferred_mode_id || '',
      preferred_model_id: agent.preferred_model_id || '',
      mode_risk_mappings: (agent.mode_risk_mappings || []).map((m) => ({ mode_id: m.mode_id, tier: m.tier })),
    };
    if (envText.trim()) payload.env = env;
    await rpc('acp.agents.save', payload);
    toast('ACP agent saved', 'success');
    await refreshAcpProviders();
  } catch (err) {
    toast(err.message, 'error');
  }
}

async function probeAgent(id) {
  try {
    const res = await rpc('acp.agents.probe', { id }, { timeoutMs: 30000 });
    toast(res.ok ? 'Probe succeeded' : (res.error || 'Probe finished'), res.ok ? 'success' : 'error');
    await refreshAcpProviders();
  } catch (err) {
    toast(err.message, 'error');
  }
}

async function refreshCatalog(id) {
  try {
    await rpc('acp.agents.refresh-catalog', { id }, { timeoutMs: 30000 });
    toast('Catalog refreshed', 'success');
    await refreshAcpProviders();
  } catch (err) {
    toast(err.message, 'error');
  }
}

function authCard(agent) {
  const card = el('div', { class: 'provider-models-card' },
    el('div', { class: 'provider-models-head' },
      el('div', {},
        el('h2', { text: 'Authentication' }),
        el('p', { text: agent.auth_method_id ? `Signed in with ${agent.auth_method_id}` : 'Choose a method advertised by the agent. Nothing is hardcoded per vendor.' }),
      ),
    ),
    el('div', { class: 'acp-auth-methods' },
      ...(agent.auth_methods || []).map((method) => {
        const btn = el('button', {
          class: `mini-btn${agent.auth_method_id === method.id ? '' : ' ghost'}`,
          type: 'button',
          text: method.name || method.id,
          title: method.description || method.id,
        });
        btn.addEventListener('click', async () => {
          try {
            await rpc('acp.agents.authenticate', { id: agent.id, method_id: method.id }, { timeoutMs: 60000 });
            toast(`Authenticated with ${method.name || method.id}`, 'success');
            await refreshAcpProviders();
          } catch (err) {
            toast(err.message, 'error');
          }
        });
        return btn;
      }),
    ),
  );
  return card;
}

function modesCard(agent) {
  const mappings = new Map((agent.mode_risk_mappings || []).map((m) => [m.mode_id, m.tier]));
  const card = el('div', { class: 'provider-models-card' },
    el('div', { class: 'provider-models-head' },
      el('div', {},
        el('h2', { text: 'Modes and risk' }),
        el('p', { text: 'Advertised by the agent. New sessions start on the strictest mapped mode. Bypass is never the default.' }),
      ),
    ),
  );
  for (const mode of agent.modes) {
    const tier = mappings.get(mode.id) || mode.risk_tier || 'read_only';
    const preferred = agent.preferred_mode_id === mode.id;
    const defaultBtn = el('button', {
      class: `mini-btn${preferred ? '' : ' ghost'}`,
      type: 'button',
      text: preferred ? 'Default' : 'Set default',
    });
    defaultBtn.addEventListener('click', async () => {
      try {
        await rpc('acp.agents.save', {
          id: agent.id, name: agent.name, command: agent.command, enabled: agent.enabled !== false,
          preferred_mode_id: mode.id,
          mode_risk_mappings: [...mappings.entries()].map(([mode_id, t]) => ({ mode_id, tier: t })),
        });
        await refreshAcpProviders();
      } catch (err) { toast(err.message, 'error'); }
    });
    const row = el('div', { class: 'acp-mode-config' },
      el('div', { class: 'acp-mode-config-copy' },
        el('strong', { text: mode.name || mode.id }),
        el('span', { text: mode.description || mode.id }),
      ),
      riskSegment(tier, async (next) => {
        mappings.set(mode.id, next);
        await saveMappings(agent, mappings);
      }),
      defaultBtn,
    );
    card.append(row);
  }
  return card;
}

function riskSegment(current, onChange) {
  const wrap = el('div', { class: 'acp-risk-segment', role: 'group', 'aria-label': 'Risk tier' });
  for (const [value, label] of [['read_only', 'Read-only'], ['edit_confirmed', 'Edits'], ['bypass', 'Bypass']]) {
    const btn = el('button', {
      class: `acp-risk-option${current === value ? ' is-active' : ''}`,
      type: 'button',
      text: label,
    });
    btn.addEventListener('click', () => onChange(value));
    wrap.append(btn);
  }
  return wrap;
}

async function saveMappings(agent, mappings) {
  try {
    await rpc('acp.agents.save', {
      id: agent.id,
      name: agent.name,
      command: agent.command,
      enabled: agent.enabled !== false,
      preferred_mode_id: agent.preferred_mode_id,
      preferred_model_id: agent.preferred_model_id,
      mode_risk_mappings: [...mappings.entries()].map(([mode_id, tier]) => ({ mode_id, tier })),
    });
    toast('Risk mapping saved', 'success');
    await refreshAcpProviders();
  } catch (err) {
    toast(err.message, 'error');
  }
}

function modelsCard(agent) {
  const card = el('div', { class: 'provider-models-card' },
    el('div', { class: 'provider-models-head' },
      el('div', {},
        el('h2', { text: 'Models' }),
        el('p', { text: 'Imported from session/new. Quality tags are local (frontier / balanced / economy).' }),
      ),
    ),
  );
  for (const model of agent.models) {
    const preferred = agent.preferred_model_id === model.id;
    const row = el('div', { class: 'provider-model-item' },
      el('div', { class: 'provider-model-item-head' },
        el('span', { class: 'provider-model-id', text: model.name || model.id }),
        el('div', { class: 'provider-model-badges' },
          model.tier ? el('span', { class: 'model-badge', text: model.tier }) : null,
          el('button', { class: `mini-btn${preferred ? '' : ' ghost'}`, type: 'button', text: preferred ? 'Preferred' : 'Prefer' }),
        ),
      ),
    );
    row.querySelector('button').addEventListener('click', async () => {
      try {
        await rpc('acp.agents.save', {
          id: agent.id, name: agent.name, command: agent.command, enabled: agent.enabled !== false,
          preferred_model_id: model.id,
          preferred_mode_id: agent.preferred_mode_id,
          mode_risk_mappings: agent.mode_risk_mappings,
        });
        await refreshAcpProviders();
      } catch (err) { toast(err.message, 'error'); }
    });
    card.append(row);
  }
  return card;
}

async function addAcpAgent() {
  const result = await dialog({
    title: 'Add ACP agent',
    message: 'A generic stdio ACP binary. Modes, models, and auth methods are discovered after Probe — not typed per vendor. Command cannot be changed later.',
    fields: [
      { name: 'name', label: 'Label', value: '', placeholder: 'e.g. Cursor CLI' },
      { name: 'command', label: 'Command', value: '', placeholder: 'e.g. cursor, claude-code-acp, devin' },
      { name: 'args', label: 'Arguments (space separated)', value: '', placeholder: 'agent acp' },
      { name: 'env', label: 'Environment (optional KEY=VALUE per line)', value: '', placeholder: 'KEY=VALUE', tag: 'textarea' },
    ],
    actions: [
      { label: 'Cancel', value: null },
      { label: 'Save', value: 'save', primary: true },
    ],
  });
  if (result.value !== 'save') return;
  const { name, command, args, env } = result.fields;
  if (!name.trim() || !command.trim()) {
    toast('Name and command are required', 'error');
    return;
  }
  try {
    const saved = await rpc('acp.agents.save', {
      name: name.trim(),
      command: command.trim(),
      args: args.split(/\s+/).filter(Boolean),
      env: parseEnv(env),
      enabled: true,
    });
    const id = saved.agents?.[0]?.id;
    toast('ACP agent saved — probe it to discover modes', 'success');
    await refreshAcpProviders();
    if (id) showDetail(id);
  } catch (err) {
    toast(err.message, 'error');
  }
}
