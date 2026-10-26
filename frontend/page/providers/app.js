import { rpc as defaultRPC } from '../../js/rpc.js';
import {
  bindTablistKeyboard,
  confirmDialog,
  debounce,
  dialog,
  el,
  toast,
} from '../../js/ui.js';
import {
  cacheTTLsFor,
  effectiveCacheTTL,
  effectiveReasoningSummary,
  reasoningSummariesFor,
} from '../../js/views/providers.js';
import {
  PROVIDER_PROTOCOLS,
  acpAgentModeCount,
  acpAgentModelCount,
  acpAgentStatusLabel,
  acpAgentStatusTone,
  filterAcpAgents,
  filterModels,
  filterProviders,
  formatModelContext,
  providerCredentialLabel,
  providerKindLabel,
  providerModelsCount,
  providerProtocol,
  providerProtocolByValue,
  providerStatusLabel,
  providerStatusTone,
} from './model.js';

const ACP_TRANSPORT_OPTIONS = [
  { value: 'stdio', label: 'stdio — local process' },
  { value: 'remote', label: 'remote — endpoint URL' },
];

const ACP_RISK_OPTIONS = [
  ['read_only', 'Read-only'],
  ['edit_confirmed', 'Edits'],
  ['bypass', 'Bypass'],
];

function text(value, fallback = '—') {
  const copy = String(value ?? '').trim();
  return copy || fallback;
}

function list(value) {
  return Array.isArray(value) ? value : [];
}

function providerMark(provider) {
  const words = String(provider?.name || '').trim().split(/\s+/).filter(Boolean);
  if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase();
  const name = words.join('');
  return name ? name.slice(0, 2).toUpperCase() : 'AI';
}

function providerProtocolHeading(provider) {
  const label = providerKindLabel(provider);
  try {
    const host = new URL(provider?.base_url || '').hostname;
    return host ? `${label} · ${host}` : label;
  } catch {
    return label;
  }
}

function providerAccent(provider) {
  const kind = String(provider?.kind || '').toLowerCase();
  return ['chat', 'responses', 'messages', 'gemini', 'codex'].includes(kind) ? kind : 'chat';
}

function modelLabel(model) {
  return text(model?.display_name || model?.id || model?.name, 'Unnamed model');
}

function modelId(model) {
  return text(model?.id || model?.name, '—');
}

function acpModes(agent) {
  return list(agent?.modes || agent?.cached_modes);
}

function acpModels(agent) {
  return list(agent?.models || agent?.cached_models);
}

// ACP catalog records expose name/tier where provider models expose
// display_name/kind — bridge the field names once so the shared filterModels
// seam searches what the card actually renders.
function filterAcpModels(models, query) {
  return filterModels(
    list(models).map((model) => ({ ...model, display_name: model?.display_name || model?.name, kind: model?.kind || model?.tier })),
    query,
  );
}

function acpAuthMethods(agent) {
  return list(agent?.auth_methods || agent?.cached_auth_methods);
}

function acpTransport(agent) {
  return String(agent?.transport || 'stdio').toLowerCase() === 'remote' ? 'remote' : 'stdio';
}

function parseKeyValueLines(value, separator = '=') {
  const result = {};
  for (const line of String(value || '').split('\n')) {
    const index = line.indexOf(separator);
    if (index > 0) {
      const key = line.slice(0, index).trim();
      const item = line.slice(index + 1).trim();
      if (key) result[key] = item;
    }
  }
  return result;
}

function formatEpoch(seconds) {
  const value = Number(seconds);
  if (!Number.isFinite(value) || value <= 0) return '';
  const date = new Date(value * 1000);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleDateString([], { month: 'short', day: 'numeric' });
}

function providerSavePayload(provider, fields = {}) {
  const payload = {
    id: provider?.id || undefined,
    kind: (fields.kind ?? provider?.kind) || undefined,
    name: (fields.name ?? provider?.name) || '',
    base_url: (fields.base_url ?? provider?.base_url) || '',
    enabled: fields.enabled ?? provider?.enabled !== false,
  };
  if (fields.api_key?.trim()) payload.api_key = fields.api_key.trim();
  if (fields.cache_ttl !== undefined) payload.cache_ttl = fields.cache_ttl;
  if (fields.reasoning_summary !== undefined) payload.reasoning_summary = fields.reasoning_summary;
  return payload;
}

function providerRowStatusNode(row) {
  return row?.querySelector('[data-provider-field="status"]');
}

