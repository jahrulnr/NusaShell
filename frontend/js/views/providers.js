// Providers workspace: Messages / Responses / Chat registries, credentials, models.

import { rpc } from '../rpc.js';
import { el, toast, dialog, confirmDialog } from '../ui.js';

let providers = [];
let detailId = null;

const KIND_META = {
  messages: { label: 'Messages', mark: 'MS', cls: 'accent-anthropic', desc: 'Messages API format' },
  responses: { label: 'Responses', mark: 'RS', cls: 'accent-openai', desc: 'Responses API format' },
  chat: { label: 'Chat', mark: 'CH', cls: 'accent-compatible', desc: 'Chat Completions API format (incl. OpenRouter hosts)' },
};
const KIND_META_FALLBACK = { label: 'Unknown', mark: '?', cls: 'accent-compatible', desc: 'Unsupported provider kind — delete and re-add as messages, responses, or chat.' };

const DRIVER_META = {
  anthropic: { label: 'Anthropic', mark: 'AN', cls: 'accent-anthropic', desc: 'Anthropic Messages API' },
  openai: { label: 'OpenAI', mark: 'OA', cls: 'accent-openai', desc: 'OpenAI Responses API' },
  openrouter: { label: 'OpenRouter', mark: 'OR', cls: 'accent-compatible', desc: 'OpenRouter-compatible API' },
};

const API_KIND_OPTIONS = [
  { value: 'responses', label: 'Responses' },
  { value: 'chat', label: 'Chat' },
  { value: 'messages', label: 'Messages' },
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
  if (Array.isArray(provider.cache_ttls) && provider.cache_ttls.length) return provider.cache_ttls;
  if (provider.kind === 'messages') return ['5m', '1h'];
  if (provider.kind === 'responses') return ['30m'];
  if (provider.driver === 'openrouter') return ['5m', '1h'];
  if (provider.kind === 'chat') return ['30m'];
  return [];
}

export function effectiveCacheTTL(provider = {}) {
  const ttls = cacheTTLsFor(provider);
  if (provider.cache_ttl && ttls.includes(provider.cache_ttl)) return provider.cache_ttl;
  return ttls[0] || '';
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
  const title = style === 'anthropic'
    ? `Selected cache_control TTL: ${selected}`
    : selected === '30m'
      ? 'Selected prompt_cache_options.ttl: 30m'
      : `Selected cache TTL: ${selected}`;
  const label = p.id === 'openrouter' ? `${selected} · via upstream` : selected;
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
    toast(`Cache TTL ${ttl}`, 'success');
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
    title: `Use prompt cache TTL ${ttl}`,
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
      el('div', {}, el('dt', { text: 'API key' }), el('dd', { text: p.has_api_key ? '••••••••' : '—' })),
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
};

async function addProvider(provider = null) {
  const driver = provider?.driver || 'openrouter';
  const selectableKind = !provider?.builtin || driver === 'openrouter';
  const defaultKinds = { anthropic: 'messages', openai: 'responses', openrouter: 'chat' };
  const initialKind = provider?.kind ?? defaultKinds[driver] ?? 'chat';
  let initialBaseURL = provider?.base_url;
  if (initialBaseURL === undefined && provider?.builtin) initialBaseURL = KIND_DEFAULTS[initialKind] ?? '';
  if (initialBaseURL === undefined) initialBaseURL = '';
  let message;
  if (!provider) {
    message = 'Custom providers use the OpenRouter-compatible provider driver. API keys are stored in the local SQLite credential store.';
  } else if (provider.builtin) {
    message = 'Update this built-in provider. OpenRouter-compatible cards can use any supported API kind.';
  } else {
    message = 'Update the custom provider. It uses the OpenRouter-compatible provider driver.';
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
          if (!urlInput) return;
          const current = urlInput.value.trim();
          const known = Object.values(KIND_DEFAULTS);
          if (current === '' || known.includes(current)) {
            urlInput.value = KIND_DEFAULTS[kindInput.value] ?? '';
          }
          urlInput.placeholder = `API base URL — vendor endpoint or AI gateway (e.g. ${KIND_DEFAULTS[kindInput.value] ?? ''})`;
        },
      }] : []),
      { name: 'name', label: 'Name', value: provider?.name ?? '', placeholder: 'e.g. my provider' },
      { name: 'base_url', label: 'Base URL', value: initialBaseURL, placeholder: `API base URL — vendor endpoint or AI gateway (e.g. ${KIND_DEFAULTS[initialKind] ?? 'https://gateway.example/v1'})` },
      { name: 'api_key', label: 'API key', type: 'password', value: '', placeholder: provider?.has_api_key ? 'leave blank to keep current key' : 'sk-…' },
    ],
    actions: [
      { label: 'Cancel', value: null },
      { label: 'Save', value: 'save' },
    ],
  });
  if (res.value !== 'save') return;
  const kind = selectableKind ? res.fields.kind : initialKind;
  const { name, base_url, api_key } = res.fields;
  if (!name.trim()) { toast('Provider name is required', 'error'); return; }
  if (!base_url.trim()) { toast('Base URL is required', 'error'); return; }
  try {
    await rpc('ai.providers.save', {
      id: provider?.id || undefined,
      driver,
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
