// Mobile shell controls, round 3 — the narrow-viewport contract for the
// desktop-only chrome (PiP mini window + pet launcher), the shared plugin
// launcher's tab hand-off, and the topbar heading that must stay page-scoped
// while asynchronous conversation writes resolve.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';
import { JSDOM } from 'jsdom';

import { openPluginWindow } from '../js/plugin-window.js';
import { normalizePrototypeRoute } from '../page/conversation/route-state.js';

const root = new URL('../', import.meta.url);

async function extractFunctions(names) {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  return names.map((name) => {
    const asyncStart = app.indexOf(`async function ${name}(`);
    const start = asyncStart >= 0 ? asyncStart : app.indexOf(`function ${name}(`);
    assert.ok(start >= 0, `expected ${name} in app.js`);
    return app.slice(start, app.indexOf('\n}', start) + 2);
  }).join('\n');
}

// The 720px media block ends at the first unindented closing brace — every
// rule inside it is single-line, so slicing to '\n}' is the block's content.
async function mobileShellBlock() {
  const css = await readFile(new URL('shell.css', root), 'utf8');
  const start = css.indexOf('@media (max-width: 720px)');
  assert.ok(start >= 0, 'the <=720px overlay block exists');
  return css.slice(start, css.indexOf('\n}\n', start));
}

test('the <=720px overlay breakpoint hides the desktop-only mini window and pet launcher', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const block = await mobileShellBlock();

  // The markup stays for desktop; only the media query hides it.
  assert.ok(document.getElementById('mini-window-btn'), 'PiP control stays in the toolbar');
  assert.ok(document.getElementById('global-nav-pet'), 'pet launcher stays in the rail');
  assert.match(block, /#mini-window-btn[^{]*\{[^}]*display:\s*none/,
    'the PiP affordance hides at the overlay breakpoint');
  assert.match(block, /#global-nav-pet[^{]*\{[^}]*display:\s*none/,
    'the pet launcher hides at the overlay breakpoint');
});

test('openPluginWindow opens a real browser tab at the mobile breakpoint', () => {
  const calls = [];
  const fakeWin = { focus: () => calls.push('focus'), document: {} };
  globalThis.window = {
    innerWidth: 390,
    matchMedia: (query) => ({ matches: query === '(max-width: 720px)' }),
    screen: { availWidth: 390, availHeight: 780 },
    open: (url, target, features) => { calls.push({ url, target, features }); return fakeWin; },
  };
  try {
    const ok = openPluginWindow({ id: 'nusashell.notes', name: 'Notes', ui: { window: { mode: 'panel' } } });
    assert.equal(ok, true);
    assert.equal(calls[0].url, '/plugins/nusashell.notes/', 'the resolved plugin URL is unchanged');
    assert.equal(calls[0].target, 'nusashell-plugin:nusashell.notes',
      'the stable named target keeps tab reuse/focus dedupe');
    assert.equal(calls[0].features, '',
      'no popup features at narrow widths — the browser opens a normal tab');
    assert.ok(calls.includes('focus'), 'the opened tab still gets the focus attempt');
    assert.equal(fakeWin.document.title, 'Notes');
  } finally {
    delete globalThis.window;
  }
});

test('openPluginWindow keeps the floating popup window on desktop widths', () => {
  const calls = [];
  const fakeWin = { focus: () => calls.push('focus'), document: {} };
  globalThis.window = {
    innerWidth: 1440,
    matchMedia: () => ({ matches: false }),
    screen: { availWidth: 1440, availHeight: 860 },
    open: (url, target, features) => { calls.push({ url, target, features }); return fakeWin; },
  };
  try {
    assert.equal(openPluginWindow({ id: 'p1', name: 'P', ui: {} }), true);
    assert.equal(calls[0].url, '/plugins/p1/');
    assert.match(calls[0].features, /popup=yes/, 'desktop keeps the separate floating window');
  } finally {
    delete globalThis.window;
  }
});

test('the narrow fallback uses innerWidth when matchMedia is unavailable', () => {
  const calls = [];
  globalThis.window = {
    innerWidth: 374,
    screen: {},
    open: (url, target, features) => { calls.push(features); return { focus() {}, document: {} }; },
  };
  try {
    openPluginWindow({ id: 'p2' });
    assert.equal(calls[0], '', '374px resolves to a tab');
    globalThis.window.innerWidth = 1440;
    openPluginWindow({ id: 'p2' });
    assert.match(calls[1], /popup=yes/, '1440px resolves to the floating window');
  } finally {
    delete globalThis.window;
  }
});

