import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { createLogsPage } from '../page/logs/app.js';
import { createProvidersPage } from '../page/providers/app.js';
import { createSkillsPage } from '../page/skills/app.js';

const root = new URL('../', import.meta.url);
const wait = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));

// Extracts the body of a top-level `@media <query>` block by brace counting so
// assertions stay scoped to one breakpoint — a plain source regex would leak
// into the (max-width: 760px) tablet seam and the reduced-motion query.
function mediaBlock(css, query) {
  const at = css.indexOf(`@media ${query}`);
  assert.notEqual(at, -1, `stylesheet must keep the @media ${query} seam`);
  const open = css.indexOf('{', at);
  let depth = 0;
  for (let i = open; i < css.length; i += 1) {
    if (css[i] === '{') depth += 1;
    else if (css[i] === '}') {
      depth -= 1;
      if (depth === 0) return css.slice(open + 1, i);
    }
  }
  assert.fail(`@media ${query} block never closes`);
}

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

test('mobile page navigation has explicit Back controls and keeps the desktop split', async () => {
  const [html, skillsCss, providersCss, logsCss] = await Promise.all([
    readFile(new URL('index.html', root), 'utf8'),
    readFile(new URL('skills.css', root), 'utf8'),
    readFile(new URL('providers.css', root), 'utf8'),
    readFile(new URL('logs.css', root), 'utf8'),
  ]);
  const { document } = new JSDOM(html).window;

  for (const selector of ['#skills-workspace', '#providers-panel .providers-workspace', '#acp-panel .providers-workspace', '#logs-workspace']) {
    assert.equal(document.querySelector(selector)?.dataset.mobilePane, 'list', `${selector} starts with the list pane`);
  }
  for (const id of ['skills-mobile-back', 'skill-file-back', 'providers-mobile-back', 'acp-mobile-back', 'logs-mobile-back']) {
    assert.ok(document.getElementById(id), `${id} is present for keyboard and touch navigation`);
  }
  assert.match(skillsCss, /\.skills-workspace\[data-mobile-pane="list"\][^{]*\{[^}]*\}/);
  assert.match(providersCss, /\.providers-workspace\[data-mobile-pane="detail"\]/);
  assert.match(logsCss, /\.logs-workspace\[data-mobile-pane="detail"\]/);
  assert.match(skillsCss, /grid-template-columns:\s*minmax\(270px, 340px\) minmax\(0, 1fr\)/);
  assert.match(providersCss, /grid-template-columns:\s*minmax\(280px, 340px\) minmax\(0, 1fr\)/);
  assert.match(logsCss, /grid-template-columns:\s*minmax\(0, 1\.35fr\) minmax\(280px, \.65fr\)/);
  assert.match(skillsCss, /\.skills-page\[data-mobile-pane="detail"\]\s+\.skills-toolbar\s*\{\s*display:\s*none;/);
  assert.match(skillsCss, /\.skills-workspace\s*\{[^}]*flex:\s*none;[^}]*overflow:\s*visible;/);
  assert.match(providersCss, /\.providers-panel\[data-mobile-pane="detail"\]\s+\.providers-toolbar\s*\{\s*display:\s*none;/);
  assert.match(logsCss, /\.logs-page\[data-mobile-pane="detail"\]\s+\.logs-controls\s*\{\s*display:\s*none;/);
  assert.match(logsCss, /\.logs-page\[data-mobile-pane="detail"\]\s+\.logs-topbar-actions\s*\{\s*display:\s*none;/);
});

test('Logs mobile workspace stays a bounded card whose panes scroll inside', async () => {
  const logsCss = await readFile(new URL('logs.css', root), 'utf8');
  const mobile = mediaBlock(logsCss, '(max-width: 720px)');
  // The mobile contract is the desktop seam, not a natural page scroller.
  // The shell row already bounds .logs-page to the leftover viewport (100%
  // height minus the 50px topbar), so the page itself must never scroll —
  // the <=760px tablet band's `overflow-y: auto` is cancelled here.
  assert.match(mobile, /\.logs-page\s*\{[^}]*overflow:\s*hidden/);
  // The workspace keeps its flex share and hard clip so the bordered card
  // fills the remaining height under the toolbar instead of growing a tall
  // outer page that paints rows past its border.
  assert.match(mobile, /\.logs-workspace\s*\{[^}]*flex:\s*1;[^}]*overflow:\s*hidden;/);
  // Each pane's own scroller handles overflow inside the card: the entry
  // list keeps its internal overflow:auto and the detail panel clips to the
  // card. No pane may fall back to natural/visible overflow at <=720px.
  assert.match(mobile, /\.logs-list\s*\{[^}]*flex:\s*1;[^}]*overflow:\s*auto;/);
  assert.match(mobile, /\.logs-detail-panel\s*\{[^}]*overflow:\s*auto;/);
  assert.doesNotMatch(mobile, /\.logs-list\s*\{[^}]*overflow:\s*visible/);
  assert.doesNotMatch(mobile, /\.logs-workspace\s*\{[^}]*overflow:\s*visible/);
  assert.doesNotMatch(mobile, /\.logs-detail-panel\s*\{[^}]*overflow:\s*visible/);
});

test('Skills Back restores the list and reopens the same package/file without rereading it', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  dom.window.innerWidth = 374;
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  const skill = {
    id: 'mobile-skill', name: 'Mobile skill', description: 'A fixture package.',
    category: 'testing', owned_by: 'user', status: 'trusted', version: 1,
    active_version: 1, files: [{ path: 'SKILL.md', type: 'file', sizeBytes: 20 }],
  };
  const rpcClient = async (method, payload) => {
    calls.push({ method, payload });
    if (method === 'skills.list') return { skills: [skill] };
    if (method === 'skills.read') return { skill };
    if (method === 'skills.file.read') return { content: '# Mobile skill\n', sizeBytes: 14, truncated: false };
    throw new Error(`unexpected RPC ${method}`);
  };
  let page;
  try {
    const rootNode = dom.window.document.querySelector('#skills-view');
    page = createSkillsPage(rootNode, { rpcClient, onEvent: () => () => {} });
    page.init();
    await page.refresh();
    await page.selectSkill(skill.id, skill.owned_by);
    assert.equal(rootNode.querySelector('#skills-workspace').dataset.mobilePane, 'list');

    const search = rootNode.querySelector('#skills-search');
    search.value = 'mobile';
    search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    await wait(170);
    rootNode.scrollTop = 128;
    const readCount = calls.filter((call) => call.method === 'skills.read').length;

    rootNode.querySelector('.skill-index-select').click();
    await wait();
    const workspace = rootNode.querySelector('#skills-workspace');
    assert.equal(workspace.dataset.mobilePane, 'detail');
    assert.equal(rootNode.scrollTop, 0, 'opening a skill starts at the top of its detail step');
    assert.equal(rootNode.dataset.mobilePane, 'detail');
    assert.equal(rootNode.querySelector('.skill-sheet-body').dataset.mobileFilePane, 'files');

    rootNode.scrollTop = 217;
    await page.openSkillFile('SKILL.md');
    assert.equal(rootNode.querySelector('.skill-sheet-body').dataset.mobileFilePane, 'content');
    assert.equal(rootNode.scrollTop, 0, 'opening a file starts at the top of its reader step');
    assert.equal(rootNode.querySelector('#skill-file-viewer').hidden, false);

    rootNode.querySelector('#skill-file-back').click();
    await wait();
    assert.equal(rootNode.querySelector('.skill-sheet-body').dataset.mobileFilePane, 'files');
    assert.equal(rootNode.scrollTop, 217, 'Back to files restores the package file-list scroll');

    rootNode.querySelector('.skill-file-row[aria-current="true"]').click();
    await wait();
    assert.equal(rootNode.querySelector('.skill-sheet-body').dataset.mobileFilePane, 'content');
    assert.equal(rootNode.scrollTop, 0);

    rootNode.querySelector('#skills-mobile-back').click();
    await wait();
    assert.equal(workspace.dataset.mobilePane, 'list');
    assert.equal(page.state.activeId, skill.id);
    assert.equal(page.state.activeFile, 'SKILL.md');
    assert.equal(search.value, 'mobile');
    assert.equal(rootNode.scrollTop, 128);

    rootNode.querySelector('.skill-index-select').click();
    await wait();
    assert.equal(workspace.dataset.mobilePane, 'detail');
    assert.equal(page.state.activeFile, 'SKILL.md');
    assert.equal(rootNode.querySelector('#skill-file-viewer').hidden, false);
    assert.equal(calls.filter((call) => call.method === 'skills.read').length, readCount);

    rootNode.querySelector('#skill-file-back').click();
    assert.equal(rootNode.querySelector('.skill-sheet-body').dataset.mobileFilePane, 'files');
    assert.equal(page.state.activeFile, 'SKILL.md');
  } finally {
    page?.destroy();
    restore();
  }
});

