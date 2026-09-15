// Behavioral tests for the Remote access panel on remote clients.
//
// Pairing management RPCs are loopback-only: a paired remote device gets
// PAIRING_UNAUTHORIZED. The panel must hide the whole section (group + nav
// jump link) instead of showing dead controls and permission-error toasts.
// Separately, a loopback pairing address gets a non-blocking warning since
// the QR would be unreachable from a remote device.
//
// This lives in its own file because pairing.js keeps module-level state
// (pairingHostOnly) that must not leak between tests.

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { refreshPairingPanel } from '../js/pairing.js';

// Minimal markup for the Settings Remote access section, matching
// index.html structure and IDs.
const SETTINGS_HTML = `
<button class="settings-section-link" id="settings-jump-remote" type="button" data-settings-section="settings-group-remote">Remote access</button>
<div class="settings-group" id="settings-remote-group" role="group" aria-labelledby="settings-group-remote">
  <h2 class="settings-group-title" id="settings-group-remote" tabindex="-1">Remote access</h2>
  <div id="pairing-panel" class="pairing-panel">
    <button class="action-btn" id="pairing-create-btn" type="button">Generate pairing link</button>
    <div class="pairing-panel-status-row">
      <div class="pairing-host-status" id="pairing-panel-status" role="status" aria-live="polite" hidden></div>
      <button class="mini-btn" id="pairing-show-qr-btn" type="button" hidden>Show pairing QR</button>
    </div>
    <label class="settings-field" for="pairing-remote-address">Address remote devices can reach
      <textarea id="pairing-remote-address"></textarea>
      <span class="pairing-address-error" id="pairing-address-error" role="alert" hidden></span>
    </label>
    <div class="pairing-session-actions" id="pairing-session-actions">
      <div class="pairing-sessions-list" id="pairing-sessions-list"></div>
      <div class="pairing-sessions-empty" id="pairing-sessions-empty">No paired devices.</div>
      <button class="mini-btn" id="pairing-revoke-all-btn" type="button" hidden>Revoke all sessions</button>
    </div>
  </div>
</div>`;

// Body-level approval dialog used by the host. It owns the pairing QR: the
// dialog covers the Settings panel, so the scannable QR, link, and manual code
// must live inside it. It stays outside the hidden settings panel so the
// prompt can appear regardless of scroll position.
const APPROVAL_POPUP_HTML = `
<div class="ui-dialog-overlay pairing-approval-overlay" id="pairing-approval-overlay" role="dialog" aria-modal="true" aria-labelledby="pairing-approval-title" hidden>
  <div class="ui-dialog pairing-approval-dialog">
    <h2 id="pairing-approval-title">Approve device pairing</h2>
    <p id="pairing-approval-message">Scan this QR code with the remote device, then approve the request.</p>
    <div class="pairing-qr-display" id="pairing-qr-display" hidden>
      <div class="pairing-qr-wrap">
        <img id="pairing-qr-img" alt="QR" />
        <p class="pairing-qr-status" id="pairing-qr-status"></p>
      </div>
      <div class="pairing-link-section">
        <code id="pairing-link-display"></code>
        <button class="mini-btn" id="pairing-copy-link-btn" type="button">Copy link</button>
        <code id="pairing-code-display"></code>
        <button class="mini-btn" id="pairing-copy-code-btn" type="button">Copy code</button>
      </div>
    </div>
    <div id="pairing-qr-alternatives"></div>
    <div class="pairing-host-status" id="pairing-host-status" role="status" aria-live="polite"></div>
    <div class="pairing-host-actions" id="pairing-host-actions">
      <label for="pairing-device-label">Device label</label>
      <input id="pairing-device-label" type="text" />
      <button id="pairing-approve-btn" type="button">Approve device</button>
      <button id="pairing-reject-btn" type="button">Reject</button>
    </div>
    <button id="pairing-approval-close" type="button">Later</button>
  </div>
</div>`;

