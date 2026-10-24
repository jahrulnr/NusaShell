import { createLazySelect, createSelect } from '../../js/ui.js';
import { formatModelLabel, modelOptionMatches, modelOptions } from './live-data.js?v=6';

const providerLabels = {
  '9router': '9Router',
  anthropic: 'Anthropic',
  codex: 'Codex',
  omniroute: 'OmniRoute',
  openai: 'OpenAI',
  openrouter: 'OpenRouter',
};

const openRouterRoutes = [
  { text: 'Auto route', value: '' },
  { text: 'Anthropic', value: 'anthropic' },
  { text: 'Google Vertex', value: 'google-vertex' },
  { text: 'Amazon Bedrock', value: 'amazon-bedrock' },
];

const codexAccounts = [
  { text: 'Auto rotation', value: '' },
  { text: 'Personal · Plus', value: 'account-personal' },
  { text: 'Work · Team', value: 'account-work' },
];

function modelProvider(model) {
  return String(model || '').split(':', 1)[0];
}

function storedModelPreference() {
  try {
    const stored = localStorage.getItem('nusashell.model');
    return typeof stored === 'string' ? stored.trim() : '';
  } catch {
    return '';
  }
}

export function routeContextForModel(model) {
  const provider = modelProvider(model);
  if (provider === 'openrouter') return { label: 'Upstream', visible: true, options: openRouterRoutes };
  if (provider === 'codex') return { label: 'Account', visible: true, options: codexAccounts };
  return { label: 'Route', visible: false, options: [] };
}

function optionData(option) {
  return {
    text: option.textContent,
    value: option.value,
    selected: option.selected,
  };
}

function selectData(select) {
  return [...select.children].map((child) => {
    if (child.tagName === 'OPTGROUP') {
      return { label: child.label, options: [...child.children].map(optionData) };
    }
    return optionData(child);
  });
}

function insertComposerNewline(composer) {
  const start = composer.selectionStart ?? composer.value.length;
  const end = composer.selectionEnd ?? start;
  composer.value = `${composer.value.slice(0, start)}\n${composer.value.slice(end)}`;
  const cursor = start + 1;
  composer.selectionStart = cursor;
  composer.selectionEnd = cursor;
  const InputEvent = composer.ownerDocument?.defaultView?.Event;
  if (typeof InputEvent === 'function') composer.dispatchEvent(new InputEvent('input', { bubbles: true }));
}