export function createProvidersPage(root = document, { rpcClient = defaultRPC } = {}) {
  const state = {
    tab: 'providers',
    providers: [],
    selectedProviderId: null,
    providerQuery: '',
    providerLoading: false,
    providerError: null,
    providerProbe: new Map(),
    modelQuery: '',
    providerAction: new Map(),
    acp: [],
    selectedAcpId: null,
    acpQuery: '',
    acpModelQuery: '',
    acpLoading: false,
    acpError: null,
    acpAction: new Map(),
    codexAccounts: new Map(),
    codexLoading: new Set(),
    // The managed Codex binary is a single global runtime — status is not
    // scoped to a provider_id (contracts.CodexRuntimeStatusResult).
    codexRuntime: null,
    codexRuntimeLoading: false,
    codexRuntimeLoaded: false,
  };

  let initialized = false;
  let providerRefreshToken = 0;
  let acpRefreshToken = 0;
  let providerRefreshPromise = null;
  let acpRefreshPromise = null;
  let codexLoadToken = 0;
  let codexRuntimeTimer = 0;
  let deviceLogin = null;
  let deviceLoginTimer = 0;
  let mobilePaneToken = 0;
  let destroyed = false;

  const find = (id) => root.querySelector(`#${id}`);
  const selectedProvider = () => state.providers.find((provider) => provider.id === state.selectedProviderId) || null;
  const selectedAcp = () => state.acp.find((agent) => agent.id === state.selectedAcpId) || null;
  const isMobile = () => Number(window.innerWidth) <= 720;

  function setMobilePane(tab, pane) {
    const panel = root.querySelector(`#${tab === 'acp' ? 'acp-panel' : 'providers-panel'}`);
    const workspace = panel?.querySelector('.providers-workspace');
    if (!workspace) return;
    const previousPane = workspace.dataset.mobilePane || 'list';
    const changed = previousPane !== pane;
    if (pane === 'detail' && previousPane === 'list' && isMobile()) {
      workspace.dataset.mobileListScroll = String(root.scrollTop || 0);
    }
    workspace.dataset.mobilePane = pane;
    panel.dataset.mobilePane = pane;
    if (!changed) return mobilePaneToken;

    const token = ++mobilePaneToken;
    if (!isMobile()) return token;
    if (pane === 'detail') {
      root.scrollTop = 0;
    } else if (pane === 'list') {
      const top = Number(workspace.dataset.mobileListScroll) || 0;
      window.setTimeout(() => {
        if (destroyed || token !== mobilePaneToken || workspace.dataset.mobilePane !== 'list' || !isMobile()) return;
        root.scrollTop = top;
      }, 0);
    }
    return token;
  }

  function focusMobileControl(id, token = mobilePaneToken) {
    if (!isMobile()) return;
    window.setTimeout(() => {
      if (destroyed || token !== mobilePaneToken) return;
      find(id)?.focus({ preventScroll: true });
    }, 0);
  }

  function focusMobileRow(tab, token) {
    if (!isMobile()) return;
    window.setTimeout(() => {
      if (destroyed || token !== mobilePaneToken) return;
      const acp = tab === 'acp';
      const id = acp ? state.selectedAcpId : state.selectedProviderId;
      rowFor(acp ? 'acp-list' : 'providers-list', acp ? 'acpId' : 'providerId', id)
        ?.querySelector('.providers-list-row')?.focus({ preventScroll: true });
    }, 0);
  }

  function setText(id, value) {
    const node = find(id);
    if (node) node.textContent = value;
    return node;
  }

  function setSyncState(id, value, tone = '') {
    const node = find(id);
    if (!node) return;
    node.textContent = value;
    node.dataset.state = tone;
  }

  function providerProbeState(provider) {
    return state.providerProbe.get(provider?.id) || {};
  }

  function providerTone(provider) {
    return providerStatusTone(provider, providerProbeState(provider).status || '');
  }

  function visibleProviders() {
    return filterProviders(state.providers, state.providerQuery);
  }

  function visibleAcp() {
    return filterAcpAgents(state.acp, state.acpQuery);
  }

  function rowFor(listId, dataKey, id) {
    const listNode = find(listId);
    if (!listNode) return null;
    return [...listNode.querySelectorAll('.providers-list-item')]
      .find((item) => item.dataset[dataKey] === id) || null;
  }

  function renderToolbar() {
    const providers = visibleProviders();
    setText('providers-count', `${state.providers.length} configured`);
    setText('providers-index-count', String(state.providers.length));
    setText('acp-count', `${state.acp.length} agents`);
    setText('acp-index-count', String(state.acp.length));
  }

  function renderStatusBadge(node, provider, copy = null) {
    if (!node) return;
    const tone = providerTone(provider);
    node.className = `providers-status-badge providers-status-${tone}`;
    node.textContent = copy || providerStatusLabel(provider, providerProbeState(provider).status || '');
    node.dataset.state = tone;
  }

  function renderProviderRowStatus(row, provider) {
    const status = providerRowStatusNode(row);
    if (!status) return;
    const tone = providerTone(provider);
    status.className = `providers-row-status providers-status-${tone}`;
    status.textContent = providerStatusLabel(provider, providerProbeState(provider).status || '');
    status.dataset.state = tone;
    const models = row.querySelector('[data-provider-field="models"]');
    if (models) models.textContent = `${providerModelsCount(provider)} models`;
    row.querySelector('.providers-list-row')?.classList.toggle('is-selected', provider.id === state.selectedProviderId);
  }

  function createProviderRow(provider) {
    const item = el('div', { class: 'providers-list-item', role: 'listitem', dataset: { providerId: provider.id } });
    const selected = provider.id === state.selectedProviderId;
    const row = el('button', {
      class: `providers-list-row providers-accent-${providerAccent(provider)}${selected ? ' is-selected' : ''}`,
      type: 'button',
      dataset: { providerId: provider.id },
      'aria-label': `Open ${text(provider.name, provider.id)} details`,
      'aria-current': selected ? 'true' : undefined,
    });
    const copy = el('span', { class: 'providers-row-copy' }, [
      el('span', { class: 'providers-row-name' }, [
        el('strong', { text: text(provider.name, provider.id) }),
        el('span', { class: 'providers-row-kind', text: providerKindLabel(provider) }),
      ]),
      el('span', { class: 'providers-row-id', text: text(provider.id) }),
      el('span', { class: 'providers-row-meta' }, [
        el('span', { text: provider.base_url || 'Base URL not set' }),
        el('span', { dataset: { providerField: 'models' }, text: `${providerModelsCount(provider)} models` }),
      ]),
    ]);
    row.append(
      el('span', { class: `providers-row-mark providers-mark-${providerAccent(provider)}`, text: providerMark(provider), 'aria-hidden': 'true' }),
      copy,
      el('span', { class: 'providers-row-status', dataset: { providerField: 'status' } }),
    );
    row.addEventListener('click', () => selectProvider(provider.id));
    item.append(row);
    renderProviderRowStatus(item, provider);
    return item;
  }

  function emptyList(copy, action = null, error = false) {
    const content = [
      el('strong', { text: copy.title }),
      el('span', { text: copy.body }),
    ];
    if (action) {
      const button = el('button', { class: 'providers-inline-action', type: 'button', text: action.label });
      button.addEventListener('click', action.onClick);
      content.push(button);
    }
    return el('div', { class: `providers-empty-list${error ? ' providers-empty-list-error' : ''}`, role: 'status' }, content);
  }

  function renderProviderList() {
    const listNode = find('providers-list');
    if (!listNode) return;
    const visible = visibleProviders();
    listNode.setAttribute('aria-busy', String(state.providerLoading));
    listNode.replaceChildren();
    renderToolbar();
    if (state.providerLoading && !state.providers.length) {
      listNode.append(emptyList({ title: 'Loading providers…', body: 'Reading saved connections.' }));
      return;
    }
    if (state.providerError && !state.providers.length) {
      listNode.append(emptyList(
        { title: 'Providers unavailable', body: state.providerError.message || 'Could not read saved connections.' },
        { label: 'Retry', onClick: () => { void refreshProviders(); } },
        true,
      ));
      return;
    }
    if (!visible.length) {
      listNode.append(emptyList(
        state.providerQuery
          ? { title: 'No matching providers', body: 'Try another search or clear the filter.' }
          : { title: 'No providers configured', body: 'Add a connection to make it available to the agent.' },
        state.providerQuery ? { label: 'Clear search', onClick: () => { state.providerQuery = ''; const input = find('providers-search'); if (input) input.value = ''; reconcileProviderSelection(); renderProviderList(); renderProviderDetail(); } } : { label: 'Add provider', onClick: () => { void openProviderDialog(); } },
      ));
      return;
    }
    for (const provider of visible) listNode.append(createProviderRow(provider));
  }

  function connectionRows(provider) {
    const rows = [
      ['Protocol', providerKindLabel(provider)],
      ['Endpoint', text(provider.base_url, 'Not set')],
      ['Credentials', providerCredentialLabel(provider)],
      ['Models', `${providerModelsCount(provider)} imported`],
    ];
    return rows;
  }

  // Cache TTL and thinking-summary picks: chip buttons that persist through
  // ai.providers.save and mark the effective value.
  function renderPreferencePicks(provider) {
    const nodes = [];
    const ttls = cacheTTLsFor(provider);
    if (ttls.length) {
      const selectedTTL = effectiveCacheTTL(provider);
      const chips = ttls.map((ttl) => {
        const selected = ttl === selectedTTL;
        const chip = el('button', {
          class: `providers-inline-action${selected ? ' is-selected' : ''}`,
          type: 'button',
          text: ttl,
          'aria-pressed': String(selected),
          disabled: state.providerAction.get(provider.id) === 'saving',
          title: ttl === 'off' ? 'Disable prompt cache for this provider' : `Use prompt cache TTL ${ttl}`,
        });
        chip.addEventListener('click', () => { void saveProviderPreference(provider, { cache_ttl: ttl }); });
        return chip;
      });
      nodes.push(el('dt', { text: 'Cache TTL' }), el('dd', { class: 'providers-chip-list', id: 'provider-cache-ttl' }, chips));
    }
    const summaries = reasoningSummariesFor(provider);
    if (summaries.length) {
      const selectedSummary = effectiveReasoningSummary(provider);
      const chips = summaries.map((summary) => {
        const selected = summary === selectedSummary;
        const chip = el('button', {
          class: `providers-inline-action${selected ? ' is-selected' : ''}`,
          type: 'button',
          text: summary,
          'aria-pressed': String(selected),
          disabled: state.providerAction.get(provider.id) === 'saving',
          title: summary === 'none' ? 'Hide Codex thinking summaries' : `Use ${summary} Codex thinking summaries`,
        });
        chip.addEventListener('click', () => { void saveProviderPreference(provider, { reasoning_summary: summary }); });
        return chip;
      });
      nodes.push(el('dt', { text: 'Thinking summary' }), el('dd', { class: 'providers-chip-list', id: 'provider-reasoning-summary' }, chips));
    }
    return nodes;
  }

  function renderConnection(provider) {
    const listNode = find('providers-connection-list');
    if (!listNode) return;
    listNode.replaceChildren();
    for (const [label, value] of connectionRows(provider)) {
      listNode.append(el('dt', { text: label }), el('dd', { text: value }));
    }
    listNode.append(...renderPreferencePicks(provider));
    const enabled = find('providers-enabled');
    if (enabled) {
      enabled.checked = provider.enabled !== false;
      enabled.disabled = false;
    }
  }

  function renderModelList(provider, targetId = 'providers-model-list') {
    const listNode = find(targetId);
    if (!listNode) return;
    const models = filterModels(list(provider?.models), state.modelQuery);
    listNode.replaceChildren();
    if (!list(provider?.models).length) {
      listNode.append(el('p', { class: 'providers-list-empty-copy', role: 'status', text: 'No models imported yet. Test the connection or import the provider catalog.' }));
      return;
    }
    if (!models.length) {
      listNode.append(el('p', { class: 'providers-list-empty-copy', role: 'status', text: 'No model matches that filter.' }));
      return;
    }
    for (const model of models.slice(0, 80)) {
      const badges = [formatModelContext(model?.context), model?.max_output ? `out ${model.max_output}` : '', model?.kind || '']
        .filter(Boolean)
        .map((value) => el('span', { class: 'providers-model-badge', text: value }));
      listNode.append(el('article', { class: 'providers-model-item', role: 'listitem' }, [
        el('div', { class: 'providers-model-copy' }, [
          el('strong', { text: modelLabel(model), title: modelId(model) }),
          el('span', { text: modelId(model) }),
        ]),
        el('span', { class: 'providers-model-badges' }, badges),
      ]));
    }
    if (models.length > 80) listNode.append(el('p', { class: 'providers-list-more', text: `${models.length - 80} more models hidden; refine the filter to find one.` }));
  }

  function renderCodexAccounts(provider) {
    const status = find('providers-codex-status');
    const listNode = find('providers-account-list');
    if (!status || !listNode) return;
    const accounts = state.codexAccounts.get(provider.id);
    listNode.replaceChildren();
    if (state.codexLoading.has(provider.id)) {
      status.textContent = 'Reading account status…';
      listNode.append(el('p', { class: 'providers-list-empty-copy', role: 'status', text: 'Checking locally stored ChatGPT accounts.' }));
      return;
    }
    if (!accounts) {
      status.textContent = 'No account snapshot yet';
      listNode.append(el('p', { class: 'providers-list-empty-copy', role: 'status', text: 'Sign in or import the Codex CLI account to connect Codex.' }));
      return;
    }
    status.textContent = `${accounts.length} account${accounts.length === 1 ? '' : 's'} stored`;
    if (!accounts.length) {
      listNode.append(el('p', { class: 'providers-list-empty-copy', role: 'status', text: 'No ChatGPT account is linked to this provider.' }));
      return;
    }
    for (const account of accounts) {
      const flags = [
        account.active ? 'Active' : '',
        account.circuit_open ? 'Circuit open' : '',
        account.plan || '',
        account.circuit_open_until ? `until ${formatEpoch(account.circuit_open_until)}` : '',
      ].filter(Boolean);
      const action = el('div', { class: 'providers-account-actions' });
      if (!account.active) {
        const switchButton = el('button', { class: 'providers-inline-action', type: 'button', text: 'Use account' });
        switchButton.addEventListener('click', () => { void switchCodexAccount(provider, account); });
        action.append(switchButton);
      }
      const removeButton = el('button', { class: 'providers-inline-action providers-inline-action-danger', type: 'button', text: 'Remove' });
      removeButton.addEventListener('click', () => { void removeCodexAccount(provider, account, removeButton); });
      action.append(removeButton);
      const windows = el('div', { class: 'providers-account-usage' });
      for (const [key, fallback] of [['primary_window', 'Usage window'], ['weekly_window', 'Additional window']]) {
        const usage = account[key];
        if (!usage || usage.used_percent == null || !Number.isFinite(Number(usage.used_percent))) continue;
        const used = Math.max(0, Math.min(100, Number(usage.used_percent)));
        const remaining = 100 - used;
        const seconds = Number(usage.limit_window_seconds);
        let label = fallback;
        if (seconds === 604800) label = 'Weekly';
        else if (seconds >= 2419200 && seconds <= 2678400 && seconds % 86400 === 0) label = `Monthly (${seconds / 86400} days)`;
        else if (seconds > 0 && seconds % 86400 === 0) label = `${seconds / 86400}-day`;
        else if (seconds > 0 && seconds % 3600 === 0) label = `${seconds / 3600}-hour`;
        else if (seconds > 0) label = `${Math.round(seconds / 60)}-minute`;
        const reset = Number(usage.reset_at) > 0
          ? new Date(Number(usage.reset_at) * 1000)
          : Number(usage.reset_after_seconds) > 0 ? new Date(Date.now() + Number(usage.reset_after_seconds) * 1000) : null;
        const meter = el('div', { class: 'providers-usage-meter', role: 'meter', 'aria-label': `${label} usage`, 'aria-valuemin': '0', 'aria-valuemax': '100', 'aria-valuenow': String(used) }, [
          el('span', { style: `width: ${used}%` }),
        ]);
        windows.append(el('section', { class: 'providers-usage-window', 'aria-label': label }, [
          el('div', { class: 'providers-usage-heading' }, [el('strong', { text: label }), el('span', { text: `${Math.round(remaining)}% available` })]),
          meter,
          el('div', { class: 'providers-usage-detail' }, [el('span', { text: `${Math.round(used)}% used` }), el('span', { text: reset && Number.isFinite(reset.getTime()) ? `Resets ${reset.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}` : 'Reset time unavailable' })]),
        ]));
      }
      if (!windows.children.length) windows.append(el('p', { class: 'providers-usage-unavailable', text: account.error || 'Usage unavailable' }));
      listNode.append(el('article', { class: 'providers-account-item', role: 'listitem' }, [
        el('div', { class: 'providers-account-copy' }, [
          el('strong', { text: text(account.name || account.email, account.account_id) }),
          el('span', { text: account.email || account.account_id }),
        ]),
        el('div', { class: 'providers-account-meta' }, flags.map((flag) => el('span', { text: flag }))),
        action,
        windows,
      ]));
    }
  }

  // Runtime and accounts have separate lifecycles and separate cards.
  function ensureCodexRuntimeCard() {
    const card = find('providers-codex-card');
    if (!card) return null;
    let runtime = find('codex-runtime-card');
    if (runtime) return runtime;
    runtime = el('section', { id: 'codex-runtime-card', class: 'providers-card providers-codex-card', 'aria-labelledby': 'codex-runtime-title', hidden: card.hidden }, [
      el('header', { class: 'providers-card-header' }, [
        el('div', {}, [
          el('h3', { id: 'codex-runtime-title', text: 'Codex runtime' }),
          el('span', { text: 'Managed official Codex CLI binary used for ACP and tooling.' }),
        ]),
        el('button', { class: 'providers-action', id: 'codex-runtime-download-btn', type: 'button', text: 'Download' }),
      ]),
      el('div', { class: 'providers-codex-status', id: 'codex-runtime-status', role: 'status' }, [
        el('p', { class: 'providers-list-empty-copy', text: 'Checking runtime status…' }),
      ]),
    ]);
    card.after(runtime);
    find('codex-runtime-download-btn')?.addEventListener('click', () => { void downloadCodexRuntime(); });
    return runtime;
  }

  function renderCodexRuntime() {
    if (!ensureCodexRuntimeCard()) return;
    const status = find('codex-runtime-status');
    const button = find('codex-runtime-download-btn');
    if (!status || !button) return;
    const info = state.codexRuntime;
    const loading = state.codexRuntimeLoading;
    button.disabled = loading || info?.downloading === true;
    button.textContent = info?.downloading ? 'Downloading…' : info?.installed ? 'Re-download' : 'Download';
    status.replaceChildren();
    if (loading && !info) {
      status.append(el('p', { class: 'providers-list-empty-copy', text: 'Checking runtime status…' }));
      return;
    }
    if (info?.error) {
      status.append(el('p', { class: 'providers-list-empty-copy', role: 'alert', text: `Runtime status unavailable: ${info.error}` }));
      return;
    }
    if (info?.downloading) {
      status.append(el('p', { class: 'providers-list-empty-copy', text: 'Downloading the Codex runtime…' }));
      return;
    }
    if (info?.download_error) {
      status.append(el('p', { class: 'providers-list-empty-copy', role: 'alert', text: `Download failed: ${info.download_error}` }));
      return;
    }
    if (info?.installed) {
      status.append(el('p', { class: 'providers-list-empty-copy' }, [
        el('strong', { text: `v${info.version || 'installed'}` }),
        document.createTextNode(` — ${info.path || 'managed install'}`),
      ]));
      return;
    }
    status.append(el('p', { class: 'providers-list-empty-copy', text: 'Not installed. Download fetches the official Codex binary (about 85 MB).' }));
  }

  async function loadCodexRuntime({ silent = false } = {}) {
    if (state.codexRuntimeLoading) return;
    state.codexRuntimeLoading = true;
    if (!silent) renderCodexRuntime();
    try {
      const result = await rpcClient('ai.codex.runtime.status', {}, { timeoutMs: 30000 });
      if (destroyed) return;
      state.codexRuntime = result || {};
      state.codexRuntimeLoaded = true;
    } catch (error) {
      if (destroyed) return;
      state.codexRuntime = { error: error.message || 'Status unavailable' };
      state.codexRuntimeLoaded = true;
    } finally {
      if (destroyed) return;
      state.codexRuntimeLoading = false;
      renderCodexRuntime();
      clearTimeout(codexRuntimeTimer);
      if (state.codexRuntime?.downloading) {
        codexRuntimeTimer = setTimeout(() => {
          if (!destroyed && state.codexRuntime?.downloading) void loadCodexRuntime({ silent: true });
        }, 3000);
      }
    }
  }

  async function downloadCodexRuntime() {
    const provider = selectedProvider();
    if (!provider || String(provider.kind).toLowerCase() !== 'codex') return;
    const button = find('codex-runtime-download-btn');
    if (button) { button.disabled = true; button.textContent = 'Downloading…'; }
    try {
      const result = await rpcClient('ai.codex.runtime.download', { force: true }, { timeoutMs: 180000 });
      toast(`Codex runtime v${result?.version || 'latest'} downloaded`, 'success');
    } catch (error) {
      toast(error.message || 'Codex runtime download failed', 'error');
    } finally {
      if (button?.isConnected) { button.disabled = false; button.textContent = 'Download'; }
    }
    await loadCodexRuntime({ silent: true });
  }

  async function loadCodexAccounts(providerId) {
    const token = ++codexLoadToken;
    state.codexLoading.add(providerId);
    const provider = state.providers.find((candidate) => candidate.id === providerId);
    if (provider?.id === state.selectedProviderId) renderCodexAccounts(provider);
    try {
      let result;
      try {
        result = await rpcClient('ai.codex.usage', { provider_id: providerId }, { timeoutMs: 30000 });
      } catch {
        result = null;
      }
      let accounts = list(result?.accounts);
      if (!accounts.length) {
        const fallback = await rpcClient('ai.codex.accounts.list', { provider_id: providerId }, { timeoutMs: 30000 });
        accounts = list(fallback?.accounts);
      }
      if (token !== codexLoadToken || destroyed) return;
      state.codexAccounts.set(providerId, accounts);
    } catch (error) {
      if (token !== codexLoadToken || destroyed) return;
      state.codexAccounts.set(providerId, []);
      const status = find('providers-codex-status');
      if (providerId === state.selectedProviderId && status) status.textContent = error.message || 'Account status unavailable';
    } finally {
      state.codexLoading.delete(providerId);
      const current = selectedProvider();
      if (current?.id === providerId && String(current.kind).toLowerCase() === 'codex') renderCodexAccounts(current);
    }
  }

  function renderProviderDetail() {
    const empty = find('providers-empty');
    const detail = find('providers-detail');
    const provider = selectedProvider();
    if (!empty || !detail) return;
    empty.hidden = Boolean(provider);
    detail.hidden = !provider;
    if (!provider) {
      detail.removeAttribute('data-kind');
      return;
    }
    detail.dataset.kind = String(provider.kind || '').toLowerCase();

    const mark = find('providers-detail-mark');
    if (mark) {
      mark.className = `providers-detail-mark providers-mark-${providerAccent(provider)}`;
      mark.textContent = providerMark(provider);
    }
    setText('providers-detail-protocol', providerProtocolHeading(provider));
    setText('providers-detail-title', text(provider.name, provider.id));
    setText('providers-detail-id', text(provider.id));
    const probe = providerProbeState(provider);
    renderStatusBadge(find('providers-detail-status'), provider);
    setText('providers-detail-latency', probe.latency_ms ? `${probe.latency_ms} ms` : '');
    setText('providers-detail-model-count', `${providerModelsCount(provider)} models`);
    setText('providers-models-summary', `${providerModelsCount(provider)} imported`);
    renderConnection(provider);
    renderModelList(provider);

    const note = find('providers-detail-note');
    if (note) {
      const noteText = provider.error || (provider.enabled === false ? 'Disabled connections are kept in the registry but are not selected for new requests.' : '');
      note.textContent = noteText;
      note.hidden = !noteText;
    }
    const codexCard = find('providers-codex-card');
    const isCodex = String(provider.kind || '').toLowerCase() === 'codex';
    if (codexCard) codexCard.hidden = !isCodex;
    const runtimeCard = ensureCodexRuntimeCard();
    if (runtimeCard) runtimeCard.hidden = !isCodex;
    if (deviceLogin && deviceLogin.providerId !== provider.id) cancelDeviceLogin();
    if (isCodex) {
      renderCodexAccounts(provider);
      if (!state.codexAccounts.has(provider.id) && !state.codexLoading.has(provider.id)) void loadCodexAccounts(provider.id);
      renderCodexRuntime();
      if (!state.codexRuntimeLoaded && !state.codexRuntimeLoading) void loadCodexRuntime();
    }
  }

  function reconcileProviderSelection() {
    if (state.selectedProviderId && !visibleProviders().some((provider) => provider.id === state.selectedProviderId)) state.selectedProviderId = null;
    if (!state.selectedProviderId && visibleProviders().length) state.selectedProviderId = visibleProviders()[0].id;
  }

  function selectProvider(id, { focus = false } = {}) {
    if (!state.providers.some((provider) => provider.id === id)) return;
    state.selectedProviderId = id;
    const paneToken = setMobilePane('providers', 'detail');
    renderProviderList();
    renderProviderDetail();
    focusMobileControl('providers-mobile-back', paneToken);
    if (focus) rowFor('providers-list', 'providerId', id)?.querySelector('.providers-list-row')?.focus();
  }

  function patchProviderStatus(id, probe) {
    const provider = state.providers.find((candidate) => candidate.id === id);
    if (!provider) return;
    const item = rowFor('providers-list', 'providerId', id);
    if (item) renderProviderRowStatus(item, provider);
    if (id !== state.selectedProviderId) return;
    const detailCopy = probe?.status === 'ready' && probe?.latency_ms
      ? `Connected · ${probe.latency_ms} ms`
      : probe?.status === 'error' && probe?.error
        ? 'Unavailable'
        : null;
    renderStatusBadge(find('providers-detail-status'), provider, detailCopy);
    setText('providers-detail-latency', probe?.latency_ms ? `${probe.latency_ms} ms` : '');
  }

  async function runProviderTest() {
    const provider = selectedProvider();
    const button = find('providers-test');
    if (!provider || !button || state.providerAction.get(provider.id) === 'testing') return;
    state.providerAction.set(provider.id, 'testing');
    button.disabled = true;
    const previous = button.textContent;
    button.textContent = 'Testing…';
    state.providerProbe.set(provider.id, { status: 'testing' });
    patchProviderStatus(provider.id, state.providerProbe.get(provider.id));
    try {
      const result = await rpcClient('ai.providers.test', { id: provider.id }, { timeoutMs: String(provider.kind).toLowerCase() === 'codex' ? 90000 : 30000 });
      const probe = { status: result?.ok === false ? 'error' : 'ready', latency_ms: Number(result?.latency_ms) || 0, error: result?.error || '' };
      if (probe.status === 'ready') provider.error = '';
      state.providerProbe.set(provider.id, probe);
      patchProviderStatus(provider.id, probe);
      if (probe.status === 'error') toast(probe.error || 'Provider test failed', 'error');
      else toast(`${text(provider.name, 'Provider')} connected`, 'success');
    } catch (error) {
      const probe = { status: 'error', error: error.message || 'Provider test failed' };
      state.providerProbe.set(provider.id, probe);
      patchProviderStatus(provider.id, probe);
      toast(probe.error, 'error');
    } finally {
      state.providerAction.delete(provider.id);
      if (button.isConnected && selectedProvider()?.id === provider.id) {
        button.disabled = false;
        button.textContent = previous;
      }
    }
  }

  async function importProviderModels() {
    const provider = selectedProvider();
    const button = find('providers-import');
    if (!provider || !button || state.providerAction.get(provider.id) === 'importing') return;
    state.providerAction.set(provider.id, 'importing');
    button.disabled = true;
    const previous = button.textContent;
    button.textContent = 'Importing…';
    try {
      const result = await rpcClient('ai.providers.import-models', { id: provider.id }, { timeoutMs: String(provider.kind).toLowerCase() === 'codex' ? 90000 : 30000 });
      const models = list(result?.models);
      provider.models = models;
      renderProviderRowStatus(rowFor('providers-list', 'providerId', provider.id), provider);
      if (selectedProvider()?.id === provider.id) {
        setText('providers-detail-model-count', `${models.length} models`);
        renderConnection(provider);
        renderModelList(provider);
        setText('providers-models-summary', `${models.length} imported`);
      }
      toast(`${models.length} model${models.length === 1 ? '' : 's'} imported`, 'success');
    } catch (error) {
      toast(error.message || 'Could not import models', 'error');
    } finally {
      state.providerAction.delete(provider.id);
      if (button.isConnected && selectedProvider()?.id === provider.id) {
        button.disabled = false;
        button.textContent = previous;
      }
    }
  }

  async function saveProviderEnabled(provider, enabled) {
    const input = find('providers-enabled');
    if (!input || state.providerAction.get(provider.id) === 'saving') return;
    const before = provider.enabled !== false;
    state.providerAction.set(provider.id, 'saving');
    input.disabled = true;
    try {
      await rpcClient('ai.providers.save', providerSavePayload(provider, { enabled }), { timeoutMs: 30000 });
      provider.enabled = enabled;
      state.providerProbe.delete(provider.id);
      renderProviderRowStatus(rowFor('providers-list', 'providerId', provider.id), provider);
      renderProviderDetail();
      toast(enabled ? 'Provider enabled' : 'Provider disabled', 'success');
    } catch (error) {
      input.checked = before;
      toast(error.message || 'Could not save provider state', 'error');
    } finally {
      state.providerAction.delete(provider.id);
      if (input.isConnected) input.disabled = false;
    }
  }

  async function saveProviderPreference(provider, fields) {
    if (state.providerAction.get(provider.id) === 'saving') return;
    if (fields.cache_ttl !== undefined && fields.cache_ttl === effectiveCacheTTL(provider)) return;
    if (fields.reasoning_summary !== undefined && fields.reasoning_summary === effectiveReasoningSummary(provider)) return;
    state.providerAction.set(provider.id, 'saving');
    renderConnection(provider);
    try {
      await rpcClient('ai.providers.save', providerSavePayload(provider, fields), { timeoutMs: 30000 });
      Object.assign(provider, fields);
      renderProviderDetail();
      if (fields.cache_ttl !== undefined) toast(fields.cache_ttl === 'off' ? 'Prompt cache off' : `Cache TTL ${fields.cache_ttl}`, 'success');
      else if (fields.reasoning_summary !== undefined) toast(fields.reasoning_summary === 'none' ? 'Codex thinking summary hidden' : `Codex thinking summary ${fields.reasoning_summary}`, 'success');
    } catch (error) {
      toast(error.message || 'Could not save provider preference', 'error');
      renderConnection(provider);
    } finally {
      state.providerAction.delete(provider.id);
      if (selectedProvider()?.id === provider.id) renderConnection(provider);
    }
  }

  function wireProviderEnabled(provider) {
    const input = find('providers-enabled');
    if (!input) return;
    input.onchange = () => { void saveProviderEnabled(provider, input.checked); };
  }

  async function openProviderDialog(provider = null) {
    const returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const protocol = providerProtocol(provider);
    let suggestedBaseURL = protocol.baseURL;
    let baseURLCustomized = Boolean(provider?.base_url && provider.base_url !== protocol.baseURL);
    const result = await dialog({
      title: provider ? 'Edit provider' : 'Add provider',
      message: 'Only configured connections are listed. API keys are stored by the backend and never returned to this form.',
      fields: [
        { name: 'protocol', label: 'Protocol', tag: 'select', value: protocol.value, options: PROVIDER_PROTOCOLS.map((item) => ({ value: item.value, label: item.label })), onChange: (input, values) => {
          const next = providerProtocolByValue(input.value);
          if (!baseURLCustomized && values.base_url) values.base_url.value = next.baseURL;
          suggestedBaseURL = next.baseURL;
        } },
        { name: 'name', label: 'Name', value: provider?.name || '', placeholder: 'e.g. Personal OpenAI' },
        { name: 'base_url', label: 'Base URL', value: provider?.base_url || protocol.baseURL, placeholder: 'https://api.example.com/v1', onChange: (input) => {
          if (input.value !== suggestedBaseURL) baseURLCustomized = true;
        } },
        { name: 'api_key', label: 'API key', type: 'password', value: '', placeholder: provider?.has_api_key ? 'Leave blank to keep stored key' : 'Optional for OAuth / local gateways' },
      ],
      actions: [
        { label: 'Cancel', value: null },
        { label: provider ? 'Save changes' : 'Save provider', value: 'save', primary: true },
      ],
    });
    if (returnFocus?.isConnected && !root.hidden) returnFocus.focus();
    if (result.value !== 'save') return;
    const fields = result.fields || {};
    const selectedProtocol = providerProtocolByValue(fields.protocol);
    const name = String(fields.name || '').trim();
    const baseURL = String(fields.base_url || '').trim();
    if (!name) return toast('Provider name is required', 'error');
    if (!/^https?:\/\//i.test(baseURL)) return toast('Base URL must start with http:// or https://', 'error');
    try {
      const saved = await rpcClient('ai.providers.save', providerSavePayload(provider, {
        kind: selectedProtocol.kind,
        name,
        base_url: baseURL,
        api_key: fields.api_key,
        enabled: provider?.enabled !== false,
      }), { timeoutMs: 30000 });
      toast(provider ? 'Provider updated' : 'Provider saved', 'success');
      await refreshProviders({ preferredId: saved?.providers?.[0]?.id || provider?.id });
    } catch (error) {
      toast(error.message || 'Could not save provider', 'error');
    }
  }

  async function deleteProvider() {
    const provider = selectedProvider();
    if (!provider) return;
    if (!await confirmDialog('Delete provider', `“${text(provider.name, provider.id)}” and its stored credentials will be removed.`, 'Delete')) {
      find('providers-delete')?.focus();
      return;
    }
    try {
      await rpcClient('ai.providers.delete', { id: provider.id }, { timeoutMs: 30000 });
      state.selectedProviderId = null;
      toast('Provider deleted', 'success');
      await refreshProviders();
      find('providers-search')?.focus();
    } catch (error) {
      toast(error.message || 'Could not delete provider', 'error');
    }
  }

  function renderAcpRow(agent) {
    const item = el('div', { class: 'providers-list-item', role: 'listitem', dataset: { acpId: agent.id } });
    const selected = agent.id === state.selectedAcpId;
    const row = el('button', {
      class: `providers-list-row providers-accent-acp${selected ? ' is-selected' : ''}`,
      type: 'button',
      dataset: { acpId: agent.id },
      'aria-label': `Open ${text(agent.name, agent.id)} details`,
      'aria-current': selected ? 'true' : undefined,
    });
    const statusTone = acpAgentStatusTone(agent);
    row.append(
      el('span', { class: 'providers-row-mark providers-mark-acp', text: 'ACP', 'aria-hidden': 'true' }),
      el('span', { class: 'providers-row-copy' }, [
        el('span', { class: 'providers-row-name' }, [
          el('strong', { text: text(agent.name, agent.id) }),
          el('span', { class: 'providers-row-kind', text: acpTransport(agent) }),
        ]),
        el('span', { class: 'providers-row-id', text: text(agent.id) }),
        el('span', { class: 'providers-row-meta' }, [
          el('span', { text: text(agent.command) }),
          el('span', { text: `${acpAgentModeCount(agent)} modes · ${acpAgentModelCount(agent)} models` }),
        ]),
      ]),
      el('span', { class: `providers-row-status providers-status-${agent.enabled === false ? 'disabled' : statusTone}`, text: agent.enabled === false ? 'Disabled' : acpAgentStatusLabel(agent), dataset: { acpField: 'status' } }),
    );
    row.addEventListener('click', () => selectAcp(agent.id));
    item.append(row);
    return item;
  }

  function renderAcpList() {
    const listNode = find('acp-list');
    if (!listNode) return;
    const visible = visibleAcp();
    listNode.setAttribute('aria-busy', String(state.acpLoading));
    listNode.replaceChildren();
    renderToolbar();
    if (state.acpLoading && !state.acp.length) {
      listNode.append(emptyList({ title: 'Loading ACP agents…', body: 'Reading registered workers.' }));
      return;
    }
    if (state.acpError && !state.acp.length) {
      listNode.append(emptyList(
        { title: 'ACP registry unavailable', body: state.acpError.message || 'Could not read registered workers.' },
        { label: 'Retry', onClick: () => { void refreshAcp(); } },
        true,
      ));
      return;
    }
    if (!visible.length) {
      listNode.append(emptyList(
        state.acpQuery
          ? { title: 'No matching ACP agents', body: 'Try another search or clear the filter.' }
          : { title: 'No ACP agents registered', body: 'Add a local or remote worker when the parent agent needs delegation.' },
        state.acpQuery ? { label: 'Clear search', onClick: () => { state.acpQuery = ''; const input = find('acp-search'); if (input) input.value = ''; reconcileAcpSelection(); renderAcpList(); renderAcpDetail(); } } : { label: 'Add ACP agent', onClick: () => { void openAcpDialog(); } },
      ));
      return;
    }
    for (const agent of visible) listNode.append(renderAcpRow(agent));
  }

  function acpConnectionRows(agent) {
    const remote = acpTransport(agent) === 'remote';
    const rows = [
      ['Transport', acpTransport(agent)],
      [remote ? 'Endpoint' : 'Command', text(agent.command)],
    ];
    if (!remote && list(agent.args).length) rows.push(['Arguments', agent.args.join(' ')]);
    if (list(agent.env_keys).length) rows.push(['Environment', `${agent.env_keys.length} stored key${agent.env_keys.length === 1 ? '' : 's'} · values hidden`]);
    if (agent.default_workspace) rows.push(['Workspace', agent.default_workspace]);
    if (agent.updated_at) rows.push(['Updated', agent.updated_at]);
    return rows;
  }

  function renderAcpConnection(agent) {
    const listNode = find('acp-connection-list');
    if (!listNode) return;
    listNode.replaceChildren();
    for (const [label, value] of acpConnectionRows(agent)) listNode.append(el('dt', { text: label }), el('dd', { text: value }));
    const enabled = find('acp-enabled');
    if (enabled) {
      enabled.checked = agent.enabled !== false;
      enabled.disabled = false;
    }
  }

  function modeRiskMap(agent) {
    return new Map(list(agent.mode_risk_mappings).map((entry) => [entry.mode_id, entry.tier]));
  }

  function renderAcpAuth(agent) {
    const card = find('acp-auth-card');
    const listNode = find('acp-auth-list');
    const methods = acpAuthMethods(agent);
    if (!card || !listNode) return;
    card.hidden = !methods.length;
    listNode.replaceChildren();
    if (!methods.length) return;
    setText('acp-auth-summary', agent.auth_method_id ? `Selected: ${agent.auth_method_id}` : 'Choose a method advertised by this worker.');
    for (const method of methods) {
      const button = el('button', { class: `providers-chip${agent.auth_method_id === method.id ? ' is-selected' : ''}`, type: 'button', text: text(method.name, method.id), title: method.description || method.id });
      button.addEventListener('click', () => { void authenticateAcp(agent, method); });
      listNode.append(button);
    }
  }

  async function saveAcp(agent, extra = {}, { refresh = true } = {}) {
    const payload = {
      id: agent?.id || undefined,
      name: agent?.name || '',
      command: agent?.command || '',
      args: list(agent?.args),
      transport: acpTransport(agent),
      enabled: agent?.enabled !== false,
      preferred_model_id: agent?.preferred_model_id || '',
      preferred_mode_id: agent?.preferred_mode_id || '',
      mode_risk_mappings: list(agent?.mode_risk_mappings),
      default_workspace: agent?.default_workspace || '',
      ...extra,
    };
    const result = await rpcClient('acp.agents.save', payload, { timeoutMs: 30000 });
    const returned = list(result?.agents)[0];
    if (returned?.id) mergeAcp(returned);
    if (refresh) {
      renderAcpList();
      renderAcpDetail();
    }
    return result;
  }

  function renderAcpModes(agent) {
    const card = find('acp-modes-card');
    const listNode = find('acp-modes-list');
    const modes = acpModes(agent);
    if (!card || !listNode) return;
    card.hidden = !modes.length;
    listNode.replaceChildren();
    if (!modes.length) return;
    const mappings = modeRiskMap(agent);
    for (const mode of modes) {
      const currentRisk = mappings.get(mode.id) || mode.risk_tier || 'read_only';
      const risk = el('div', { class: 'providers-risk-segment', role: 'group', 'aria-label': `${text(mode.name, mode.id)} risk` });
      for (const [value, label] of ACP_RISK_OPTIONS) {
        const button = el('button', { class: `providers-risk-option${currentRisk === value ? ' is-selected' : ''}`, type: 'button', text: label });
        button.addEventListener('click', () => { void updateAcpRisk(agent, mode.id, value); });
        risk.append(button);
      }
      const preferred = el('button', { class: `providers-inline-action${agent.preferred_mode_id === mode.id ? ' is-selected' : ''}`, type: 'button', text: agent.preferred_mode_id === mode.id ? 'Default' : 'Set default' });
      preferred.addEventListener('click', () => { void updateAcpPreference(agent, { preferred_mode_id: mode.id }); });
      listNode.append(el('article', { class: 'providers-mode-item', role: 'listitem' }, [
        el('div', { class: 'providers-mode-copy' }, [el('strong', { text: text(mode.name, mode.id) }), el('span', { text: text(mode.description, mode.id) })]),
        risk,
        preferred,
      ]));
    }
  }

  function renderAcpModels(agent) {
    const card = find('acp-models-card');
    const listNode = find('acp-models-list');
    const all = acpModels(agent);
    if (!card || !listNode) return;
    card.hidden = !all.length;
    listNode.replaceChildren();
    if (!all.length) return;
    setText('acp-models-summary', `${all.length} advertised`);
    const models = filterAcpModels(all, state.acpModelQuery);
    if (!models.length) {
      listNode.append(el('p', { class: 'providers-list-empty-copy', role: 'status', text: 'No model matches that filter.' }));
      return;
    }
    for (const model of models) {
      const preferred = agent.preferred_model_id === model.id;
      const button = el('button', { class: `providers-inline-action${preferred ? ' is-selected' : ''}`, type: 'button', text: preferred ? 'Preferred' : 'Prefer' });
      button.addEventListener('click', () => { void updateAcpPreference(agent, { preferred_model_id: model.id }); });
      listNode.append(el('article', { class: 'providers-model-item', role: 'listitem' }, [
        el('div', { class: 'providers-model-copy' }, [el('strong', { text: text(model.name, model.id) }), el('span', { text: text(model.description, model.id) })]),
        el('span', { class: 'providers-model-badges' }, model.tier ? [el('span', { class: 'providers-model-badge', text: model.tier })] : []),
        button,
      ]));
    }
  }

  function renderAcpDetail() {
    const empty = find('acp-empty');
    const detail = find('acp-detail');
    const agent = selectedAcp();
    if (!empty || !detail) return;
    empty.hidden = Boolean(agent);
    detail.hidden = !agent;
    if (!agent) return;
    setText('acp-detail-transport', acpTransport(agent));
    setText('acp-detail-title', text(agent.name, agent.id));
    setText('acp-detail-id', text(agent.id));
    const status = find('acp-detail-status');
    if (status) {
      const tone = agent.enabled === false ? 'disabled' : acpAgentStatusTone(agent);
      status.className = `providers-status-badge providers-status-${tone}`;
      status.textContent = agent.enabled === false ? 'Disabled' : acpAgentStatusLabel(agent);
    }
    setText('acp-detail-mode-count', `${acpAgentModeCount(agent)} modes`);
    setText('acp-detail-model-count', `${acpAgentModelCount(agent)} models`);
    renderAcpConnection(agent);
    renderAcpAuth(agent);
    renderAcpModes(agent);
    renderAcpModels(agent);
    const capabilities = agent.capabilities || agent.cached_capabilities || {};
    setText('acp-detail-note', [
      capabilities.has_mcp ? 'MCP' : '',
      capabilities.has_fs ? 'filesystem' : '',
      capabilities.load_session ? 'session reload' : '',
    ].filter(Boolean).join(' · ') || 'Probe the worker to discover capabilities, modes, models, and authentication methods.');
  }

  function reconcileAcpSelection() {
    if (state.selectedAcpId && !visibleAcp().some((agent) => agent.id === state.selectedAcpId)) state.selectedAcpId = null;
    if (!state.selectedAcpId && visibleAcp().length) state.selectedAcpId = visibleAcp()[0].id;
  }

  function selectAcp(id, { focus = false } = {}) {
    if (!state.acp.some((agent) => agent.id === id)) return;
    state.selectedAcpId = id;
    const paneToken = setMobilePane('acp', 'detail');
    renderAcpList();
    renderAcpDetail();
    focusMobileControl('acp-mobile-back', paneToken);
    if (focus) rowFor('acp-list', 'acpId', id)?.querySelector('.providers-list-row')?.focus();
  }

  function mergeAcp(agent) {
    const index = state.acp.findIndex((candidate) => candidate.id === agent.id);
    if (index < 0) state.acp = [...state.acp, agent];
    else state.acp[index] = { ...state.acp[index], ...agent };
  }

  async function updateAcpEnabled(agent, enabled) {
    const input = find('acp-enabled');
    const before = agent.enabled !== false;
    if (!input) return;
    input.disabled = true;
    try {
      await saveAcp(agent, { enabled }, { refresh: false });
      agent.enabled = enabled;
      renderAcpList();
      renderAcpDetail();
      toast(enabled ? 'ACP agent enabled' : 'ACP agent disabled', 'success');
    } catch (error) {
      input.checked = before;
      toast(error.message || 'Could not save ACP state', 'error');
    } finally {
      if (input.isConnected) input.disabled = false;
    }
  }

  async function updateAcpRisk(agent, modeId, tier) {
    const mappings = modeRiskMap(agent);
    mappings.set(modeId, tier);
    try {
      await saveAcp(agent, { mode_risk_mappings: [...mappings.entries()].map(([id, value]) => ({ mode_id: id, tier: value })) }, { refresh: false });
      agent.mode_risk_mappings = [...mappings.entries()].map(([id, value]) => ({ mode_id: id, tier: value }));
      renderAcpDetail();
      toast('Risk mapping saved', 'success');
    } catch (error) {
      toast(error.message || 'Could not save risk mapping', 'error');
    }
  }

  async function updateAcpPreference(agent, fields) {
    try {
      await saveAcp(agent, fields, { refresh: false });
      Object.assign(agent, fields);
      renderAcpList();
      renderAcpDetail();
      toast('ACP preference saved', 'success');
    } catch (error) {
      toast(error.message || 'Could not save ACP preference', 'error');
    }
  }

  async function authenticateAcp(agent, method) {
    const key = `${agent.id}:auth`;
    if (state.acpAction.get(key)) return;
    state.acpAction.set(key, true);
    try {
      const result = await rpcClient('acp.agents.authenticate', { id: agent.id, method_id: method.id }, { timeoutMs: 60000 });
      if (result?.agent?.id) mergeAcp(result.agent);
      else agent.auth_method_id = method.id;
      renderAcpList();
      renderAcpDetail();
      toast(`Authenticated with ${text(method.name, method.id)}`, 'success');
    } catch (error) {
      toast(error.message || 'ACP authentication failed', 'error');
    } finally {
      state.acpAction.delete(key);
    }
  }

  async function probeAcp() {
    const agent = selectedAcp();
    if (!agent) return;
    const button = find('acp-probe');
    if (button) { button.disabled = true; button.textContent = 'Probing…'; }
    try {
      const result = await rpcClient('acp.agents.probe', { id: agent.id }, { timeoutMs: 30000 });
      if (result?.agent?.id) mergeAcp(result.agent);
      renderAcpList();
      renderAcpDetail();
      toast(result?.ok === false ? (result.error || 'Probe failed') : 'ACP probe succeeded', result?.ok === false ? 'error' : 'success');
    } catch (error) {
      toast(error.message || 'ACP probe failed', 'error');
    } finally {
      if (button?.isConnected) { button.disabled = false; button.textContent = 'Probe'; }
    }
  }

  async function refreshAcpCatalog() {
    const agent = selectedAcp();
    if (!agent) return;
    const button = find('acp-refresh-catalog');
    if (button) { button.disabled = true; button.textContent = 'Refreshing…'; }
    try {
      const result = await rpcClient('acp.agents.refresh-catalog', { id: agent.id }, { timeoutMs: 30000 });
      if (result?.agent?.id) mergeAcp(result.agent);
      renderAcpList();
      renderAcpDetail();
      toast('ACP catalog refreshed', 'success');
    } catch (error) {
      toast(error.message || 'Could not refresh ACP catalog', 'error');
    } finally {
      if (button?.isConnected) { button.disabled = false; button.textContent = 'Refresh catalog'; }
    }
  }

  async function deleteAcp() {
    const agent = selectedAcp();
    if (!agent) return;
    if (!await confirmDialog('Delete ACP agent', `“${text(agent.name, agent.id)}” will be removed from the worker registry.`, 'Delete')) {
      find('acp-delete')?.focus();
      return;
    }
    try {
      await rpcClient('acp.agents.delete', { id: agent.id }, { timeoutMs: 30000 });
      state.selectedAcpId = null;
      toast('ACP agent deleted', 'success');
      await refreshAcp();
      find('acp-search')?.focus();
    } catch (error) {
      toast(error.message || 'Could not delete ACP agent', 'error');
    }
  }

  async function openAcpDialog(agent = null) {
    const returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const result = await dialog({
      title: agent ? 'Edit ACP agent' : 'Add ACP agent',
      message: agent ? 'The registered command is immutable. Update the label, transport details, args, or workspace.' : 'Register a spawn-only worker. Probe it after saving to check connectivity and auth methods; Refresh catalog discovers modes and models.',
      fields: [
        { name: 'name', label: 'Name', value: agent?.name || '', placeholder: 'e.g. Cursor CLI' },
        { name: 'transport', label: 'Transport', tag: 'select', value: acpTransport(agent), options: ACP_TRANSPORT_OPTIONS, onChange: (input, values) => {
          const remote = input.value === 'remote';
          const args = values.args?.closest('label');
          if (args) args.hidden = remote;
          if (values.command) values.command.placeholder = remote ? 'https://localhost:9000/acp' : 'e.g. claude-code-acp';
        } },
        { name: 'command', label: 'Command / endpoint', value: agent?.command || '', placeholder: acpTransport(agent) === 'remote' ? 'https://localhost:9000/acp' : 'e.g. claude-code-acp', disabled: Boolean(agent) },
        { name: 'args', label: 'Arguments', tag: 'textarea', value: list(agent?.args).join(' '), placeholder: 'agent acp' },
        { name: 'workspace', label: 'Default workspace', value: agent?.default_workspace || '', placeholder: 'Optional absolute path' },
        ...(!agent ? [{ name: 'env', label: 'Environment', tag: 'textarea', value: '', placeholder: 'KEY=VALUE per line' }] : []),
      ],
      actions: [
        { label: 'Cancel', value: null },
        { label: agent ? 'Save changes' : 'Register agent', value: 'save', primary: true },
      ],
    });
    if (returnFocus?.isConnected && !root.hidden) returnFocus.focus();
    if (result.value !== 'save') return;
    const fields = result.fields || {};
    const name = String(fields.name || '').trim();
    const transport = fields.transport === 'remote' ? 'remote' : 'stdio';
    const command = String(fields.command || '').trim();
    if (!name || (!agent && !command)) return toast('Name and command are required', 'error');
    if (transport === 'remote' && !/^https?:\/\//i.test(command)) return toast('Remote ACP endpoint must use http:// or https://', 'error');
    try {
      const payload = {
        id: agent?.id || undefined,
        name,
        command: agent?.command || command,
        transport,
        args: transport === 'stdio' ? String(fields.args || '').split(/\s+/).filter(Boolean) : [],
        enabled: agent?.enabled !== false,
        preferred_model_id: agent?.preferred_model_id || '',
        preferred_mode_id: agent?.preferred_mode_id || '',
        mode_risk_mappings: list(agent?.mode_risk_mappings),
        default_workspace: String(fields.workspace || '').trim(),
      };
      if (!agent && String(fields.env || '').trim()) payload.env = parseKeyValueLines(fields.env);
      const saved = await rpcClient('acp.agents.save', payload, { timeoutMs: 30000 });
      const returned = list(saved?.agents)[0];
      if (returned?.id) state.selectedAcpId = returned.id;
      toast(agent ? 'ACP agent updated' : 'ACP agent registered', 'success');
      await refreshAcp();
    } catch (error) {
      toast(error.message || 'Could not save ACP agent', 'error');
    }
  }

  async function switchCodexAccount(provider, account) {
    try {
      await rpcClient('ai.codex.accounts.switch', { provider_id: provider.id, account_id: account.account_id }, { timeoutMs: 30000 });
      toast('Codex account switched', 'success');
      await loadCodexAccounts(provider.id);
    } catch (error) {
      toast(error.message || 'Could not switch Codex account', 'error');
    }
  }

  async function removeCodexAccount(provider, account, trigger = null) {
    if (!await confirmDialog('Remove ChatGPT account', `${text(account.email, account.account_id)} will be removed from this Codex provider.`, 'Remove')) {
      if (trigger?.isConnected) trigger.focus();
      else find('providers-codex-refresh')?.focus();
      return;
    }
    try {
      await rpcClient('ai.codex.logout', { provider_id: provider.id, account_id: account.account_id }, { timeoutMs: 30000 });
      toast('ChatGPT account removed', 'success');
      await loadCodexAccounts(provider.id);
      find('providers-codex-refresh')?.focus();
    } catch (error) {
      toast(error.message || 'Could not remove account', 'error');
    }
  }

  function cancelDeviceLogin() {
    clearTimeout(deviceLoginTimer);
    deviceLogin = null;
    const panel = find('providers-device-login');
    if (panel) { panel.hidden = true; panel.replaceChildren(); }
    const button = find('providers-codex-login');
    if (button) { button.disabled = false; button.textContent = 'Sign in'; }
  }

  async function startDeviceLogin(provider) {
    if (deviceLogin) return;
    const login = { providerId: provider.id };
    deviceLogin = login;
    const button = find('providers-codex-login');
    const panel = find('providers-device-login');
    if (!panel) { cancelDeviceLogin(); return; }
    button.disabled = true;
    button.textContent = 'Getting code…';
    panel.hidden = false;
    const status = el('p', { role: 'status', text: 'Getting your sign-in code…' });
    const cancel = el('button', { class: 'providers-action', type: 'button', text: 'Cancel sign in', onclick: cancelDeviceLogin });
    panel.replaceChildren(status, cancel);
    try {
      const code = await rpcClient('ai.codex.device.start', { provider_id: provider.id }, { timeoutMs: 35000 });
      if (deviceLogin !== login || destroyed) return;
      login.code = code;
      if (!code.device_auth_id || !code.user_code || !code.verification_url || !(code.expires_at * 1000 > Date.now())) throw new Error('No valid sign-in code was returned. Please try again.');
      const copy = el('button', { class: 'providers-action', type: 'button', text: 'Copy code', onclick: async () => {
        try { await window.navigator.clipboard.writeText(code.user_code); copy.textContent = 'Copied'; }
        catch { status.textContent = 'Copy is unavailable. Select the code above and copy it manually.'; }
      } });
      const link = el('a', { class: 'providers-action', href: code.verification_url, target: '_blank', rel: 'noopener', text: 'Open sign-in page' });
      status.textContent = 'Waiting for approval… This code expires in 15 minutes.';
      panel.replaceChildren(
        el('strong', { text: 'Sign in with ChatGPT' }),
        el('p', { text: 'Open the sign-in page, then enter this code. You can use this phone or another device.' }),
        el('code', { class: 'providers-device-code', text: code.user_code, tabindex: '0' }),
        el('div', { class: 'providers-inline-actions' }, [link, copy, cancel]), status,
      );
      button.textContent = 'Waiting for approval…';
      const interval = Math.max(1, Number(code.interval_seconds) || 5) * 1000;
      const poll = async () => {
        if (deviceLogin !== login || destroyed) return;
        if (Date.now() >= code.expires_at * 1000) { fail('This sign-in code has expired. Choose Sign in to get a new code.'); return; }
        try {
          const result = await rpcClient('ai.codex.device.poll', { provider_id: provider.id, device_auth_id: code.device_auth_id, user_code: code.user_code }, { timeoutMs: 35000 });
          if (deviceLogin !== login || destroyed) return;
          if (result.status === 'complete') {
            cancelDeviceLogin();
            toast('ChatGPT account connected', 'success');
            await refreshProviders({ preferredId: provider.id });
            await loadCodexAccounts(provider.id);
          } else if (result.status === 'pending') deviceLoginTimer = setTimeout(poll, interval);
          else fail('The sign-in response was not recognized. Please try again.');
        } catch (error) { if (deviceLogin === login) fail(error.message || 'Could not check sign in. Please try again.'); }
      };
      deviceLoginTimer = setTimeout(poll, interval);
    } catch (error) {
      if (deviceLogin === login) fail(error.message || 'Could not start sign in. Please try again.');
    }
    function fail(message) {
      clearTimeout(deviceLoginTimer);
      deviceLogin = null;
      button.disabled = false;
      button.textContent = 'Sign in';
      status.textContent = message;
      status.setAttribute('role', 'alert');
      panel.replaceChildren(status, cancel);
    }
  }

  async function codexAction(method, label) {
    const provider = selectedProvider();
    if (!provider || String(provider.kind).toLowerCase() !== 'codex') return;
    if (method === 'ai.codex.login') return startDeviceLogin(provider);
    const button = find('providers-codex-import');
    if (button) { button.disabled = true; button.textContent = 'Importing…'; }
    try {
      await rpcClient(method, { provider_id: provider.id }, { timeoutMs: 30000 });
      toast(label, 'success');
      await refreshProviders({ preferredId: provider.id });
      await loadCodexAccounts(provider.id);
    } catch (error) {
      toast(error.message || 'Codex account action failed', 'error');
    } finally {
      if (button?.isConnected) { button.disabled = false; button.textContent = 'Import CLI'; }
    }
  }

  async function refreshCodexCircuits() {
    const provider = selectedProvider();
    const button = find('providers-codex-refresh');
    if (!provider || !button) return;
    button.disabled = true;
    const previous = button.textContent;
    button.textContent = 'Refreshing…';
    try {
      await rpcClient('ai.codex.refresh-circuits', {}, { timeoutMs: 30000 });
      await loadCodexAccounts(provider.id);
      toast('Codex account status refreshed', 'success');
    } catch (error) {
      toast(error.message || 'Could not refresh Codex status', 'error');
    } finally {
      if (button.isConnected) { button.disabled = false; button.textContent = previous; }
    }
  }

  async function refreshProviders({ preferredId = null } = {}) {
    if (providerRefreshPromise) return providerRefreshPromise;
    providerRefreshPromise = refreshProvidersImpl(preferredId);
    try {
      return await providerRefreshPromise;
    } finally {
      providerRefreshPromise = null;
    }
  }

  async function refreshProvidersImpl(preferredId = null) {
    const token = ++providerRefreshToken;
    state.providerLoading = true;
    state.providerError = null;
    setSyncState('providers-sync-state', 'Syncing…');
    renderProviderList();
    try {
      const result = await rpcClient('ai.providers.list', {}, { timeoutMs: 30000 });
      if (token !== providerRefreshToken || destroyed) return;
      // The backend list is authoritative, but an incomplete saved record is
      // not actionable in this surface. It belongs in setup, not in the
      // configured connection index; importantly, no default cards are
      // synthesized here.
      state.providers = list(result?.providers).filter((provider) => provider?.configured !== false);
      state.providerLoading = false;
      if (preferredId && state.providers.some((provider) => provider.id === preferredId)) state.selectedProviderId = preferredId;
      reconcileProviderSelection();
      setSyncState('providers-sync-state', 'Synced', 'ready');
      renderProviderList();
      renderProviderDetail();
    } catch (error) {
      if (token !== providerRefreshToken || destroyed) return;
      state.providerLoading = false;
      state.providerError = error;
      setSyncState('providers-sync-state', 'Sync failed', 'error');
      renderProviderList();
      renderProviderDetail();
    }
  }

  async function refreshAcp() {
    if (acpRefreshPromise) return acpRefreshPromise;
    acpRefreshPromise = refreshAcpImpl();
    try {
      return await acpRefreshPromise;
    } finally {
      acpRefreshPromise = null;
    }
  }

  async function refreshAcpImpl() {
    const token = ++acpRefreshToken;
    state.acpLoading = true;
    state.acpError = null;
    setSyncState('acp-sync-state', 'Syncing…');
    renderAcpList();
    try {
      const result = await rpcClient('acp.agents.list', {}, { timeoutMs: 30000 });
      if (token !== acpRefreshToken || destroyed) return;
      state.acp = list(result?.agents);
      state.acpLoading = false;
      reconcileAcpSelection();
      setSyncState('acp-sync-state', 'Synced', 'ready');
      renderAcpList();
      renderAcpDetail();
    } catch (error) {
      if (token !== acpRefreshToken || destroyed) return;
      state.acpLoading = false;
      state.acpError = error;
      setSyncState('acp-sync-state', 'Sync failed', 'error');
      renderAcpList();
      renderAcpDetail();
    }
  }

  function setTab(tab, { focus = false } = {}) {
    const next = tab === 'acp' ? 'acp' : 'providers';
    state.tab = next;
    root.querySelectorAll('[data-providers-tab]').forEach((button) => {
      const active = button.dataset.providersTab === next;
      button.classList.toggle('is-active', active);
      button.setAttribute('aria-selected', String(active));
      if (focus && active) button.focus();
    });
    root.querySelectorAll('[data-providers-panel]').forEach((panel) => {
      const active = panel.dataset.providersPanel === next;
      panel.hidden = !active;
      panel.classList.toggle('is-active', active);
    });
    if (next === 'providers' && !state.providers.length && !state.providerLoading && !state.providerError) void refreshProviders();
    if (next === 'acp' && !state.acp.length && !state.acpLoading && !state.acpError) void refreshAcp();
  }

  async function refresh() {
    await Promise.all([refreshProviders(), refreshAcp()]);
  }

  function init() {
    if (initialized) return controller;
    initialized = true;
    setMobilePane('providers', 'list');
    setMobilePane('acp', 'list');
    root.querySelectorAll('[data-providers-tab]').forEach((button) => button.addEventListener('click', () => setTab(button.dataset.providersTab)));
    find('providers-mobile-back')?.addEventListener('click', () => {
      const paneToken = setMobilePane('providers', 'list');
      focusMobileRow('providers', paneToken);
    });
    find('acp-mobile-back')?.addEventListener('click', () => {
      const paneToken = setMobilePane('acp', 'list');
      focusMobileRow('acp', paneToken);
    });
    bindTablistKeyboard(find('providers-tabs'));
    find('providers-search')?.addEventListener('input', debounce((event) => {
      state.providerQuery = event.target.value;
      reconcileProviderSelection();
      renderProviderList();
      renderProviderDetail();
    }, 120));
    find('providers-model-search')?.addEventListener('input', debounce((event) => {
      state.modelQuery = event.target.value;
      renderModelList(selectedProvider());
    }, 100));
    find('acp-search')?.addEventListener('input', debounce((event) => {
      state.acpQuery = event.target.value;
      reconcileAcpSelection();
      renderAcpList();
      renderAcpDetail();
    }, 120));
    find('acp-model-search')?.addEventListener('input', debounce((event) => {
      state.acpModelQuery = event.target.value;
      const agent = selectedAcp();
      if (agent) renderAcpModels(agent);
    }, 100));
    find('providers-refresh')?.addEventListener('click', () => { void refreshProviders(); });
    find('acp-refresh')?.addEventListener('click', () => { void refreshAcp(); });
    find('providers-add')?.addEventListener('click', () => { void openProviderDialog(); });
    find('providers-empty-add')?.addEventListener('click', () => { void openProviderDialog(); });
    find('acp-add')?.addEventListener('click', () => { void openAcpDialog(); });
    find('acp-empty-add')?.addEventListener('click', () => { void openAcpDialog(); });
    find('providers-edit')?.addEventListener('click', () => { void openProviderDialog(selectedProvider()); });
    find('providers-test')?.addEventListener('click', () => { void runProviderTest(); });
    find('providers-import')?.addEventListener('click', () => { void importProviderModels(); });
    find('providers-delete')?.addEventListener('click', () => { void deleteProvider(); });
    find('providers-enabled')?.addEventListener('change', () => { const provider = selectedProvider(); if (provider) void saveProviderEnabled(provider, find('providers-enabled').checked); });
    find('providers-codex-login')?.addEventListener('click', () => { void codexAction('ai.codex.login', 'Codex sign-in completed'); });
    find('providers-codex-import')?.addEventListener('click', () => { void codexAction('ai.codex.import', 'Codex CLI account imported'); });
    find('providers-codex-refresh')?.addEventListener('click', () => { void refreshCodexCircuits(); });
    find('acp-edit')?.addEventListener('click', () => { void openAcpDialog(selectedAcp()); });
    find('acp-probe')?.addEventListener('click', () => { void probeAcp(); });
    find('acp-refresh-catalog')?.addEventListener('click', () => { void refreshAcpCatalog(); });
    find('acp-delete')?.addEventListener('click', () => { void deleteAcp(); });
    find('acp-enabled')?.addEventListener('change', () => { const agent = selectedAcp(); if (agent) void updateAcpEnabled(agent, find('acp-enabled').checked); });
    renderProviderList();
    renderProviderDetail();
    renderAcpList();
    renderAcpDetail();
    setTab(state.tab);
    window.lucide?.createIcons?.({ attrs: { 'stroke-width': 1.7 } });
    return controller;
  }

  function closeOverlays() {
    cancelDeviceLogin();
    // Provider dialogs use the shared dialog registry and close automatically
    // when the router dismisses dialogs. This hook keeps the page contract
    // symmetrical with Plugins without inventing a second overlay system.
  }

  function destroy() {
    mobilePaneToken += 1;
    destroyed = true;
    providerRefreshToken += 1;
    acpRefreshToken += 1;
    codexLoadToken += 1;
    clearTimeout(codexRuntimeTimer);
    closeOverlays();
  }

  const controller = {
    init,
    refresh,
    refreshProviders,
    refreshAcp,
    closeOverlays,
    destroy,
    selectProvider,
    selectAcp,
    state,
  };
  return controller;
}
