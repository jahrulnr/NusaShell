import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import {
  filterPlugins,
  pluginFilterLabel,
  pluginNeedsAttention,
  pluginStatusLabel,
  pluginTransport,
  pluginTypeLabel,
} from '../page/plugins/model.js';
import {
  canAutoUpdate,
  hasContract,
  hasPluginUI,
  hasUpdate,
  pluginKind,
} from '../js/views/plugins-model.js';
import { createPluginsPage } from '../page/plugins/app.js';

const root = new URL('../', import.meta.url);

function installDOMGlobals(window) {
  const names = [
    'window', 'document', 'MutationObserver', 'requestAnimationFrame',
    'cancelAnimationFrame', 'Node', 'HTMLElement', 'HTMLInputElement',
    'HTMLTextAreaElement', 'localStorage', 'FileReader', 'File',
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

const manual = {
  id: 'plugin-files',
  name: 'Files',
  plugin: false,
  status: 'idle',
  version: '0.1.0',
  manifest: { mcp: { transport: 'stdio', command: 'npx', args: ['-y', 'server-files'] } },
};

const connectedApp = {
  id: 'nusashell.notes',
  name: 'Notes',
  plugin: true,
  catalog: true,
  hasUI: true,
  category: 'Productivity',
  version: '2.0.2',
  status: 'connected',
  autostart: true,
  autoUpdate: false,
  contractEntry: 'CONTRACT.md',
  manifest: { mcp: { transport: 'stdio', command: 'notes' }, ui: { entry: 'ui/index.html' } },
  tools: [{ name: 'note_search', description: 'Search notes' }, { name: 'note_save', description: 'Save a note' }],
};

test('plugin model keeps type, transport, attention, and search semantics readable', () => {
  assert.equal(pluginTypeLabel(manual), 'MCP server');
  assert.equal(pluginTypeLabel(connectedApp), 'MCP + UI');
  assert.equal(pluginTransport(manual), 'stdio');
  assert.equal(pluginStatusLabel('connected'), 'Connected');
  assert.equal(pluginStatusLabel('error'), 'Needs attention');
  assert.equal(pluginNeedsAttention({ ...manual, updateAvailable: '1.0.0' }), true);
  assert.deepEqual(filterPlugins([manual, connectedApp], 'note_search'), [connectedApp]);
  assert.deepEqual(filterPlugins([manual, connectedApp], '', 'connected'), [connectedApp]);
  assert.equal(pluginFilterLabel('attention', 2), '2 needs attention');
});

test('experimental Plugins page exposes a content-first inventory and inspector', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const css = await readFile(new URL('plugins.css', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const pageApp = await readFile(new URL('page/plugins/app.js', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const page = document.querySelector('#plugins-view');

  assert.ok(page);
  assert.equal(page.getAttribute('aria-label'), 'Plugins workspace');
  assert.ok(page.querySelector('.plugins-toolbar h1.sr-only'));
  assert.equal(document.querySelector('[data-global-route="plugins"]')?.getAttribute('href'), '#plugins');
  assert.match(app, /pluginsPage\?\.refresh/);
  assert.match(app, /pluginsView\?\.hidden/);
  assert.match(app, /pluginsSearch/);
  assert.match(app, /plugins-active/);
  assert.ok(page.querySelector('#plugins-search'));
  assert.ok(page.querySelector('#plugins-list'));
  assert.ok(page.querySelector('#plugins-inspector'));
  assert.ok(page.querySelector('#plugins-install-overlay'));
  assert.equal(page.querySelector('#plugins-install-cancel'), null);
  assert.match(css, /\.plugins-workspace\s*\{/);
  assert.match(css, /\.plugins-toolbar-main\s*\{[^}]*display:\s*block;/);
  assert.match(css, /grid-template-columns:\s*minmax\(300px, 360px\) minmax\(0, 1fr\)/);
  assert.match(css, /\.plugins-toolbar,\s*\.plugins-filter-row\s*\{[^}]*flex:\s*none;/);
  assert.match(css, /\.plugins-detail-grid\s*\{/);
  assert.match(css, /\.plugins-action\[hidden\][\s\S]*display:\s*none;/);
  assert.match(css, /\.plugins-install-footer\[hidden\][\s\S]*display:\s*none;/);
  assert.match(css, /\.plugins-empty-list\s*\{[^}]*display:\s*grid;[^}]*gap:/);
  assert.match(css, /\.plugins-empty-list strong\s*\{[^}]*display:\s*block;/);
  assert.match(css, /\.plugins-empty-list span\s*\{[^}]*display:\s*block;/);
  assert.match(css, /\.shell\.plugins-active ~ \.ui-dialog-overlay \.ui-dialog-field\s*\{/);
  assert.match(css, /\.shell\.plugins-active ~ \.ss-content\.slim-select\s*\{[^}]*z-index:\s*1100 !important;/);
  assert.match(css, /\.plugins-catalog-name-line\s*\{[^}]*grid-template-columns:\s*minmax\(0, 1fr\) max-content;/);
  assert.match(css, /\.shell\.plugins-active \.context-dock\s*\{[^}]*max-width:\s*0;/);
  assert.match(pageApp, /plugin\.list/);
  assert.match(pageApp, /plugin\.test/);
  assert.match(pageApp, /plugin\.install/);
  assert.match(pageApp, /plugin\.set_autostart/);
});

test('Plugins page selects an entry and renders its tools and lifecycle actions', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  let plugins = [manual, connectedApp];
  const rpcClient = async (method, payload) => {
    calls.push({ method, payload });
    if (method === 'plugin.list') return { plugins };
    if (method === 'plugin.check_updates') return { plugins: [] };
    if (method === 'plugin.stop') return { ok: true };
    if (method === 'plugin.test') {
      plugins = plugins.map((plugin) => plugin.id === payload.id ? { ...plugin, status: 'connected', tools: [{ name: 'read' }] } : plugin);
      return { tools: [{ name: 'read' }] };
    }
    if (method === 'plugin.set_autostart') return { ok: true };
    throw new Error(`unexpected RPC ${method}`);
  };
  try {
    const page = createPluginsPage(dom.window.document.querySelector('#plugins-view'), { rpcClient, onEvent: () => () => {} });
    page.init();
    await page.refresh();

    assert.equal(dom.window.document.querySelectorAll('#plugins-list .plugins-list-item').length, 2);
    assert.equal(dom.window.document.querySelector('#plugins-detail-title').textContent, 'Files');
    assert.equal(dom.window.document.querySelector('#plugins-detail-type').textContent, 'MCP server');
    assert.equal(dom.window.document.querySelector('#plugins-tools-list').textContent.includes('Start the server'), true);
    assert.equal(dom.window.document.querySelector('#plugins-detail-stop').hidden, true);
    assert.equal(dom.window.document.querySelector('#plugins-detail-restart').hidden, true);
    assert.equal(dom.window.document.querySelector('#plugins-detail-edit').hidden, false);
    assert.equal(dom.window.document.querySelector('#plugins-detail-open-ui').hidden, true);

    dom.window.document.querySelector('.plugins-list-row[data-plugin-id="nusashell.notes"]')?.click();
    assert.equal(dom.window.document.querySelector('#plugins-detail-title').textContent, 'Notes');
    assert.equal(dom.window.document.querySelectorAll('#plugins-tools-list .plugins-tool-item').length, 2);
    assert.equal(dom.window.document.querySelector('#plugins-detail-open-ui').hidden, false);
    assert.equal(dom.window.document.querySelector('#plugins-detail-autoupdate').hidden, false);

    dom.window.document.querySelector('#plugins-detail-autostart').checked = false;
    dom.window.document.querySelector('#plugins-detail-autostart').dispatchEvent(new dom.window.Event('change', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.deepEqual(calls.find(({ method }) => method === 'plugin.set_autostart')?.payload, { id: 'nusashell.notes', enabled: false });

    dom.window.document.querySelector('.plugins-list-row[data-plugin-id="plugin-files"]')?.click();
    dom.window.document.querySelector('#plugins-detail-start').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.ok(calls.some(({ method, payload }) => method === 'plugin.test' && payload.id === 'plugin-files'));
    page.destroy();
  } finally {
    restore();
  }
});

test('opening a plugin UI starts an idle server and patches runtime nodes in place', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  const opened = [];
  const idleApp = { ...connectedApp, status: 'idle', tools: [] };
  let resolveTest;
  const testResult = new Promise((resolve) => { resolveTest = resolve; });
  dom.window.open = (url, target, features) => {
    opened.push({ url, target, features });
    return {
      url,
      target,
      features,
      focus() {},
      document: { title: '' },
    };
  };
  const rpcClient = async (method, payload) => {
    calls.push({ method, payload });
    if (method === 'plugin.list') return { plugins: [idleApp] };
    if (method === 'plugin.test') return testResult;
    throw new Error(`unexpected RPC ${method}`);
  };
  let page;
  try {
    page = createPluginsPage(dom.window.document.querySelector('#plugins-view'), { rpcClient });
    page.init();
    await page.refresh();

    const row = dom.window.document.querySelector('.plugins-list-row[data-plugin-id="nusashell.notes"]');
    const rowStatus = row.querySelector('.plugins-row-status');
    const detail = dom.window.document.querySelector('#plugins-detail');
    const detailStatus = dom.window.document.querySelector('#plugins-detail-status');
    const tools = dom.window.document.querySelector('#plugins-tools-list');
    const pluginListCallsBeforeOpen = calls.filter(({ method }) => method === 'plugin.list').length;
    dom.window.document.querySelector('#plugins-detail-open-ui').click();

    assert.equal(opened.length, 1);
    assert.equal(calls.filter(({ method }) => method === 'plugin.test').length, 1);
    assert.equal(calls.filter(({ method }) => method === 'plugin.list').length, pluginListCallsBeforeOpen);
    assert.equal(page.state.plugins[0].status, 'connecting');
    assert.equal(detailStatus.textContent, 'Connecting');
    assert.equal(row, dom.window.document.querySelector('.plugins-list-row[data-plugin-id="nusashell.notes"]'));
    assert.equal(rowStatus, row.querySelector('.plugins-row-status'));
    assert.equal(detail, dom.window.document.querySelector('#plugins-detail'));
    assert.equal(tools, dom.window.document.querySelector('#plugins-tools-list'));

    resolveTest({ tools: [{ name: 'note_search', description: 'Search notes' }] });
    await new Promise((resolve) => setTimeout(resolve, 0));

    assert.equal(page.state.plugins[0].status, 'connected');
    assert.equal(detailStatus.textContent, 'Connected');
    assert.equal(rowStatus.textContent, 'Connected');
    assert.equal(dom.window.document.querySelector('#plugins-detail-start').hidden, true);
    assert.equal(dom.window.document.querySelector('#plugins-detail-stop').hidden, false);
    assert.equal(tools.querySelectorAll('.plugins-tool-item').length, 1);
    assert.equal(calls.filter(({ method }) => method === 'plugin.list').length, pluginListCallsBeforeOpen);
    assert.equal(row, dom.window.document.querySelector('.plugins-list-row[data-plugin-id="nusashell.notes"]'));
    assert.equal(rowStatus, row.querySelector('.plugins-row-status'));
    assert.equal(detailStatus, dom.window.document.querySelector('#plugins-detail-status'));
    assert.equal(tools, dom.window.document.querySelector('#plugins-tools-list'));
  } finally {
    page?.destroy();
    restore();
  }
});

test('empty plugin filters clear a selection that is no longer in view', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const rpcClient = async (method) => {
    if (method === 'plugin.list') return { plugins: [manual, connectedApp] };
    throw new Error(`unexpected RPC ${method}`);
  };
  let page;
  try {
    page = createPluginsPage(dom.window.document.querySelector('#plugins-view'), { rpcClient });
    page.init();
    await page.refresh();
    assert.equal(dom.window.document.querySelector('#plugins-detail-title').textContent, 'Files');

    dom.window.document.querySelector('[data-plugin-filter="connected"]').click();
    assert.equal(page.state.selectedId, null);
    assert.equal(dom.window.document.querySelector('#plugins-empty').hidden, false);
    assert.equal(dom.window.document.querySelector('#plugins-detail').hidden, true);

    dom.window.document.querySelector('[data-plugin-filter="attention"]').click();
    assert.equal(dom.window.document.querySelector('#plugins-list .plugins-empty-list strong').textContent, 'No matching integrations');
    assert.equal(dom.window.document.querySelector('#plugins-empty').hidden, false);
    assert.equal(dom.window.document.querySelector('#plugins-detail').hidden, true);
  } finally {
    page?.destroy();
    restore();
  }
});

test('Plugins page keeps catalog and GitHub installation paths actionable', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  const rpcClient = async (method, payload) => {
    calls.push({ method, payload });
    if (method === 'plugin.list') return { plugins: [] };
    if (method === 'plugin.catalog') return { plugins: [{ id: 'notes', pluginId: 'nusashell.notes', name: 'Notes', version: '2.0.2' }] };
    if (method === 'plugin.install') return { plugin: { id: 'nusashell.notes' } };
    throw new Error(`unexpected RPC ${method}`);
  };
  try {
    const page = createPluginsPage(dom.window.document.querySelector('#plugins-view'), { rpcClient, onEvent: () => () => {} });
    page.init();
    await page.refresh();
    await page.openInstall();

    assert.equal(dom.window.document.querySelector('#plugins-install-overlay').hidden, false);
    assert.equal(dom.window.document.querySelector('#plugins-install-footer').hidden, true);
    assert.equal(dom.window.document.querySelectorAll('#plugins-catalog-list .plugins-catalog-item').length, 1);
    assert.ok(dom.window.document.querySelector('#plugins-catalog-list .plugins-catalog-name-line .plugins-catalog-version'));
    dom.window.document.querySelector('[data-plugin-install-tab="github"]').click();
    assert.equal(dom.window.document.querySelector('#plugins-install-panel-github').hidden, false);
    dom.window.document.querySelector('#plugins-github-url').value = 'owner/repo';
    dom.window.document.querySelector('#plugins-install-confirm').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.deepEqual(calls.find(({ method }) => method === 'plugin.install')?.payload, {
      source: 'github', url: 'owner/repo', subdir: '', ref: '',
    });
    assert.equal(dom.window.document.querySelector('#plugins-install-overlay').hidden, true);
    page.destroy();
  } finally {
    restore();
  }
});

// Ported from production frontend/tests/plugins.test.mjs — plugins-model.js
// helpers stay live through page/plugins/model.js and page/home/mcp.js.
test('Plugins distinguishes native MCP, headless plugin, and MCP plus UI', () => {
  assert.equal(pluginKind({ plugin: false }), 'mcp');
  assert.equal(pluginKind({ plugin: true, hasUI: false }), 'plugin');
  assert.equal(pluginKind({ plugin: true, hasUI: true }), 'plugin-ui');
  assert.equal(hasPluginUI({ plugin: true, hasUI: true }), true);
  assert.equal(hasPluginUI({ plugin: true, hasUI: false }), false);
  assert.equal(hasPluginUI({ plugin: false, hasUI: true }), false);
});

test('Plugins marks UI entry from plugin manifest when the list DTO has no flag', () => {
  assert.equal(hasPluginUI({ plugin: true, manifest: { ui: { entry: 'ui/index.html' } } }), true);
  assert.equal(hasPluginUI({ plugin: true, manifest: { ui: { entry: '' } } }), false);
});

test('Auto-update applies only to catalog-managed plugins', () => {
  assert.equal(canAutoUpdate({ catalog: true }), true);
  assert.equal(canAutoUpdate({ catalog: false }), false);
  assert.equal(canAutoUpdate({}), false);
});

test('Update availability is driven by the updateAvailable version', () => {
  assert.equal(hasUpdate({ updateAvailable: '1.4.0' }), true);
  assert.equal(hasUpdate({ updateAvailable: '' }), false);
  assert.equal(hasUpdate({}), false);
});

test('Usage contract badge is driven by the contractEntry DTO field', () => {
  assert.equal(hasContract({ contractEntry: 'CONTRACT.md' }), true);
  assert.equal(hasContract({ contractEntry: 'docs/RULES.md' }), true);
  assert.equal(hasContract({}), false);
  assert.equal(hasContract({ contractEntry: '' }), false);
});
