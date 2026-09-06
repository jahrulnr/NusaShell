// Providers workspace: Messages / Responses / Chat / Codex registries, credentials, models.

import { rpc } from '../rpc.js';
import { el, toast, dialog, confirmDialog } from '../ui.js';

let providers = [];
let detailId = null;

const KIND_META = {
  messages: { label: 'Messages', mark: 'MS', cls: 'accent-anthropic', desc: 'Messages API format' },
  responses: { label: 'Responses', mark: 'RS', cls: 'accent-openai', desc: 'Responses API format' },
  chat: { label: 'Chat', mark: 'CH', cls: 'accent-compatible', desc: 'Chat Completions API format (incl. OpenRouter hosts)' },
  codex: { label: 'Codex', mark: 'CX', cls: 'accent-codex', desc: 'ChatGPT Codex backend (OAuth, remote v2 compaction)' },
};
const KIND_META_FALLBACK = { label: 'Unknown', mark: '?', cls: 'accent-compatible', desc: 'Unsupported provider kind — delete and re-add as messages, responses, chat, or codex.' };

const DRIVER_META = {
  anthropic: { label: 'Anthropic', mark: 'AN', cls: 'accent-anthropic', desc: 'Anthropic Messages API' },
  openai: { label: 'OpenAI', mark: 'OA', cls: 'accent-openai', desc: 'OpenAI Responses API' },
  openrouter: { label: 'OpenRouter', mark: 'OR', cls: 'accent-compatible', desc: 'OpenRouter-compatible API' },
  codex: { label: 'Codex', mark: 'CX', cls: 'accent-codex', desc: 'ChatGPT Codex backend (OAuth, no API key needed)' },
};

const API_KIND_OPTIONS = [
  { value: 'responses', label: 'Responses' },
  { value: 'chat', label: 'Chat' },
  { value: 'messages', label: 'Messages' },
  { value: 'codex', label: 'Codex' },
];

export const BUILTIN_PROVIDERS = [
  {
    id: 'anthropic',
    driver: 'anthropic',
    kind: 'messages',
    name: 'Anthropic',
    base_url: 'https://api.anthropic.com',
    enabled: true,
    configured: false,
    has_api_key: false,
    models: [],
    builtin: true,
  },
  {
    id: 'openai',
    driver: 'openai',
    kind: 'responses',
    name: 'OpenAI',
    base_url: 'https://api.openai.com/v1',
    enabled: true,
    configured: false,
    has_api_key: false,
    models: [],
    builtin: true,
  },
  {
    id: 'openrouter',
    driver: 'openrouter',
    kind: 'chat',
    name: 'OpenRouter',
    base_url: 'https://openrouter.ai/api/v1',
    enabled: true,
    configured: false,
    has_api_key: false,
    models: [],
    builtin: true,
  },
  {
    id: 'codex',
    driver: 'codex',
    kind: 'codex',
    name: 'Codex',
    base_url: 'https://chatgpt.com/backend-api/codex',
    enabled: true,
    configured: false,
    has_api_key: false,
    models: [],
    builtin: true,
  },
];

export function mergeProviderRegistry(configured = []) {
  const records = Array.isArray(configured) ? configured : [];
  const saved = new Map(records.filter((provider) => provider?.id).map((provider) => [provider.id, provider]));
  const fixed = BUILTIN_PROVIDERS.map((definition) => {
    const provider = saved.get(definition.id);
    if (!provider) return { ...definition, models: [] };
    const kind = definition.id === 'openrouter' && ['messages', 'responses', 'chat'].includes(provider.kind)
      ? provider.kind
      : definition.kind;
    return {
      ...definition,
      ...provider,
      id: definition.id,
      driver: definition.driver,
      kind,
      name: provider.name?.trim() || definition.name,
      base_url: provider.base_url || definition.base_url,
      enabled: provider.enabled !== undefined ? provider.enabled : definition.enabled,
      models: [...(provider.models ?? [])],
      builtin: true,
    };
  });
  const custom = records
    .filter((provider) => !BUILTIN_PROVIDERS.some((definition) => definition.id === provider.id))
    .map((provider) => ({ ...provider, builtin: false }));
  return [...fixed, ...custom];
}