function mockFetch(responder) {
  const original = globalThis.fetch;
  globalThis.fetch = async (url, opts) => responder(String(url), opts);
  return () => { globalThis.fetch = original; };
}

function okResponse(result = {}) {
  return { ok: true, status: 200, json: async () => ({ ok: true, result }) };
}

// A 403 PAIRING_UNAUTHORIZED response, as pairing management methods return
// to a paired remote session.
function unauthorizedResponse() {
  return {
    ok: false,
    status: 403,
    json: async () => ({ error: { code: 'PAIRING_UNAUTHORIZED', message: 'pairing management is local-only' } }),
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

// Capture setInterval callbacks so poll loops can be fired deterministically
// (or just prevented from hitting the network after fetch is restored).
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
    count: () => intervals.size,
    async fire() {
      const fns = [...intervals.values()];
      await Promise.all(fns.map((fn) => Promise.resolve().then(fn)));
    },
    restore() { Object.assign(globalThis, original); },
  };
}

// The loopback warning must run while pairingHostOnly is still false, so it
// comes first in this file.
test('loopback remote address warns but still generates the pairing link', async () => {
  const timers = installFakeIntervals();
  const restore = mockFetch((url) => {
    if (url === '/rpc/pairing/sessions/list') return okResponse({ sessions: [] });
    if (url === '/rpc/pairing/challenge/create') {
      return okResponse({ challenge_id: 'pair_x', code: 'ABCD2345', expires_in: 300 });
    }
    return okResponse({});
  });
  try {
    await withDom(SETTINGS_HTML + APPROVAL_POPUP_HTML, 'http://127.0.0.1:10994/', async () => {
      await refreshPairingPanel();
      const addr = document.getElementById('pairing-remote-address');
      addr.value = 'http://127.0.0.1:10994';
      document.getElementById('pairing-create-btn').click();
      // startHostPairing is async — let the microtask chain settle.
      await new Promise((r) => setTimeout(r, 20));
      const errEl = document.getElementById('pairing-address-error');
      assert.equal(errEl.hidden, false, 'loopback address should show a notice');
      assert.equal(errEl.dataset.severity, 'warning', 'loopback notice should be a warning, not a blocking error');
      assert.match(errEl.textContent, /loopback/i);
      const link = document.getElementById('pairing-link-display');
      assert.match(link.textContent, /pairing_challenge=pair_x/, 'challenge should still be created');
      assert.match(link.textContent, /127\.0\.0\.1/);
    });
  } finally {
    restore();
    timers.restore();
  }
});

test('pending approval opens the host popup', async () => {
  const timers = installFakeIntervals();
  const restore = mockFetch((url) => {
    if (url === '/rpc/pairing/sessions/list') return okResponse({ sessions: [] });
    if (url === '/rpc/pairing/challenge/create') {
      return okResponse({ challenge_id: 'pair_popup', code: 'ABCD2345', expires_in: 300 });
    }
    return okResponse({});
  });
  try {
    await withDom(SETTINGS_HTML + APPROVAL_POPUP_HTML, 'http://127.0.0.1:10994/', async () => {
      await refreshPairingPanel();
      document.getElementById('pairing-create-btn').click();
      await new Promise((r) => setTimeout(r, 20));
      assert.equal(document.getElementById('pairing-approval-overlay').hidden, false);
      assert.equal(document.getElementById('pairing-approve-btn').disabled, false);
    });
  } finally {
    restore();
    timers.restore();
  }
});

