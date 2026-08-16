// Providers workspace: Messages / Responses / Chat registries, credentials, models.

import { rpc } from '../rpc.js';
import { el, toast, dialog, confirmDialog } from '../ui.js';

let providers = [];
let detailId = null;

const KIND_META = {
  messages: { label: 'Messages', mark: 'MS', cls: 'accent-anthropic', desc: 'Messages API format' },
  responses: { label: 'Responses', mark: 'RS', cls: 'accent-openai', desc: 'Responses API format' },
  chat: { label: 'Chat', mark: 'CH', cls: 'accent-compatible', desc: 'Chat Completions API format' },
  ollama: { label: 'Ollama', mark: 'OL', cls: 'accent-compatible', desc: 'Local Ollama (chat + embeddings, no API key needed)' },
  codex: { label: 'Codex', mark: 'CX', cls: 'accent-codex', desc: 'ChatGPT Codex backend (OAuth, no API key needed)' },
};

export async function initProviders() {
  document.getElementById('add-provider-btn').addEventListener('click', () => addProvider());
  await refresh();
}

export async function refresh() {
  const res = await rpc('ai.providers.list');
  providers = res.providers ?? [];
  if (detailId && !providers.find((p) => p.id === detailId)) detailId = null;
  renderRegistry();
}

function renderRegistry() {
  const registry = document.getElementById('provider-registry');
  const detail = document.getElementById('provider-detail');
  registry.style.display = detailId ? 'none' : '';
  detail.hidden = !detailId;
  document.getElementById('add-provider-btn').hidden = !!detailId;
  registry.innerHTML = '';
  if (!providers.length) {
    registry.append(el('div', { class: 'empty-state', style: 'grid-column:1/-1' },
      el('div', { class: 'empty-mark', text: '▣' }),
      el('strong', { text: 'No providers configured' }),
      el('span', { text: 'Add a provider in the Messages, Responses or Chat API format, then import its models to chat with the agent.' }),
    ));
    return;
  }
  for (const p of providers) {
    const meta = KIND_META[p.kind] || KIND_META.compatible;
    const card = el('div', {
      class: `provider-registry-card ${meta.cls}${p.id === detailId ? ' is-active' : ''}`,
    },
      el('div', { class: 'provider-card-head' },
        el('div', { class: 'provider-mark', text: meta.mark }),
        el('div', { style: 'min-width:0' },
          el('h2', { text: p.name }),
          el('p', { text: p.base_url || meta.label }),
        ),
        el('label', { class: 'toggle provider-enabled', title: p.enabled === false ? 'Enable provider' : 'Disable provider' },
          el('input', { type: 'checkbox', checked: p.enabled !== false }),
          el('span', { class: 'toggle-slider' }),
        ),
      ),
      el('p', { text: meta.desc }),
      el('div', { class: 'provider-card-footer' },
        el('span', { class: `provider-status${p.configured ? ' configured' : ''}`, text: p.configured ? `${p.models?.length ?? 0} models` : 'not configured' }),
        el('div', { class: 'provider-card-actions' },
          el('button', { class: 'mini-btn ghost', type: 'button', text: 'Configure' }),
          el('button', { class: 'mini-btn danger', type: 'button', text: 'Delete' }),
        ),
      ),
    );
    card.querySelectorAll('button')[0].addEventListener('click', () => showDetail(p.id));
    card.querySelectorAll('button')[1].addEventListener('click', async (e) => {
      e.stopPropagation();
      const ok = await confirmDialog('Delete provider', `"${p.name}" and its stored API key will be removed.`, 'Delete');
      if (!ok) return;
      try {
        await rpc('ai.providers.delete', { id: p.id });
        toast('Provider deleted', 'success');
        await refresh();
      } catch (err) { toast(err.message, 'error'); }
    });
    card.querySelector('.provider-enabled input').addEventListener('change', async (event) => {
      const toggle = event.currentTarget;
      toggle.disabled = true;
      try {
        await rpc('ai.providers.save', {
          id: p.id,
          kind: p.kind,
          name: p.name,
          base_url: p.base_url,
          enabled: toggle.checked,
        });
        toast(toggle.checked ? 'Provider enabled' : 'Provider disabled', 'success');
        await refresh();
      } catch (err) {
        toggle.checked = !toggle.checked;
        toast(err.message, 'error');
      } finally {
        toggle.disabled = false;
      }
    });
    registry.append(card);
  }
  if (detailId) renderDetail(providers.find((p) => p.id === detailId));
}