export function cacheTTLsFor(provider = {}) {
  let ttls = [];
  if (Array.isArray(provider.cache_ttls) && provider.cache_ttls.length) {
    ttls = [...provider.cache_ttls];
  } else if (provider.kind === 'messages') ttls = ['5m', '1h'];
  else if (provider.kind === 'responses') ttls = ['30m'];
  else if (provider.kind === 'codex') ttls = ['30m'];
  else if (provider.driver === 'openrouter') ttls = ['5m', '1h'];
  else if (provider.kind === 'chat') ttls = ['30m'];
  if (!ttls.length) return ttls;
  return ttls.includes('off') ? ttls : [...ttls, 'off'];
}

export function effectiveCacheTTL(provider = {}) {
  const ttls = cacheTTLsFor(provider);
  if (provider.cache_ttl === 'off') return 'off';
  if (provider.cache_ttl && ttls.includes(provider.cache_ttl)) return provider.cache_ttl;
  return ttls.find((ttl) => ttl !== 'off') || '';
}

function providerMeta(provider) {
  const kindMeta = KIND_META[provider.kind] || KIND_META_FALLBACK;
  if (!provider.builtin || !DRIVER_META[provider.driver]) return kindMeta;
  const driverMeta = DRIVER_META[provider.driver];
  if (provider.driver !== 'openrouter') return driverMeta;
  return {
    ...driverMeta,
    desc: `${kindMeta.label} API via OpenRouter`,
  };
}

export async function initProviders() {
  document.getElementById('add-provider-btn').addEventListener('click', () => addProvider());
  await refresh();
}

export async function refresh() {
  const res = await rpc('ai.providers.list');
  providers = mergeProviderRegistry(res.providers ?? []);
  if (detailId && !providers.some((p) => p.id === detailId)) detailId = null;
  renderRegistry();
}

function renderRegistry() {
  const registry = document.getElementById('provider-registry');
  const detail = document.getElementById('provider-detail');
  registry.style.display = detailId ? 'none' : '';
  detail.hidden = !detailId;
  document.getElementById('add-provider-btn').hidden = !!detailId;
  const acpSection = document.getElementById('provider-acp-section');
  const addAcp = document.getElementById('add-acp-agent-btn');
  if (acpSection) acpSection.hidden = !!detailId;
  if (addAcp) addAcp.hidden = !!detailId;
  registry.innerHTML = '';
  if (!providers.length) {
    registry.append(el('div', { class: 'empty-state', style: 'grid-column:1/-1' },
      el('div', { class: 'empty-mark', text: '▣' }),
      el('strong', { text: 'No providers configured' }),
      el('span', { text: 'Configure one of the built-in providers or add a custom provider, then import its models to chat with the agent.' }),
    ));
    return;
  }
  for (const p of providers) registry.append(renderProviderCard(p));
  if (detailId) renderDetail(providers.find((p) => p.id === detailId));
}

function renderCacheTTLBadge(p) {
  const selected = effectiveCacheTTL(p);
  if (!selected) return null;
  const style = p.cache_style || (p.kind === 'messages' ? 'anthropic' : 'openai');
  let title;
  if (selected === 'off') {
    title = 'Prompt cache disabled for this provider';
  } else if (style === 'anthropic') {
    title = `Selected cache_control TTL: ${selected}`;
  } else if (selected === '30m') {
    title = 'Selected prompt_cache_options.ttl: 30m';
  } else {
    title = `Selected cache TTL: ${selected}`;
  }
  const label = selected === 'off'
    ? 'off'
    : (p.id === 'openrouter' ? `${selected} · via upstream` : selected);
  return el('span', { class: 'provider-cache-ttl', title, text: `cache ${label}` });
}

