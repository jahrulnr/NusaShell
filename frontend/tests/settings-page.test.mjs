import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { emit } from '../js/rpc.js';
import { refreshPairingPanel } from '../js/pairing.js';
import { createSettingsPage } from '../page/settings/app.js';

const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');

function installDOMGlobals(window) {
  const names = [
    'window', 'document', 'MutationObserver', 'requestAnimationFrame',
    'cancelAnimationFrame', 'Node', 'HTMLElement', 'HTMLInputElement',
    'HTMLTextAreaElement', 'HTMLSelectElement', 'localStorage', 'navigator',
  ];
  const previous = new Map();
  for (const name of names) {
    previous.set(name, Object.getOwnPropertyDescriptor(globalThis, name));
    Object.defineProperty(globalThis, name, { configurable: true, value: window[name] });
  }
  // jsdom only provides requestAnimationFrame with pretendToBeVisual; page
  // toasts call it through the global binding — fall back to a sync stub.
  if (typeof globalThis.requestAnimationFrame !== 'function') {
    Object.defineProperty(globalThis, 'requestAnimationFrame', {
      configurable: true,
      value: (callback) => { callback(0); return 0; },
    });
  }
  return () => {
    for (const name of names) {
      const descriptor = previous.get(name);
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else delete globalThis[name];
    }
  };
}

function harness(options = {}) {
  const dto = {
    max_tool_rounds: 8,
    prompt_caching: false,
    compaction_workflow: 'dedicated',
    sound_notifications: true,
    remote_access_enabled: false,
    remote_access_addresses: [],
    ...(options.settings || {}),
  };
  const calls = [];
  const rpcClient = async (method, params) => {
    calls.push({ method, params });
    if (typeof options.onCall === 'function') {
      const custom = options.onCall(method, params);
      if (custom !== undefined) return custom;
    }
    if (method === 'settings.get') return { settings: dto };
    if (method === 'settings.set') {
      Object.assign(dto, params);
      return { settings: { ...dto } };
    }
    if (method === 'ai.models.list') return { models: options.models ?? [] };
    if (method === 'app.info') return options.info ?? { version: 'test', data_dir: '/tmp/nusashell' };
    if (method === 'settings.stt_install_status') {
      return options.stt ?? { ready: false, supported: false, models: [], engine_installed: false };
    }
    if (method === 'settings.tts_install_status') return options.tts ?? { ready: false, voices: [] };
    if (method === 'settings.pets_status') return options.pets ?? { supported: false };
    return {};
  };
  const dom = new JSDOM(html, { url: 'http://127.0.0.1:8766/' });
  const restore = installDOMGlobals(dom.window);
  const page = createSettingsPage(dom.window.document.querySelector('#settings-view'), {
    rpcClient,
    pairingRefresh: options.pairingRefresh,
  });
  page.init();
  return {
    calls,
    document: dom.window.document,
    page,
    async activate() { await page.activate(); await new Promise((resolve) => setTimeout(resolve, 0)); },
    restore() { page.destroy(); restore(); },
  };
}

test('settings page renders the saved tool-round limit', async () => {
  const env = harness();
  try {
    await env.activate();
    assert.equal(env.document.querySelector('[data-setting="max_tool_rounds"]').value, '8');
  } finally {
    env.restore();
  }
});

test('staged save sends only the dirty tool-round limit', async () => {
  const env = harness();
  try {
    await env.activate();
    const input = env.document.querySelector('[data-setting="max_tool_rounds"]');
    input.value = '9';
    input.dispatchEvent(new env.document.defaultView.Event('input', { bubbles: true }));
    assert.match(env.document.querySelector('#settings-dirty-count').textContent, /1/);
    env.document.querySelector('#settings-save').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    const saved = env.calls.filter((call) => call.method === 'settings.set');
    assert.equal(saved.length, 1);
    assert.deepEqual(saved[0].params, { max_tool_rounds: 9 });
  } finally {
    env.restore();
  }
});