function showDetail(id) {
  detailId = id;
  renderRegistry();
}

function backToRegistry() {
  detailId = null;
  renderRegistry();
}

function renderDetail(p) {
  const meta = KIND_META[p.kind] || KIND_META.chat;
  const detail = document.getElementById('provider-detail');
  detail.innerHTML = '';
  detail.append(el('div', { class: 'provider-detail-head' },
    el('button', { class: 'provider-back', type: 'button', text: '← All providers' }),
    el('span', { class: 'provider-status' + (p.configured ? ' configured' : ''), text: p.configured ? 'configured' : 'not configured' }),
  ));
  detail.append(el('div', { class: 'provider-detail-card' },
    el('div', { class: 'provider-card-head' },
      el('div', { class: `provider-mark ${meta.cls}`, text: meta.mark }),
      el('div', { style: 'min-width:0' },
        el('h2', { text: p.name }),
        el('p', { text: meta.label }),
      ),
    ),
    el('dl', { class: 'provider-detail-grid' },
      el('div', {}, el('dt', { text: 'Kind' }), el('dd', { text: (KIND_META[p.kind] || KIND_META.chat).label })),
      el('div', {}, el('dt', { text: 'Base URL' }), el('dd', { text: p.base_url || '—' })),
      el('div', {}, el('dt', { text: p.kind === 'codex' ? 'Auth' : 'API key' }), el('dd', { text: p.kind === 'codex' ? (p.has_api_key ? 'ChatGPT OAuth ✓' : 'Not signed in') : (p.has_api_key ? '••••••••' : '—') })),
      el('div', {}, el('dt', { text: 'Status' }), el('dd', { text: p.enabled === false ? 'disabled' : 'enabled' })),
    ),
    el('div', { class: 'provider-detail-actions' },
      el('button', { class: 'mini-btn', type: 'button', text: 'Edit' }),
      el('button', { class: 'mini-btn ghost', type: 'button', text: 'Test connection' }),
      el('button', { class: 'mini-btn ghost', type: 'button', text: 'Import models' }),
      el('button', { class: 'mini-btn danger', type: 'button', text: 'Delete' }),
    ),
  ));

  // Codex-specific sections: unified accounts+usage card + runtime binary
  if (p.kind === 'codex') {
    const codexGrid = el('div', { class: 'codex-cards-grid' },
      renderCodexAccounts(p),
      renderCodexRuntime(p),
    );
    detail.append(codexGrid);
  }

  detail.append(el('div', { class: 'provider-models-card' },
    el('div', { class: 'provider-models-head' },
      el('div', {},
        el('h2', { text: 'Models' }),
        el('p', { text: `${p.models?.length ?? 0} imported` }),
      ),
    ),
    el('div', { class: 'provider-model-list' }, renderModels(p)),
  ));

  const [editBtn, testBtn, importBtn, delBtn] = detail.querySelectorAll('.provider-detail-actions button');
  detail.querySelector('.provider-back')?.addEventListener('click', backToRegistry);
  editBtn.addEventListener('click', () => addProvider(p));
  testBtn.addEventListener('click', async () => {
    testBtn.disabled = true;
    testBtn.textContent = 'Testing…';
    try {
      const res = await rpc('ai.providers.test', { id: p.id });
      toast(`Connected · ${res.models ?? 0} models`, 'success');
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      testBtn.disabled = false;
      testBtn.textContent = 'Test connection';
    }
  });
  importBtn.addEventListener('click', async () => {
    importBtn.disabled = true;
    importBtn.textContent = 'Importing…';
    try {
      const res = await rpc('ai.providers.import-models', { id: p.id });
      toast(`Imported ${res.models?.length ?? 0} models`, 'success');
      await refresh();
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      importBtn.disabled = false;
      importBtn.textContent = 'Import models';
    }
  });
  delBtn.addEventListener('click', async () => {
    const ok = await confirmDialog('Delete provider', `"${p.name}" and its stored credentials will be removed.`, 'Delete');
    if (!ok) return;
    try {
      await rpc('ai.providers.delete', { id: p.id });
      detailId = null;
      toast('Provider deleted', 'success');
      await refresh();
    } catch (err) { toast(err.message, 'error'); }
  });
}