function renderProviderCard(p) {
  const meta = providerMeta(p);
  let modelStatus = 'not configured';
  if (p.configured) modelStatus = `${p.models?.length ?? 0} models`;
  else if (p.builtin) modelStatus = 'built-in · not configured';
  const cacheBadge = renderCacheTTLBadge(p);
  const enabledToggle = p.builtin && !p.configured
    ? null
    : el('label', { class: 'toggle provider-enabled', title: p.enabled === false ? 'Enable provider' : 'Disable provider' },
      el('input', { type: 'checkbox', checked: p.enabled !== false }),
      el('span', { class: 'toggle-slider' }),
    );
  const configureButton = el('button', { class: 'mini-btn ghost provider-configure', type: 'button', text: 'Configure' });
  const deleteButton = p.builtin
    ? null
    : el('button', { class: 'mini-btn danger provider-delete', type: 'button', text: 'Delete' });
  const card = el('article', {
    class: `provider-registry-card ${meta.cls}${p.id === detailId ? ' is-active' : ''}`,
  },
    el('div', { class: 'provider-card-head' },
      el('div', { class: 'provider-mark', text: meta.mark }),
      el('div', { style: 'min-width:0' },
        el('h2', { text: p.name }),
        el('p', { text: p.base_url || meta.label }),
      ),
      enabledToggle,
    ),
    el('p', { text: meta.desc }),
    cacheBadge ? el('div', { class: 'provider-card-meta' }, cacheBadge) : null,
    el('div', { class: 'provider-card-footer' },
      el('span', { class: `provider-status${p.configured ? ' configured' : ''}`, text: modelStatus }),
      el('div', { class: 'provider-card-actions' }, configureButton, deleteButton),
    ),
  );
  configureButton.addEventListener('click', (event) => {
    event.stopPropagation();
    showDetail(p.id);
  });
  deleteButton?.addEventListener('click', (event) => deleteProvider(p, event));
  enabledToggle?.querySelector('input')?.addEventListener('change', (event) => toggleProvider(p, event));
  return card;
}

async function deleteProvider(provider, event) {
  event.stopPropagation();
  const ok = await confirmDialog('Delete provider', `"${provider.name}" and its stored API key will be removed.`, 'Delete');
  if (!ok) return;
  try {
    await rpc('ai.providers.delete', { id: provider.id });
    toast('Provider deleted', 'success');
    await refresh();
  } catch (err) { toast(err.message, 'error'); }
}

function providerSaveFields(provider, extra = {}) {
  return {
    id: provider.id,
    driver: provider.driver || undefined,
    kind: provider.kind,
    name: provider.name,
    base_url: provider.base_url,
    enabled: provider.enabled !== false,
    ...extra,
  };
}

async function saveProviderCacheTTL(provider, ttl) {
  if (!ttl || ttl === effectiveCacheTTL(provider)) return;
  try {
    await rpc('ai.providers.save', providerSaveFields(provider, { cache_ttl: ttl }));
    toast(ttl === 'off' ? 'Prompt cache off' : `Cache TTL ${ttl}`, 'success');
    await refresh();
  } catch (err) {
    toast(err.message, 'error');
  }
}

async function toggleProvider(provider, event) {
  const toggle = event.currentTarget;
  toggle.disabled = true;
  try {
    await rpc('ai.providers.save', providerSaveFields(provider, { enabled: toggle.checked }));
    toast(toggle.checked ? 'Provider enabled' : 'Provider disabled', 'success');
    await refresh();
  } catch (err) {
    toggle.checked = !toggle.checked;
    toast(err.message, 'error');
  } finally {
    toggle.disabled = false;
  }
}

function showDetail(id) {
  detailId = id;
  renderRegistry();
}

function backToRegistry() {
  detailId = null;
  renderRegistry();
}

function renderCacheTTLPicks(p) {
  const ttls = cacheTTLsFor(p);
  const selected = effectiveCacheTTL(p);
  const chips = ttls.map((ttl) => el('button', {
    class: `provider-cache-ttl-chip${ttl === selected ? ' is-active' : ''}`,
    type: 'button',
    text: ttl,
    dataset: { ttl },
    'aria-pressed': ttl === selected ? 'true' : 'false',
    title: ttl === 'off'
      ? 'Disable prompt cache for this provider'
      : `Use prompt cache TTL ${ttl}`,
  }));
  const picks = el('dd', { class: 'provider-cache-ttl-picks', id: 'provider-cache-ttl' },
    ...chips,
    p.id === 'openrouter' ? el('span', { class: 'provider-cache-ttl-note', text: 'via upstream' }) : null,
  );
  picks.querySelectorAll('.provider-cache-ttl-chip').forEach((chip) => {
    chip.addEventListener('click', (event) => {
      event.stopPropagation();
      saveProviderCacheTTL(p, chip.dataset.ttl);
    });
  });
  return picks;
}

