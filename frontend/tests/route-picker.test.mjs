import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { bindRoutePicker } from '../js/views/agent/route-picker.js';

function makeDom() {
  const dom = new JSDOM(`<!doctype html><html><body>
    <button id="route-trigger" type="button" aria-expanded="false">
      <span id="route-trigger-icon"></span><span id="route-trigger-label">Auto</span>
    </button>
    <div id="route-menu" hidden></div>
  </body></html>`, { url: 'http://localhost/' });
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  globalThis.location = dom.window.location;
  return dom;
}

function cleanup(dom) {
  delete globalThis.window;
  delete globalThis.document;
  delete globalThis.location;
  delete globalThis.fetch;
  dom.window.close();
}

test('route menu offers provider search and renders per-provider pricing', async () => {
  const dom = makeDom();
  const routes = [
    { slug: 'nebius/fp8', name: 'Nebius', input_cost: 1.5, output_cost: 2.5 },
    { slug: 'deepinfra/fp4', name: 'DeepInfra', input_cost: 0, output_cost: 0 },
    { slug: 'cerebras', name: 'Cerebras' },
  ];
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ ok: true, result: { routes } }) });
  let selectedRoute = '';
  try {
    const picker = bindRoutePicker({
      getModels: () => [{ provider_id: 'openrouter', id: 'model-a', route_support: true }],
      getSelectedModel: () => 'openrouter:model-a',
      getSelectedRoute: () => selectedRoute,
      selectRoute: (route) => { selectedRoute = route; },
    });

    await picker.refresh();
    document.getElementById('route-trigger').click();

    const menu = document.getElementById('route-menu');
    const search = menu.querySelector('input[type="search"]');
    assert.ok(search, 'route menu should expose a search input');
    assert.equal(search.getAttribute('aria-label'), 'Search providers');
    assert.match(menu.textContent, /\$1\.5\/M in · \$2\.5\/M out/);
    assert.match(menu.textContent, /\$0\/M in · \$0\/M out/);

    search.value = 'deep';
    search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 150));
    const rows = [...menu.querySelectorAll('.agent-model-row')];
    const nebiusRow = rows.find((row) => row.textContent.includes('Nebius'));
    const deepInfraRow = rows.find((row) => row.textContent.includes('DeepInfra'));
    const cerebrasRow = rows.find((row) => row.textContent.includes('Cerebras'));
    assert.doesNotMatch(cerebrasRow.textContent, /\$\d+\/M/);
    assert.equal(nebiusRow.hidden, true);
    assert.equal(deepInfraRow.hidden, false);
    assert.equal(cerebrasRow.hidden, true);
  } finally {
    cleanup(dom);
  }
});
