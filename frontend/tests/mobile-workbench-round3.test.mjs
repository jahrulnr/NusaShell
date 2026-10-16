import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { createLearningPage } from '../page/learning/app.js';
import { createPluginsPage } from '../page/plugins/app.js';

const root = new URL('../', import.meta.url);
const wait = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));

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
  return () => {
    for (const name of names) {
      const descriptor = previous.get(name);
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else delete globalThis[name];
    }
  };
}

const manualPlugin = {
  id: 'plugin-files',
  name: 'Files',
  plugin: false,
  status: 'idle',
  version: '0.1.0',
  manifest: { mcp: { transport: 'stdio', command: 'npx', args: ['-y', 'server-files'] } },
};

const connectedPlugin = {
  id: 'nusashell.notes',
  name: 'Notes',
  plugin: true,
  catalog: true,
  hasUI: true,
  category: 'Productivity',
  version: '2.0.2',
  status: 'connected',
  manifest: { mcp: { transport: 'stdio', command: 'notes' }, ui: { entry: 'ui/index.html' } },
  tools: [{ name: 'note_search', description: 'Search notes' }],
};

test('mobile workbench contract: pane attributes, mounted Back controls, CSS panes', async () => {
  const [html, pluginsCss, learningCss] = await Promise.all([
    readFile(new URL('index.html', root), 'utf8'),
    readFile(new URL('plugins.css', root), 'utf8'),
    readFile(new URL('learning.css', root), 'utf8'),
  ]);
  const { document } = new JSDOM(html).window;

  // Mount seams the controllers own: root markup stays untouched, the page
  // controllers attach their Back controls into these stable containers.
  assert.ok(document.querySelector('#plugins-view .plugins-detail-header'));
  assert.ok(document.querySelector('#plugins-view .plugins-toolbar'));
  assert.ok(document.querySelector('#plugins-view .plugins-filter-row'));
  assert.ok(document.querySelector('#learning-panel-memory .learning-atlas-layout'));
  assert.ok(document.querySelector('#learning-panel-experiences .learning-experiences-layout'));
  assert.ok(document.querySelector('#learning-panel-overview .learning-document-card'));
  assert.ok(document.querySelector('#learning-panel-overview .learning-map-card'));

  // One mobile pane at a time; list-only chrome hides while a detail is open.
  assert.match(pluginsCss, /\.plugins-page\[data-mobile-pane="list"\][^{]*\.plugins-inspector/);
  assert.match(pluginsCss, /\.plugins-page\[data-mobile-pane="detail"\][^{]*\.plugins-toolbar/);
  assert.match(pluginsCss, /\.plugins-page\[data-mobile-pane="detail"\][^{]*\.plugins-index/);
  assert.match(pluginsCss, /\.plugins-mobile-back\s*\{[^}]*display:\s*none/);
  assert.match(pluginsCss, /@media \(max-width: 720px\)[\s\S]*\.plugins-mobile-back\s*\{[^}]*display:\s*inline-flex/);
  assert.match(learningCss, /\.learning-panel\[data-mobile-pane="list"\][^{]*\.learning-mobile-back/);
  assert.match(learningCss, /#learning-panel-memory\[data-mobile-pane="detail"\][^{]*\.learning-results-panel/);
  assert.match(learningCss, /#learning-panel-experiences\[data-mobile-pane="detail"\][^{]*\.learning-experience-list/);
  assert.match(learningCss, /#learning-panel-memory\[data-mobile-pane="list"\][^{]*\.learning-selection/);
  assert.match(learningCss, /#learning-panel-experiences\[data-mobile-pane="list"\][^{]*\.learning-experience-detail/);
  assert.match(learningCss, /\.learning-mobile-back[^{]*\{[^}]*display:\s*none/);
  assert.match(learningCss, /@media \(max-width: 720px\)[\s\S]*\.learning-mobile-back[^{]*\{[^}]*display:\s*inline-flex/);
  // Overview disclosure: editors and the map collapse at mobile widths only.
  assert.match(learningCss, /@media \(max-width: 720px\)[\s\S]*\.learning-document-card\[data-editor-open="false"\][^{]*textarea/);
  assert.match(learningCss, /@media \(max-width: 720px\)[\s\S]*\.learning-map-card\[data-map-open="false"\][^{]*\.learning-graph-frame/);
  // Desktop arrangements stay intact.
  assert.match(pluginsCss, /grid-template-columns:\s*minmax\(300px, 360px\) minmax\(0, 1fr\)/);
  assert.match(learningCss, /\.learning-atlas-layout\s*\{[^}]*grid-template-columns:\s*minmax\(250px, \.68fr\) minmax\(0, 1\.32fr\);/);
});

test('Plugins mobile flow: list -> detail -> Back restores filter, scroll, and selection', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  dom.window.innerWidth = 374;
  const restore = installDOMGlobals(dom.window);
  const rpcClient = async (method) => {
    if (method === 'plugin.list') return { plugins: [manualPlugin, connectedPlugin] };
    throw new Error(`unexpected RPC ${method}`);
  };
  let page;
  try {
    const rootNode = dom.window.document.querySelector('#plugins-view');
    page = createPluginsPage(rootNode, { rpcClient });
    page.init();
    const back = rootNode.querySelector('#plugins-mobile-back');
    assert.ok(back, 'the controller mounts a Back control into the detail header');
    assert.equal(back.closest('.plugins-detail-header'), rootNode.querySelector('.plugins-detail-header'));
    assert.equal(rootNode.dataset.mobilePane, 'list');

    await page.refresh();
    // refresh() auto-selects the first plugin internally — the mobile surface
    // still opens on the library, not on a detail the user never asked for.
    assert.equal(page.state.selectedId, manualPlugin.id);
    assert.equal(rootNode.dataset.mobilePane, 'list');

    const search = rootNode.querySelector('#plugins-search');
    search.value = 'note';
    search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    await wait(160);
    rootNode.scrollTop = 184;
    rootNode.querySelector(`.plugins-list-row[data-plugin-id="${connectedPlugin.id}"]`).click();
    assert.equal(rootNode.dataset.mobilePane, 'detail');
    assert.equal(rootNode.scrollTop, 0, 'entering detail puts its top (and Back) in view');
    assert.equal(rootNode.querySelector('#plugins-detail-title').textContent, 'Notes');

    back.click();
    await wait();
    assert.equal(rootNode.dataset.mobilePane, 'list');
    assert.equal(page.state.selectedId, connectedPlugin.id);
    assert.equal(search.value, 'note');
    assert.equal(rootNode.scrollTop, 184);
    assert.equal(rootNode.querySelector('.plugins-list-row.is-selected')?.dataset.pluginId, connectedPlugin.id);

    // Resizing across the breakpoint is presentation-only: the same pane and
    // selection are kept, and the desktop side-by-side CSS ignores the attr.
    dom.window.innerWidth = 1440;
    dom.window.dispatchEvent(new dom.window.Event('resize'));
    dom.window.innerWidth = 374;
    dom.window.dispatchEvent(new dom.window.Event('resize'));
    assert.equal(rootNode.dataset.mobilePane, 'list');
    assert.equal(page.state.selectedId, connectedPlugin.id);
  } finally {
    page?.destroy();
    restore();
  }
});

test('Plugins mobile detail leaves to the list when the selection disappears', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  dom.window.innerWidth = 374;
  const restore = installDOMGlobals(dom.window);
  let plugins = [manualPlugin];
  const rpcClient = async (method) => {
    if (method === 'plugin.list') return { plugins };
    throw new Error(`unexpected RPC ${method}`);
  };
  let page;
  try {
    const rootNode = dom.window.document.querySelector('#plugins-view');
    page = createPluginsPage(rootNode, { rpcClient });
    page.init();
    await page.refresh();
    rootNode.querySelector('.plugins-list-row').click();
    assert.equal(rootNode.dataset.mobilePane, 'detail');

    // A runtime filter change that hides the selected row must not strand the
    // mobile user on an empty inspector.
    plugins = [];
    await page.refresh();
    assert.equal(page.state.selectedId, null);
    assert.equal(rootNode.dataset.mobilePane, 'list');
  } finally {
    page?.destroy();
    restore();
  }
});

const learningRecord = {
  id: 'memory-1', tier: 'record', type: 'preference', status: 'stable',
  body: 'Use the existing extension seam', content: 'Use the existing extension seam',
  scope: { level: 'project', project: 'NusaShell' }, updated_at: '2026-09-12T05:00:00Z',
};

const learningExperience = {
  id: 'experience-1', goal: 'Polish the Learning surface', timestamp: '2026-09-12T06:00:00Z',
  outcome: { status: 'success' }, signals: { verified_success: true },
};

function learningRPC(calls) {
  return async (method, payload) => {
    calls.push({ method, payload });
    if (method === 'memory.list') return { entries: [learningRecord] };
    if (method === 'experience.list') return { experiences: [learningExperience], total: 1 };
    if (method === 'experience.get') return { experience: { ...learningExperience, actions: ['open inspector'], observations: ['Keep the seam.'] } };
    if (method === 'learning.graph') return { nodes: [{ id: 'memory-1', kind: 'memory', tier: 'record', name: 'Use the seam' }], edges: [] };
    if (method === 'learning.search') return { items: [{ ...learningRecord, kind: 'memory', name: learningRecord.body, score: 0.91 }] };
    if (method === 'learning.log') return { entries: [], has_more: false };
    if (method === 'learning.jobs.list') return { jobs: [] };
    throw new Error(`unexpected RPC ${method}`);
  };
}

test('Learning atlas mobile flow: results -> record detail -> Back keeps query and selection', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  dom.window.innerWidth = 374;
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  let page;
  try {
    const rootNode = dom.window.document.querySelector('#learning-view');
    page = createLearningPage(rootNode, { rpcClient: learningRPC(calls), onEvent: () => () => {}, graphFactory: () => null });
    page.init();
    const memoryPanel = rootNode.querySelector('#learning-panel-memory');
    const atlasBack = rootNode.querySelector('#learning-atlas-back');
    assert.equal(memoryPanel.dataset.mobilePane, 'list');
    assert.ok(atlasBack, 'the controller mounts an atlas Back control');
    assert.equal(atlasBack.closest('.learning-atlas-layout'), rootNode.querySelector('.learning-atlas-layout'));

    await page.refresh();
    page.switchView('memory');
    await wait(30);
    const search = rootNode.querySelector('#learning-search-input');
    search.value = 'seam';
    search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    await wait(240);
    rootNode.scrollTop = 156;
    rootNode.querySelector('#learning-results [data-result-id]').click();
    assert.equal(memoryPanel.dataset.mobilePane, 'detail');
    assert.equal(rootNode.scrollTop, 0);
    assert.match(rootNode.querySelector('#learning-selection').textContent, /Use the existing extension seam/);

    atlasBack.click();
    await wait();
    assert.equal(memoryPanel.dataset.mobilePane, 'list');
    assert.equal(page.state.selectedResultId, 'memory-1');
    assert.equal(search.value, 'seam');
    assert.equal(rootNode.scrollTop, 156);
    assert.equal(rootNode.querySelector('.learning-result-item.is-selected')?.dataset.resultId, 'memory-1');
  } finally {
    page?.destroy();
    restore();
  }
});

test('Learning experiences mobile flow: list -> episode detail -> Back keeps list state', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  dom.window.innerWidth = 374;
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  let page;
  try {
    const rootNode = dom.window.document.querySelector('#learning-view');
    page = createLearningPage(rootNode, { rpcClient: learningRPC(calls), onEvent: () => () => {}, graphFactory: () => null });
    page.init();
    const panel = rootNode.querySelector('#learning-panel-experiences');
    const back = rootNode.querySelector('#learning-experience-back');
    assert.equal(panel.dataset.mobilePane, 'list');
    assert.ok(back, 'the controller mounts an experiences Back control');

    await page.refresh();
    page.switchView('experiences');
    await wait(30);
    rootNode.scrollTop = 92;
    rootNode.querySelector('#learning-experience-list .learning-experience-item').click();
    await wait();
    assert.equal(panel.dataset.mobilePane, 'detail');
    assert.match(rootNode.querySelector('#learning-experience-detail').textContent, /Keep the seam/);

    back.click();
    await wait();
    assert.equal(panel.dataset.mobilePane, 'list');
    assert.equal(page.state.selectedExperienceId, 'experience-1');
    assert.equal(rootNode.scrollTop, 92);
    assert.equal(rootNode.querySelector('.learning-experience-item.is-selected')?.dataset.outcome, 'success');
  } finally {
    page?.destroy();
    restore();
  }
});

test('Learning experiences mobile flow: Back then tapping the same episode reopens its detail', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  dom.window.innerWidth = 374;
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  let page;
  try {
    const rootNode = dom.window.document.querySelector('#learning-view');
    page = createLearningPage(rootNode, { rpcClient: learningRPC(calls), onEvent: () => () => {}, graphFactory: () => null });
    page.init();
    const panel = rootNode.querySelector('#learning-panel-experiences');
    const back = rootNode.querySelector('#learning-experience-back');
    await page.refresh();
    page.switchView('experiences');
    await wait(30);

    const item = () => rootNode.querySelector('#learning-experience-list .learning-experience-item');
    const detail = () => rootNode.querySelector('#learning-experience-detail');
    // Repeated reentry: Back keeps the selection highlighted, and tapping that
    // same selected episode must reopen its detail every time — the mobile
    // list cannot toggle a detail it is not showing.
    for (let cycle = 1; cycle <= 2; cycle += 1) {
      item().click();
      await wait();
      assert.equal(panel.dataset.mobilePane, 'detail', `reentry ${cycle}: the detail pane opens`);
      assert.equal(detail().dataset.loadedId, 'experience-1');
      assert.match(detail().textContent, /Keep the seam/);
      assert.equal(page.state.selectedExperienceId, 'experience-1');

      back.click();
      await wait();
      assert.equal(panel.dataset.mobilePane, 'list', `reentry ${cycle}: Back returns to the list`);
      assert.equal(page.state.selectedExperienceId, 'experience-1', 'selection survives Back for list highlighting');
    }
  } finally {
    page?.destroy();
    restore();
  }
});

test('Learning experiences desktop flow: clicking the open episode still deselects it', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  dom.window.innerWidth = 1440;
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  let page;
  try {
    const rootNode = dom.window.document.querySelector('#learning-view');
    page = createLearningPage(rootNode, { rpcClient: learningRPC(calls), onEvent: () => () => {}, graphFactory: () => null });
    page.init();
    await page.refresh();
    page.switchView('experiences');
    await wait(30);

    const item = () => rootNode.querySelector('#learning-experience-list .learning-experience-item');
    const detail = () => rootNode.querySelector('#learning-experience-detail');
    item().click();
    await wait();
    assert.equal(page.state.selectedExperienceId, 'experience-1');
    assert.equal(detail().dataset.loadedId, 'experience-1');

    // Desktop keeps both panes visible, so clicking the selected row is the
    // established toggle-off gesture.
    item().click();
    await wait();
    assert.equal(page.state.selectedExperienceId, null);
    assert.equal(detail().dataset.loadedId, undefined);
    assert.match(detail().textContent, /Choose an episode/);
    assert.equal(rootNode.querySelector('.learning-experience-item.is-selected'), null);

    item().click();
    await wait();
    assert.equal(page.state.selectedExperienceId, 'experience-1');
    assert.equal(detail().dataset.loadedId, 'experience-1');
  } finally {
    page?.destroy();
    restore();
  }
});

test('Learning overview disclosures keep dirty drafts and reach the map on demand', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  dom.window.innerWidth = 374;
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  const graphCalls = [];
  let page;
  try {
    const rootNode = dom.window.document.querySelector('#learning-view');
    page = createLearningPage(rootNode, {
      rpcClient: learningRPC(calls),
      onEvent: () => () => {},
      graphFactory: () => ({
        update: () => ({}),
        fit: () => graphCalls.push('fit'),
        select: () => {},
        destroy: () => {},
      }),
    });
    page.init();
    await page.refresh();

    const userCard = rootNode.querySelector('.learning-document-user');
    const textarea = rootNode.querySelector('#learning-user-memory');
    const toggle = userCard.querySelector('.learning-doc-toggle');
    assert.ok(toggle, 'each document card gains a mobile Edit disclosure');
    assert.equal(userCard.dataset.editorOpen, 'false');
    assert.equal(toggle.getAttribute('aria-expanded'), 'false');

    // A dirty draft survives disclosure toggles: the textarea stays in the DOM
    // and the editor state flags are untouched.
    textarea.value = 'Prefer short, focused reviews';
    textarea.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    assert.equal(textarea.dataset.dirty, 'true');
    assert.equal(rootNode.querySelector('#learning-user-status').textContent, 'Unsaved changes');
    toggle.click();
    assert.equal(userCard.dataset.editorOpen, 'true');
    assert.equal(toggle.getAttribute('aria-expanded'), 'true');
    toggle.click();
    assert.equal(userCard.dataset.editorOpen, 'false');
    assert.equal(textarea.value, 'Prefer short, focused reviews');
    assert.equal(textarea.dataset.dirty, 'true');
    assert.equal(rootNode.querySelector('#learning-user-save').disabled, false);

    const mapCard = rootNode.querySelector('.learning-map-card');
    const mapToggle = mapCard.querySelector('.learning-map-toggle');
    assert.ok(mapToggle, 'the map card gains a mobile preview disclosure');
    assert.equal(mapCard.dataset.mapOpen, 'false');
    mapToggle.click();
    assert.equal(mapCard.dataset.mapOpen, 'true');
    await wait(40);
    assert.ok(graphCalls.includes('fit'), 'revealing the map refits the graph into its frame');
    mapToggle.click();
    assert.equal(mapCard.dataset.mapOpen, 'false');
  } finally {
    page?.destroy();
    restore();
  }
});