function renderDetail(p) {
  const meta = providerMeta(p);
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
        el('p', { text: p.builtin ? meta.desc : meta.label }),
      ),
    ),
    el('dl', { class: 'provider-detail-grid' },
      el('div', {}, el('dt', { text: 'Provider' }), el('dd', { text: DRIVER_META[p.driver]?.label || 'Automatic' })),
      el('div', {}, el('dt', { text: 'API kind' }), el('dd', { text: (KIND_META[p.kind] || KIND_META.chat).label })),
      el('div', {}, el('dt', { text: 'Base URL' }), el('dd', { text: p.base_url || '—' })),
      el('div', {},
        el('dt', { text: p.kind === 'codex' ? 'Auth' : 'API key' }),
        el('dd', {
          text: p.kind === 'codex'
            ? (p.has_api_key ? 'ChatGPT OAuth ✓' : 'Not signed in')
            : (p.has_api_key ? '••••••••' : '—'),
        }),
      ),
      el('div', {}, el('dt', { text: 'Status' }), el('dd', { text: p.enabled === false ? 'disabled' : 'enabled' })),
      ...(cacheTTLsFor(p).length
        ? [el('div', {},
          el('dt', { text: 'Cache TTL' }),
          renderCacheTTLPicks(p),
        )]
        : []),
    ),
    el('div', { class: 'provider-detail-actions' },
      el('button', { class: 'mini-btn provider-edit', type: 'button', text: 'Edit' }),
      el('button', { class: 'mini-btn ghost provider-test', type: 'button', text: 'Test connection', disabled: !p.configured }),
      el('button', { class: 'mini-btn ghost provider-import', type: 'button', text: 'Import models', disabled: !p.configured }),
      p.builtin ? null : el('button', { class: 'mini-btn danger provider-delete', type: 'button', text: 'Delete' }),
    ),
  ));

  // Codex-specific sections: unified accounts+usage card + runtime binary
  if (p.kind === 'codex') {
    detail.append(el('div', { class: 'codex-cards-grid' },
      renderCodexAccounts(p),
      renderCodexRuntime(p),
    ));
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

  const editBtn = detail.querySelector('.provider-edit');
  const testBtn = detail.querySelector('.provider-test');
  const importBtn = detail.querySelector('.provider-import');
  const delBtn = detail.querySelector('.provider-delete');
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
  delBtn?.addEventListener('click', async () => {
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
  const card = el('div', { class: 'provider-models-card codex-accounts-card', id: 'codex-accounts-card' },
    el('div', { class: 'provider-models-head' },
      el('div', {},
        el('h2', { text: 'ChatGPT Accounts' }),
        el('p', { text: 'Sign in or import from Codex CLI — account info, plan, and usage quota' }),
      ),
      el('div', { class: 'codex-auth-buttons' },
        el('button', { class: 'mini-btn ghost', type: 'button', id: 'codex-import-cli-btn', text: 'Import from Codex CLI' }),
        el('button', { class: 'mini-btn ghost', type: 'button', id: 'codex-refresh-circuits-btn', text: '\u21bb Refresh' }),
        el('button', { class: 'mini-btn', type: 'button', id: 'codex-login-btn', text: 'Sign in with ChatGPT' }),
      ),
    ),
    el('div', { class: 'codex-account-list', id: 'codex-account-list' },
      el('div', { class: 'provider-model-empty', text: 'Loading accounts\u2026' }),
    ),
  );
  const importBtn = card.querySelector('#codex-import-cli-btn');
  const refreshBtn = card.querySelector('#codex-refresh-circuits-btn');
  const loginBtn = card.querySelector('#codex-login-btn');
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
  setTimeout(() => refreshCodexAccounts(p.id), 0);
  return card;
}

async function refreshCodexAccounts(providerId) {
  const list = document.getElementById('codex-account-list');
  if (!list) return;
  try {
    // Prefer the unified usage payload (identity + quota). Fall back to
    // accounts.list when usage is unavailable so Switch/Remove still work.
    let accounts = [];
    try {
      const res = await rpc('ai.codex.usage', { provider_id: providerId });
      accounts = res.accounts ?? [];
    } catch (usageErr) {
      const res = await rpc('ai.codex.accounts.list', { provider_id: providerId });
      accounts = (res.accounts ?? []).map((acc) => ({ ...acc, error: usageErr.message }));
    }
    if (!accounts.length) {
      list.innerHTML = '';
      list.append(el('div', { class: 'provider-model-empty', text: 'No accounts signed in. Click "Sign in with ChatGPT" or "Import from Codex CLI" to add one.' }));
      return;
    }
    list.innerHTML = '';
    for (const acc of accounts) {
      const display = acc.email || acc.name || acc.account_id;
      const secondary = (acc.email && acc.name) ? `${acc.name} \u00b7 ${acc.account_id}` : (acc.email ? acc.account_id : (acc.name ? acc.account_id : ''));
      const planLabel = acc.plan ? acc.plan.charAt(0).toUpperCase() + acc.plan.slice(1) : (acc.error ? 'Error' : '\u2014');
      const circuitUntil = acc.circuit_open
        ? (acc.circuit_open_until ? new Date(acc.circuit_open_until * 1000).toLocaleString() : 'soon')
        : '';

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
        el('div', { class: 'codex-account-info' },
          el('span', { class: 'codex-account-id', text: display }),
          ...(secondary ? [el('span', { class: 'codex-account-secondary', text: secondary })] : []),
          el('span', { class: 'codex-account-meta' },
            ...(acc.active ? [el('span', { class: 'codex-account-badge active', text: 'active' })] : []),
            ...(acc.circuit_open ? [el('span', { class: 'codex-account-badge circuit-open', text: `limit \u00b7 resets ${circuitUntil}` })] : []),
            ...(acc.limit_reached && !acc.circuit_open ? [el('span', { class: 'codex-account-badge circuit-open', text: 'limit reached' })] : []),
          ),
        ),
        el('div', { class: 'codex-account-quota' },
          el('span', { class: 'codex-account-plan', text: planLabel }),
          ...usageParts,
        ),
        el('div', { class: 'codex-account-actions' },
          ...(acc.active ? [] : [el('button', { class: 'mini-btn ghost', type: 'button', text: 'Switch', 'data-acc': acc.account_id, 'data-action': 'switch' })]),
          el('button', { class: 'mini-btn danger', type: 'button', text: 'Remove', 'data-acc': acc.account_id, 'data-action': 'logout' }),
        ),
      ));
    }
    list.querySelectorAll('button').forEach((btn) => {
      btn.addEventListener('click', async (e) => {
        const accId = e.currentTarget.dataset.acc;
        const action = e.currentTarget.dataset.action;
        if (action === 'switch') {
          try {
            await rpc('ai.codex.accounts.switch', { provider_id: providerId, account_id: accId });
            toast('Switched account', 'success');
            await refreshCodexAccounts(providerId);
            await refresh();
          } catch (err) { toast(err.message, 'error'); }
        } else if (action === 'logout') {
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

export function renderUsageBar(label, win) {
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

function renderCodexRuntime(_p) {
  const card = el('div', { class: 'provider-models-card codex-runtime-card', id: 'codex-runtime-card' },
    el('div', { class: 'provider-models-head' },
      el('div', {},
        el('h2', { text: 'Codex Runtime' }),
        el('p', { text: 'Managed official Codex CLI binary (ACP and tooling)' }),
      ),
      el('button', { class: 'mini-btn', type: 'button', id: 'codex-runtime-download-btn', text: 'Download' }),
    ),
    el('div', { class: 'codex-runtime-status', id: 'codex-runtime-status' },
      el('div', { class: 'provider-model-empty', text: 'Checking runtime status…' }),
    ),
  );
  const dlBtn = card.querySelector('#codex-runtime-download-btn');
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
  codex: 'https://chatgpt.com/backend-api/codex',
};

async function addProvider(provider = null) {
  const driver = provider?.driver || 'openrouter';
  const selectableKind = !provider?.builtin || driver === 'openrouter';
  const defaultKinds = { anthropic: 'messages', openai: 'responses', openrouter: 'chat', codex: 'codex' };
  const initialKind = provider?.kind ?? defaultKinds[driver] ?? 'chat';
  const isCodex = initialKind === 'codex' || provider?.kind === 'codex';
  let initialBaseURL = provider?.base_url;
  if (initialBaseURL === undefined && provider?.builtin) initialBaseURL = KIND_DEFAULTS[initialKind] ?? '';
  if (initialBaseURL === undefined) initialBaseURL = '';
  let message;
  if (isCodex && provider?.builtin) {
    message = 'Prefer Sign in with ChatGPT or Import from Codex CLI on the Codex detail page. Pasting an OAuth access token here is an optional fallback only.';
  } else if (!provider) {
    message = 'Custom providers use the selected API driver. For Codex, prefer Sign in / Import from CLI on the detail page; pasting a token is optional. Credentials are stored in the local SQLite credential store.';
  } else if (provider.builtin) {
    message = 'Update this built-in provider. OpenRouter-compatible cards can use any supported API kind. API keys are optional.';
  } else if (isCodex) {
    message = 'Update the Codex provider. Prefer Sign in / Import from CLI on the detail page; pasting an OAuth access token is an optional fallback.';
  } else {
    message = 'Update the custom provider. Provider kinds may accept a blank key.';
  }
  const res = await dialog({
    title: provider ? 'Edit provider' : 'Add custom provider',
    message,
    fields: [
      ...(selectableKind ? [{
        name: 'kind', label: 'API kind', tag: 'select',
        options: API_KIND_OPTIONS,
        value: initialKind,
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
          urlInput.placeholder = isCod
            ? 'ChatGPT Codex backend URL'
            : `API base URL — vendor endpoint or AI gateway (e.g. ${KIND_DEFAULTS[kindInput.value] ?? ''})`;
          if (apiKeyInput) {
            apiKeyInput.placeholder = isCod
              ? (provider?.has_api_key ? 'leave blank to keep current credential' : 'optional fallback — prefer Sign in / Import from CLI')
              : (provider?.has_api_key ? 'leave blank to keep current credential' : 'leave blank if the host needs no auth');
          }
        },
      }] : []),
      { name: 'name', label: 'Name', value: provider?.name ?? '', placeholder: 'e.g. my provider' },
      { name: 'base_url', label: 'Base URL', value: initialBaseURL, placeholder: `API base URL — vendor endpoint or AI gateway (e.g. ${KIND_DEFAULTS[initialKind] ?? 'https://gateway.example/v1'})` },
      {
        name: 'api_key',
        label: isCodex ? 'OAuth access token (optional fallback)' : 'API key (optional)',
        type: 'password',
        value: '',
        placeholder: provider?.has_api_key
          ? 'leave blank to keep current credential'
          : isCodex
            ? 'optional fallback — prefer Sign in / Import from CLI'
            : 'leave blank if the host needs no auth',
      },
    ],
    actions: [
      { label: 'Cancel', value: null },
      { label: 'Save', value: 'save' },
    ],
  });
  if (res.value !== 'save') return;
  const kind = selectableKind ? res.fields.kind : initialKind;
  const selectedDriver = kind === 'codex' ? 'codex' : driver;
  const { name, base_url, api_key } = res.fields;
  if (!name.trim()) { toast('Provider name is required', 'error'); return; }
  if (!base_url.trim()) { toast('Base URL is required', 'error'); return; }
  try {
    await rpc('ai.providers.save', {
      id: provider?.id || undefined,
      driver: selectedDriver,
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
