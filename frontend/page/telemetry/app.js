import { rpc as defaultRPC } from '../../js/rpc.js';
import { el } from '../../js/ui.js';
import {
  formatTelemetryDate,
  formatTelemetryMoney,
  formatTelemetryNumber,
  formatTelemetryPercent,
  formatTokenLabel,
  telemetryHasUsage,
  telemetryReportParts,
} from './model.js';

const CHART_PATH = '../../vendor/chartjs/chart.umd.min.js';
const TOKEN_SERIES = [
  { key: 'input_tokens', label: 'Input', color: 'lagoon' },
  { key: 'output_tokens', label: 'Output', color: 'ocean' },
  { key: 'cache_read_tokens', label: 'Cache read', color: 'sand' },
  { key: 'cache_write_tokens', label: 'Cache write', color: 'mangrove' },
];

let chartLoader = null;

function loadChartLibrary() {
  if (window.Chart) return Promise.resolve(window.Chart);
  if (!chartLoader) {
    chartLoader = new Promise((resolve, reject) => {
      const script = document.createElement('script');
      script.src = new URL(CHART_PATH, import.meta.url).href;
      script.async = true;
      script.dataset.telemetryChart = 'true';
      script.addEventListener('load', () => {
        if (window.Chart) resolve(window.Chart);
        else reject(new Error('Chart.js loaded without exposing its chart API.'));
      }, { once: true });
      script.addEventListener('error', () => reject(new Error('The local chart library could not be loaded.')), { once: true });
      document.head.append(script);
    }).catch((error) => {
      chartLoader = null;
      throw error;
    });
  }
  return chartLoader;
}

function eventListener(listeners, target, type, handler, options) {
  if (!target) return;
  target.addEventListener(type, handler, options);
  listeners.push(() => target.removeEventListener(type, handler, options));
}

