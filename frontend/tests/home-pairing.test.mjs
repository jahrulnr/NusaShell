// Behavioral tests for the unpaired-remote Home + view-catch cleanup.
//
// When /plugins returns the structured PAIRING_REQUIRED error, Home must keep
// the launcher quiet (no error banner, no misleading "No plugins installed"
// state) while the pairing gate is the dominant UI. Genuine backend failures
// (5xx, network) keep the banner + Retry. Direct view catches that toast must
// suppress the expected PAIRING_REQUIRED error via the shared
// isPairingRequiredError predicate, without hiding genuine `unavailable` or
// unexpected errors.

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { isPairingRequiredError, clearPairingRequired } from '../js/rpc.js';
import { refresh as refreshHome } from '../js/views/home.js';
import { refreshAcpProviders } from '../js/views/providers-acp.js';

// Home view markup matching frontend/index.html (only the bits refresh/render
// touch). #toast-container is needed because providers-acp toasts on errors.
const HOME_HTML = `
<div class="plugin-load-error" id="plugin-load-error" hidden role="alert">
  <span class="plugin-load-error-icon">⚠</span>
  <span id="plugin-load-error-text">Could not load plugins.</span>
  <button type="button" class="mini-btn" id="plugin-load-retry">Retry</button>
</div>
<div class="app-grid" id="app-grid"></div>
<div id="toast-container" class="toast-container"></div>`;

function withDom(html, fn) {
  const dom = new JSDOM(html, { pretendToBeVisual: true });
  const prev = {
    document: globalThis.document,
    window: globalThis.window,
    MutationObserver: globalThis.MutationObserver,
    self: globalThis.self,
    requestAnimationFrame: globalThis.requestAnimationFrame,
    getComputedStyle: globalThis.getComputedStyle,
  };
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.self = dom.window;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.getComputedStyle = dom.window.getComputedStyle.bind(dom.window);
  return Promise.resolve()
    .then(() => fn(dom.window))
    .finally(() => {
      Object.assign(globalThis, prev);
      dom.window.close();
    });
}

function mockFetch(responder) {
  const original = globalThis.fetch;
  globalThis.fetch = async (url, opts) => responder(String(url), opts);
  return () => { globalThis.fetch = original; };
}

// /plugins success body shape (plugin_handler.handleList): { plugins: [...] }.
function pluginsResponse(plugins = []) {
  return { ok: true, status: 200, json: async () => ({ plugins }) };
}

// /plugins PAIRING_REQUIRED 401 envelope (auth middleware writePairingRequired).
function pairingRequiredResponse() {
  return {
    ok: false,
    status: 401,
    json: async () => ({ error: { code: 'PAIRING_REQUIRED', message: 'Device pairing required.' } }),
  };
}

function serverErrorResponse() {
  return { ok: false, status: 500, json: async () => ({ error: { message: 'boom' } }) };
}

// rpc() success envelope: { ok: true, result: {...} }.
function rpcOkResponse(result = {}) {
  return { ok: true, status: 200, json: async () => ({ ok: true, result }) };
}

// rpc() PAIRING_REQUIRED envelope (HTTP 401 from auth middleware).
function rpcPairingRequiredResponse() {
  return {
    ok: false,
    status: 401,
    json: async () => ({ ok: false, error: { code: 'PAIRING_REQUIRED', message: 'Device pairing required.' } }),
  };
}

function banner() { return document.getElementById('plugin-load-error'); }
function grid() { return document.getElementById('app-grid'); }
function emptyState() { return grid().querySelector('.app-grid-empty'); }
function toastCount() { return document.querySelectorAll('.toast').length; }

// ---------------------------------------------------------------------------
// isPairingRequiredError predicate (rpc.js)
// ---------------------------------------------------------------------------

test('isPairingRequiredError keys off the error code only', () => {
  const pairing = new Error('Device pairing required.');
  pairing.code = 'PAIRING_REQUIRED';
  const unavailable = new Error('Backend unreachable');
  unavailable.code = 'unavailable';
  assert.equal(isPairingRequiredError(pairing), true);
  assert.equal(isPairingRequiredError(unavailable), false, 'unavailable must not be suppressed');
  assert.equal(isPairingRequiredError(new Error('unexpected')), false);
  assert.equal(isPairingRequiredError(null), false);
  assert.equal(isPairingRequiredError(undefined), false);
});

// ---------------------------------------------------------------------------
// Home /plugins: PAIRING_REQUIRED keeps the launcher quiet
// ---------------------------------------------------------------------------

