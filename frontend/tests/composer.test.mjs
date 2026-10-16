import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { initComposerSelects } from '../page/conversation/composer.js';

// R1 BLOCKER-1 regression: SlimSelect emits afterChange for programmatic
// writes too — setData() rebuilds option objects so the selected-id set always
// differs — not only for setSelected(). Every route-picker mutation during
// room/model syncs must stay inside the programmatic guard or it turns into a
// real agent.conversations.set-provider backend write on a read path (opening
// a routed room used to write provider_route:'' before restoring the route).

const MARKUP = `<form id="message-form">
  <button id="model-config-trigger" type="button" aria-controls="model-config-popover" aria-expanded="false"></button>
  <div id="model-config-popover" hidden>
    <span class="composer-select-key" id="model-label"></span>
    <strong id="model-config-summary"></strong>
    <span id="effort-config-summary"></span>
    <select class="slim-select model-catalog" id="model-select">
      <optgroup label="OpenRouter">
        <option value="openrouter:anthropic/claude-sonnet-4.5" selected>Claude Sonnet 4.5</option>
        <option value="openrouter:google/gemini-3.1-pro">Gemini 3.1 Pro</option>
      </optgroup>
    </select>
    <select class="slim-select" id="effort-select">
      <option value="Auto" selected>Auto</option>
      <option value="Medium">Medium</option>
    </select>
    <div class="composer-select composer-select-route">
      <span class="composer-select-key" id="route-label"></span>
      <select class="slim-select" id="route-select">
        <option value="" selected>Auto route</option>
      </select>
    </div>
  </div>
</form>`;

const CATALOG = [
  {
    provider_id: 'openrouter',
    provider_name: 'OpenRouter',
    provider_kind: 'openrouter',
    id: 'anthropic/claude-sonnet-4.5',
    display_name: 'Claude Sonnet 4.5',
    kind: 'chat',
    route_support: true,
    supported_efforts: ['medium', 'high'],
    default_effort: 'medium',
  },
  {
    provider_id: 'openrouter',
    provider_name: 'OpenRouter',
    provider_kind: 'openrouter',
    id: 'google/gemini-3.1-pro',
    display_name: 'Gemini 3.1 Pro',
    kind: 'chat',
    route_support: true,
    supported_efforts: ['medium'],
    default_effort: 'medium',
  },
];

const ROUTES = {
  routes: [
    { slug: 'anthropic', name: 'Anthropic' },
    { slug: 'google-vertex', name: 'Google Vertex' },
  ],
};

function installDOMGlobals(window) {
  const names = [
    'window', 'document', 'MutationObserver', 'requestAnimationFrame',
    'cancelAnimationFrame', 'Node', 'HTMLElement', 'HTMLInputElement',
    'HTMLTextAreaElement', 'HTMLSelectElement', 'localStorage', 'navigator',
    'getComputedStyle', 'self',
  ];
  const previous = new Map();
  for (const name of names) {
    previous.set(name, Object.getOwnPropertyDescriptor(globalThis, name));
    Object.defineProperty(globalThis, name, { configurable: true, value: window[name] });
  }
  return () => {
    for (const name of names) {
      const descriptor = previous.get(name);
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else delete globalThis[name];
    }
  };
}

