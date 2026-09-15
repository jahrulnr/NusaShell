// Behavioral tests for PAIRING_REQUIRED suppression.
//
// When the backend rejects a remote client with PAIRING_REQUIRED, the
// pairing gate must become the dominant user-facing state. These tests
// exercise the shared rpc/app/offline-screen seams — not source regexes —
// to verify:
//   - rpc.js sets a shared pairing-required flag and emits rpc:error.
//   - the generic offline overlay is suppressed while pairing is required.
//   - genuine backend-unreachable errors still show the offline overlay.
//   - the pairing gate shows (and is focused) on rpc:error PAIRING_REQUIRED.
//   - an invalid challenge shows a clear error inside the gate, not the
//     offline overlay.

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import {
  rpc,
  on,
  off,
  isPairingRequired,
  clearPairingRequired,
  isRemoteAccessDisabled,
  clearRemoteAccessDisabled,
} from '../js/rpc.js';
import { initOfflineScreen } from '../js/offline-screen.js';
import { initPairingGate } from '../js/pairing.js';

// Markup shared by the offline overlay + pairing gate, matching index.html.
const OVERLAY_HTML = `
<div class="offline-screen" id="offline-screen" role="status" aria-live="polite" hidden>
  <h2>Sorry, it looks like your agent is offline.</h2>
  <button class="offline-retry-btn" id="offline-retry-btn" type="button">Try again</button>
</div>
<div class="pairing-gate" id="pairing-gate" role="dialog" aria-modal="true" aria-labelledby="pairing-gate-title" hidden>
  <div class="pairing-gate-card">
    <h2 id="pairing-gate-title" tabindex="-1">Device pairing required</h2>
    <div id="pairing-pending" hidden>
      <div class="pairing-status" id="pairing-status" data-state="pending" role="status" aria-live="polite">Waiting</div>
      <p class="pairing-countdown" id="pairing-countdown" aria-live="polite"></p>
    </div>
    <div id="pairing-success" class="pairing-success" hidden><p>paired</p></div>
    <div id="pairing-error" class="pairing-error" role="alert" hidden>
      <p id="pairing-error-message"></p>
      <button class="action-btn" id="pairing-retry-btn" type="button">Try again</button>
    </div>
    <button class="link-btn" id="pairing-help-btn" type="button" aria-expanded="false" aria-controls="pairing-help">Need help?</button>
    <div id="pairing-help" class="pairing-help" hidden></div>
  </div>
</div>`;

function mockFetch(responder) {
  const original = globalThis.fetch;
  globalThis.fetch = async (url, opts) => responder(String(url), opts);
  return () => { globalThis.fetch = original; };
}

// A PAIRING_REQUIRED HTTP 401 response, as the backend returns it.
function pairingRequiredResponse() {
  return {
    ok: false,
    status: 401,
    json: async () => ({ error: { code: 'PAIRING_REQUIRED', message: 'Device pairing required' } }),
  };
}

function remoteAccessDisabledResponse() {
  return {
    ok: false,
    status: 403,
    json: async () => ({ error: { code: 'REMOTE_ACCESS_DISABLED', message: 'Remote access is disabled' } }),
  };
}

// A successful RPC response.
function okResponse(result = {}) {
  return { ok: true, status: 200, json: async () => ({ ok: true, result }) };
}

// Install a minimal fake timer that captures setInterval callbacks so tests
// can fire the pairing status poll deterministically instead of waiting
// 1500ms. setTimeout stays real so the gate's focus delay still runs.
function installFakeIntervals() {
  const intervals = new Map();
  let nextId = 1;
  const original = {
    setInterval: globalThis.setInterval,
    clearInterval: globalThis.clearInterval,
  };
  globalThis.setInterval = (fn) => {
    const id = nextId++;
    intervals.set(id, fn);
    return id;
  };
  globalThis.clearInterval = (id) => { intervals.delete(id); };
  return {
    async fire() {
      const fns = [...intervals.values()];
      await Promise.all(fns.map((fn) => Promise.resolve().then(fn)));
    },
    restore() { Object.assign(globalThis, original); },
  };
}