test('a blocked mobile tab reports failure so callers keep the existing feedback', () => {
  globalThis.window = { matchMedia: () => ({ matches: true }), open: () => null };
  try {
    assert.equal(openPluginWindow({ id: 'p3', name: 'P' }), false,
      'a blocked tab surfaces through the same false contract as a blocked popup');
  } finally {
    delete globalThis.window;
  }
});

test('Home and Plugins Open flow through the same launcher seam', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const plugins = await readFile(new URL('page/plugins/app.js', root), 'utf8');
  const specifiers = [];
  for (const [name, source] of [['conversation/app.js', app], ['plugins/app.js', plugins]]) {
    const imported = source.match(/import \{ openPluginWindow as launchPluginWindow \} from '([^']+)'/);
    assert.ok(imported, `${name} keeps the shared launcher import`);
    specifiers.push(imported[1]);
    assert.match(source, /launchPluginWindow\(\{/, `${name} opens through the shared seam`);
  }
  assert.equal(specifiers[0], specifiers[1],
    'both importers pin the same plugin-window specifier so they share one module instance');
});

// The heading gate: async conversation writes resolve after route changes, so
// they must check the active route — not enumerate other views (Automation
// and Telemetry were missing from the old enumeration).
async function headingHarness() {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const code = await extractFunctions([
    'agentRouteActive', 'syncPrototypeRoute', 'setActiveSurface', 'renameConversation',
  ]);
  const toasts = [];
  const context = {
    window: document.defaultView,
    location: { hash: '#automation' },
    displayedSurface: '',
    normalizePrototypeRoute,
    dismissOpenDialogs() {},
    narrowPanels: () => false,
    shell: document.querySelector('.shell'),
    conversationSurface: document.querySelector('#conversation-surface'),
    homeView: document.querySelector('#home-view'),
    skillsView: document.querySelector('#skills-view'),
    pluginsView: document.querySelector('#plugins-view'),
    learningView: document.querySelector('#learning-view'),
    providersView: document.querySelector('#providers-view'),
    logsView: document.querySelector('#logs-view'),
    automationView: document.querySelector('#automation-view'),
    telemetryView: document.querySelector('#telemetry-view'),
    settingsView: document.querySelector('#settings-view'),
    globalNav: document.querySelector('#global-nav'),
    homeSearch: document.querySelector('#home-search'),
    skillsPage: null, pluginsPage: null, learningPage: null, providersPage: null,
    logsPage: null, automationPage: null, telemetryPage: null, settingsPage: null,
    roomHeadingTitle: document.querySelector('.room-heading strong'),
    activeConversation: { id: 'c1', title: 'Audit room', status: 'idle' },
    conversations: [{ id: 'c1', title: 'Audit room' }],
    rpc: async (method, payload) => ({ conversation: { id: payload.id, title: payload.title } }),
    syncRoomInfo() {},
    renderRooms() {},
    toast: (message, tone) => toasts.push({ message, tone }),
  };
  runInNewContext(code, context);
  return { context, heading: context.roomHeadingTitle, toasts };
}

test('a deferred conversation write never repaints a non-Agent page label', async () => {
  const { context, heading } = await headingHarness();

  context.setActiveSurface('automation');
  assert.equal(heading.textContent, 'Automation');

  // The deferred conversation refresh resolves while Automation is routed.
  await context.renameConversation('c1', 'Renamed room');
  assert.equal(heading.textContent, 'Automation',
    'the async conversation write must not clobber the current page label');
  assert.equal(context.activeConversation.title, 'Renamed room',
    'the conversation model still updates in the background');

  // Returning to Agent paints the live conversation title.
  context.location.hash = '#agent';
  context.setActiveSurface('agent');
  assert.equal(heading.textContent, 'Renamed room');

  // On Agent the same async write now lands in the heading.
  await context.renameConversation('c1', 'Second rename');
  assert.equal(heading.textContent, 'Second rename');
});

test('every async conversation heading write routes through the agent-route gate', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');

  const openConversation = app.slice(
    app.indexOf('async function openConversation('),
    app.indexOf('// Refresh the authoritative transcript'),
  );
  assert.match(openConversation, /agentRouteActive\(\)/,
    'the deferred openConversation refresh gates its heading write on the route');
  assert.doesNotMatch(openConversation, /homeView\?\.hidden !== false/,
    'the view-by-view enumeration that missed Automation/Telemetry is gone');

  const clearView = app.slice(
    app.indexOf('function clearDeletedConversationView('),
    app.indexOf('function invalidateDeletedConversationLoad('),
  );
  assert.match(clearView, /agentRouteActive\(\)/,
    'the post-delete reset is gated too — it resolves after refreshConversationList');
});
