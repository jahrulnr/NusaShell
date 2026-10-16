import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { createTelemetryPage } from '../page/telemetry/app.js';

const HTML = `<!doctype html><body>
  <div id="telemetry-model-list"></div>
  <div id="telemetry-provider-list"></div>
  <div id="telemetry-sync-state"></div>
  <div id="telemetry-error" hidden><span id="telemetry-error-message"></span></div>
</body>`;

function install(dom) {
  const previous = {
    window: globalThis.window,
    document: globalThis.document,
    HTMLElement: globalThis.HTMLElement,
    requestAnimationFrame: globalThis.requestAnimationFrame,
  };
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.requestAnimationFrame = (callback) => { callback(); return 0; };
  dom.window.Chart = function Chart() { this.resize = () => {}; this.destroy = () => {}; };
  return () => {
    globalThis.window = previous.window;
    globalThis.document = previous.document;
    globalThis.HTMLElement = previous.HTMLElement;
    globalThis.requestAnimationFrame = previous.requestAnimationFrame;
    dom.window.close();
  };
}

async function renderReport(report) {
  const dom = new JSDOM(HTML, { url: 'http://localhost/' });
  const restore = install(dom);
  const page = createTelemetryPage(dom.window.document, {
    rpcClient: async (method) => {
      assert.equal(method, 'telemetry.report');
      return report;
    },
  });
  page.init();
  page.activate();
  await new Promise((resolve) => setTimeout(resolve, 0));
  return { document: dom.window.document, restore };
}

test('telemetry breakdown renders hostile model and provider names as text', async () => {
  const hostileModel = '"><img src=x onerror=alert(1)>';
  const hostileProvider = '<svg onload=alert(1)></svg>';
  const { document, restore } = await renderReport({
    summary: { total_spend: 1.75, total_requests: 10, total_tokens: 4200, cache_hit_percent: 0 },
    series: [],
    top_models: [{ model_id: hostileModel, provider_id: 'p1', provider_name: 'OpenRouter', spend: 1.5, requests: 3, tokens: 4200 }],
    top_providers: [{ provider_id: 'p1', provider_name: hostileProvider, spend: 0.25, requests: 7, tokens: 100 }],
  });
  try {
    const modelName = document.querySelector('#telemetry-model-list .telemetry-breakdown-name');
    const providerName = document.querySelector('#telemetry-provider-list .telemetry-breakdown-name');
    assert.equal(modelName.textContent, hostileModel);
    assert.equal(modelName.querySelector('img'), null);
    assert.equal(providerName.textContent, hostileProvider);
    assert.equal(providerName.querySelector('svg'), null);
    assert.equal(document.querySelector('#telemetry-model-list img, #telemetry-provider-list svg'), null);
    assert.match(document.querySelector('#telemetry-model-list .telemetry-breakdown-spend').textContent, /\$1\.50/);
    assert.equal(document.querySelector('#telemetry-model-list .telemetry-breakdown-rank').textContent, '01');
  } finally {
    restore();
  }
});

test('telemetry breakdown renders an empty-state placeholder when the report has no rows', async () => {
  const { document, restore } = await renderReport({
    summary: { total_spend: 0, total_requests: 0, total_tokens: 0, cache_hit_percent: 0 },
    series: [],
    top_models: [],
    top_providers: [],
  });
  try {
    assert.equal(document.querySelector('#telemetry-model-list .telemetry-breakdown-empty').textContent.includes('No usage in this period.'), true);
    assert.equal(document.querySelector('#telemetry-provider-list .telemetry-breakdown-empty').textContent.includes('No usage in this period.'), true);
    assert.equal(document.querySelector('.telemetry-breakdown-row'), null);
  } finally {
    restore();
  }
});