function withDom(html, url, fn) {
  const dom = new JSDOM(html, { url: url || 'https://localhost/' });
  const prev = {
    document: globalThis.document,
    window: globalThis.window,
    location: globalThis.location,
  };
  globalThis.document = dom.window.document;
  globalThis.window = dom.window;
  globalThis.location = dom.window.location;
  return Promise.resolve()
    .then(() => fn(dom.window))
    .finally(() => {
      Object.assign(globalThis, prev);
      dom.window.close();
    });
}

function dispatchStatus(window, status) {
  window.dispatchEvent(new window.CustomEvent('nusashell:connection-status', { detail: { status } }));
}

// ---------------------------------------------------------------------------
// rpc.js: shared pairing-required flag + rpc:error event
// ---------------------------------------------------------------------------

test('rpc sets isPairingRequired and emits rpc:error on PAIRING_REQUIRED', async () => {
  clearPairingRequired();
  const restore = mockFetch(() => pairingRequiredResponse());
  let event = null;
  const handler = (detail) => { event = detail; };
  on('rpc:error', handler);
  try {
    await assert.rejects(() => rpc('app.info'), (err) => {
      assert.equal(err.code, 'PAIRING_REQUIRED');
      return true;
    });
    assert.equal(isPairingRequired(), true, 'flag should be set after PAIRING_REQUIRED');
    assert.deepEqual(event, { code: 'PAIRING_REQUIRED', method: 'app.info' });
  } finally {
    off('rpc:error', handler);
    restore();
    clearPairingRequired();
  }
});

test('rpc clears isPairingRequired on the next successful response', async () => {
  clearPairingRequired();
  const restore = mockFetch((url) =>
    url.startsWith('/rpc/') ? pairingRequiredResponse() : okResponse({ version: '1.0' }),
  );
  try {
    await assert.rejects(() => rpc('app.info'));
    assert.equal(isPairingRequired(), true);
    // Next call succeeds (e.g. after pairing completed and session is valid).
    restore();
    const restore2 = mockFetch(() => okResponse({ version: '1.0' }));
    const info = await rpc('app.info');
    assert.equal(info.version, '1.0');
    assert.equal(isPairingRequired(), false, 'flag should clear on success');
    restore2();
  } finally {
    clearPairingRequired();
  }
});

test('rpc does not set isPairingRequired for genuine backend-unreachable', async () => {
  clearPairingRequired();
  const restore = mockFetch(async () => { throw new TypeError('NetworkError'); });
  try {
    await assert.rejects(() => rpc('app.info'), { code: 'unavailable' });
    assert.equal(isPairingRequired(), false, 'unavailable must not set pairing flag');
  } finally {
    restore();
    clearPairingRequired();
  }
});

test('remote access disabled is explicit and suppresses the offline overlay', async () => {
  clearPairingRequired();
  clearRemoteAccessDisabled();
  const restore = mockFetch(() => remoteAccessDisabledResponse());
  try {
    await withDom(OVERLAY_HTML, 'https://localhost/', async (window) => {
      initOfflineScreen();
      initPairingGate();
      await assert.rejects(() => rpc('app.info'), (err) => {
        assert.equal(err.code, 'REMOTE_ACCESS_DISABLED');
        return true;
      });
      assert.equal(isPairingRequired(), false);
      assert.equal(isRemoteAccessDisabled(), true);
      dispatchStatus(window, 'offline');
      assert.equal(document.getElementById('offline-screen').hidden, true);
      assert.equal(document.getElementById('pairing-gate').hidden, false);
      assert.match(document.getElementById('pairing-gate-title').textContent, /disabled/i);
    });
  } finally {
    restore();
    clearPairingRequired();
    clearRemoteAccessDisabled();
  }
});

// ---------------------------------------------------------------------------
// offline-screen.js: overlay suppression while pairing is required
// ---------------------------------------------------------------------------