test('discard restores the saved tool-round limit', async () => {
  const env = harness();
  try {
    await env.activate();
    const input = env.document.querySelector('[data-setting="max_tool_rounds"]');
    input.value = '9';
    input.dispatchEvent(new env.document.defaultView.Event('input', { bubbles: true }));
    env.document.querySelector('#settings-discard').click();
    assert.equal(env.document.querySelector('[data-setting="max_tool_rounds"]').value, '8');
    assert.equal(env.document.querySelector('#settings-savebar').hidden, true);
  } finally {
    env.restore();
  }
});

test('a typed search key is the only saved field', async () => {
  const env = harness();
  try {
    await env.activate();
    const input = env.document.querySelector('[data-secret="web_search_brave_api_key"]');
    input.value = 'sk-test';
    input.dispatchEvent(new env.document.defaultView.Event('input', { bubbles: true }));
    env.document.querySelector('#settings-save').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    const saved = env.calls.filter((call) => call.method === 'settings.set');
    assert.deepEqual(saved.at(-1).params, { web_search_brave_api_key: 'sk-test' });
  } finally {
    env.restore();
  }
});

test('a disk reload keeps a dirty tool-round edit', async () => {
  const env = harness();
  try {
    await env.activate();
    const input = env.document.querySelector('[data-setting="max_tool_rounds"]');
    input.value = '9';
    input.dispatchEvent(new env.document.defaultView.Event('input', { bubbles: true }));
    emit('settings.applied', {});
    assert.equal(env.document.querySelector('#settings-disk-banner').hidden, false);
    assert.equal(input.value, '9');
  } finally {
    env.restore();
  }
});

test('speech install progress is shown on the STT card', async () => {
  const env = harness();
  try {
    await env.activate();
    emit('stt.install.progress', { phase: 'binary', message: 'Downloading whisper.cpp engine…', bytes_fetched: 10, bytes_total: 100 });
    assert.match(env.document.querySelector('#settings-stt-card').textContent, /Downloading whisper\.cpp engine/);
  } finally {
    env.restore();
  }
});

test('an overview model tile keeps the page in view and skips the hidden select', async () => {
  const env = harness();
  try {
    await env.activate();
    const page = env.document.querySelector('#settings-view');
    const main = env.document.querySelector('.settings-main');
    main.style.overflowY = 'auto';
    const scrolled = [];
    main.scrollTo = (options) => scrolled.push(options);
    let pageScrolled = false;
    page.scrollTo = () => { pageScrolled = true; };
    const intoView = [];
    const original = env.document.defaultView.Element.prototype.scrollIntoView;
    env.document.defaultView.Element.prototype.scrollIntoView = function record() { intoView.push(this.id); };
    try {
      env.document.querySelector('[data-capability="vision"]').click();
    } finally {
      env.document.defaultView.Element.prototype.scrollIntoView = original;
    }
    assert.equal(intoView.length, 0);
    assert.equal(pageScrolled, false);
    assert.notEqual(env.document.activeElement?.tagName, 'SELECT');
    assert.ok(scrolled.length >= 1);
    assert.equal(env.document.querySelector('#settings-panel-media').hidden, false);
  } finally {
    env.restore();
  }
});