test('generating a pairing link renders a scannable QR inside the approval dialog', async () => {
  const timers = installFakeIntervals();
  const restore = mockFetch((url) => {
    if (url === '/rpc/pairing/sessions/list') return okResponse({ sessions: [] });
    if (url === '/rpc/pairing/challenge/create') {
      return okResponse({ challenge_id: 'pair_qr', code: 'ABCD2345', expires_in: 300 });
    }
    return okResponse({});
  });
  try {
    await withDom(SETTINGS_HTML + APPROVAL_POPUP_HTML, 'http://127.0.0.1:10994/', async () => {
      await refreshPairingPanel();
      document.getElementById('pairing-create-btn').click();
      await new Promise((r) => setTimeout(r, 20));
      const overlay = document.getElementById('pairing-approval-overlay');
      const qrDisplay = document.getElementById('pairing-qr-display');
      const qrImg = document.getElementById('pairing-qr-img');
      assert.equal(overlay.hidden, false, 'the dialog must own the scan surface');
      assert.ok(overlay.contains(qrDisplay), 'the QR must live inside the dialog, not behind its overlay');
      assert.equal(qrDisplay.hidden, false, 'the QR block must be visible once a challenge exists');
      assert.match(qrImg.src, /^data:image\//, 'the QR must be rendered client-side');
      assert.match(qrImg.alt, /pair_qr/, 'the QR alt text must name the encoded pairing URL');
      assert.match(document.getElementById('pairing-link-display').textContent, /pairing_challenge=pair_qr/);
      assert.equal(document.getElementById('pairing-code-display').textContent, 'ABCD2345');
      assert.equal(
        document.getElementById('pairing-panel').querySelector('#pairing-qr-img'),
        null,
        'the panel must not keep a second QR behind the dialog',
      );
    });
  } finally {
    restore();
    timers.restore();
  }
});

test('closing and reopening the pairing dialog reuses the same challenge', async () => {
  const timers = installFakeIntervals();
  let creates = 0;
  const restore = mockFetch((url) => {
    if (url === '/rpc/pairing/sessions/list') return okResponse({ sessions: [] });
    if (url === '/rpc/pairing/challenge/create') {
      creates += 1;
      return okResponse({ challenge_id: 'pair_reopen', code: 'CODE1234', expires_in: 300 });
    }
    return okResponse({});
  });
  try {
    await withDom(SETTINGS_HTML + APPROVAL_POPUP_HTML, 'http://127.0.0.1:10994/', async () => {
      await refreshPairingPanel();
      document.getElementById('pairing-create-btn').click();
      await new Promise((r) => setTimeout(r, 20));
      const overlay = document.getElementById('pairing-approval-overlay');
      const qrSrc = document.getElementById('pairing-qr-img').src;
      // While the dialog is open it is the only live pairing status region.
      assert.equal(document.getElementById('pairing-panel-status').getAttribute('aria-live'), 'off');

      document.getElementById('pairing-approval-close').click();
      assert.equal(overlay.hidden, true, 'closing the dialog must hide it');
      assert.equal(
        document.getElementById('pairing-panel-status').getAttribute('aria-live'),
        'polite',
        'the panel summary announces again once the dialog is closed',
      );

      const reopenBtn = document.getElementById('pairing-show-qr-btn');
      assert.equal(reopenBtn.hidden, false, 'a pending challenge must offer a way back to the QR');
      reopenBtn.click();
      await new Promise((r) => setTimeout(r, 20));
      assert.equal(overlay.hidden, false, 'reopen must show the dialog again');
      assert.equal(document.getElementById('pairing-qr-img').src, qrSrc, 'reopen must reuse the same QR');
      assert.equal(creates, 1, 'reopening must not create a second challenge');
    });
  } finally {
    restore();
    timers.restore();
  }
});

test('the panel summary tracks the challenge state and stops offering a dead QR', async () => {
  const timers = installFakeIntervals();
  const restore = mockFetch((url) => {
    if (url === '/rpc/pairing/sessions/list') return okResponse({ sessions: [] });
    if (url === '/rpc/pairing/challenge/create') {
      return okResponse({ challenge_id: 'pair_done', code: 'ABCD2345', expires_in: 300 });
    }
    if (url === '/rpc/pairing/status') return okResponse({ state: 'used', expires_in: 0 });
    return okResponse({});
  });
  try {
    await withDom(SETTINGS_HTML + APPROVAL_POPUP_HTML, 'http://127.0.0.1:10994/', async () => {
      await refreshPairingPanel();
      document.getElementById('pairing-create-btn').click();
      await new Promise((r) => setTimeout(r, 20));
      const panelStatus = document.getElementById('pairing-panel-status');
      assert.equal(panelStatus.hidden, false, 'generating a link must publish a summary in the panel');
      assert.match(panelStatus.textContent, /Waiting for a remote device/);

      await timers.fire();
      assert.match(panelStatus.textContent, /Paired successfully/);
      assert.equal(panelStatus.dataset.state, 'used');
      assert.equal(
        document.getElementById('pairing-show-qr-btn').hidden,
        true,
        'a consumed link must not offer a QR reopen',
      );
      assert.equal(
        document.getElementById('pairing-approval-overlay').hidden,
        true,
        'a consumed link must close the dialog',
      );
    });
  } finally {
    restore();
    timers.restore();
  }
});

test('revoked sessions are omitted from the paired devices list', async () => {
  const restore = mockFetch((url) => {
    if (url === '/rpc/pairing/sessions/list') {
      return okResponse({ sessions: [
        {
          id: 'sess_active',
          device_label: 'Laptop',
          created_at: '2026-09-14T00:00:00Z',
          last_seen_at: '2026-09-14T00:00:00Z',
          revoked: false,
        },
        {
          id: 'sess_revoked',
          device_label: 'Old phone',
          created_at: '2026-09-13T00:00:00Z',
          last_seen_at: '2026-09-13T00:00:00Z',
          revoked: true,
        },
      ] });
    }
    return okResponse({});
  });
  try {
    await withDom(SETTINGS_HTML, 'http://127.0.0.1:10994/', async () => {
      await refreshPairingPanel();
      const rows = document.querySelectorAll('#pairing-sessions-list .pairing-session-row');
      assert.equal(rows.length, 1);
      assert.match(rows[0].textContent, /Laptop/);
      assert.doesNotMatch(document.getElementById('pairing-sessions-list').textContent, /Revoked|Old phone/);
      assert.equal(document.getElementById('pairing-sessions-empty').hidden, true);
    });
  } finally {
    restore();
  }
});

// Regression: a LAN address typed while the listener is loopback-only used
// to produce a QR/link that silently timed out on the remote device.
test('LAN address warns when the listener is loopback-only', async () => {
  const timers = installFakeIntervals();
  const restore = mockFetch((url) => {
    if (url === '/rpc/pairing/sessions/list') return okResponse({ sessions: [] });
    if (url === '/rpc/app/info') return okResponse({ name: 'NusaShell', listen_addr: '127.0.0.1:10994' });
    if (url === '/rpc/pairing/challenge/create') {
      return okResponse({ challenge_id: 'pair_lan', code: 'ABCD2345', expires_in: 300 });
    }
    return okResponse({});
  });
  try {
    await withDom(SETTINGS_HTML, 'http://127.0.0.1:10994/', async () => {
      await refreshPairingPanel();
      document.getElementById('pairing-remote-address').value = 'http://192.168.18.81:10994';
      document.getElementById('pairing-create-btn').click();
      await new Promise((r) => setTimeout(r, 20));
      const errEl = document.getElementById('pairing-address-error');
      assert.equal(errEl.hidden, false, 'loopback-only listener should warn for a LAN link');
      assert.equal(errEl.dataset.severity, 'warning', 'must warn, not block — tunnels/proxies are legit');
      assert.match(errEl.textContent, /only listening on 127\.0\.0\.1:10994/);
    });
  } finally {
    restore();
    timers.restore();
  }
});

test('LAN address does not warn when the listener binds all interfaces', async () => {
  const timers = installFakeIntervals();
  const restore = mockFetch((url) => {
    if (url === '/rpc/pairing/sessions/list') return okResponse({ sessions: [] });
    if (url === '/rpc/app/info') return okResponse({ name: 'NusaShell', listen_addr: '0.0.0.0:10994' });
    if (url === '/rpc/pairing/challenge/create') {
      return okResponse({ challenge_id: 'pair_any', code: 'ABCD2345', expires_in: 300 });
    }
    return okResponse({});
  });
  try {
    await withDom(SETTINGS_HTML, 'http://127.0.0.1:10994/', async () => {
      await refreshPairingPanel();
      document.getElementById('pairing-remote-address').value = 'http://192.168.18.81:10994';
      document.getElementById('pairing-create-btn').click();
      await new Promise((r) => setTimeout(r, 20));
      assert.equal(document.getElementById('pairing-address-error').hidden, true);
    });
  } finally {
    restore();
    timers.restore();
  }
});

test('non-loopback remote address does not warn', async () => {
  const timers = installFakeIntervals();
  const restore = mockFetch((url) => {
    if (url === '/rpc/pairing/sessions/list') return okResponse({ sessions: [] });
    if (url === '/rpc/pairing/challenge/create') {
      return okResponse({ challenge_id: 'pair_y', code: 'ABCD2345', expires_in: 300 });
    }
    return okResponse({});
  });
  try {
    await withDom(SETTINGS_HTML, 'http://127.0.0.1:10994/', async () => {
      await refreshPairingPanel();
      document.getElementById('pairing-remote-address').value = 'http://192.168.1.10:10994';
      document.getElementById('pairing-create-btn').click();
      await new Promise((r) => setTimeout(r, 20));
      const errEl = document.getElementById('pairing-address-error');
      assert.equal(errEl.hidden, true, 'LAN address should not warn');
    });
  } finally {
    restore();
    timers.restore();
  }
});

test('multiple remote addresses produce one link per configured host', async () => {
  const timers = installFakeIntervals();
  const restore = mockFetch((url) => {
    if (url === '/rpc/pairing/sessions/list') return okResponse({ sessions: [] });
    if (url === '/rpc/pairing/challenge/create') {
      return okResponse({ challenge_id: 'pair_multi', code: 'ABCD2345', expires_in: 300 });
    }
    return okResponse({});
  });
  try {
    await withDom(SETTINGS_HTML + APPROVAL_POPUP_HTML, 'http://127.0.0.1:10994/', async () => {
      await refreshPairingPanel();
      document.getElementById('pairing-remote-address').value = [
        'https://shell.example',
        'https://tunnel.example/base',
      ].join('\n');
      document.getElementById('pairing-create-btn').click();
      await new Promise((r) => setTimeout(r, 20));
      const links = document.getElementById('pairing-qr-alternatives');
      assert.equal(links.querySelectorAll('[data-pairing-address]').length, 1);
      assert.match(links.textContent, /tunnel\.example/);
      assert.match(document.getElementById('pairing-link-display').textContent, /shell\.example/);
    });
  } finally {
    restore();
    timers.restore();
  }
});

test('disabled remote access skips pairing management calls', async () => {
  let calls = 0;
  const restore = mockFetch(() => {
    calls += 1;
    return okResponse({ sessions: [] });
  });
  try {
    await withDom(SETTINGS_HTML, 'http://127.0.0.1:10994/', async () => {
      await refreshPairingPanel({ enabled: false });
      assert.equal(document.getElementById('pairing-panel').hidden, true);
      assert.equal(calls, 0);
    });
  } finally {
    restore();
  }
});

// This test sets module-level pairingHostOnly — keep it last.
test('paired remote client hides the Remote access section on PAIRING_UNAUTHORIZED', async () => {
  const restore = mockFetch((url) => {
    if (url === '/rpc/pairing/sessions/list') return unauthorizedResponse();
    return okResponse({});
  });
  try {
    await withDom(SETTINGS_HTML, 'https://192.168.1.10:10994/', async () => {
      await refreshPairingPanel();
      const group = document.getElementById('settings-remote-group');
      const navBtn = document.getElementById('settings-jump-remote');
      assert.equal(group.hidden, true, 'remote access group must be hidden for a paired remote client');
      assert.equal(navBtn.hidden, true, 'nav jump link must be hidden for a paired remote client');
      // A second refresh must stay hidden (and not re-toast).
      await refreshPairingPanel();
      assert.equal(group.hidden, true);
    });
  } finally {
    restore();
  }
});
