import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { createLogsPage } from '../page/logs/app.js';
import { filterLogEntries, logLevelLabel } from '../page/logs/model.js';

const root = new URL('../', import.meta.url);

function installDOMGlobals(window) {
  const names = [
    'window', 'document', 'MutationObserver', 'requestAnimationFrame',
    'cancelAnimationFrame', 'Node', 'HTMLElement', 'HTMLInputElement',
    'HTMLTextAreaElement', 'localStorage',
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

const entries = [
  { id: 'log-1', time: '2026-09-14T09:00:00Z', level: 'info', source: 'agent', message: 'conversation created' },
  { id: 'log-2', time: '2026-09-14T09:01:00Z', level: 'warn', source: 'provider', message: 'retrying request' },
  { id: 'log-3', time: '2026-09-14T09:02:00Z', level: 'error', source: 'transport', message: 'connection closed' },
];

test('log model keeps readable levels and searches source/message', () => {
  assert.equal(logLevelLabel('warn'), 'Warning');
  assert.deepEqual(filterLogEntries(entries, 'provider', ''), [entries[1]]);
  assert.deepEqual(filterLogEntries(entries, '', 'error'), [entries[2]]);
  assert.deepEqual(filterLogEntries(entries, 'missing', ''), []);
});

test('experimental Logs route exposes a content-first runtime trail', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const css = await readFile(new URL('logs.css', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const pageApp = await readFile(new URL('page/logs/app.js', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const page = document.querySelector('#logs-view');

  assert.ok(page);
  assert.equal(page.getAttribute('aria-label'), 'Logs workspace');
  assert.ok(page.querySelector('#logs-list'));
  assert.ok(page.querySelector('#logs-search'));
  assert.ok(page.querySelector('#logs-level'));
  assert.ok(page.querySelector('#logs-follow'));
  assert.ok(page.querySelector('#logs-clear'));
  assert.ok(page.querySelector('#logs-detail'));
  assert.match(app, /logsPage\?\.refresh/);
  assert.match(app, /logs-active/);
  assert.match(pageApp, /logs\.list/);
  assert.match(pageApp, /logs\.clear/);
  assert.match(pageApp, /logs\.append/);
  assert.match(css, /\.logs-workspace\s*\{/);
  assert.match(css, /\.logs-workspace\s*\{[\s\S]*margin-bottom:\s*14px/);
  assert.match(css, /\.logs-list\s*\{/);
  assert.match(css, /\.shell\.logs-active/);
});

test('Logs scroll seams target the in-card list and detail scrollers on every width', async () => {
  const pageApp = await readFile(new URL('page/logs/app.js', root), 'utf8');
  // One scroll-container contract at all widths: the follow tail, bottom
  // detection, and Back scroll retention live on the card's own #logs-list
  // (and .logs-detail-panel for the detail pane). No mobile branch may point
  // them at the outer page — the page is not a scroller at <=720px.
  assert.doesNotMatch(pageApp, /isMobile\(\)\s*\?\s*pageNode\(\)/);
  assert.doesNotMatch(pageApp, /addListener\(pageNode\(\),\s*'scroll'/);
  assert.doesNotMatch(pageApp, /page\.scrollTop\s*=/);
  assert.match(pageApp, /dataset\.mobileListScroll\s*=\s*String\(list\?\.scrollTop/);
});

test('Logs page renders backend entries, patches live append, filters, and opens detail', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  const subscriptions = new Map();
  const rpcClient = async (method, payload) => {
    calls.push({ method, payload });
    if (method === 'logs.list') return { entries };
    if (method === 'logs.clear') return { ok: true };
    throw new Error(`unexpected RPC ${method}`);
  };
  const onEvent = (type, handler) => {
    subscriptions.set(type, handler);
    return () => subscriptions.delete(type);
  };
  let page;
  try {
    page = createLogsPage(dom.window.document.querySelector('#logs-view'), { rpcClient, onEvent });
    page.init();
    await page.refresh();
    assert.equal(dom.window.document.querySelectorAll('#logs-list .logs-entry').length, 3);
    assert.equal(dom.window.document.querySelector('#logs-count').textContent, '3 entries');
    assert.equal(calls[0].method, 'logs.list');
    assert.deepEqual(calls[0].payload, { limit: 300 });

    dom.window.document.querySelector('#logs-list .logs-entry')?.click();
    assert.equal(dom.window.document.querySelector('#logs-detail').hidden, false);
    assert.equal(dom.window.document.querySelector('#logs-detail-message').textContent, 'conversation created');

    subscriptions.get('logs.append')({ entry: { id: 'log-4', time: '2026-09-14T09:03:00Z', level: 'info', source: 'agent', message: 'turn finished' } });
    assert.equal(dom.window.document.querySelectorAll('#logs-list .logs-entry').length, 4);

    const search = dom.window.document.querySelector('#logs-search');
    search.value = 'retrying';
    search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 140));
    assert.equal(dom.window.document.querySelectorAll('#logs-list .logs-entry').length, 1);
    assert.equal(dom.window.document.querySelector('#logs-detail').hidden, true);
  } finally {
    page?.destroy();
    restore();
  }
});

test('Logs clear requires confirmation and leaves a useful empty state', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  const rpcClient = async (method) => {
    calls.push(method);
    if (method === 'logs.list') return { entries };
    if (method === 'logs.clear') return { ok: true };
    throw new Error(`unexpected RPC ${method}`);
  };
  try {
    const page = createLogsPage(dom.window.document.querySelector('#logs-view'), { rpcClient, onEvent: () => () => {} });
    page.init();
    await page.refresh();
    dom.window.document.querySelector('#logs-clear').click();
    // index.html also carries static overlays (e.g. #pet-install-overlay) —
    // the confirm dialog is the visible one.
    const confirmOverlay = [...dom.window.document.querySelectorAll('.ui-dialog-overlay:not([hidden])')].pop();
    assert.ok(confirmOverlay, 'clear requires a visible confirm dialog');
    confirmOverlay.querySelector('.ui-dialog-actions button:last-child').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.ok(calls.includes('logs.clear'));
    assert.equal(dom.window.document.querySelectorAll('#logs-list .logs-entry').length, 0);
    assert.match(dom.window.document.querySelector('#logs-list').textContent, /No log entries/);
    page.destroy();
  } finally {
    restore();
  }
});

test('Logs page keeps an RPC error visible with retry affordance', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  try {
    const page = createLogsPage(dom.window.document.querySelector('#logs-view'), { rpcClient: async () => { throw new Error('log backend unavailable'); }, onEvent: () => () => {} });
    page.init();
    await page.refresh();
    assert.equal(dom.window.document.querySelector('#logs-error').hidden, false);
    assert.match(dom.window.document.querySelector('#logs-list').textContent, /log backend unavailable/);
    page.destroy();
  } finally {
    restore();
  }
});