function flush() {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

test('a number meaning line appears only for a zero sentinel', async () => {
  const env = harness();
  try {
    await env.activate();
    const rounds = env.document.querySelector('[data-meaning="max_tool_rounds"]');
    assert.equal(rounds.hidden, true);
    assert.equal(rounds.textContent, '');
    const slow = env.document.querySelector('[data-meaning="slow_down"]');
    assert.equal(slow.hidden, false);
    assert.equal(slow.textContent, 'Off');
    const input = env.document.querySelector('[data-setting="slow_down"]');
    input.value = '2';
    input.dispatchEvent(new env.document.defaultView.Event('input', { bubbles: true }));
    assert.equal(slow.hidden, true);
    assert.equal(slow.textContent, '');
    input.value = '0';
    input.dispatchEvent(new env.document.defaultView.Event('input', { bubbles: true }));
    assert.equal(slow.hidden, false);
    assert.equal(slow.textContent, 'Off');
  } finally {
    env.restore();
  }
});

test('search with no matches shows an empty state', async () => {
  const env = harness();
  try {
    await env.activate();
    const input = env.document.querySelector('#settings-search');
    input.value = 'zzzq';
    input.dispatchEvent(new env.document.defaultView.Event('input', { bubbles: true }));
    const empty = env.document.querySelector('#settings-search-empty');
    assert.ok(empty);
    assert.equal(empty.hidden, false);
    assert.equal(empty.textContent, 'No settings match "zzzq"');
    assert.equal([...env.document.querySelectorAll('[data-settings-panel]')].every((panel) => panel.hidden), true);
    input.value = 'tool';
    input.dispatchEvent(new env.document.defaultView.Event('input', { bubbles: true }));
    assert.equal(empty.hidden, true);
  } finally {
    env.restore();
  }
});

test('overview and review name delegate models from the catalogue', async () => {
  const env = harness({
    settings: { delegate_model: 'openai:gpt-4o' },
    models: [
      { provider_id: 'openai', id: 'gpt-4o', provider_name: 'OpenAI', kind: 'chat' },
      { provider_id: 'openai', id: 'gpt-4o-mini', provider_name: 'OpenAI', kind: 'chat' },
    ],
  });
  try {
    await env.activate();
    const summary = env.document.querySelector('#settings-overview-summary').textContent;
    assert.match(summary, /gpt-4o · OpenAI/);
    assert.doesNotMatch(summary, /openai:gpt-4o/);
    const select = env.document.querySelector('#settings-field-review_model');
    select.slim.setSelected(['openai:gpt-4o-mini']);
    env.document.querySelector('#settings-review-open').click();
    const review = env.document.querySelector('#settings-review');
    assert.match(review.textContent, /gpt-4o-mini · OpenAI/);
    assert.equal(review.querySelector('[data-review-close]').classList.contains('settings-quiet-button'), true);
    assert.equal(review.querySelector('[data-revert]').classList.contains('settings-quiet-button'), true);
  } finally {
    env.restore();
  }
});

test('tts status uses singular and plural voice counts', async () => {
  for (const [count, label] of [[0, '0 voices'], [1, '1 voice'], [2, '2 voices']]) {
    const voices = Array.from({ length: count }, (_, index) => ({ id: `v${index}`, installed: true }));
    const env = harness({ tts: { ready: true, voices } });
    try {
      await env.activate();
      assert.equal(env.document.querySelector('#settings-tts-status').textContent, `Offline speech ready · ${label}`);
    } finally {
      env.restore();
    }
  }
});

test('a pet install start refreshes once and ignores a second click', async () => {
  const pets = { supported: true, installed: false, install_active: false };
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  const env = harness({
    pets,
    onCall(method) {
      if (method !== 'settings.pets_install_start') return undefined;
      return gate.then(() => {
        pets.install_active = true;
        return {};
      });
    },
  });
  try {
    await env.activate();
    const button = env.document.querySelector('#settings-pet-action');
    assert.equal(button.textContent, 'Install');
    button.click();
    button.click();
    assert.equal(env.calls.filter((call) => call.method === 'settings.pets_install_start').length, 1);
    release();
    await flush();
    await flush();
    const action = env.document.querySelector('#settings-pet-action');
    assert.equal(action.disabled, true);
    assert.equal(action.textContent, 'Installing…');
    action.click();
    assert.equal(env.calls.filter((call) => call.method === 'settings.pets_install_start').length, 1);
  } finally {
    env.restore();
  }
});

test('an STT poll repaints only the speech card and keeps progress', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] });
  let running = false;
  const env = harness({
    onCall(method) {
      if (method === 'settings.stt_install_status') {
        return {
          ready: false,
          supported: true,
          engine_installed: true,
          running,
          models: [{ id: 'tiny', label: 'Tiny', installed: false }],
        };
      }
      if (method === 'settings.stt_install_start') {
        running = true;
        return {};
      }
      return undefined;
    },
  });
  try {
    await env.activate();
    const tts = env.document.querySelector('#settings-tts-card');
    tts.dataset.marker = 'keep';
    const stop = () => env.document.querySelector('[data-action="stt-stop"]');
    assert.equal(stop().hidden, true);
    const select = env.document.querySelector('#settings-stt-install-model');
    if (select.slim) select.slim.setSelected(['tiny']);
    else select.value = 'tiny';
    env.document.querySelector('[data-action="stt-install"]').click();
    await flush();
    emit('stt.install.progress', { phase: 'binary', message: 'Downloading whisper.cpp engine…', bytes_fetched: 10, bytes_total: 100 });
    assert.match(env.document.querySelector('#settings-stt-card').textContent, /Downloading whisper\.cpp engine/);
    t.mock.timers.tick(1500);
    await flush();
    assert.equal(stop().hidden, false);
    assert.match(env.document.querySelector('#settings-stt-card').textContent, /Downloading whisper\.cpp engine/);
    assert.equal(env.document.querySelector('#settings-tts-card').dataset.marker, 'keep');
    running = false;
    t.mock.timers.tick(1500);
    await flush();
    assert.equal(stop().hidden, true);
    assert.match(env.document.querySelector('#settings-stt-card').textContent, /Downloading whisper\.cpp engine/);
  } finally {
    t.mock.timers.reset();
    env.restore();
  }
});