export function createTelemetryPage(root = document, { rpcClient = defaultRPC } = {}) {
  const state = {
    report: null,
    catalog: null,
    minutes: 10080,
    filters: { providerID: '', modelID: '' },
    openFilter: '',
    loading: false,
    loaded: false,
    error: null,
    chartError: null,
  };
  let initialized = false;
  let destroyed = false;
  let active = false;
  let reportVersion = 0;
  let chart = null;
  let chartTask = null;
  const listeners = [];
  const find = (id) => root.querySelector(`#${id}`);

  function setSync(copy, tone = 'idle') {
    const node = find('telemetry-sync-state');
    if (!node) return;
    node.textContent = copy;
    node.dataset.state = tone;
  }

  function setError(error) {
    state.error = error || null;
    const panel = find('telemetry-error');
    const message = find('telemetry-error-message');
    if (panel) panel.hidden = !state.error;
    if (message) message.textContent = state.error?.message || (typeof state.error === 'string' ? state.error : '');
  }

  function currentParts() {
    return telemetryReportParts(state.report);
  }

  function providerName(providerID) {
    const provider = telemetryReportParts(state.catalog).providers.find((item) => item.provider_id === providerID);
    return provider?.provider_name || providerID || 'All providers';
  }

  function updateToolbar() {
    root.querySelectorAll('[data-telemetry-range]').forEach((button) => {
      const selected = Number(button.dataset.telemetryRange) === state.minutes;
      button.setAttribute('aria-pressed', String(selected));
      button.disabled = state.loading;
    });
    const providerTrigger = find('telemetry-provider-filter');
    const modelTrigger = find('telemetry-model-filter');
    if (providerTrigger) {
      providerTrigger.querySelector('span').textContent = state.filters.providerID ? providerName(state.filters.providerID) : 'All providers';
      providerTrigger.setAttribute('aria-expanded', String(state.openFilter === 'provider'));
      providerTrigger.disabled = state.loading || !state.catalog;
    }
    if (modelTrigger) {
      modelTrigger.querySelector('span').textContent = state.filters.modelID || 'All models';
      modelTrigger.setAttribute('aria-expanded', String(state.openFilter === 'model'));
      modelTrigger.disabled = state.loading || !state.catalog;
    }
    const refresh = find('telemetry-refresh');
    if (refresh) refresh.disabled = state.loading;
    const overview = root.querySelector('.telemetry-overview');
    if (overview) overview.setAttribute('aria-busy', String(state.loading && !state.loaded));
  }

  function renderFilterMenu(kind) {
    const menu = find(`telemetry-${kind}-menu`);
    if (!menu) return;
    const isProvider = kind === 'provider';
    const key = isProvider ? 'providerID' : 'modelID';
    const activeValue = state.filters[key];
    const parts = telemetryReportParts(state.catalog);
    const options = isProvider ? parts.providers : parts.models;
    const visibleOptions = isProvider || !state.filters.providerID
      ? options
      : options.filter((model) => model.provider_id === state.filters.providerID);
    menu.hidden = state.openFilter !== kind;
    menu.replaceChildren();
    const addOption = (value, label, detail, checked) => {
      const button = el('button', {
        class: `telemetry-filter-option${checked ? ' is-selected' : ''}`,
        type: 'button',
        role: 'menuitemradio',
        'aria-checked': String(checked),
      }, [
        el('span', { class: 'telemetry-filter-option-copy' }, [el('strong', { text: label }), detail ? el('small', { text: detail }) : null]),
        el('span', { class: 'telemetry-filter-option-mark', 'aria-hidden': 'true', text: checked ? '●' : '' }),
      ]);
      button.addEventListener('click', () => applyFilter(kind, value));
      menu.append(button);
    };
    addOption('', isProvider ? 'All providers' : 'All models', 'Show all matching records', !activeValue);
    for (const option of visibleOptions) {
      const value = isProvider ? option.provider_id : option.model_id;
      const label = isProvider ? option.provider_name || option.provider_id : option.model_id;
      const detail = `${formatTelemetryMoney(option.spend)} · ${formatTelemetryNumber(option.requests)} requests`;
      addOption(value, label, detail, activeValue === value);
    }
    if (visibleOptions.length === 0) {
      menu.append(el('p', { class: 'telemetry-filter-empty', role: 'status', text: 'No options in this period.' }));
    }
  }

  function renderFilters() {
    renderFilterMenu('provider');
    renderFilterMenu('model');
  }

  function closeFilterMenus() {
    state.openFilter = '';
    renderFilters();
    updateToolbar();
  }

  async function applyFilter(kind, value, source = null) {
    const key = kind === 'provider' ? 'providerID' : 'modelID';
    const restoreMenuFocus = state.openFilter === kind;
    const focusBreakdownID = source?.dataset?.telemetryId || '';
    if (state.filters[key] === value) {
      closeFilterMenus();
      if (restoreMenuFocus) find(`telemetry-${kind}-filter`)?.focus();
      return;
    }
    state.filters[key] = value;
    if (kind === 'provider' && value && state.filters.modelID) {
      const model = telemetryReportParts(state.catalog).models.find((item) => item.model_id === state.filters.modelID);
      if (model?.provider_id !== value) state.filters.modelID = '';
    }
    state.openFilter = '';
    if (restoreMenuFocus) find(`telemetry-${kind}-filter`)?.focus();
    await refresh();
    if (focusBreakdownID) {
      const nextRow = [...root.querySelectorAll('[data-telemetry-kind][data-telemetry-id]')]
        .find((row) => row.dataset.telemetryKind === kind && row.dataset.telemetryId === focusBreakdownID);
      nextRow?.focus();
    }
  }

  function renderSummary() {
    const parts = currentParts();
    const { summary } = parts;
    // Blended $/1M = total_spend / (total_tokens / 1M), same formula as the
    // production telemetry view (tm-blended).
    const blended = summary.total_tokens > 0 ? summary.total_spend / (summary.total_tokens / 1e6) : 0;
    const values = {
      'telemetry-stat-spend': state.report ? formatTelemetryMoney(summary.total_spend) : '—',
      'telemetry-stat-requests': state.report ? formatTelemetryNumber(summary.total_requests) : '—',
      'telemetry-stat-tokens': state.report ? formatTelemetryNumber(summary.total_tokens) : '—',
      'telemetry-stat-cache': state.report ? formatTelemetryPercent(summary.cache_hit_percent) : '—',
      'telemetry-stat-blended': state.report ? formatTelemetryMoney(blended) : '—',
    };
    for (const [id, value] of Object.entries(values)) {
      const node = find(id);
      if (node) node.textContent = value;
    }
    const period = find('telemetry-period-copy');
    if (period) {
      if (!state.report) period.textContent = state.loading ? 'Loading the selected report…' : 'Choose a period to inspect recorded usage.';
      else if (summary.period_start && summary.period_end) {
        period.textContent = `Activity from ${formatTelemetryDate(summary.period_start)} to ${formatTelemetryDate(summary.period_end)}${state.filters.providerID ? ` · ${providerName(state.filters.providerID)}` : ''}${state.filters.modelID ? ` · ${state.filters.modelID}` : ''}`;
      } else period.textContent = 'No recorded activity in this period.';
    }
  }

  function renderTokenMix() {
    const { summary } = currentParts();
    const total = Math.max(0, Number(summary.total_tokens) || 0);
    const stack = find('telemetry-token-stack');
    const legend = find('telemetry-token-legend');
    const totalNode = find('telemetry-token-total');
    if (totalNode) totalNode.textContent = `${formatTelemetryNumber(total)} total`;
    if (stack) {
      stack.replaceChildren();
      stack.setAttribute('aria-label', TOKEN_SERIES.map((item) => `${item.label}: ${formatTokenLabel(summary[item.key])}`).join(', '));
      if (total > 0) {
        for (const item of TOKEN_SERIES) {
          const value = Math.max(0, Number(summary[item.key]) || 0);
          if (!value) continue;
          stack.append(el('span', {
            class: `telemetry-token-segment is-${item.color}`,
            style: `width:${Math.min(100, value / total * 100)}%`,
            title: `${item.label}: ${formatTokenLabel(value)}`,
            'aria-hidden': 'true',
          }));
        }
      }
    }
    if (legend) {
      legend.replaceChildren(...TOKEN_SERIES.map((item) => el('div', { class: 'telemetry-token-row' }, [
        el('span', { class: `telemetry-token-mark is-${item.color}`, 'aria-hidden': 'true' }),
        el('span', { class: 'telemetry-token-label', text: item.label }),
        el('strong', { text: formatTelemetryNumber(summary[item.key]) }),
      ])));
    }
  }

  function emptyBreakdown(copy) {
    return el('div', { class: 'telemetry-breakdown-empty', role: 'status' }, [
      el('span', { 'data-lucide': 'chart-no-axes-column', 'aria-hidden': 'true' }),
      el('p', { text: copy }),
    ]);
  }

  function renderBreakdown(kind) {
    const isProvider = kind === 'provider';
    const list = find(`telemetry-${kind}-list`);
    if (!list) return;
    const parts = currentParts();
    const values = (isProvider ? parts.providers : parts.models).slice(0, 8);
    list.replaceChildren();
    if (!values.length) {
      list.append(emptyBreakdown(state.report ? 'No usage in this period.' : 'Waiting for the first report.'));
      return;
    }
    const maxSpend = Math.max(...values.map((item) => Number(item.spend) || 0), 0);
    values.forEach((item, index) => {
      const id = isProvider ? item.provider_id : item.model_id;
      const label = isProvider ? item.provider_name || item.provider_id : item.model_id;
      const active = isProvider ? state.filters.providerID === id : state.filters.modelID === id;
      const detail = isProvider ? `${formatTelemetryNumber(item.requests)} requests · ${formatTelemetryNumber(item.tokens)} input/output tokens` : `${item.provider_name || item.provider_id || 'Unknown provider'} · ${formatTelemetryNumber(item.requests)} requests`;
      const button = el('button', {
        class: `telemetry-breakdown-row${active ? ' is-selected' : ''}`,
        type: 'button',
        dataset: { telemetryKind: kind, telemetryId: id },
        disabled: state.loading,
        'aria-pressed': String(active),
        title: `${label} — ${formatTelemetryMoney(item.spend)}`,
      }, [
        el('span', { class: 'telemetry-breakdown-rank', text: String(index + 1).padStart(2, '0'), 'aria-hidden': 'true' }),
        el('span', { class: 'telemetry-breakdown-main' }, [
          el('span', { class: 'telemetry-breakdown-name', text: label }),
          el('span', { class: 'telemetry-breakdown-meta', text: detail }),
          el('span', { class: 'telemetry-breakdown-meter', 'aria-hidden': 'true' }, [el('span', { style: `width:${maxSpend > 0 ? Math.max(3, (Number(item.spend) || 0) / maxSpend * 100) : 0}%` })]),
        ]),
        el('strong', { class: 'telemetry-breakdown-spend', text: formatTelemetryMoney(item.spend) }),
      ]);
      button.addEventListener('click', () => void applyFilter(kind, active ? '' : id, button));
      list.append(button);
    });
  }

  function renderChartState(message, { visible = true, tone = '' } = {}) {
    const stateNode = find('telemetry-chart-state');
    if (!stateNode) return;
    stateNode.textContent = message;
    stateNode.dataset.tone = tone;
    stateNode.hidden = !visible;
  }

  function destroyChart() {
    chart?.destroy();
    chart = null;
  }

  function renderChart() {
    const frame = find('telemetry-chart-frame');
    const canvas = find('telemetry-spend-chart');
    if (!frame || !canvas || !active) return;
    frame.setAttribute('aria-busy', String(state.loading));
    if (!state.report) {
      renderChartState(state.loading ? 'Loading report…' : 'A report will appear here.');
      return;
    }
    if (state.loading && state.loaded && chart) {
      renderChartState('', { visible: false });
      return;
    }
    if (state.chartError) {
      renderChartState('Chart library unavailable. Refresh to try again.', { tone: 'error' });
      destroyChart();
      return;
    }
    if (!window.Chart) {
      renderChartState('Preparing chart…');
      return;
    }
    if (!telemetryHasUsage(state.report)) {
      renderChartState('No usage recorded in this period.');
      destroyChart();
      return;
    }
    const { series } = currentParts();
    if (!series.length) {
      renderChartState('The report contains no time buckets.');
      destroyChart();
      return;
    }
    renderChartState('', { visible: false });
    destroyChart();
    const context = canvas.getContext('2d');
    if (!context) {
      renderChartState('Canvas is not available in this browser.', { tone: 'error' });
      return;
    }
    const height = canvas.clientHeight || 280;
    const gradient = context.createLinearGradient(0, 0, 0, height);
    gradient.addColorStop(0, 'rgba(88, 209, 195, .24)');
    gradient.addColorStop(1, 'rgba(88, 209, 195, 0)');
    const Chart = window.Chart;
    Chart.defaults.color = '#8998ad';
    Chart.defaults.borderColor = 'rgba(116, 129, 151, .14)';
    Chart.defaults.font.family = getComputedStyle(root).getPropertyValue('--font-body').trim() || 'sans-serif';
    chart = new Chart(context, {
      type: 'line',
      data: {
        labels: series.map((bucket) => bucket.date),
        datasets: [{
          label: 'Estimated spend',
          data: series.map((bucket) => Number(bucket.spend) || 0),
          borderColor: '#58d1c3',
          backgroundColor: gradient,
          fill: true,
          borderWidth: 2,
          pointRadius: 0,
          pointHoverRadius: 4,
          pointHoverBackgroundColor: '#edf2f4',
          pointHoverBorderColor: '#58d1c3',
          tension: .32,
        }],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        interaction: { intersect: false, mode: 'index' },
        layout: { padding: { top: 7, right: 5, bottom: 0, left: 2 } },
        plugins: {
          legend: { display: false },
          tooltip: {
            displayColors: false,
            callbacks: { label: (item) => ` ${formatTelemetryMoney(item.parsed.y)} estimated` },
          },
        },
        scales: {
          x: {
            grid: { display: false },
            border: { display: false },
            ticks: { autoSkip: true, maxTicksLimit: 8, maxRotation: 0, color: '#748197', font: { size: 10 } },
          },
          y: {
            beginAtZero: true,
            grid: { color: 'rgba(116, 129, 151, .11)' },
            border: { display: false },
            ticks: { maxTicksLimit: 5, color: '#748197', font: { size: 10 }, callback: (value) => formatTelemetryMoney(value) },
          },
        },
      },
    });
  }

  function render() {
    setError(state.error);
    updateToolbar();
    renderFilters();
    renderSummary();
    renderTokenMix();
    renderBreakdown('model');
    renderBreakdown('provider');
    renderChart();
    window.lucide?.createIcons?.({ attrs: { 'stroke-width': 1.7 } });
  }

  function loadChartForActivePage() {
    if (chartTask) return chartTask;
    state.chartError = null;
    chartTask = loadChartLibrary().then(() => {
      if (destroyed) return;
      state.chartError = null;
      renderChart();
    }).catch((error) => {
      if (destroyed) return;
      state.chartError = error;
      renderChart();
    }).finally(() => {
      chartTask = null;
    });
    return chartTask;
  }

  async function refresh() {
    if (destroyed) return null;
    if (active && state.chartError && !window.Chart) loadChartForActivePage();
    const token = ++reportVersion;
    const requestFilters = { ...state.filters };
    state.loading = true;
    state.error = null;
    setSync(state.loaded ? 'Refreshing report' : 'Loading report', 'loading');
    render();
    try {
      const payload = { minutes: state.minutes };
      if (requestFilters.providerID) payload.provider_id = requestFilters.providerID;
      if (requestFilters.modelID) payload.model_id = requestFilters.modelID;
      const result = await rpcClient('telemetry.report', payload);
      if (destroyed || token !== reportVersion) return null;
      state.report = result || {};
      if (!requestFilters.providerID && !requestFilters.modelID) state.catalog = state.report;
      state.loaded = true;
      state.error = null;
      setSync(`Updated ${new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' }).format(new Date())}`, 'ready');
      return state.report;
    } catch (error) {
      if (destroyed || token !== reportVersion) return null;
      state.error = error;
      setSync(state.loaded ? 'Showing last report' : 'Unavailable', 'error');
      return null;
    } finally {
      if (!destroyed && token === reportVersion) {
        state.loading = false;
        render();
      }
    }
  }

  function activate() {
    if (destroyed) return;
    active = true;
    state.chartError = null;
    renderChart();
    if (!state.loaded || state.error) {
      if (!state.loading) void refresh();
    }
    if (!window.Chart) loadChartForActivePage();
    if (state.loaded && !state.error) requestAnimationFrame(() => {
      if (active && chart) chart.resize();
    });
  }

  function deactivate() {
    active = false;
    state.openFilter = '';
    renderFilters();
    updateToolbar();
  }

  function changeRange(minutes) {
    if (state.minutes === minutes || state.loading) return;
    state.minutes = minutes;
    state.filters = { providerID: '', modelID: '' };
    state.openFilter = '';
    void refresh();
  }

  function toggleFilter(kind) {
    state.openFilter = state.openFilter === kind ? '' : kind;
    renderFilters();
    updateToolbar();
    if (state.openFilter) find(`telemetry-${kind}-menu`)?.querySelector('[role="menuitemradio"]')?.focus();
  }

  function onDocumentClick(event) {
    if (!state.openFilter) return;
    const target = event.target;
    if (!(target instanceof Element) || !root.contains(target) || !target.closest('.telemetry-filter-wrap')) closeFilterMenus();
  }

  // The blended-cost card is appended to the overview strip at init so
  // index.html keeps its stable four-card contract; the strip switches to five
  // tracks on wide layouts and defers to the stylesheet's two-column wrap
  // below 861px.
  function ensureBlendedStat() {
    const overview = root.querySelector('.telemetry-overview');
    if (!overview || find('telemetry-stat-blended')) return;
    overview.append(el('article', { class: 'telemetry-stat telemetry-stat-blended' }, [
      el('span', { text: 'Blended $/1M' }),
      el('strong', { id: 'telemetry-stat-blended', text: '—' }),
      el('small', { text: 'Avg cost per million tokens' }),
    ]));
    const wide = typeof window.matchMedia === 'function' ? window.matchMedia('(min-width: 861px)') : null;
    const applyColumns = () => {
      if (wide && !wide.matches) overview.style.removeProperty('grid-template-columns');
      else overview.style.gridTemplateColumns = 'repeat(5, minmax(0, 1fr))';
    };
    applyColumns();
    wide?.addEventListener?.('change', applyColumns);
  }

  function init() {
    if (initialized) return;
    initialized = true;
    ensureBlendedStat();
    root.querySelectorAll('[data-telemetry-range]').forEach((button) => {
      eventListener(listeners, button, 'click', () => changeRange(Number(button.dataset.telemetryRange)));
    });
    eventListener(listeners, find('telemetry-refresh'), 'click', () => void refresh());
    eventListener(listeners, find('telemetry-retry'), 'click', () => void refresh());
    eventListener(listeners, find('telemetry-provider-filter'), 'click', () => toggleFilter('provider'));
    eventListener(listeners, find('telemetry-model-filter'), 'click', () => toggleFilter('model'));
    eventListener(listeners, document, 'click', onDocumentClick);
    eventListener(listeners, root, 'keydown', (event) => {
      if (event.key === 'Escape' && state.openFilter) {
        const filter = state.openFilter;
        closeFilterMenus();
        root.querySelector(`#telemetry-${filter}-filter`)?.focus();
        return;
      }
      if (!state.openFilter || !['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
      const items = [...(find(`telemetry-${state.openFilter}-menu`)?.querySelectorAll('[role="menuitemradio"]') || [])];
      if (!items.length) return;
      const index = items.indexOf(document.activeElement);
      const next = event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? items.length - 1
          : index < 0
            ? 0
            : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
      event.preventDefault();
      items[next].focus();
    });
    render();
  }

  function destroy() {
    destroyed = true;
    reportVersion += 1;
    active = false;
    listeners.splice(0).forEach((remove) => remove());
    destroyChart();
  }

  return { init, activate, deactivate, refresh, destroy };
}