test('Providers and ACP Back preserve each tab selection and search query across resize', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  dom.window.innerWidth = 374;
  const restore = installDOMGlobals(dom.window);
  const provider = {
    id: 'provider-fixture', kind: 'chat', name: 'Fixture Router',
    base_url: 'https://router.example/v1', configured: true, enabled: true,
    has_api_key: true, models: [{ id: 'fixture-model', provider_id: 'provider-fixture', context: 32000 }],
  };
  const agent = { id: 'acp-fixture', name: 'Fixture Worker', transport: 'stdio', enabled: true, modes: [], models: [] };
  const rpcClient = async (method) => {
    if (method === 'ai.providers.list') return { providers: [provider] };
    if (method === 'acp.agents.list') return { agents: [agent] };
    throw new Error(`unexpected RPC ${method}`);
  };
  let page;
  try {
    const rootNode = dom.window.document.querySelector('#providers-view');
    page = createProvidersPage(rootNode, { rpcClient });
    page.init();
    await page.refresh();
    assert.equal(rootNode.querySelector('#providers-panel .providers-workspace').dataset.mobilePane, 'list');
    assert.equal(rootNode.querySelector('#acp-panel .providers-workspace').dataset.mobilePane, 'list');

    const search = rootNode.querySelector('#providers-search');
    search.value = 'Fixture';
    search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    await wait(140);
    rootNode.scrollTop = 509;
    rootNode.querySelector('#providers-list .providers-list-row').click();
    const providersWorkspace = rootNode.querySelector('#providers-panel .providers-workspace');
    assert.equal(providersWorkspace.dataset.mobilePane, 'detail');
    assert.equal(rootNode.querySelector('#providers-panel').dataset.mobilePane, 'detail');
    assert.equal(rootNode.scrollTop, 0, 'opening provider detail starts at the top of the focused step');

    const originalSetTimeout = dom.window.setTimeout;
    const queuedCallbacks = [];
    dom.window.setTimeout = (callback) => { queuedCallbacks.push(callback); return queuedCallbacks.length; };
    rootNode.querySelector('#providers-mobile-back').click();
    rootNode.scrollTop = 83;
    rootNode.querySelector('#providers-list .providers-list-row').click();
    rootNode.querySelector('#providers-mobile-back').click();
    queuedCallbacks.reverse().forEach((callback) => callback());
    dom.window.setTimeout = originalSetTimeout;

    assert.equal(providersWorkspace.dataset.mobilePane, 'list');
    assert.equal(rootNode.scrollTop, 83, 'a stale Back restoration cannot override a newer list visit');
    assert.equal(page.state.selectedProviderId, provider.id);
    assert.equal(search.value, 'Fixture');

    dom.window.innerWidth = 1440;
    dom.window.dispatchEvent(new dom.window.Event('resize'));
    dom.window.innerWidth = 374;
    dom.window.dispatchEvent(new dom.window.Event('resize'));
    assert.equal(providersWorkspace.dataset.mobilePane, 'list');
    assert.equal(page.state.selectedProviderId, provider.id);

    rootNode.querySelector('[data-providers-tab="acp"]').click();
    const acpWorkspace = rootNode.querySelector('#acp-panel .providers-workspace');
    rootNode.scrollTop = 324;
    rootNode.querySelector('#acp-list .providers-list-row').click();
    assert.equal(acpWorkspace.dataset.mobilePane, 'detail');
    assert.equal(rootNode.scrollTop, 0, 'opening ACP detail starts at the top of the focused step');
    rootNode.querySelector('#acp-mobile-back').click();
    await wait();
    assert.equal(acpWorkspace.dataset.mobilePane, 'list');
    assert.equal(rootNode.scrollTop, 324);
    assert.equal(page.state.selectedAcpId, agent.id);
    assert.equal(search.value, 'Fixture');
  } finally {
    page?.destroy();
    restore();
  }
});