// Ctrl/Cmd+Enter submits the shared send and steer form once. Alt+Enter is a
// newline; preventDefault keeps that key from also activating submit.
export function bindComposerSendKeys(composer, form) {
  composer.addEventListener('keydown', (event) => {
    if (event.isComposing || event.key.toLowerCase() !== 'enter') return;
    if (event.altKey && !(event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      insertComposerNewline(composer);
      return;
    }
    if (!(event.metaKey || event.ctrlKey)) return;
    event.preventDefault();
    form.requestSubmit();
  });
}

export function initComposerSelects(root, { rpc, onRouteChange } = {}) {
  const trigger = root.querySelector('#model-config-trigger');
  const popover = root.querySelector('#model-config-popover');
  const modelSelect = root.querySelector('#model-select');
  const effortSelect = root.querySelector('#effort-select');
  const routeField = root.querySelector('.composer-select-route');
  const routeLabel = root.querySelector('#route-label');
  const modelLabel = root.querySelector('#model-label');
  const modelConfigSummary = root.querySelector('#model-config-summary');
  const effortConfigSummary = root.querySelector('#effort-config-summary');
  const routeSelect = root.querySelector('#route-select');
  let catalog = [];
  let routeGeneration = 0;
  let desiredRoute = '';
  // null so the first setModels call — even with an empty catalog — counts
  // as a change and applies enable/disable.
  let lastCatalogSignature = null;
  let lastSyncedModel;

  const effort = createSelect(effortSelect, {
    data: selectData(effortSelect),
    value: effortSelect.value,
    search: false,
    onChange: (value) => { effortConfigSummary.textContent = value; },
  });
  let routeProgrammatic = false;
  const route = createSelect(routeSelect, {
    data: openRouterRoutes,
    value: '',
    search: false,
    onChange: (value) => {
      // SlimSelect fires afterChange for programmatic writes too — setSelected
      // AND setData (rebuilt options always differ on internal ids), so
      // programmatic room/model syncs must never become set-provider writes.
      if (!routeProgrammatic) onRouteChange?.(value);
    },
  });
  // runRouteProgrammatic marks a SlimSelect mutation as machine-driven; only a
  // real user pick may reach onRouteChange (which persists
  // agent.conversations.set-provider).
  function runRouteProgrammatic(fn) {
    routeProgrammatic = true;
    try {
      fn();
    } finally {
      routeProgrammatic = false;
    }
  }
  function setRouteSelected(value) {
    runRouteProgrammatic(() => route.setSelected([value]));
  }
  function setRouteData(data) {
    runRouteProgrammatic(() => route.setData(data));
  }

  async function syncModel(value) {
    const selectedModel = catalog.find((entry) => `${entry.provider_id}:${entry.id}` === value);
    const provider = selectedModel?.provider_name || modelProvider(value);
    modelLabel.textContent = `Model · ${providerLabels[provider] || provider}`;
    modelConfigSummary.textContent = formatModelLabel(selectedModel || value, catalog);
    const effortOptions = [{ text: 'Auto', value: 'auto' }, ...(selectedModel?.supported_efforts || []).map((item) => ({ text: item[0].toUpperCase() + item.slice(1), value: item }))];
    effort.setData(effortOptions);
    effort.setSelected([selectedModel?.default_effort || 'auto']);

    const generation = ++routeGeneration;
    if (!selectedModel || typeof rpc !== 'function') {
      const context = routeContextForModel(value);
      routeField.hidden = !context.visible;
      if (context.visible) {
        routeLabel.textContent = context.label;
        route.enable();
        setRouteData(context.options);
        setRouteSelected(desiredRoute || '');
      } else {
        route.disable();
        setRouteSelected('');
      }
      return;
    }
    const isCodex = selectedModel.provider_kind === 'codex';
    routeField.hidden = !isCodex && !selectedModel.route_support;
    if (routeField.hidden) {
      route.disable();
      // A hidden picker must not keep sending the previous room's route in
      // getConfig().providerRoute for turns started from this model.
      setRouteSelected('');
      return;
    }
    routeLabel.textContent = isCodex ? 'Account' : 'Upstream';
    route.enable();
    setRouteData([{ text: isCodex ? 'Auto rotation' : 'Auto route', value: '' }]);
    try {
      const result = isCodex
        ? await rpc('ai.codex.accounts.list', { provider_id: selectedModel.provider_id })
        : await rpc('ai.models.endpoints', { provider_id: selectedModel.provider_id, model_id: selectedModel.id });
      if (generation !== routeGeneration) return;
      const options = isCodex
        ? (result.accounts || []).map((account) => ({ text: account.email || account.name || account.account_id, value: account.account_id }))
        : (result.routes || []).map((endpoint) => ({ text: endpoint.name || endpoint.slug, value: endpoint.slug }));
      setRouteData([{ text: isCodex ? 'Auto rotation' : 'Auto route', value: '' }, ...options]);
      setRouteSelected(options.some((option) => option.value === desiredRoute) ? desiredRoute : '');
    } catch {
      if (generation === routeGeneration) setRouteSelected(desiredRoute || '');
    }
  }

  const model = createLazySelect(modelSelect, {
    data: selectData(modelSelect),
    value: modelSelect.value,
    searchFilter: modelOptionMatches,
    onChange: syncModel,
  });

  function setOpen(open) {
    popover.hidden = !open;
    trigger.setAttribute('aria-expanded', String(open));
    if (open) modelSelect.nextElementSibling?.focus();
  }

  trigger.addEventListener('click', () => setOpen(popover.hidden));
  root.ownerDocument.addEventListener('click', (event) => {
    const path = event.composedPath();
    if (popover.hidden || path.includes(trigger) || path.includes(popover)) return;
    if (path.some((node) => node?.classList?.contains('ss-content'))) return;
    setOpen(false);
  });
  root.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape' || popover.hidden || root.ownerDocument.querySelector('.ss-content.ss-open')) return;
    setOpen(false);
    trigger.focus();
  });

  syncModel(model.getSelected()[0]);
  effortConfigSummary.textContent = effort.getSelected()[0];
  return {
    model,
    effort,
    route,
    setModels(models, selected = '') {
      catalog = Array.isArray(models) ? models : [];
      const options = modelOptions(catalog);
      const values = options.flatMap(group => group.options.map(option => option.value));
      // The catalog refreshes on every Agent-view entry; rebuilding the option
      // window when nothing changed is pure DOM churn.
      const signature = values.join('\u0000');
      const catalogChanged = signature !== lastCatalogSignature;
      if (catalogChanged) {
        lastCatalogSignature = signature;
        model.setData(options.length ? options : [{ text: 'No chat models available', value: '', placeholder: true }]);
        if (options.length) model.enable();
        else model.disable();
      }
      const value = [selected, storedModelPreference(), values[0]].find(candidate => values.includes(candidate)) || '';
      if (value !== (model.getSelected()[0] || '')) model.setSelected(value ? [value] : []);
      if (catalogChanged || value !== lastSyncedModel) {
        lastSyncedModel = value;
        void syncModel(value);
      }
      if (!value) modelConfigSummary.textContent = 'No chat models available';
    },
    setConversation(conversation = {}) {
      desiredRoute = conversation.provider_route || '';
      // SlimSelect re-renders the option window even when the selection does
      // not change, so every programmatic sync must first prove it is needed.
      const wanted = conversation.model || (!conversation.id ? storedModelPreference() : '');
      if (wanted && wanted !== (model.getSelected()[0] || '')) model.setSelected([wanted]);
      if (conversation.effort && conversation.effort !== (effort.getSelected()[0] || '')) effort.setSelected([conversation.effort]);
      // Rooms without a saved route fall back to Auto — leaving the previous
      // room's selection visible would silently misroute the next turn.
      if (desiredRoute !== (route.getSelected()[0] || '')) setRouteSelected(desiredRoute);
    },
    getConfig() {
      return {
        model: model.getSelected()[0] || '',
        effort: effort.getSelected()[0] || 'auto',
        providerRoute: route.getSelected()[0] || '',
      };
    },
  };
}
