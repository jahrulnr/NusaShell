// Route picker: selects the upstream provider that serves the chosen
// model on aggregator gateways (OpenRouter). Always visible next to the
// model picker; the icon communicates capability:
//   - router icon + clickable menu = multi-provider model, "Auto" default
//   - home icon + dashed border = single upstream, non-interactive
// The list is fetched on model selection (RPC ai.models.endpoints),
// cached per (provider, model) in this session, and pinned routes are
// stored by the caller in state + localStorage.
import { debounce, el } from '../../ui.js';
import { rpc } from '../../rpc.js';

const ICONS = {
  router: '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" aria-hidden="true"><path d="M5 18h14M5 18a2 2 0 1 1-2-2 2 2 0 0 1 2 2zm14 0a2 2 0 1 0 2-2 2 2 0 0 0-2 2z" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/><path d="M8.5 15.5 12 5m4 10.5L12 5" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>',
  home: '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" aria-hidden="true"><path d="M3 10.5 12 3l9 7.5" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/><path d="M5.5 9.5V20a1 1 0 0 0 1 1h3.5v-5.5a2 2 0 0 1 4 0V21h3.5a1 1 0 0 0 1-1V9.5" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>',
  spinner: '<svg class="agent-route-spin" viewBox="0 0 24 24" width="13" height="13" fill="none" aria-hidden="true"><path d="M12 3a9 9 0 1 0 9 9" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>',
};

function positionMenu(menu, trigger) {
  const rect = trigger.getBoundingClientRect();
  const menuHeight = menu.offsetHeight || 320;
  const spaceBelow = window.innerHeight - rect.bottom;
  const spaceAbove = rect.top;
  const menuWidth = menu.offsetWidth || 330;
  menu.style.left = `${Math.max(8, Math.min(rect.left, window.innerWidth - menuWidth - 8))}px`;
  menu.style.top = `${spaceBelow < menuHeight + 6 && spaceAbove > spaceBelow
    ? Math.max(8, rect.top - menuHeight - 6)
    : rect.bottom + 6}px`;
}

function formatLatency(ms) {
  if (ms == null) return null;
  return `${Math.round(ms)}ms`;
}

function formatThroughput(tokensPerSec) {
  if (tokensPerSec == null) return null;
  return `${Math.round(tokensPerSec)} tok/s`;
}

function formatRoutePrice(value) {
  if (value == null || value === '') return null;
  const numeric = Number(value);
  return Number.isFinite(numeric) ? String(numeric) : null;
}

function formatRoutePricing(inputCost, outputCost) {
  const parts = [];
  const input = formatRoutePrice(inputCost);
  const output = formatRoutePrice(outputCost);
  if (input != null) parts.push(`$${input}/M in`);
  if (output != null) parts.push(`$${output}/M out`);
  return parts.join(' · ');
}

function routeMatchesQuery(route, query) {
  const needle = String(query ?? '').trim().toLowerCase();
  if (!needle) return true;
  return [route.name, route.slug, route.quantization]
    .some((value) => String(value ?? '').toLowerCase().includes(needle));
}

