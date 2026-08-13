// Settings workspace: native browser preferences plus the Go runtime controls.

import { autoReconnectEnabled, rpc, setAutoReconnect } from '../rpc.js';
import { toast } from '../ui.js';

let bound = false;

export async function initSettings() {
  if (!bound) {
    bound = true;
    document.getElementById('settings-save-btn').addEventListener('click', save);
    document.getElementById('settings-sidebar-compact').addEventListener('change', saveSidebarPreference);
    document.getElementById('settings-auto-reconnect').addEventListener('change', saveReconnectPreference);
    document.getElementById('settings-check-connection-btn').addEventListener('click', checkConnection);
    window.addEventListener('hashchange', () => {
      if (location.hash === '#settings') void refresh();
    });
  }
  await refresh();
}

async function refresh() {
  const [settingsResult, infoResult, modelsResult] = await Promise.allSettled([
    rpc('settings.get'),
    rpc('app.info', {}, { timeoutMs: 4000 }),
    rpc('ai.models.list'),
  ]);

  if (settingsResult.status === 'fulfilled') {
    const { settings } = settingsResult.value;
    document.getElementById('settings-compaction-enabled').checked = settings.compaction_enabled !== false;
    document.getElementById('settings-prompt-caching').checked = settings.prompt_caching === true;
    document.getElementById('settings-max-tool-rounds').value = settings.max_tool_rounds ?? 8;
    document.getElementById('settings-max-input-tokens').value = settings.max_input_tokens ?? 200000;
    document.getElementById('settings-max-output-tokens').value = settings.max_output_tokens ?? 65536;
  } else {
    setStatus(`Could not load runtime settings: ${settingsResult.reason.message}`, true);
  }

  renderModelOptions(modelsResult.status === 'fulfilled' ? modelsResult.value.models ?? [] : []);
  document.getElementById('settings-sidebar-compact').checked = localStorage.getItem('nusashell.sidebarMode') === 'icons';
  document.getElementById('settings-auto-reconnect').checked = autoReconnectEnabled();

  if (infoResult.status === 'fulfilled') {
    renderAppInfo(infoResult.value);
  } else {
    setConnectionStatus('Could not reach the local backend.', true);
  }
}

function renderModelOptions(models) {
  const select = document.getElementById('settings-preferred-model');
  const selected = localStorage.getItem('nusashell.model') || '';
  select.replaceChildren(option('Automatic — choose in each conversation', ''));
  for (const model of models) {
    const label = model.provider_name ? `${model.id} · ${model.provider_name}` : model.id;
    select.append(option(label, model.id));
  }
  select.value = models.some((model) => model.id === selected) ? selected : '';
}

function option(label, value) {
  const node = document.createElement('option');
  node.value = value;
  node.textContent = label;
  return node;
}

function renderAppInfo(info) {
  document.getElementById('settings-version').textContent = info.version || 'development build';
  document.getElementById('settings-data-dir').textContent = info.data_dir || '—';
  document.getElementById('settings-transports').textContent = (info.transports || []).join(' · ') || '—';
}

async function save() {
  const button = document.getElementById('settings-save-btn');
  const maxToolRounds = Number(document.getElementById('settings-max-tool-rounds').value);
  const maxInputTokens = Number(document.getElementById('settings-max-input-tokens').value);
  const maxOutputTokens = Number(document.getElementById('settings-max-output-tokens').value);
  if (!Number.isInteger(maxToolRounds) || maxToolRounds < 1 || maxToolRounds > 10000) {
    setStatus('Maximum tool rounds must be between 1 and 10,000.', true);
    return;
  }
  if (!Number.isInteger(maxInputTokens) || maxInputTokens < 1000 || maxInputTokens > 2000000) {
    setStatus('Max input tokens must be between 1,000 and 2,000,000.', true);
    return;
  }
  if (!Number.isInteger(maxOutputTokens) || maxOutputTokens < 256 || maxOutputTokens > 1000000) {
    setStatus('Max output tokens must be between 256 and 1,000,000.', true);
    return;
  }
  button.disabled = true;
  try {
    await rpc('settings.set', {
      compaction_enabled: document.getElementById('settings-compaction-enabled').checked,
      prompt_caching: document.getElementById('settings-prompt-caching').checked,
      max_tool_rounds: maxToolRounds,
      max_input_tokens: maxInputTokens,
      max_output_tokens: maxOutputTokens,
    });
    const model = document.getElementById('settings-preferred-model').value;
    localStorage.setItem('nusashell.model', model);
    window.dispatchEvent(new CustomEvent('nusashell:preferred-model', { detail: { model } }));
    setStatus('Saved on this device.');
    toast('Settings saved', 'success');
  } catch (err) {
    setStatus(err.message, true);
    toast(err.message, 'error');
  } finally {
    button.disabled = false;
  }
}

function saveSidebarPreference(event) {
  window.nusashell?.setSidebarCompact(event.currentTarget.checked);
}

function saveReconnectPreference(event) {
  setAutoReconnect(event.currentTarget.checked);
  setConnectionStatus(event.currentTarget.checked ? 'Automatic reconnect is on.' : 'Automatic reconnect is off.');
}

async function checkConnection() {
  const button = document.getElementById('settings-check-connection-btn');
  button.disabled = true;
  try {
    await rpc('app.info', {}, { timeoutMs: 4000 });
    setConnectionStatus('Your local agent responded.');
  } catch {
    setConnectionStatus('Sorry, it looks like your agent is offline.', true);
  } finally {
    button.disabled = false;
  }
}

function setStatus(message, isError = false) {
  const status = document.getElementById('settings-save-status');
  status.textContent = message;
  status.style.color = isError ? 'var(--red)' : '';
}

function setConnectionStatus(message, isError = false) {
  const status = document.getElementById('settings-connection-status');
  status.textContent = message;
  status.style.color = isError ? 'var(--red)' : '';
}
