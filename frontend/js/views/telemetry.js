// Telemetry view: aggregate usage, spend, and caching across conversations.
// Mirrors the OpenRouter Activity dashboard layout.

import { rpc } from '../rpc.js';
import { createSelect } from '../ui.js';

let charts = {};
let currentRange = 180; // 3h default
let chartReady = false;

const PALETTE = ['#a3e635', '#22d3ee', '#a78bfa', '#fb923c', '#facc15', '#60a5fa', '#f472b6', '#94a3b8', '#ef4444'];

function getChart() {
  const C = window.Chart;
  if (!C) return null;
  if (!chartReady) {
    C.defaults.color = '#94a3b8';
    C.defaults.borderColor = 'rgba(148,163,184,0.1)';
    C.defaults.font.family = 'system-ui, sans-serif';
    chartReady = true;
  }
  return C;
}

export async function initTelemetry() {
  const rangeSel = document.getElementById('telemetry-range');
  const refreshBtn = document.getElementById('telemetry-refresh-btn');
  if (rangeSel) {
    createSelect(rangeSel, {
      data: [
        { text: '15m', value: '15' },
        { text: '30m', value: '30' },
        { text: '1h', value: '60' },
        { text: '3h', value: '180' },
        { text: '1d', value: '1440' },
        { text: '2d', value: '2880' },
        { text: '1w', value: '10080' },
        { text: '1mo', value: '43200' },
        { text: '1y', value: '525600' },
        { text: 'All', value: '0' },
      ],
      value: '180',
      search: false,
      onChange: (value) => {
        const next = parseInt(value, 10) || 0;
        if (next === currentRange) return;
        currentRange = next;
        refresh();
      },
    });
  }
  if (refreshBtn) refreshBtn.addEventListener('click', () => refresh());
}

export async function refresh() {
  try {
    const Chart = getChart();
    if (!Chart) {
      console.error('telemetry: Chart.js not loaded (window.Chart missing)');
      return;
    }
    const res = await rpc('telemetry.report', { minutes: currentRange });
    renderSummary(res.summary);
    renderTables(res);
    // Defer chart rendering until the browser has laid out the canvas
    // (the view may have just become visible and canvas still reports 0×0).
    requestAnimationFrame(() => renderCharts(res, Chart));
  } catch (err) {
    console.error('telemetry load failed:', err);
  }
}

function renderSummary(s) {
  const set = (id, val) => { const el = document.getElementById(id); if (el) el.textContent = val; };
  set('tm-spend', formatUSD(s.total_spend));
  set('tm-requests', formatNum(s.total_requests));
  set('tm-tokens', formatNum(s.total_tokens));
  set('tm-cache', `${s.cache_hit_percent.toFixed(1)}%`);
  // Blended $/1M = total_spend / (total_tokens / 1M)
  const blended = s.total_tokens > 0 ? (s.total_spend / (s.total_tokens / 1e6)) : 0;
  set('tm-blended', formatUSD(blended));
}