test('offline overlay is suppressed on immediate offline status while pairing required', async () => {
  clearPairingRequired();
  const restore = mockFetch(() => pairingRequiredResponse());
  try {
    // Trigger PAIRING_REQUIRED so the shared flag is set (as app.info would).
    await assert.rejects(() => rpc('app.info'));
    assert.equal(isPairingRequired(), true);

    await withDom(OVERLAY_HTML, 'https://localhost/', async (window) => {
      initOfflineScreen();
      const screen = document.getElementById('offline-screen');
      // The boot HTTP probe would set 'offline'; it must be suppressed.
      dispatchStatus(window, 'offline');
      assert.equal(screen.hidden, true, 'offline overlay must stay hidden while pairing required');
    });
  } finally {
    restore();
    clearPairingRequired();
  }
});

test('offline overlay shows on immediate offline status when pairing is NOT required', async () => {
  clearPairingRequired();
  await withDom(OVERLAY_HTML, 'https://localhost/', async (window) => {
    initOfflineScreen();
    const screen = document.getElementById('offline-screen');
    dispatchStatus(window, 'offline');
    assert.equal(screen.hidden, false, 'genuine offline must still show the overlay');
  });
});

test('offline overlay shows immediately when an RPC reports the backend is unreachable', async () => {
  clearPairingRequired();
  await withDom(OVERLAY_HTML, 'https://localhost/', async (window) => {
    initOfflineScreen();
    const restore = mockFetch(() => ({
      ok: false,
      status: 502,
      json: async () => ({ error: { code: 'proxy_error', message: 'connect ECONNREFUSED 127.0.0.1:10994' } }),
    }));
    try {
      await assert.rejects(() => rpc('agent.conversations.list'), { code: 'unavailable' });
      assert.equal(document.getElementById('offline-screen').hidden, false, 'request failures must surface the offline overlay');
    } finally {
      restore();
    }
  });
});

test('offline overlay hides after a later RPC confirms the backend is reachable', async () => {
  clearPairingRequired();
  await withDom(OVERLAY_HTML, 'https://localhost/', async (window) => {
    initOfflineScreen();
    dispatchStatus(window, 'offline');
    assert.equal(document.getElementById('offline-screen').hidden, false);
    const restore = mockFetch(() => okResponse({ ok: true }));
    try {
      await rpc('app.info');
      assert.equal(document.getElementById('offline-screen').hidden, true, 'a successful RPC should clear the offline overlay');
    } finally {
      restore();
    }
  });
});

test('offline overlay grace timer is suppressed when pairing becomes required before fire', async () => {
  clearPairingRequired();
  await withDom(OVERLAY_HTML, 'https://localhost/', async (window) => {
    initOfflineScreen({ graceMs: 5 });
    const screen = document.getElementById('offline-screen');
    // WS closes (graced) before app.info has returned: start the grace timer.
    dispatchStatus(window, 'closed');
    assert.equal(screen.hidden, true, 'graced closed must not show immediately');
    // Now app.info returns PAIRING_REQUIRED (sets the flag) before the timer fires.
    const restore = mockFetch(() => pairingRequiredResponse());
    try {
      await assert.rejects(() => rpc('app.info'));
      assert.equal(isPairingRequired(), true);
      // Wait past the grace window; the timer callback must re-check and suppress.
      await new Promise((r) => setTimeout(r, 30));
      assert.equal(screen.hidden, true, 'overlay must stay hidden once pairing is required');
    } finally {
      restore();
      clearPairingRequired();
    }
  });
});

test('offline overlay shows after grace when pairing is NOT required', async () => {
  clearPairingRequired();
  await withDom(OVERLAY_HTML, 'https://localhost/', async (window) => {
    initOfflineScreen({ graceMs: 5 });
    const screen = document.getElementById('offline-screen');
    dispatchStatus(window, 'closed');
    await new Promise((r) => setTimeout(r, 30));
    assert.equal(screen.hidden, false, 'graced closed must show the overlay when not pairing');
  });
});