test('cancelling speech install hides Stop and ignores a stale poll', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] });
  let running = false;
  let holdNextStatus = false;
  let releaseStatus = () => {};
  const env = harness({
    onCall(method) {
      if (method === 'settings.stt_install_status') {
        const status = {
          ready: false,
          supported: true,
          engine_installed: true,
          running,
          models: [{ id: 'tiny', label: 'Tiny', installed: false }],
        };
        if (holdNextStatus) {
          holdNextStatus = false;
          return new Promise((resolve) => {
            releaseStatus = () => resolve(status);
          });
        }
        return status;
      }
      if (method === 'settings.stt_install_start') {
        running = true;
        return {};
      }
      if (method === 'settings.stt_install_cancel') {
        running = false;
        return {};
      }
      return undefined;
    },
  });
  try {
    await env.activate();
    const stop = () => env.document.querySelector('[data-action="stt-stop"]');
    const select = env.document.querySelector('#settings-stt-install-model');
    if (select.slim) select.slim.setSelected(['tiny']);
    else select.value = 'tiny';
    env.document.querySelector('[data-action="stt-install"]').click();
    await flush();
    t.mock.timers.tick(1500);
    await flush();
    assert.equal(stop().hidden, false);
    holdNextStatus = true;
    t.mock.timers.tick(1500);
    stop().click();
    await flush();
    await flush();
    assert.equal(env.calls.filter((call) => call.method === 'settings.stt_install_cancel').length, 1);
    assert.equal(stop().hidden, true);
    assert.ok(env.document.querySelector('#settings-stt-card'));
    releaseStatus();
    await flush();
    assert.equal(stop().hidden, true);
    assert.ok(env.document.querySelector('#settings-stt-card'));
  } finally {
    releaseStatus();
    t.mock.timers.reset();
    env.restore();
  }
});