// ---- Codex-specific UI ----

function renderCodexAccounts(p) {
  const card = el('div', { class: 'provider-models-card codex-accounts-card' },
    el('div', { class: 'provider-models-head' },
      el('div', {},
        el('h2', { text: 'ChatGPT Accounts' }),
        el('p', { text: 'Account info, plan, and usage quota' }),
      ),
      el('div', { class: 'codex-auth-buttons' },
        el('button', { class: 'mini-btn ghost', type: 'button', text: 'Import from Codex CLI' }),
        el('button', { class: 'mini-btn ghost', type: 'button', text: '\u21bb Refresh' }),
        el('button', { class: 'mini-btn', type: 'button', text: 'Sign in with ChatGPT' }),
      ),
    ),
    el('div', { class: 'codex-account-list', id: 'codex-account-list' },
      el('div', { class: 'provider-model-empty', text: 'Loading accounts\u2026' }),
    ),
  );
  const buttons = card.querySelectorAll('button');
  const importBtn = buttons[0];
  const refreshBtn = buttons[1];
  const loginBtn = buttons[2];
  refreshBtn.addEventListener('click', async () => {
    refreshBtn.disabled = true;
    refreshBtn.textContent = 'Refreshing\u2026';
    try {
      const res = await rpc('ai.codex.refresh-circuits', {});
      toast(`Checked ${res.checked ?? 0} accounts`, 'success');
      await refreshCodexAccounts(p.id);
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      refreshBtn.disabled = false;
      refreshBtn.textContent = '\u21bb Refresh';
    }
  });
  importBtn.addEventListener('click', async () => {
    importBtn.disabled = true;
    importBtn.textContent = 'Importing\u2026';
    try {
      const res = await rpc('ai.codex.import', { provider_id: p.id });
      if (res.skipped) {
        toast(`Account ${res.email || res.account_id} already imported`, 'info');
      } else {
        toast(`Imported ${res.email || res.account_id} from Codex CLI`, 'success');
      }
      await refreshCodexAccounts(p.id);
      await refresh();
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      importBtn.disabled = false;
      importBtn.textContent = 'Import from Codex CLI';
    }
  });
  loginBtn.addEventListener('click', async () => {
    loginBtn.disabled = true;
    loginBtn.textContent = 'Opening browser\u2026';
    try {
      await rpc('ai.codex.login', { provider_id: p.id });
      toast('ChatGPT login successful', 'success');
      await refreshCodexAccounts(p.id);
      await refresh();
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      loginBtn.disabled = false;
      loginBtn.textContent = 'Sign in with ChatGPT';
    }
  });
  // Defer refresh until the card is in the DOM
  setTimeout(() => refreshCodexAccounts(p.id), 0);
  return card;
}