function flush() {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

function harness() {
  const dom = new JSDOM(MARKUP, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const setProviderCalls = [];
  const rpc = async (method) => {
    if (method === 'ai.models.endpoints') return ROUTES;
    if (method === 'ai.codex.accounts.list') return { accounts: [] };
    return {};
  };
  const selects = initComposerSelects(dom.window.document.querySelector('#message-form'), {
    rpc,
    // app.js wires this to persistProviderRoute → agent.conversations.set-provider.
    onRouteChange: (route) => setProviderCalls.push({ method: 'agent.conversations.set-provider', provider_route: route }),
  });
  return { dom, selects, setProviderCalls, restore };
}

test('programmatic room/model syncs never emit set-provider writes', async () => {
  const env = harness();
  try {
    await flush();
    // Model catalogue refresh while a routed room is active — previously each
    // route.setData() emitted a stray set-provider write.
    env.selects.setModels(CATALOG, 'openrouter:anthropic/claude-sonnet-4.5');
    await flush();
    assert.deepEqual(env.setProviderCalls, [], 'setModels must not write set-provider');

    // Opening a room whose saved provider_route is 'anthropic': syncModel used
    // to write provider_route:'' before the UI restored 'anthropic'.
    env.selects.setConversation({
      id: 'room-1',
      model: 'openrouter:anthropic/claude-sonnet-4.5',
      provider_route: 'anthropic',
    });
    await flush();
    await flush();
    assert.deepEqual(env.setProviderCalls, [], 'setConversation must not write set-provider');
    assert.equal(env.selects.getConfig().providerRoute, 'anthropic', 'saved route restored in the picker');

    // A later setModels() refresh on the routed room stays silent too.
    env.selects.setModels(CATALOG, 'openrouter:anthropic/claude-sonnet-4.5');
    await flush();
    await flush();
    assert.deepEqual(env.setProviderCalls, [], 'setModels refresh must not write set-provider');
    assert.equal(env.selects.getConfig().providerRoute, 'anthropic');
  } finally {
    env.restore();
  }
});

test('a real user route pick still emits one set-provider write', async () => {
  const env = harness();
  try {
    env.selects.setModels(CATALOG, 'openrouter:anthropic/claude-sonnet-4.5');
    env.selects.setConversation({
      id: 'room-1',
      model: 'openrouter:anthropic/claude-sonnet-4.5',
      provider_route: 'anthropic',
    });
    await flush();
    await flush();
    assert.equal(env.selects.getConfig().providerRoute, 'anthropic');

    // Simulate the user picking "Google Vertex" in the rendered SlimSelect
    // dropdown — the only path that should reach onRouteChange.
    const target = env.selects.route.getData().find((option) => option.value === 'google-vertex');
    const optionEl = [...env.dom.window.document.querySelectorAll('.ss-option')]
      .find((option) => option.dataset.id === target.id);
    assert.ok(optionEl, 'rendered route option exists');
    optionEl.dispatchEvent(new env.dom.window.MouseEvent('click', { bubbles: true, cancelable: true }));

    assert.deepEqual(env.setProviderCalls, [
      { method: 'agent.conversations.set-provider', provider_route: 'google-vertex' },
    ]);
    assert.equal(env.selects.getConfig().providerRoute, 'google-vertex');
  } finally {
    env.restore();
  }
});

test('a stale route response cannot replace a newer Codex account list', async () => {
  const dom = new JSDOM(MARKUP, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const pending = [];
  const rpc = (method) => new Promise((resolve) => { pending.push({ method, resolve }); });
  try {
    const selects = initComposerSelects(dom.window.document.querySelector('#message-form'), { rpc });
    const catalog = [
      ...CATALOG,
      {
        provider_id: 'codex',
        provider_name: 'Codex',
        provider_kind: 'codex',
        id: 'gpt-5.4',
        display_name: 'GPT-5.4',
        kind: 'chat',
      },
    ];
    selects.setModels(catalog, 'openrouter:anthropic/claude-sonnet-4.5');
    await flush();
    selects.setModels(catalog, 'codex:gpt-5.4');
    await flush();
    const endpoints = pending.filter((call) => call.method === 'ai.models.endpoints');
    const accounts = pending.filter((call) => call.method === 'ai.codex.accounts.list');
    assert.ok(endpoints.length >= 1, 'the first model asks for upstream routes');
    assert.ok(accounts.length >= 1, 'the Codex model asks for accounts');
    accounts.at(-1).resolve({ accounts: [{ account_id: 'plus', email: 'plus@example.test' }] });
    await flush();
    await flush();
    const labels = () => selects.route.getData().map((option) => option.text || option.label || option.value).join(' ');
    assert.match(labels(), /plus@example\.test/);
    endpoints[0].resolve({ routes: [{ slug: 'anthropic', name: 'Anthropic' }] });
    await flush();
    await flush();
    const after = labels();
    assert.equal(after.includes('Anthropic'), false, 'the older endpoints response must not replace the Codex account list');
    assert.match(after, /plus@example\.test/);
  } finally {
    restore();
    dom.window.close();
  }
});
