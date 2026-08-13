// Settings workspace: small, explicit controls backed by the Go settings API.

import { rpc } from '../rpc.js';
import { toast } from '../ui.js';

let bound = false;

export async function initSettings() {
  if (!bound) {
    bound = true;
    document.getElementById('settings-save-btn').addEventListener('click', save);
  }
  await refresh();
}

async function refresh() {
  try {
    const { settings } = await rpc('settings.get');
    document.getElementById('settings-compaction-enabled').checked = settings.compaction_enabled !== false;
    document.getElementById('settings-compaction-threshold').value = settings.compaction_threshold ?? 40000;
    document.getElementById('settings-prompt-caching').checked = settings.prompt_caching === true;
  } catch (err) {
    setStatus(`Could not load settings: ${err.message}`, true);
  }
}

async function save() {
  const button = document.getElementById('settings-save-btn');
  const threshold = Number(document.getElementById('settings-compaction-threshold').value);
  if (!Number.isInteger(threshold) || threshold < 1000) {
    setStatus('Threshold must be at least 1000 tokens.', true);
    return;
  }
  button.disabled = true;
  try {
    await rpc('settings.set', {
      compaction_enabled: document.getElementById('settings-compaction-enabled').checked,
      compaction_threshold: threshold,
      prompt_caching: document.getElementById('settings-prompt-caching').checked,
    });
    setStatus('Saved locally.');
    toast('Settings saved', 'success');
  } catch (err) {
    setStatus(err.message, true);
    toast(err.message, 'error');
  } finally {
    button.disabled = false;
  }
}

function setStatus(message, isError = false) {
  const status = document.getElementById('settings-save-status');
  status.textContent = message;
  status.style.color = isError ? 'var(--red)' : '';
}
