import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { refresh } from '../js/views/telemetry.js';

const HTML = `<!DOCTYPE html><html><body>
  <div id="tm-top-models"></div>
  <div id="tm-top-providers"></div>
</body></html>`;

function report(overrides = {}) {
  return {
    summary: { total_spend: 0, total_requests: 0, total_tokens: 0, cache_hit_percent: 0 },
    series: [],
    top_models: [],
    top_providers: [],
    ...overrides,
  };
}

// withTelemetryDOM installs a jsdom document, a minimal Chart stub, a
// synchronous requestAnimationFrame, and a fetch stub answering
// telemetry.report — everything refresh() needs to reach renderTables().
async function withTelemetryDOM(res, fn) {
  const dom = new JSDOM(HTML);
  const previous = {
    window: globalThis.window,
    document: globalThis.document,
    fetch: globalThis.fetch,
    requestAnimationFrame: globalThis.requestAnimationFrame,
  };
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  dom.window.Chart = function Chart() {};
  dom.window.Chart.defaults = { font: {} };
  globalThis.requestAnimationFrame = (cb) => { cb(); return 0; };
  globalThis.fetch = async () => ({
    ok: true,
    json: async () => ({ ok: true, result: res }),
  });
  try {
    await refresh();
    return fn();
  } finally {
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete globalThis[key];
      else globalThis[key] = value;
    }
  }
}

test('telemetry top-model row renders hostile model_id as text, not markup', async () => {
  const hostile = '"><img src=x onerror=alert(1)>';
  await withTelemetryDOM(report({
    top_models: [{ model_id: hostile, spend: 1.5, requests: 3, tokens: 4200 }],
  }), () => {
    const modelsEl = document.getElementById('tm-top-models');
    const row = modelsEl.querySelector('.telemetry-table-row');
    assert.ok(row, 'model row rendered');
    const name = row.querySelector('.tm-name');
    assert.equal(name.textContent, hostile);
    assert.equal(name.getAttribute('title'), hostile);
    assert.equal(name.children.length, 0, 'payload stayed inert text inside .tm-name');
    assert.equal(modelsEl.querySelector('img'), null, 'no img element was created');
    assert.equal(row.querySelector('.tm-rank').textContent, '1');
    assert.equal(row.querySelector('.tm-value').textContent, '$1.50');
    assert.equal(row.querySelector('.tm-sub').textContent, '3 req · 4.20K tok');
  });
});

test('telemetry top-provider row renders hostile provider_name as text, not markup', async () => {
  const hostile = '<svg onload=alert(1)></svg>';
  await withTelemetryDOM(report({
    top_providers: [{ provider_id: 'p1', provider_name: hostile, spend: 0.25, requests: 7 }],
  }), () => {
    const provEl = document.getElementById('tm-top-providers');
    const row = provEl.querySelector('.telemetry-table-row');
    assert.ok(row, 'provider row rendered');
    const name = row.querySelector('.tm-name');
    assert.equal(name.textContent, hostile);
    assert.equal(name.children.length, 0, 'payload stayed inert text inside .tm-name');
    assert.equal(provEl.querySelector('svg'), null, 'no svg element was created');
    assert.equal(row.querySelector('.tm-rank').textContent, '1');
    assert.equal(row.querySelector('.tm-value').textContent, '$0.2500');
    assert.equal(row.querySelector('.tm-sub').textContent, '7 req');
  });
});

test('telemetry top-provider row falls back to provider_id when name is empty', async () => {
  await withTelemetryDOM(report({
    top_providers: [{ provider_id: 'anthropic', provider_name: '', spend: 2, requests: 1500 }],
  }), () => {
    const row = document.querySelector('#tm-top-providers .telemetry-table-row');
    assert.equal(row.querySelector('.tm-name').textContent, 'anthropic');
    assert.equal(row.querySelector('.tm-sub').textContent, '1.50K req');
  });
});

test('telemetry tables render empty-state placeholders', async () => {
  await withTelemetryDOM(report(), () => {
    assert.equal(document.querySelector('#tm-top-models .telemetry-empty').textContent, 'No usage data yet.');
    assert.equal(document.querySelector('#tm-top-providers .telemetry-empty').textContent, 'No provider data.');
  });
});