async function refreshCodexAccounts(providerId) {
  const list = document.getElementById('codex-account-list');
  if (!list) return;
  try {
    // Fetch unified accounts+usage data in one call
    const res = await rpc('ai.codex.usage', { provider_id: providerId });
    const accounts = res.accounts ?? [];
    if (!accounts.length) {
      list.innerHTML = '';
      list.append(el('div', { class: 'provider-model-empty', text: 'No accounts signed in. Click \"Sign in with ChatGPT\" to add one.' }));
      return;
    }
    list.innerHTML = '';
    for (const acc of accounts) {
      const display = acc.email || acc.name || acc.account_id;
      const secondary = (acc.email && acc.name) ? `${acc.name} \u00b7 ${acc.account_id}` : (acc.email ? acc.account_id : (acc.name ? acc.account_id : ''));
      const planLabel = acc.plan ? acc.plan.charAt(0).toUpperCase() + acc.plan.slice(1) : (acc.error ? 'Error' : '\u2014');
      const circuitUntil = acc.circuit_open ? new Date(acc.circuit_open_until * 1000).toLocaleString() : '';

      // Build usage windows section
      const usageParts = [];
      if (acc.error) {
        usageParts.push(el('span', { class: 'codex-account-usage-error', text: acc.error }));
      } else if (acc.primary_window || acc.weekly_window) {
        if (acc.primary_window) usageParts.push(renderUsageBar('Session', acc.primary_window));
        if (acc.weekly_window) usageParts.push(renderUsageBar('Weekly', acc.weekly_window));
      } else {
        usageParts.push(el('span', { class: 'codex-account-usage-empty', text: 'No usage data' }));
      }

      list.append(el('div', { class: 'codex-account-item unified' },
        // Left: account identity + badges
        el('div', { class: 'codex-account-info' },
          el('span', { class: 'codex-account-id', text: display }),
          ...(secondary ? [el('span', { class: 'codex-account-secondary', text: secondary })] : []),
          el('span', { class: 'codex-account-meta' },
            ...(acc.active ? [el('span', { class: 'codex-account-badge active', text: 'active' })] : []),
            ...(acc.circuit_open ? [el('span', { class: 'codex-account-badge circuit-open', text: `limit \u00b7 resets ${circuitUntil}` })] : []),
            ...(acc.limit_reached && !acc.circuit_open ? [el('span', { class: 'codex-account-badge circuit-open', text: 'limit reached' })] : []),
          ),
        ),
        // Middle: plan + usage quota
        el('div', { class: 'codex-account-quota' },
          el('span', { class: 'codex-account-plan', text: planLabel }),
          ...usageParts,
        ),
        // Right: actions
        el('div', { class: 'codex-account-actions' },
          ...(acc.active ? [] : [el('button', { class: 'mini-btn ghost', type: 'button', text: 'Switch', 'data-acc': acc.account_id })]),
          el('button', { class: 'mini-btn danger', type: 'button', text: 'Remove', 'data-acc': acc.account_id }),
        ),
      ));
    }
    // Wire buttons
    list.querySelectorAll('button').forEach((btn) => {
      btn.addEventListener('click', async (e) => {
        const accId = e.currentTarget.dataset.acc;
        if (e.currentTarget.textContent === 'Switch') {
          try {
            await rpc('ai.codex.accounts.switch', { provider_id: providerId, account_id: accId });
            toast('Switched account', 'success');
            await refreshCodexAccounts(providerId);
            await refresh();
          } catch (err) { toast(err.message, 'error'); }
        } else if (e.currentTarget.textContent === 'Remove') {
          const ok = await confirmDialog('Remove account', `Remove ChatGPT account ${accId}?`, 'Remove');
          if (!ok) return;
          try {
            await rpc('ai.codex.logout', { provider_id: providerId, account_id: accId });
            toast('Account removed', 'success');
            await refreshCodexAccounts(providerId);
            await refresh();
          } catch (err) { toast(err.message, 'error'); }
        }
      });
    });
  } catch (err) {
    list.innerHTML = '';
    list.append(el('div', { class: 'provider-model-empty', text: `Error: ${err.message}` }));
  }
}