test('scroll spy marks the section nearest the top and a rail click wins', async () => {
  const env = harness();
  const view = env.document.defaultView;
  const instances = [];
  view.IntersectionObserver = class {
    constructor(callback, options) {
      this.callback = callback;
      this.root = options.root;
      this.disconnected = false;
      this.observe = () => {};
      this.disconnect = () => { this.disconnected = true; };
      instances.push(this);
    }
  };
  try {
    await env.activate();
    assert.equal(instances[0].root, env.document.querySelector('#settings-view'));
    const media = env.document.querySelector('#settings-panel-media');
    instances.at(-1).callback([{
      target: media,
      isIntersecting: true,
      intersectionRatio: 0.4,
      boundingClientRect: { top: 16 },
      rootBounds: { top: 0 },
    }]);
    assert.equal(env.document.querySelector('#settings-jump-media').getAttribute('aria-current'), 'location');
    assert.equal(env.document.querySelector('#settings-jump-overview').hasAttribute('aria-current'), false);
    const main = env.document.querySelector('.settings-main');
    main.style.overflowY = 'auto';
    env.document.querySelector('#settings-discard').click();
    assert.equal(instances[0].disconnected, true);
    assert.equal(instances.at(-1).root, main);
    env.document.querySelector('#settings-jump-agent').click();
    instances.at(-1).callback([{
      target: env.document.querySelector('#settings-panel-overview'),
      isIntersecting: true,
      intersectionRatio: 1,
      boundingClientRect: { top: 0 },
      rootBounds: { top: 0 },
    }]);
    assert.equal(env.document.querySelector('#settings-jump-agent').getAttribute('aria-current'), 'location');
    env.page.destroy();
    assert.equal(instances.at(-1).disconnected, true);
  } finally {
    env.restore();
  }
});

