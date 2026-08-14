// Providers workspace: Messages / Responses / Chat registries, credentials, models.

import { rpc } from '../rpc.js';
import { el, toast, dialog, confirmDialog } from '../ui.js';

let providers = [];
let detailId = null;

const KIND_META = {
  messages: { label: 'Messages', mark: 'MS', cls: 'accent-anthropic', desc: 'Messages API format' },
  responses: { label: 'Responses', mark: 'RS', cls: 'accent-openai', desc: 'Responses API format' },
  chat: { label: 'Chat', mark: 'CH', cls: 'accent-compatible', desc: 'Chat Completions API format' },
};

export async function initProviders() {
  document.getElementById('add-provider-btn').addEventListener('click', () => addProvider());
  await refresh();
}

async function refresh() {
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
  const meta = KIND_META[p.kind] || KIND_META.compatible;
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
      el('div', {}, el('dt', { text: 'Kind' }), el('dd', { text: (KIND_META[p.kind] || KIND_META.compatible).label })),
      el('div', {}, el('dt', { text: 'Base URL' }), el('dd', { text: p.base_url || '—' })),
      el('div', {}, el('dt', { text: 'API key' }), el('dd', { text: p.has_api_key ? '••••••••' : '—' })),
      el('div', {}, el('dt', { text: 'Status' }), el('dd', { text: p.enabled === false ? 'disabled' : 'enabled' })),
    ),
    el('div', { class: 'provider-detail-actions' },
      el('button', { class: 'mini-btn', type: 'button', text: 'Edit' }),
      el('button', { class: 'mini-btn ghost', type: 'button', text: 'Test connection' }),
      el('button', { class: 'mini-btn ghost', type: 'button', text: 'Import models' }),
      el('button', { class: 'mini-btn danger', type: 'button', text: 'Delete' }),
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
    const ok = await confirmDialog('Delete provider', `"${p.name}" and its stored API key will be removed.`, 'Delete');
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
  const initialKind = provider?.kind ?? 'chat';
  const res = await dialog({
    title: provider ? 'Edit provider' : 'Add provider',
    message: provider ? 'Update provider settings.' : 'API keys are stored in the local SQLite credential store.',
    fields: [
      {
        name: 'kind', label: 'Kind', tag: 'select',
        options: [
          { value: 'messages', label: 'Messages' },
          { value: 'responses', label: 'Responses' },
          { value: 'chat', label: 'Chat' },
        ],
        value: initialKind,
        onChange: (kindInput, all) => {
          const urlInput = all.base_url;
          if (!urlInput) return;
          const current = urlInput.value.trim();
          // suggest the new kind's default only while the field is empty or
          // still holds a previous suggestion; never overwrite user input
          const known = Object.values(KIND_DEFAULTS);
          if (current === '' || known.includes(current)) {
            urlInput.value = KIND_DEFAULTS[kindInput.value] ?? '';
          }
          urlInput.placeholder = `API base URL — vendor endpoint or AI gateway (e.g. ${KIND_DEFAULTS[kindInput.value] ?? ''})`;
        },
      },
      { name: 'name', label: 'Name', value: provider?.name ?? '', placeholder: 'e.g. my provider' },
      { name: 'base_url', label: 'Base URL', value: provider?.base_url ?? KIND_DEFAULTS[initialKind] ?? '', placeholder: `API base URL — vendor endpoint or AI gateway (e.g. ${KIND_DEFAULTS[initialKind]})` },
      { name: 'api_key', label: 'API key', value: '', placeholder: provider?.has_api_key ? 'leave blank to keep current key' : 'sk-…' },
    ],
    actions: [
      { label: 'Cancel', value: null },
      { label: 'Save', value: 'save' },
    ],
  });
  if (res.value !== 'save') return;
  const { kind, name, base_url, api_key } = res.fields;
  if (!name.trim()) { toast('Provider name is required', 'error'); return; }
  if (!base_url.trim()) { toast('Base URL is required', 'error'); return; }
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