function renderCharts(res, Chart) {
  const labels = res.series.map(d => d.date);
  const stackedBar = (canvasId, datasets) => {
    const ctx = document.getElementById(canvasId);
    if (!ctx) return;
    if (charts[canvasId]) charts[canvasId].destroy();
    charts[canvasId] = new Chart(ctx, {
      type: 'bar',
      data: { labels, datasets },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        layout: { padding: { top: 2, bottom: 0, left: 0, right: 0 } },
        scales: {
          x: { stacked: true, grid: { display: false }, ticks: { maxRotation: 0, autoSkip: true, maxTicksLimit: 8, font: { size: 10 } } },
          y: { stacked: true, grid: { color: 'rgba(148,163,184,0.06)' }, ticks: { callback: (v) => formatNum(v), font: { size: 10 }, maxTicksLimit: 5 } },
        },
        plugins: {
          legend: { position: 'bottom', labels: { boxWidth: 8, boxHeight: 8, padding: 6, font: { size: 10 } } },
          tooltip: { mode: 'index', intersect: false, bodyFont: { size: 11 }, titleFont: { size: 11 } },
        },
      },
    });
  };

  // Usage by model (stacked bar, spend per model per day)
  const modelNames = uniqueModels(res.series);
  const usageDatasets = modelNames.map((m, i) => ({
    label: m,
    data: res.series.map(d => {
      const entry = d.per_model?.find(pm => pm.model_id === m);
      return entry ? entry.spend : 0;
    }),
    backgroundColor: PALETTE[i % PALETTE.length],
  }));
  stackedBar('chart-usage-model', usageDatasets);

  // Request volume by model
  const reqDatasets = modelNames.map((m, i) => ({
    label: m,
    data: res.series.map(d => {
      const entry = d.per_model?.find(pm => pm.model_id === m);
      return entry ? entry.requests : 0;
    }),
    backgroundColor: PALETTE[i % PALETTE.length],
  }));
  stackedBar('chart-requests', reqDatasets);

  // Token breakdown (prompt / completion / reasoning-as-output)
  stackedBar('chart-token-breakdown', [
    { label: 'Prompt', data: res.series.map(d => d.input_tokens), backgroundColor: '#60a5fa' },
    { label: 'Completion', data: res.series.map(d => d.output_tokens), backgroundColor: '#a78bfa' },
    { label: 'Cache read', data: res.series.map(d => d.cache_read), backgroundColor: '#f472b6' },
  ]);

  // Prompt token caching (cached vs uncached)
  stackedBar('chart-caching', [
    { label: 'Cached', data: res.series.map(d => d.cache_read), backgroundColor: '#fb923c' },
    { label: 'Uncached', data: res.series.map(d => d.input_tokens), backgroundColor: '#94a3b8' },
  ]);
}

function renderTables(res) {
  const modelsEl = document.getElementById('tm-top-models');
  if (modelsEl) {
    modelsEl.innerHTML = '';
    res.top_models.slice(0, 8).forEach((m, i) => {
      const row = document.createElement('div');
      row.className = 'telemetry-table-row';
      row.innerHTML = `<span class="tm-rank">${i + 1}</span><span class="tm-name" title="${m.model_id}">${m.model_id}</span><span class="tm-value">${formatUSD(m.spend)}</span><span class="tm-sub">${formatNum(m.requests)} req · ${formatNum(m.tokens)} tok</span>`;
      modelsEl.appendChild(row);
    });
    if (res.top_models.length === 0) modelsEl.innerHTML = '<div class="telemetry-empty">No usage data yet.</div>';
  }
  const provEl = document.getElementById('tm-top-providers');
  if (provEl) {
    provEl.innerHTML = '';
    res.top_providers.slice(0, 8).forEach((p, i) => {
      const row = document.createElement('div');
      row.className = 'telemetry-table-row';
      row.innerHTML = `<span class="tm-rank">${i + 1}</span><span class="tm-name">${p.provider_name || p.provider_id}</span><span class="tm-value">${formatUSD(p.spend)}</span><span class="tm-sub">${formatNum(p.requests)} req</span>`;
      provEl.appendChild(row);
    });
    if (res.top_providers.length === 0) provEl.innerHTML = '<div class="telemetry-empty">No provider data.</div>';
  }
}

function uniqueModels(series) {
  const set = new Set();
  series.forEach(d => d.per_model?.forEach(pm => set.add(pm.model_id)));
  return [...set];
}

function formatUSD(v) {
  if (v >= 1) return `$${v.toFixed(2)}`;
  if (v >= 0.01) return `$${v.toFixed(4)}`;
  return `$${v.toFixed(6)}`;
}

function formatNum(v) {
  if (v >= 1e9) return `${(v / 1e9).toFixed(2)}B`;
  if (v >= 1e6) return `${(v / 1e6).toFixed(2)}M`;
  if (v >= 1e3) return `${(v / 1e3).toFixed(2)}K`;
  return String(v);
}