export function bindRoutePicker({ getModels, getSelectedModel, getSelectedRoute, selectRoute }) {
  const trigger = document.getElementById('route-trigger');
  const menu = document.getElementById('route-menu');
  const iconEl = document.getElementById('route-trigger-icon');
  const labelEl = document.getElementById('route-trigger-label');
  if (!trigger || !menu || !iconEl || !labelEl) return null;

  const sessionCache = new Map(); // `${provider_id}:${model_id}` -> routes
  const state = { routes: null, loading: false, error: null };

  const closeMenu = () => {
    menu.hidden = true;
    trigger.setAttribute('aria-expanded', 'false');
  };

  const setIcon = (kind) => {
    iconEl.innerHTML = ICONS[kind] || '';
  };

  const chosenModel = () => {
    const model = getSelectedModel();
    if (!model) return null;
    return getModels().find((m) => `${m.provider_id}:${m.id}` === model)
      || getModels().find((m) => m.id === model) || null;
  };

  // refresh aligns the trigger with the current model + route state.
  // Async only on the first fetch for a model (cache miss); afterwards it
  // resolves synchronously so switching models is instant.
  async function refresh() {
    const model = chosenModel();
    if (!model) {
      trigger.hidden = true;
      return;
    }
    trigger.hidden = false;
    const route = getSelectedRoute() || '';
    if (!model.route_support) {
      // Direct provider / no routing concept: home icon, non-interactive.
      state.routes = null;
      state.loading = false;
      state.error = null;
      setIcon('home');
      labelEl.textContent = route || 'Auto';
      trigger.classList.add('is-single');
      trigger.title = route ? `Route: ${route}` : 'Provider tunggal — diatur oleh gateway';
      return;
    }
    trigger.classList.remove('is-single');
    const key = `${model.provider_id}:${model.id}`;
    if (sessionCache.has(key)) {
      state.routes = sessionCache.get(key);
      state.loading = false;
    } else if (!state.loading) {
      state.loading = true;
      state.routes = null;
    }
    if (state.loading && state.routes == null) setIcon('spinner');
    labelEl.textContent = route || 'Auto';
    if (state.loading && state.routes == null) {
      try {
        const res = await rpc('ai.models.endpoints', { provider_id: model.provider_id, model_id: model.id });
        const routes = (res && res.routes) || [];
        sessionCache.set(key, routes);
        state.routes = routes;
        state.loading = false;
        state.error = null;
      } catch (error) {
        console.warn('route picker:', error);
        state.loading = false;
        state.routes = [];
        state.error = (error && error.message) || 'fetch failed';
      }
    }
    const routes = state.routes || [];
    if (routes.length) {
      setIcon('router');
      trigger.classList.remove('is-single');
      trigger.title = route ? `Route: ${route} (klik untuk ganti)` : 'Auto — pilih provider upstream';
    } else {
      setIcon('home');
      trigger.classList.add('is-single');
      trigger.title = state.error ? `Gagal memuat daftar provider: ${state.error}` : 'Provider tunggal — diatur oleh gateway';
    }
  }

  const renderMenu = () => {
    menu.innerHTML = '';
    const routes = state.routes || [];
    const route = getSelectedRoute() || '';
    if (!routes.length) {
      menu.append(el('div', { class: 'agent-model-empty', text: state.error
        ? `Gagal memuat daftar provider: ${state.error}`
        : 'Auto — gateway mengelola pemilihan provider.' }));
      return;
    }
    const list = el('div', { class: 'agent-model-list' });
    const search = el('input', {
      type: 'search',
      placeholder: 'Search providers…',
      autocomplete: 'off',
      'aria-label': 'Search providers',
    });
    const searchBar = el('div', { class: 'agent-model-search' }, search);
    const noResults = el('div', {
      class: 'agent-model-empty',
      text: 'No providers match your search.',
      hidden: true,
    });
    const providerRows = [];
    const autoRow = el('div', { class: `agent-model-row${!route ? ' is-selected' : ''}` },
      el('button', { class: 'agent-model-choice', type: 'button' },
        el('span', { class: 'agent-model-name' },
          el('span', { text: 'Auto' }),
          el('span', { class: 'agent-model-id', text: 'load balance · pricing varies' }),
        ),
      ),
    );
    autoRow.querySelector('button').addEventListener('click', () => {
      selectRoute('');
      closeMenu();
      refresh();
    });
    list.append(autoRow);
    for (const r of routes) {
      const badges = [];
      if (r.quantization && r.quantization !== 'unknown') {
        badges.push(el('span', { class: 'agent-model-badge', text: r.quantization, title: 'Quantization' }));
      }
      const lat = formatLatency(r.latency);
      if (lat) badges.push(el('span', { class: 'agent-model-badge', text: lat, title: 'Latency 30m' }));
      const tp = formatThroughput(r.throughput);
      if (tp) badges.push(el('span', { class: 'agent-model-badge', text: tp, title: 'Throughput 30m' }));
      const pricing = formatRoutePricing(r.input_cost, r.output_cost);
      if (pricing) badges.push(el('span', { class: 'agent-model-badge agent-route-cost', text: pricing, title: 'USD per 1M tokens' }));
      const row = el('div', { class: `agent-model-row${route === r.slug ? ' is-selected' : ''}` },
        el('button', { class: 'agent-model-choice', type: 'button' },
          el('span', { class: 'agent-model-name' },
            el('span', { text: r.name }),
            el('span', { class: 'agent-model-id', text: r.slug }),
            badges.length ? el('span', { class: 'agent-model-badges' }, badges) : null,
          ),
        ),
      );
      row.querySelector('button').addEventListener('click', () => {
        selectRoute(r.slug);
        closeMenu();
        refresh();
      });
      providerRows.push({ route: r, row });
      list.append(row);
    }
    search.addEventListener('input', debounce(() => {
      let visibleCount = 0;
      for (const { route: providerRoute, row } of providerRows) {
        const visible = routeMatchesQuery(providerRoute, search.value);
        row.hidden = !visible;
        if (visible) visibleCount++;
      }
      noResults.hidden = !search.value.trim() || visibleCount > 0;
    }, 120));
    menu.append(searchBar, list, noResults);
    if (state.error) {
      menu.append(el('div', { class: 'agent-route-note', text: `Fetch gagal — menampilkan cache lama. ${state.error}` }));
    }
  };

  const openMenu = () => {
    const routes = state.routes || [];
    const route = getSelectedRoute() || '';
    if (!routes.length && !route) return; // single-upstream: non-interactive
    renderMenu();
    menu.hidden = false;
    positionMenu(menu, trigger);
    trigger.setAttribute('aria-expanded', 'true');
    menu.querySelector('input')?.focus();
  };

  trigger.addEventListener('click', () => {
    if (!menu.hidden) {
      closeMenu();
      return;
    }
    openMenu();
  });
  document.addEventListener('mousedown', (event) => {
    if (!menu.hidden && !menu.contains(event.target) && event.target !== trigger) closeMenu();
  });
  window.addEventListener('hashchange', closeMenu);

  return { refresh };
}