test('Logs Back restores the in-card list scroll and follow-tail state', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  dom.window.innerWidth = 374;
  const restore = installDOMGlobals(dom.window);
  const subscriptions = new Map();
  const entries = [
    { id: 'mobile-log-1', time: '2026-10-14T09:00:00Z', level: 'info', source: 'agent', message: 'conversation started' },
    { id: 'mobile-log-2', time: '2026-10-14T09:01:00Z', level: 'warn', source: 'provider', message: 'retrying fixture request' },
  ];
  const rpcClient = async (method) => {
    if (method === 'logs.list') return { entries };
    if (method === 'logs.clear') return { ok: true };
    throw new Error(`unexpected RPC ${method}`);
  };
  let page;
  try {
    const rootNode = dom.window.document.querySelector('#logs-view');
    page = createLogsPage(rootNode, {
      rpcClient,
      onEvent: (type, handler) => { subscriptions.set(type, handler); return () => subscriptions.delete(type); },
    });
    page.init();
    await page.refresh();
    const workspace = rootNode.querySelector('.logs-workspace');
    const list = rootNode.querySelector('#logs-list');
    const detailPanel = rootNode.querySelector('.logs-detail-panel');
    // jsdom reports 0 for layout scroll metrics; pin the card's scrollable
    // capacity so the follow-tail math is exercisable.
    Object.defineProperty(list, 'scrollHeight', { configurable: true, value: 1200 });
    Object.defineProperty(list, 'clientHeight', { configurable: true, value: 260 });
    assert.equal(workspace.dataset.mobilePane, 'list');

    const search = rootNode.querySelector('#logs-search');
    search.value = 'retrying';
    search.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    await wait(160);
    list.scrollTop = 204;
    rootNode.querySelector('#logs-list .logs-entry').click();
    assert.equal(workspace.dataset.mobilePane, 'detail');
    assert.equal(rootNode.dataset.mobilePane, 'detail');
    assert.equal(detailPanel.scrollTop, 0, 'the detail pane opens at the top of its card');
    assert.equal(page.state.selectedId, entries[1].id);

    rootNode.querySelector('#logs-mobile-back').click();
    await wait();
    assert.equal(workspace.dataset.mobilePane, 'list');
    assert.equal(page.state.selectedId, entries[1].id);
    assert.equal(page.state.follow, true);
    assert.equal(search.value, 'retrying');
    assert.equal(list.scrollTop, 1200, 'Back while following lands on the newest entry inside the card');
    assert.equal(rootNode.querySelector('#logs-list .logs-entry').dataset.logId, entries[1].id);
    assert.equal(rootNode.scrollTop, 0, 'the outer page is never the scroller');

    const follow = rootNode.querySelector('#logs-follow');
    follow.checked = false;
    follow.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
    list.scrollTop = 204;
    rootNode.querySelector('#logs-list .logs-entry').click();
    assert.equal(detailPanel.scrollTop, 0);
    // A real browser resets the hidden list scroller to 0; model it so Back
    // restoration is proven to write the saved offset, not keep a stale one.
    list.scrollTop = 0;
    rootNode.querySelector('#logs-mobile-back').click();
    await wait();
    assert.equal(page.state.follow, false);
    assert.equal(list.scrollTop, 204, 'Back restores the in-card list reading position');
    assert.equal(rootNode.scrollTop, 0, 'the outer page is never the scroller');
  } finally {
    page?.destroy();
    restore();
  }
});