function renderUsageBar(label, win) {
  const remaining = win.remaining_percent ?? (100 - win.used_percent);
  const resetDate = win.reset_at ? new Date(win.reset_at * 1000).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) : 'unknown';
  const barColor = win.used_percent >= 90 ? 'critical' : (win.used_percent >= 70 ? 'warning' : 'ok');
  return el('div', { class: 'codex-usage-window' },
    el('div', { class: 'codex-usage-window-head' },
      el('span', { class: 'codex-usage-window-label', text: label }),
      el('span', { class: 'codex-usage-window-pct', text: `${win.used_percent}%` }),
    ),
    el('div', { class: 'codex-usage-bar' },
      el('div', { class: `codex-usage-bar-fill ${barColor}`, style: `width:${win.used_percent}%` }),
    ),
    el('div', { class: 'codex-usage-window-foot' },
      el('span', { text: `${remaining}% left` }),
      el('span', { class: 'codex-usage-reset', text: resetDate }),
    ),
  );
}

function renderCodexRuntime(p) {
  const card = el('div', { class: 'provider-models-card codex-runtime-card' },
    el('div', { class: 'provider-models-head' },
      el('div', {},
        el('h2', { text: 'Codex Runtime' }),
        el('p', { text: 'Official Codex CLI binary for compaction + ACP' }),
      ),
      el('button', { class: 'mini-btn', type: 'button', text: 'Download' }),
    ),
    el('div', { class: 'codex-runtime-status', id: 'codex-runtime-status' },
      el('div', { class: 'provider-model-empty', text: 'Checking runtime status…' }),
    ),
  );
  const dlBtn = card.querySelector('button');
  dlBtn.addEventListener('click', async () => {
    dlBtn.disabled = true;
    dlBtn.textContent = 'Downloading…';
    try {
      const res = await rpc('ai.codex.runtime.download', {});
      toast(`Codex runtime v${res.version} downloaded`, 'success');
      refreshCodexRuntime();
    } catch (err) {
      toast(err.message, 'error');
    } finally {
      dlBtn.disabled = false;
      dlBtn.textContent = 'Download';
    }
  });
  // Defer refresh until the card is in the DOM
  setTimeout(() => refreshCodexRuntime(), 0);
  return card;
}

async function refreshCodexRuntime() {
  const status = document.getElementById('codex-runtime-status');
  if (!status) return;
  try {
    const res = await rpc('ai.codex.runtime.status', {});
    status.innerHTML = '';
    if (res.downloading) {
      status.append(el('div', { class: 'codex-runtime-info downloading', text: 'Downloading…' }));
      return;
    }
    if (res.download_error) {
      status.append(el('div', { class: 'codex-runtime-info error', text: `Download failed: ${res.download_error}` }));
      return;
    }
    if (res.installed) {
      status.append(el('div', { class: 'codex-runtime-info installed' },
        el('span', { text: `v${res.version}` }),
        el('span', { class: 'codex-runtime-path', text: res.path }),
      ));
    } else {
      status.append(el('div', { class: 'provider-model-empty', text: 'Not installed. Click "Download" to get the official Codex binary (~85 MB).' }));
    }
  } catch (err) {
    status.innerHTML = '';
    status.append(el('div', { class: 'provider-model-empty', text: `Error: ${err.message}` }));
  }
}

function renderModels(p) {
  const models = p.models ?? [];
  if (!models.length) {
    return el('div', { class: 'provider-model-empty', text: 'No models imported. Use "Import models" to fetch them from the provider.' });
  }
  return models.map((m) => el('div', { class: 'provider-model-item' },
    el('div', { class: 'provider-model-item-head' },
      el('span', { class: 'provider-model-id', text: m.id }),
      el('div', { class: 'provider-model-badges' },
        ...(m.context ? [el('span', { class: 'model-badge model-badge-context', text: `${m.context}` })] : []),
        ...(m.max_output ? [el('span', { class: 'model-badge model-badge-output', text: `out ${m.max_output}` })] : []),
        ...(m.input_cost ? [el('span', { class: 'model-badge model-badge-input', text: `$${m.input_cost}/M in` })] : []),
      ),
    ),
  ));
}