test('cancelling remote access does not save or stage a change', async () => {
  const env = harness();
  try {
    await env.activate();
    const box = env.document.querySelector('#settings-remote-access-enabled');
    box.checked = true;
    box.dispatchEvent(new env.document.defaultView.Event('change', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    const cancel = [...env.document.querySelectorAll('button')].find((button) => button.textContent === 'Cancel');
    cancel.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(env.calls.some((call) => call.method === 'settings.set'), false);
    assert.equal(env.document.querySelector('#settings-savebar').hidden, true);
  } finally {
    env.restore();
  }
});

test('the review bar masks staged secrets instead of printing them', async () => {
  const env = harness();
  try {
    await env.activate();
    const input = env.document.querySelector('[data-secret="web_search_brave_api_key"]');
    input.value = 'sk-shoulder-surf';
    input.dispatchEvent(new env.document.defaultView.Event('input', { bubbles: true }));
    env.document.querySelector('#settings-review-open').click();
    const review = env.document.querySelector('#settings-review');
    assert.equal(review.hidden, false);
    assert.doesNotMatch(review.textContent, /sk-shoulder-surf/, 'typed API key must never render plaintext');
    assert.match(review.textContent, /••••••••/);
    assert.ok(review.querySelector('[data-revert-secret]'));
  } finally {
    env.restore();
  }
});

// The settings page is mounted with a wrapped rpc client in production, so the
// pairing panel refresh is injected: load() hands it the saved remote flag,
// and without an injection no pairing management call may fire (the refresh
// uses the real rpc transport, which would hit fetch in tests).
test('load hands the saved remote flag to the injected pairing refresh', async () => {
  for (const enabled of [true, false]) {
    const calls = [];
    const env = harness({
      settings: { remote_access_enabled: enabled },
      pairingRefresh: (options) => {
        calls.push(options);
        return Promise.resolve();
      },
    });
    try {
      await env.activate();
      assert.deepEqual(calls, [{ enabled }]);
    } finally {
      env.restore();
    }
  }
});

test('load without an injected pairing refresh makes no pairing calls', async () => {
  const requested = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    requested.push(String(url));
    return { ok: true, status: 200, json: async () => ({ ok: true, result: {} }) };
  };
  const env = harness({ settings: { remote_access_enabled: true } });
  try {
    await env.activate();
    assert.equal(
      requested.filter((url) => url.includes('/rpc/pairing/')).length,
      0,
      `pairing management must stay quiet without an injected refresh, got: ${requested.join(', ')}`,
    );
  } finally {
    env.restore();
    globalThis.fetch = originalFetch;
  }
});

test('enabling remote access reveals a wired pairing panel without a sessions query', async () => {
  const calls = [];
  const env = harness({
    pairingRefresh: (options) => {
      calls.push(options);
      return Promise.resolve();
    },
  });
  try {
    await env.activate();
    assert.deepEqual(calls, [{ enabled: false }]);
    assert.equal(env.document.querySelector('#pairing-panel').hidden, true);
    const box = env.document.querySelector('#settings-remote-access-enabled');
    box.checked = true;
    box.dispatchEvent(new env.document.defaultView.Event('change', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    // Scope to dialog overlays — the plugins view has a 'Restart' button and a
    // static pet overlay with 'Install'/'Cancel' lives in index.html too.
    const restart = [...env.document.querySelectorAll('.ui-dialog-overlay button')]
      .find((button) => button.textContent === 'Restart');
    assert.ok(restart, 'the restart confirm dialog must be open');
    restart.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(env.calls.filter((call) => call.method === 'settings.set').length, 1);
    assert.equal(env.document.querySelector('#pairing-panel').hidden, false);
    // Toggling remote access restarts the backend, so the toggle reveals the
    // panel and refreshes it with populate:false — wiring buttons and the
    // address default without querying pairing.sessions.list mid-shutdown.
    // The next Settings activation re-runs load().
    assert.deepEqual(calls, [{ enabled: false }, { enabled: true, populate: false }]);
  } finally {
    env.restore();
  }
});

test('enabling remote access unhides the pairing panel even without an injected refresh', async () => {
  const env = harness();
  try {
    await env.activate();
    assert.equal(env.document.querySelector('#pairing-panel').hidden, true);
    const box = env.document.querySelector('#settings-remote-access-enabled');
    box.checked = true;
    box.dispatchEvent(new env.document.defaultView.Event('change', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    const restart = [...env.document.querySelectorAll('.ui-dialog-overlay button')]
      .find((button) => button.textContent === 'Restart');
    assert.ok(restart, 'the restart confirm dialog must be open');
    restart.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(env.document.querySelector('#pairing-panel').hidden, false);
  } finally {
    env.restore();
  }
});

test('typing a remote address stages remote_access_addresses in the save patch', async () => {
  const env = harness();
  try {
    await env.activate();
    const input = env.document.querySelector('#pairing-remote-address');
    input.value = 'https://192.168.1.10:10994';
    input.dispatchEvent(new env.document.defaultView.Event('input', { bubbles: true }));
    env.document.querySelector('#settings-save').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    const saved = env.calls.filter((call) => call.method === 'settings.set');
    assert.equal(saved.length, 1);
    assert.deepEqual(saved[0].params, { remote_access_addresses: ['https://192.168.1.10:10994'] });
  } finally {
    env.restore();
  }
});

// This test flips pairing.js module-level pairingHostOnly — keep it last.
test('a paired remote client keeps the Remote access section hidden across renders', async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    if (String(url) === '/rpc/pairing/sessions/list') {
      return {
        ok: false,
        status: 403,
        json: async () => ({ error: { code: 'PAIRING_UNAUTHORIZED', message: 'pairing management is local-only' } }),
      };
    }
    return { ok: true, status: 200, json: async () => ({ ok: true, result: {} }) };
  };
  const env = harness({
    settings: { remote_access_enabled: true },
    pairingRefresh: (options) => refreshPairingPanel(options),
  });
  try {
    await env.activate();
    await new Promise((resolve) => setTimeout(resolve, 0));
    await new Promise((resolve) => setTimeout(resolve, 0));
    const group = env.document.querySelector('#settings-remote-group');
    assert.equal(group.hidden, true, 'PAIRING_UNAUTHORIZED must hide the remote section');
    assert.equal(env.document.querySelector('#settings-jump-remote').hidden, true);
    // A search keystroke re-runs updateChrome; the group must stay hidden
    // instead of flickering back with controls that would only 403.
    const search = env.document.querySelector('#settings-search');
    search.value = 'remote';
    search.dispatchEvent(new env.document.defaultView.Event('input', { bubbles: true }));
    assert.equal(group.hidden, true, 'updateChrome must not resurrect a host-only section');
    // A full re-render (e.g. discard) re-runs renderAll; same expectation.
    env.document.querySelector('#settings-discard').click();
    assert.equal(group.hidden, true, 'renderAll must not resurrect a host-only section');
    assert.equal(env.document.querySelector('#settings-jump-remote').hidden, true);
  } finally {
    env.restore();
    globalThis.fetch = originalFetch;
  }
});