// ---------------------------------------------------------------------------
// pairing gate: shows + focuses on rpc:error PAIRING_REQUIRED, overlay hidden
// ---------------------------------------------------------------------------

test('pairing gate shows and offline overlay stays hidden on rpc:error PAIRING_REQUIRED', async () => {
  clearPairingRequired();
  await withDom(OVERLAY_HTML, 'https://localhost/', async (window) => {
    initPairingGate();
    const gate = document.getElementById('pairing-gate');
    const screen = document.getElementById('offline-screen');
    const title = document.getElementById('pairing-gate-title');
    assert.equal(gate.hidden, true, 'gate starts hidden');

    // Simulate the boot sequence: app.info fails with PAIRING_REQUIRED,
    // which sets the flag and emits rpc:error.
    const restore = mockFetch(() => pairingRequiredResponse());
    try {
      await assert.rejects(() => rpc('app.info'));
    } finally {
      restore();
    }

    // The gate listener shows the gate synchronously on rpc:error.
    assert.equal(gate.hidden, false, 'gate must be shown on PAIRING_REQUIRED');
    // The offline overlay must remain hidden (suppressed by the flag).
    assert.equal(isPairingRequired(), true);
    dispatchStatus(window, 'offline');
    assert.equal(screen.hidden, true, 'offline overlay must stay hidden behind the gate');

    // Focus lands on the visible gate title (accessibility), not the hidden
    // retry button, after the gate's focus delay.
    await new Promise((r) => setTimeout(r, 80));
    assert.equal(window.document.activeElement, title, 'gate title must receive focus');
  });
  clearPairingRequired();
});

// ---------------------------------------------------------------------------
// Invalid challenge: clear error inside the gate, not the offline overlay
// ---------------------------------------------------------------------------

test('invalid challenge shows a clear error inside the gate, not the offline overlay', async () => {
  clearPairingRequired();
  const timers = installFakeIntervals();
  // /rpc/* returns PAIRING_REQUIRED (sets the flag as app.info would);
  // /pairing/status returns PAIRING_NOT_FOUND (invalid challenge).
  const restore = mockFetch((url) => {
    if (url.startsWith('/pairing/status')) {
      return { ok: true, status: 200, json: async () => ({ ok: false, error: { code: 'PAIRING_NOT_FOUND' } }) };
    }
    return pairingRequiredResponse();
  });
  try {
    await withDom(
      OVERLAY_HTML,
      'https://localhost/?pairing_challenge=abc&pairing_code=123',
      async (window) => {
        initPairingGate();
        // Set the pairing-required flag as the boot app.info probe would.
        await assert.rejects(() => rpc('app.info'));
        assert.equal(isPairingRequired(), true);

        const gate = document.getElementById('pairing-gate');
        const errorEl = document.getElementById('pairing-error');
        const errorMsg = document.getElementById('pairing-error-message');
        const retryBtn = document.getElementById('pairing-retry-btn');
        const screen = document.getElementById('offline-screen');

        assert.equal(gate.hidden, false, 'gate must be shown for the remote waiting flow');
        // Fire the status poll (the remote polls /pairing/status).
        await timers.fire();
        assert.equal(errorEl.hidden, false, 'gate error must be shown for an invalid challenge');
        assert.match(errorMsg.textContent, /no longer valid/i, 'gate must explain the invalid link');
        assert.equal(retryBtn.hidden, true, 'an invalid link must not offer a misleading retry button');
        // The offline overlay must stay hidden — the backend is reachable,
        // it just requires pairing.
        dispatchStatus(window, 'offline');
        assert.equal(screen.hidden, true, 'offline overlay must stay hidden for an invalid challenge');
        // Let the gate's focus setTimeout (50ms) settle while the jsdom
        // globals are still mounted so it does not race teardown.
        await new Promise((r) => setTimeout(r, 80));
      },
    );
  } finally {
    restore();
    timers.restore();
    clearPairingRequired();
  }
});