// Per-kind default base URLs are a UX concern: the UI suggests them
// visibly (and the user can replace them with any gateway endpoint); the
// backend never invents defaults and rejects an empty base URL.
const KIND_DEFAULTS = {
  messages: 'https://api.anthropic.com',
  responses: 'https://api.openai.com/v1',
  chat: 'https://api.openai.com/v1',
  ollama: 'http://localhost:11434',
  codex: 'https://chatgpt.com/backend-api/codex',
};

async function addProvider(provider = null) {
  const initialKind = provider?.kind ?? 'chat';
  const isCodex = initialKind === 'codex';
  const res = await dialog({
    title: provider ? 'Edit provider' : 'Add provider',
    message: provider?.kind === 'codex'
      ? 'Codex is provider-specific — its kind cannot be changed here.'
      : provider
        ? 'Update provider settings. The kind (wire format) can be changed.'
        : isCodex
          ? 'Codex uses ChatGPT OAuth — no API key needed. Sign in after saving.'
          : 'API keys are stored in the local SQLite credential store.',
    fields: [
      {
        name: 'kind', label: 'Kind', tag: 'select',
        options: [
          { value: 'messages', label: 'Messages' },
          { value: 'responses', label: 'Responses' },
          { value: 'chat', label: 'Chat' },
          { value: 'ollama', label: 'Ollama (local)' },
          { value: 'codex', label: 'Codex (ChatGPT)' },
        ],
        value: initialKind,
        // Codex is provider-specific (OAuth accounts, fixed backend URL):
        // its kind cannot be changed in place.
        disabled: isCodex,
        onChange: (kindInput, all) => {
          const urlInput = all.base_url;
          const apiKeyInput = all.api_key;
          if (!urlInput) return;
          const current = urlInput.value.trim();
          const known = Object.values(KIND_DEFAULTS);
          if (current === '' || known.includes(current)) {
            urlInput.value = KIND_DEFAULTS[kindInput.value] ?? '';
          }
          const isCod = kindInput.value === 'codex';
          const isOllama = kindInput.value === 'ollama';
          urlInput.disabled = isCod;
          urlInput.placeholder = isCod ? 'Fixed — uses ChatGPT Codex backend' : `API base URL — vendor endpoint or AI gateway (e.g. ${KIND_DEFAULTS[kindInput.value] ?? ''})`;
          if (apiKeyInput) {
            apiKeyInput.disabled = isCod;
            if (isCod) {
              apiKeyInput.placeholder = 'Not needed — uses ChatGPT OAuth';
            } else if (isOllama) {
              apiKeyInput.placeholder = 'optional — only if Ollama is behind an auth proxy';
            } else {
              apiKeyInput.placeholder = provider?.has_api_key ? 'leave blank to keep current key' : 'sk-…';
            }
          }
        },
      },
      { name: 'name', label: 'Name', value: provider?.name ?? '', placeholder: 'e.g. my provider' },
      { name: 'base_url', label: 'Base URL', value: provider?.base_url ?? KIND_DEFAULTS[initialKind] ?? '', placeholder: `API base URL — vendor endpoint or AI gateway (e.g. ${KIND_DEFAULTS[initialKind]})`, disabled: isCodex },
      { name: 'api_key', label: 'API key', value: '', placeholder: isCodex ? 'Not needed — uses ChatGPT OAuth' : (provider?.has_api_key ? 'leave blank to keep current key' : 'sk-…'), disabled: isCodex },
    ],
    actions: [
      { label: 'Cancel', value: null },
      { label: 'Save', value: 'save' },
    ],
  });
  if (res.value !== 'save') return;
  const { kind, name, base_url, api_key } = res.fields;
  if (!name.trim()) { toast('Provider name is required', 'error'); return; }
  if (kind !== 'codex' && !base_url.trim()) { toast('Base URL is required', 'error'); return; }
  try {
    await rpc('ai.providers.save', {
      id: provider?.id || undefined,
      kind,
      name: name.trim(),
      base_url: base_url.trim(),
      api_key: api_key || undefined,
      enabled: provider?.enabled !== false,
    });
    toast('Provider saved', 'success');
    await refresh();
  } catch (err) {
    toast(err.message, 'error');
  }
}
