import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { createSkillsPage } from '../page/skills/app.js';
import { createSettingsPage } from '../page/settings/app.js';

const root = new URL('../', import.meta.url);

async function source(path) {
  return readFile(new URL(path, root), 'utf8');
}

function matches(value, pattern, message) {
  assert.ok(pattern.test(value), message);
}

function installDOMGlobals(window) {
  const names = [
    'window', 'document', 'MutationObserver', 'requestAnimationFrame',
    'cancelAnimationFrame', 'Node', 'HTMLElement', 'HTMLInputElement',
    'HTMLTextAreaElement', 'localStorage', 'navigator',
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

test('the context meter is reserved for the Agent route', async () => {
  const css = await source('shell.css');
  matches(css, /\.shell:not\(\.agent-active\)\s+\.context-meter\s*\{[^}]*display:\s*none;/, 'all non-Agent routes hide the context meter');
});

test('Skills delete follows backend ownership rules and the styled confirmation flow', async () => {
  const html = await source('index.html');
  const css = await source('skills.css');
  const dom = new JSDOM(html, { url: 'http://127.0.0.1:8766/#skills' });
  const restore = installDOMGlobals(dom.window);
  const skills = [
    { id: 'builtin-sample', name: 'Built-in sample', owned_by: 'builtin', status: 'trusted', version: 1, active_version: 1 },
    { id: 'plugin-sample', name: 'Plugin sample', owned_by: 'plugin:notes', status: 'trusted', version: 1, active_version: 1 },
    { id: 'learned-sample', name: 'Learned sample', owned_by: 'learned', status: 'experimental', version: 2, active_version: 1 },
  ];
  const calls = [];
  const rpcClient = async (method, params = {}) => {
    calls.push({ method, params });
    if (method === 'skills.list') return { skills: [...skills] };
    if (method === 'skills.read') return { skill: skills.find((skill) => skill.id === params.id) };
    if (method === 'skills.delete') {
      const index = skills.findIndex((skill) => skill.id === params.id);
      if (index >= 0) skills.splice(index, 1);
      return { ok: true };
    }
    throw new Error(`unexpected RPC ${method}`);
  };
  let page;
  try {
    page = createSkillsPage(dom.window.document.querySelector('#skills-view'), { rpcClient, onEvent: () => () => {} });
    page.init();
    await page.refresh();

    await page.selectSkill('builtin-sample', 'builtin');
    dom.window.document.querySelector('#skill-actions-trigger').click();
    const builtinRemove = dom.window.document.querySelector('#skills-delete');
    assert.equal(builtinRemove.hidden, false);
    assert.equal(dom.window.document.querySelector('#skills-rollback').disabled, false, 'built-in removal does not disable the existing rollback action');
    matches(dom.window.document.querySelector('#skill-actions-guidance')?.textContent || '', /built-in.*prevents.*automatic reinstallation/i, 'built-in removal explains the automatic-install tombstone');
    builtinRemove.click();

    let confirmOverlay = [...dom.window.document.querySelectorAll('.ui-dialog-overlay:not([hidden])')].pop();
    let dialog = confirmOverlay?.querySelector('.ui-dialog');
    assert.ok(dialog, 'built-in delete uses the shared styled dialog');
    assert.ok(dialog.textContent.includes('Built-in sample'), `confirmation text: ${JSON.stringify(dialog.textContent)}`);
    matches(dialog.textContent, /not automatically reinstall/i, 'built-in confirmation explains the automatic-install tombstone');
    assert.equal(calls.some(({ method }) => method === 'skills.delete'), false, 'built-in deletion waits for confirmation');
    [...dialog.querySelectorAll('button')].find((button) => button.textContent === 'Delete').click();
    await new Promise((resolve) => setTimeout(resolve, 0));

    assert.deepEqual(calls.find(({ method }) => method === 'skills.delete')?.params, { id: 'builtin-sample', owned_by: 'builtin' });
    assert.equal(skills.some((skill) => skill.id === 'builtin-sample'), false, 'the fixture RPC removes the built-in sample');

    await page.selectSkill('plugin-sample', 'plugin:notes');
    dom.window.document.querySelector('#skill-actions-trigger').click();
    assert.equal(dom.window.document.querySelector('#skills-delete').hidden, true);
    matches(dom.window.document.querySelector('#skill-actions-guidance')?.textContent || '', /Uninstall the owning plugin/i, 'plugin ownership points to its existing removal flow');
    const deleteCallCount = calls.filter(({ method }) => method === 'skills.delete').length;
    dom.window.document.querySelector('#skills-delete').click();
    assert.equal(calls.filter(({ method }) => method === 'skills.delete').length, deleteCallCount, 'plugin-owned deletion is guarded');

    await page.selectSkill('learned-sample', 'learned');
    dom.window.document.querySelector('#skill-actions-trigger').click();
    const remove = dom.window.document.querySelector('#skills-delete');
    assert.equal(remove.hidden, false);
    matches(html, /id="skill-actions-menu"[\s\S]*?id="skills-delete"[^>]*role="menuitem"/, 'delete action remains inside the menu');
    matches(css, /\.skill-actions-menu\s*,\s*\.skill-actions-menu\[hidden\]\s*\{[^}]*display:\s*grid;/, 'the action menu retains its existing styling');
    const callsBeforeLearnedConfirm = calls.filter(({ method }) => method === 'skills.delete').length;
    remove.click();

    confirmOverlay = [...dom.window.document.querySelectorAll('.ui-dialog-overlay:not([hidden])')].pop();
    dialog = confirmOverlay?.querySelector('.ui-dialog');
    assert.ok(dialog, 'delete uses the shared styled dialog');
    assert.ok(dialog.textContent.includes('Learned sample'), `confirmation text: ${JSON.stringify(dialog.textContent)}`);
    assert.equal(calls.filter(({ method }) => method === 'skills.delete').length, callsBeforeLearnedConfirm, 'learned deletion waits for confirmation');
    [...dialog.querySelectorAll('button')].find((button) => button.textContent === 'Delete').click();
    await new Promise((resolve) => setTimeout(resolve, 0));

    assert.deepEqual(calls.find(({ method, params }) => method === 'skills.delete' && params.id === 'learned-sample')?.params, { id: 'learned-sample', owned_by: 'learned' });
    assert.equal(skills.some((skill) => skill.id === 'learned-sample'), false);
    assert.equal(dom.window.document.querySelector('#skills-empty').hidden, false);
  } finally {
    page?.destroy();
    restore();
  }
});

test('Codex detail keeps its account card after a natural-height model grid', async () => {
  const app = await source('page/providers/app.js');
  const css = await source('providers.css');
  matches(app, /detail\.dataset\.kind/, 'provider detail records the selected kind for scoped layout');
  matches(css, /#providers-detail\[data-kind="codex"\][^{]*\{[^}]*height:\s*auto;[^}]*min-height:\s*100%;/, 'Codex detail grows with the account card');
  matches(css, /#providers-detail\[data-kind="codex"\]\s*>\s*\.providers-detail-grid\s*\{[^}]*flex:\s*none;/, 'Codex model grid stays in natural flow');
  matches(css, /#providers-detail\[data-kind="codex"\] \.providers-model-list\s*\{[^}]*overflow:\s*auto;/, 'Codex model list scrolls inside the grid');
});

test('Logs Slim Select restores vertical centering after its compact value override', async () => {
  const css = await source('logs.css');
  matches(css, /\.logs-level-field \.ss-main \.ss-values \.ss-single\s*\{[^}]*margin-block:\s*auto;/, 'Slim Select single value keeps vertical auto margins');
});

test('Settings overview summary gives version, data directory, and access state a responsive status layout', async () => {
  const app = await source('page/settings/app.js');
  const css = await source('settings.css');
  matches(app, /settings-overview-summary/, 'summary uses its scoped layout class');
  matches(app, /settings-system-item/, 'system status is grouped into items');
  matches(app, /settings-system-version/, 'version status has a labeled item');
  matches(app, /settings-system-directory/, 'data directory has a labeled item');
  matches(app, /settings-system-remote/, 'remote access status has a labeled item');
  matches(css, /\.settings-overview-summary\s*\{[^}]*display:\s*grid;/, 'summary is styled as a panel');
  matches(css, /\.settings-system\s*\{[^}]*grid-template-columns:/, 'status items use a responsive grid');
  matches(css, /\.settings-system-location/, 'copy action and path use a bounded row');
  matches(css, /@media\s*\(max-width:\s*680px\)[\s\S]*\.settings-system\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/, 'status items stack on narrow screens');
});

test('Settings overview preserves current state and data-directory copy behavior', async () => {
  const html = await source('index.html');
  const dom = new JSDOM(html, { url: 'http://127.0.0.1:8766/#settings' });
  const restore = installDOMGlobals(dom.window);
  const copied = [];
  Object.defineProperty(dom.window.navigator, 'clipboard', {
    configurable: true,
    value: { writeText: async (value) => copied.push(value) },
  });
  const page = createSettingsPage(dom.window.document.querySelector('#settings-view'), {
    rpcClient: async (method) => {
      if (method === 'settings.get') return { settings: { delegate_model: 'deepseek-v4.1-flash', max_tool_rounds: 2000, compaction_workflow: 'reuse', remote_access_enabled: true } };
      if (method === 'app.info') return { version: '0.9.12', data_dir: '/fixture/data/nusashell' };
      if (method === 'ai.models.list') return { models: [] };
      if (method === 'settings.stt_install_status') return { ready: false, supported: false, models: [], engine_installed: false };
      if (method === 'settings.tts_install_status') return { ready: false, voices: [] };
      if (method === 'settings.pets_status') return { supported: false };
      return {};
    },
  });
  try {
    page.init();
    await page.activate();
    await new Promise((resolve, reject) => {
      const panels = dom.window.document.querySelector('#settings-panels');
      const finish = (error) => {
        clearTimeout(timeout);
        observer.disconnect();
        if (error) reject(error);
        else resolve();
      };
      const observer = new dom.window.MutationObserver(() => {
        if (panels.querySelector('.settings-system-version')) finish();
      });
      const timeout = setTimeout(() => finish(new Error('Settings overview did not render')), 1000);
      observer.observe(panels, { childList: true, subtree: true });
      if (panels.querySelector('.settings-system-version')) finish();
    });
    assert.equal(dom.window.document.querySelector('.settings-system-version .settings-system-value').textContent, '0.9.12');
    assert.equal(dom.window.document.querySelector('#settings-data-dir').textContent, '/fixture/data/nusashell');
    assert.equal(dom.window.document.querySelector('.settings-system-remote .settings-system-value').textContent, 'On');
    dom.window.document.querySelector('#settings-copy-data-dir').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.deepEqual(copied, ['/fixture/data/nusashell']);
  } finally {
    page.destroy();
    restore();
  }
});

test('changed stylesheet URLs match the service worker cache manifest', async () => {
  const html = await source('index.html');
  const sw = await source('sw.js');
  for (const asset of [
    'shell.css?v=62',
    'skills.css?v=7',
    'plugins.css?v=8',
    'providers.css?v=12',
    'logs.css?v=8',
    'automation.css?v=4',
    'learning.css?v=7',
    'settings.css?v=3',
  ]) {
    assert.ok(html.includes(`href="./${asset}"`), `index.html requests ${asset}`);
    assert.ok(sw.includes(`'./${asset}'`), `service worker precaches ${asset}`);
  }
  // Every page module page/conversation/app.js imports with a ?v= marker is
  // pinned under the same specifier in the precache manifest.
  for (const mod of [
    'page/skills/app.js?v=2',
    'page/plugins/app.js?v=1',
    'page/learning/app.js?v=5',
    'page/providers/app.js?v=4',
    'page/logs/app.js?v=2',
    'page/automation/app.js?v=3',
    'js/plugin-window.js?v=1',
  ]) {
    assert.ok(sw.includes(`'./${mod}'`), `service worker precaches ${mod}`);
  }
});

test('composer catalog clears empty choices and rejects stale model preferences', async () => {
  const dom = new JSDOM(await source('index.html'), { url: 'http://localhost/', pretendToBeVisual: true });
  const restore = installDOMGlobals(dom.window);
  let controls;
  try {
    const { initComposerSelects } = await import('../page/conversation/composer.js');
    controls = initComposerSelects(dom.window.document.querySelector('#message-form'), { rpc: async () => ({}) });
    const select = dom.window.document.querySelector('#model-select');
    controls.setModels([{ id: 'image-only', provider_id: 'p', kind: 'image' }]);
    assert.equal(select.disabled, true);
    assert.equal(select.options.length, 1);
    assert.match(select.textContent, /No chat models available/);
    dom.window.localStorage.setItem('nusashell.model', 'removed:model');
    const catalog = [{ id: 'one', provider_id: 'p' }, { id: 'two', provider_id: 'p' }];
    controls.setModels(catalog, 'removed:room-model');
    assert.equal(select.disabled, false);
    assert.equal(select.value, 'p:one');
    dom.window.localStorage.setItem('nusashell.model', 'p:two');
    controls.setModels(catalog);
    assert.equal(select.value, 'p:two');
    controls.setModels(catalog, 'p:one');
    assert.equal(select.value, 'p:one');
    controls.setModels([]);
    assert.equal(select.disabled, true);
    assert.equal(select.value, '');
  } finally {
    controls?.model.destroy(); controls?.effort.destroy(); controls?.route.destroy();
    restore(); dom.window.close();
  }
});