test('Home suppresses the banner and empty state on /plugins PAIRING_REQUIRED', async () => {
  clearPairingRequired();
  const restore = mockFetch(() => pairingRequiredResponse());
  try {
    await withDom(HOME_HTML, async () => {
      await refreshHome();
      assert.equal(banner().hidden, true, 'no error banner behind the pairing gate');
      assert.equal(grid().children.length, 0, 'launcher grid stays empty');
      assert.equal(emptyState(), null, 'no "No plugins installed" empty state');
    });
  } finally {
    restore();
    clearPairingRequired();
  }
});

test('Home keeps the banner + Retry empty state on /plugins 5xx', async () => {
  const restore = mockFetch(() => serverErrorResponse());
  try {
    await withDom(HOME_HTML, async () => {
      await refreshHome();
      assert.equal(banner().hidden, false, 'genuine 5xx must show the banner');
      assert.match(document.getElementById('plugin-load-error-text').textContent, /boom/, 'banner surfaces the backend error message');
      assert.notEqual(emptyState(), null, 'Retry empty state must remain for genuine failures');
      assert.match(emptyState().textContent, /Could not load plugins/);
    });
  } finally {
    restore();
  }
});

test('Home banner falls back to the HTTP status when the 5xx body has no message', async () => {
  const restore = mockFetch(() => ({ ok: false, status: 503, json: async () => ({}) }));
  try {
    await withDom(HOME_HTML, async () => {
      await refreshHome();
      assert.equal(banner().hidden, false);
      assert.match(document.getElementById('plugin-load-error-text').textContent, /HTTP 503/);
    });
  } finally {
    restore();
  }
});

test('Home keeps the banner on /plugins network failure', async () => {
  const restore = mockFetch(async () => { throw new TypeError('NetworkError'); });
  try {
    await withDom(HOME_HTML, async () => {
      await refreshHome();
      assert.equal(banner().hidden, false, 'network failure must show the banner');
      assert.notEqual(emptyState(), null);
    });
  } finally {
    restore();
  }
});

test('Home shows "No plugins installed" for a genuine empty 200 response', async () => {
  const restore = mockFetch(() => pluginsResponse([]));
  try {
    await withDom(HOME_HTML, async () => {
      await refreshHome();
      assert.equal(banner().hidden, true, 'no banner when the list loaded empty');
      assert.notEqual(emptyState(), null, 'empty 200 shows the installed-empty state');
      assert.match(emptyState().textContent, /No plugins installed/);
    });
  } finally {
    restore();
  }
});

test('Home recovers from PAIRING_REQUIRED to a successful plugin load', async () => {
  clearPairingRequired();
  let calls = 0;
  const restore = mockFetch(() => {
    calls += 1;
    return calls === 1 ? pairingRequiredResponse() : pluginsResponse([{ id: 'notes', name: 'Notes', hasUI: true }]);
  });
  try {
    await withDom(HOME_HTML, async () => {
      await refreshHome();
      assert.equal(banner().hidden, true);
      assert.equal(grid().children.length, 0, 'quiet while pairing required');
      // After pairing succeeds, /plugins loads and the launcher renders tiles.
      await refreshHome();
      assert.equal(banner().hidden, true);
      assert.notEqual(grid().querySelector('.app-cell'), null, 'plugin tile renders after recovery');
      assert.equal(emptyState(), null);
    });
  } finally {
    restore();
    clearPairingRequired();
  }
});

// ---------------------------------------------------------------------------
// providers-acp refresh: direct view catch suppresses PAIRING_REQUIRED toast
// ---------------------------------------------------------------------------

test('providers-acp refresh does not toast on PAIRING_REQUIRED', async () => {
  clearPairingRequired();
  const restore = mockFetch(() => rpcPairingRequiredResponse());
  try {
    await withDom(HOME_HTML, async () => {
      await refreshAcpProviders();
      assert.equal(toastCount(), 0, 'PAIRING_REQUIRED must not toast behind the gate');
    });
  } finally {
    restore();
    clearPairingRequired();
  }
});

test('providers-acp refresh still toasts on a genuine unavailable error', async () => {
  clearPairingRequired();
  const restore = mockFetch(async () => { throw new TypeError('NetworkError'); });
  try {
    await withDom(HOME_HTML, async () => {
      await refreshAcpProviders();
      assert.equal(toastCount(), 1, 'genuine unavailable must still toast');
      assert.match(document.querySelector('.toast').textContent, /Backend unreachable/);
    });
  } finally {
    restore();
    clearPairingRequired();
  }
});

test('providers-acp refresh still toasts on an unexpected RPC error', async () => {
  clearPairingRequired();
  const restore = mockFetch(() => ({
    ok: true,
    status: 200,
    json: async () => ({ ok: false, error: { code: 'ACP_RUNTIME_DOWN', message: 'runtime down' } }),
  }));
  try {
    await withDom(HOME_HTML, async () => {
      await refreshAcpProviders();
      assert.equal(toastCount(), 1, 'unexpected error must still toast');
    });
  } finally {
    restore();
    clearPairingRequired();
  }
});