test('desktop workbench blocks sit behind a min-width seam with transparent mobile wrappers', async () => {
  const [html, logsCss, learningCss, providersCss] = await Promise.all([
    readFile(new URL('index.html', root), 'utf8'),
    readFile(new URL('logs.css', root), 'utf8'),
    readFile(new URL('learning.css', root), 'utf8'),
    readFile(new URL('providers.css', root), 'utf8'),
  ]);
  const { document } = new JSDOM(html).window;

  // Wrappers added for the desktop band/sub-band must generate no box on
  // mobile, where the pre-workbench layout is pinned.
  assert.match(logsCss, /\.logs-toolbar\s*\{\s*display:\s*contents;\s*\}/);
  assert.match(learningCss, /\.learning-atlas-bar\s*\{\s*display:\s*contents;\s*\}/);

  // The workbench overrides live in a single desktop media block per sheet.
  for (const [name, css] of [['logs', logsCss], ['learning', learningCss], ['providers', providersCss]]) {
    assert.match(css, /@media \(min-width: 761px\)/, `${name}.css must gate desktop overrides on @media (min-width: 761px)`);
  }

  // Desktop focus order runs filters before actions: inside .logs-toolbar the
  // controls precede the topbar in DOM order (mobile reorders visually via
  // .logs-topbar { order: -1 }).
  const toolbar = document.querySelector('#logs-view .logs-toolbar');
  assert.ok(toolbar, 'logs page must wrap controls + topbar in .logs-toolbar');
  const controls = toolbar.querySelector('.logs-controls');
  const topbar = toolbar.querySelector('.logs-topbar');
  assert.ok(controls && topbar, '.logs-toolbar must contain .logs-controls and .logs-topbar');
  assert.ok(controls.compareDocumentPosition(topbar) & document.defaultView.Node.DOCUMENT_POSITION_FOLLOWING,
    '.logs-controls must come before .logs-topbar inside .logs-toolbar');
});
