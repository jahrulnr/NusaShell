import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import {
  filterMemoryEntries,
  formatLearningDate,
  isMemoryRecord,
  memoryEntryBody,
  sortExperiencesNewestFirst,
  summarizeSignals,
} from '../page/learning/model.js';
import { createLearningPage } from '../page/learning/app.js';

const root = new URL('../', import.meta.url);

test('refresh indicators stay busy until graph/activity finish and clear after failure', async () => {
  const dom = new JSDOM(await readFile(new URL('index.html', root), 'utf8'), { url: 'http://localhost/', pretendToBeVisual: true });
  const restore = installDOMGlobals(dom.window);
  let releaseGraph;
  let rejectActivity;
  const graph = new Promise(resolve => { releaseGraph = resolve; });
  const activity = new Promise((_, reject) => { rejectActivity = reject; });
  activity.catch(() => {});
  const view = dom.window.document.querySelector('#learning-view');
  const page = createLearningPage(view, {
    rpcClient: method => method === 'learning.graph' ? graph : method === 'learning.log' ? activity : Promise.resolve({}),
    onEvent: () => () => {}, graphFactory: () => null,
  });
  try {
    page.init();
    const pending = page.refresh();
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.equal(view.querySelector('#learning-refresh').getAttribute('aria-busy'), 'true');
    assert.equal(page.state.loading, true, 'a finished memory load must not stop the graph spinner');
    releaseGraph({ nodes: [], edges: [] });
    await pending;
    assert.equal(view.querySelector('#learning-refresh').getAttribute('aria-busy'), 'false');
    assert.equal(page.state.loading, false);
    page.switchView('activity');
    assert.equal(view.querySelector('#learning-log-refresh'), null);
    assert.equal(view.querySelector('#learning-graph-refresh'), null);
    const activityRefresh = page.refresh();
    assert.equal(view.querySelector('#learning-refresh').getAttribute('aria-busy'), 'true');
    rejectActivity(new Error('Fixture unavailable'));
    await new Promise(resolve => setTimeout(resolve, 20));
    await activityRefresh;
    assert.equal(view.querySelector('#learning-refresh').getAttribute('aria-busy'), 'false');
    assert.equal(view.querySelector('#learning-refresh').disabled, false);
  } finally {
    releaseGraph({});
    rejectActivity(new Error('Fixture cleanup'));
    page.destroy();
    restore();
    dom.window.close();
  }
});

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

test('Learning model separates always-on documents from structured records', () => {
  const user = { id: 'user-1', tier: 'user', content: 'Prefers compact UI' };
  const record = { id: 'record-1', tier: 'record', type: 'preference', body: 'Use tabs for navigation' };
  const legacyRecord = { id: 'record-2', type: 'fact', content: 'The shell runs locally' };

  assert.equal(isMemoryRecord(user), false);
  assert.equal(isMemoryRecord(record), true);
  assert.equal(isMemoryRecord(legacyRecord), true);
  assert.equal(memoryEntryBody({ subject: 'NusaShell', predicate: 'uses', object: 'Go' }), 'NusaShell uses Go');
  assert.deepEqual(filterMemoryEntries([user, record, legacyRecord], 'LOCAL'), [legacyRecord]);
});

test('Learning model summarizes learning signals into readable labels', () => {
  assert.deepEqual(summarizeSignals({
    user_corrections: 2,
    retries: 1,
    failed_actions: 0,
    verified_success: true,
    root_cause_recovered: true,
  }), ['2 corrections', '1 retry', 'verified', 'recovered']);
  assert.deepEqual(summarizeSignals({}), []);
  assert.equal(formatLearningDate('not-a-date'), 'Unknown time');
});

test('experimental Learning page exposes a focused workbench surface', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const css = await readFile(new URL('learning.css', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const pageApp = await readFile(new URL('page/learning/app.js', root), 'utf8');
  const graph = await readFile(new URL('page/learning/graph.js', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const page = document.querySelector('#learning-view');

  assert.ok(page);
  assert.equal(page.getAttribute('aria-label'), 'Learning workbench');
  assert.equal(document.querySelector('[data-global-route="learning"]')?.getAttribute('href'), '#learning');
  assert.match(app, /learning-active/);
  assert.match(app, /learningPage\?\.activate/);
  assert.doesNotMatch(document.querySelector('#learning-view .learning-header')?.textContent || '', /Keep the useful parts/);
  assert.equal(page.querySelector('.learning-metrics'), null);
  assert.equal(page.querySelectorAll('.learning-panel-heading').length, 0);
  assert.equal(page.querySelectorAll('#learning-panel-memory .learning-card-kicker, #learning-panel-experiences .learning-card-kicker, #learning-panel-activity .learning-card-kicker').length, 0);
  assert.ok(page.querySelector('#learning-results-count'));
  assert.ok(page.querySelector('#learning-experiences-count'));
  assert.ok(page.querySelector('#learning-panel-activity .learning-activity-actions'));
  assert.equal(document.querySelectorAll('[data-learning-tab]').length, 4);
  assert.ok(page.querySelector('#learning-user-memory'));
  assert.ok(page.querySelector('#learning-agent-memory'));
  assert.ok(page.querySelector('#learning-search-input'));
  assert.ok(page.querySelector('#learning-graph'));
  assert.ok(page.querySelector('#learning-log'));
  assert.equal(page.querySelector('#learning-experience-detail')?.getAttribute('aria-label'), 'Learning record details');
  // Tree-relative needles hold both in the prototype (styles/...) — the
  // vendored mirror shipped by P1 — and in the promoted frontend/ copy.
  assert.match(html, /styles\/agent\.css/);
  assert.match(html, /styles\/agent-tools\.css/);
  assert.match(css, /\.learning-page\s*\{/);
  assert.match(css, /\.learning-navigation-row\s*\{/);
  assert.match(css, /\.learning-panel-tools\s*\{/);
  assert.match(css, /\.learning-atlas-layout\s*\{[^}]*grid-template-columns:\s*minmax\(250px, \.68fr\) minmax\(0, 1\.32fr\);/);
  assert.match(css, /\.learning-graph-canvas\s*\{/);
  assert.match(css, /\.learning-graph-canvas\s+\.vis-network\s*> canvas\s*\{/);
  assert.match(css, /\.learning-recent-item\s*\{[^}]*background:\s*transparent;/);
  assert.match(css, /\.learning-experience-item\s*\{[^}]*background:\s*transparent;/);
  assert.match(css, /\.learning-empty\s*\{[^}]*display:\s*grid;[^}]*gap:/);
  assert.match(css, /\.learning-selection-empty\s*\{[^}]*display:\s*grid;[^}]*gap:/);
  assert.match(css, /\.learning-log-meta\s*\{/);
  assert.match(css, /\.learning-log-conversation-panel \.agent-message\s*\{[^}]*width:\s*100%;[^}]*max-width:\s*100%;/);
  assert.match(css, /\.learning-log-conversation-panel \.agent-bubble\s*\{[^}]*width:\s*100%;[^}]*max-width:\s*100%;/);
  assert.match(css, /\.learning-log-conversation-panel \.agent-bubble-text[\s\S]*font-size:\s*11px/);
  assert.match(css, /\.learning-log-conversation-panel\s*\{/);
  assert.match(css, /\.learning-log-entry-actions\s*\{/);
  assert.match(css, /\.learning-log-delete-button\s*\{/);
  assert.match(css, /\.learning-log-conversation-panel\s*\{[^}]*overflow:\s*visible;/);
  assert.match(css, /\.learning-log-conversation-panel \.agent-bubble\s*\{[^}]*font-size:\s*11px/);
  assert.match(css, /\.learning-panel\[data-learning-panel="activity"\]\s*\{[^}]*overflow:\s*hidden;/);
  assert.match(css, /\.shell\.learning-active \.context-dock\s*\{[^}]*max-width:\s*0;/);
  assert.match(css, /@media \(max-width: 760px\)/);
  assert.match(css, /@media \(max-width: 430px\)[\s\S]*\.learning-tab\s*\{[^}]*padding-inline:\s*4px;[^}]*font-size:\s*9px;/);
  assert.match(graph, /js\/views\/learning\.js/);
  assert.match(graph, /vis-network\/vis-network\.esm\.min\.js/);
  assert.match(pageApp, /js\/views\/agent\/render\.js/);
  assert.match(pageApp, /renderConversation/);
  assert.match(pageApp, /createLearningGraph/);
});

test('Learning page blocks an over-cap profile draft before it can be saved', () => {
  const dom = new JSDOM(`
    <section id="learning">
      <textarea id="learning-user-memory"></textarea>
      <span id="learning-user-count"></span>
      <span id="learning-user-status"></span>
      <button id="learning-user-reload" type="button"></button>
      <button id="learning-user-save" type="button"></button>
    </section>`, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  try {
    const page = createLearningPage(dom.window.document.querySelector('#learning'), {
      rpcClient: async () => ({ entries: [] }),
      onEvent: () => () => {},
    });
    page.init();
    const textarea = dom.window.document.querySelector('#learning-user-memory');
    textarea.value = 'x'.repeat(4001);
    textarea.dispatchEvent(new dom.window.Event('input', { bubbles: true }));

    assert.equal(dom.window.document.querySelector('#learning-user-count').dataset.state, 'over');
    assert.equal(dom.window.document.querySelector('#learning-user-status').textContent, 'Unsaved changes');
    assert.equal(dom.window.document.querySelector('#learning-user-save').disabled, true);
    page.destroy();
  } finally {
    restore();
  }
});

test('Learning refresh stays syncing until a slow graph request finishes', async () => {
  const dom = new JSDOM(`
    <section id="learning">
      <span id="learning-sync-state"></span>
    </section>`, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  let finishGraph;
  try {
    const page = createLearningPage(dom.window.document.querySelector('#learning'), {
      rpcClient: async (method) => {
        if (method === 'memory.list') return { entries: [] };
        if (method === 'experience.list') return { experiences: [], total: 0 };
        if (method === 'learning.graph') return new Promise((resolve) => { finishGraph = resolve; });
        throw new Error(`unexpected RPC ${method}`);
      },
      onEvent: () => () => {},
    });
    page.init();
    const refresh = page.refresh();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(dom.window.document.querySelector('#learning-sync-state').textContent, 'Syncing…');
    finishGraph({ nodes: [], edges: [] });
    await refresh;
    assert.equal(dom.window.document.querySelector('#learning-sync-state').textContent, 'Synced');
  } finally {
    restore();
  }
});

test('Learning page reloads the graph when the backend finishes a rebuild', async () => {
  const dom = new JSDOM('<section id="learning"></section>', { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const handlers = new Map();
  const graphCalls = [];
  try {
    const page = createLearningPage(dom.window.document.querySelector('#learning'), {
      rpcClient: async (method) => {
        if (method === 'learning.graph') { graphCalls.push(method); return { nodes: [], edges: [] }; }
        if (method === 'memory.list') return { entries: [] };
        if (method === 'experience.list') return { experiences: [], total: 0 };
        throw new Error(`unexpected RPC ${method}`);
      },
      onEvent: (name, handler) => { handlers.set(name, handler); return () => {}; },
      graphFactory: () => null,
    });
    page.init();
    // The async edge builder emits learning.graph.updated after a changed
    // fingerprint — the subscription is the only path that picks it up.
    assert.ok(handlers.has('learning.graph.updated'), 'learning.graph.updated must be subscribed');
    handlers.get('learning.graph.updated')();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(graphCalls.length, 1, 'the event triggers one graph reload');
    page.destroy();
  } finally {
    restore();
  }
});

test('Learning activation rehydrates the graph after the surface becomes visible', () => {
  const dom = new JSDOM('<section id="learning"></section>', { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  try {
    const page = createLearningPage(dom.window.document.querySelector('#learning'), {
      rpcClient: async () => ({ entries: [] }),
      onEvent: () => () => {},
      graphFactory: () => ({
        update: () => calls.push('update'),
        fit: () => calls.push('fit'),
        select: () => {},
        destroy: () => {},
      }),
    });
    page.init();
    page.state.entries = [{ id: 'memory-1', tier: 'record', body: 'Keep this' }];
    page.state.graphNodes = [{ id: 'memory-1', kind: 'memory', tier: 'record', name: 'Keep this' }];
    page.activate();

    assert.deepEqual(calls, ['update', 'fit']);
  } finally {
    restore();
  }
});

test('Learning page loads the live contract and keeps memory actions local to the selected item', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  const user = { id: 'user-1', tier: 'user', content: 'Prefers compact reviews', created_at: '2026-09-12T04:00:00Z' };
  const agent = { id: 'agent-1', tier: 'agent', content: 'Keep changes focused' };
  const record = {
    id: 'memory-1', tier: 'record', type: 'preference', status: 'stable',
    body: 'Use the existing extension seam', content: 'Use the existing extension seam',
    scope: { level: 'project', project: 'NusaShell' }, updated_at: '2026-09-12T05:00:00Z',
  };
  const rpcClient = async (method, payload) => {
    calls.push({ method, payload });
    if (method === 'memory.list') return { entries: [user, agent, record] };
    if (method === 'experience.list') return {
      experiences: [{
        id: 'experience-1', goal: 'Polish the Learning surface', timestamp: '2026-09-12T06:00:00Z',
        outcome: { status: 'success' }, signals: { verified_success: true },
      }],
      total: 1,
    };
    if (method === 'learning.graph') return {
      nodes: [
        { id: 'skill-1', kind: 'skill', name: 'Frontend UI' },
        { id: 'memory-1', kind: 'memory', tier: 'record', name: 'Use the seam' },
      ],
      edges: [{ from: 'skill-1', to: 'memory-1', type: 'related', weight: 0.8 }],
    };
    if (method === 'learning.search') return { items: [{ ...record, kind: 'memory', name: record.body, score: 0.91 }] };
    if (method === 'memory.user.update') return { entry: { ...user, content: payload.content } };
    throw new Error(`unexpected RPC ${method}`);
  };

  try {
    const page = createLearningPage(dom.window.document.querySelector('#learning-view'), {
      rpcClient,
      onEvent: () => () => {},
      graphFactory: () => null,
    });
    page.init();
    await page.refresh();

    assert.equal(dom.window.document.querySelector('.learning-metrics'), null);
    assert.equal(dom.window.document.querySelector('#learning-user-memory').value, user.content);
    assert.deepEqual(page.state.graphNodes.map(({ id }) => id), ['skill-1', 'memory-1']);

    dom.window.document.querySelector('#learning-tab-memory').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(dom.window.document.querySelectorAll('#learning-results [data-result-id]').length, 1);
    dom.window.document.querySelector('#learning-results [data-result-id]').click();
    assert.match(dom.window.document.querySelector('#learning-selection').textContent, /Use the existing extension seam/);

    const textarea = dom.window.document.querySelector('#learning-user-memory');
    textarea.value = 'Prefer short, focused reviews';
    textarea.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
    assert.equal(dom.window.document.querySelector('#learning-user-save').disabled, false);
    dom.window.document.querySelector('#learning-user-save').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.deepEqual(calls.at(-1), { method: 'memory.user.update', payload: { content: 'Prefer short, focused reviews' } });
    assert.equal(dom.window.document.querySelector('#learning-user-status').textContent, 'Saved');
  } finally {
    restore();
  }
});

test('Learning activity opens the persisted background agent conversation inline', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  const rpcClient = async (method, payload) => {
    calls.push({ method, payload });
    if (method === 'learning.log') return {
      entries: [{
        type: 'review',
        status: 'done',
        ts: '2026-09-12T06:00:00Z',
        conversation_id: 'source-1',
        conversation_title: 'Polish the Learning surface',
        llm_conversation_id: 'agent-run-1',
        model: 'openrouter:nex-agi/nex-n2.5-pro:free',
        mutations: [{ kind: 'memory.upsert', snippet: 'Use the existing seam' }],
        detail: { job_id: 'job-1', kind: 'learner' },
      }],
      has_more: false,
    };
    if (method === 'learning.jobs.list') return { jobs: [] };
    if (method === 'agent.conversations.get') return {
      messages: [
        { id: 'message-1', role: 'user', content: 'EXPERIENCE PACKET: keep the existing seam', created_at: '2026-09-12T05:59:00Z' },
        { id: 'message-2', role: 'assistant', content: 'Keep one durable memory record.', created_at: '2026-09-12T06:00:00Z' },
      ],
    };
    throw new Error(`unexpected RPC ${method}`);
  };
  const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
  try {
    const page = createLearningPage(dom.window.document.querySelector('#learning-view'), {
      rpcClient,
      onEvent: () => () => {},
      graphFactory: () => null,
    });
    page.init();
    page.switchView('activity');
    await flush();

    const button = dom.window.document.querySelector('.learning-log-conversation-button');
    assert.ok(button, 'expected a background agent log button');
    const actions = button.closest('.learning-log-entry')?.querySelector('.learning-log-entry-actions');
    assert.ok(actions, 'activity actions should share one row');
    assert.equal(actions.querySelectorAll('button').length, 2);
    const metadata = button.closest('.learning-log-entry')?.querySelector('.learning-log-meta');
    assert.equal(metadata?.querySelector('.learning-log-meta-kind')?.textContent, 'kind: learner');
    assert.equal(metadata?.querySelector('.learning-log-meta-model')?.textContent, 'model: openrouter:nex-agi/nex-n2.5-pro:free');
    button.click();
    await flush();
    await flush();

    const panel = dom.window.document.querySelector('.learning-log-conversation-panel');
    assert.ok(panel, 'agent transcript should open below the activity entry');
    assert.match(panel.textContent, /EXPERIENCE PACKET/);
    assert.match(panel.textContent, /Keep one durable memory record/);
    assert.equal(button.textContent, 'Hide agent log');
    assert.deepEqual(calls.filter(({ method }) => method === 'agent.conversations.get'), [
      { method: 'agent.conversations.get', payload: { id: 'agent-run-1' } },
    ]);

    button.click();
    assert.equal(dom.window.document.querySelector('.learning-log-conversation-panel'), null);
    assert.equal(button.textContent, 'View agent log');
    page.destroy();
  } finally {
    restore();
  }
});

// Ported from production frontend/tests/learning.test.mjs — re-pointed at the
// live page/learning/model.js copy (the js/views/learning.js twin is dead
// code post-promotion; model.js was made null-safe to keep this contract).
test('Experience items are sorted newest first without mutating the payload', () => {
  const items = [
    { id: 'oldest', timestamp: '2026-01-01T00:00:00Z' },
    { id: 'newest', timestamp: '2026-09-01T00:00:00Z' },
    { id: 'middle', timestamp: '2026-05-01T00:00:00Z' },
    { id: 'untimed' },
  ];
  const sorted = sortExperiencesNewestFirst(items);
  assert.deepEqual(
    sorted.map((e) => e.id),
    ['newest', 'middle', 'oldest', 'untimed'],
  );
  assert.deepEqual(items.map((e) => e.id), ['oldest', 'newest', 'middle', 'untimed'], 'payload must not be reordered in place');
  assert.deepEqual(sortExperiencesNewestFirst(null), []);
});

// Ported subscription audit — now asserted against page/learning/app.js,
// which keeps the same event set (jobs + experience, never review events).
test('Learning subscribes to jobs and recorded experience, not review events', async () => {
  const learningView = await readFile(new URL('page/learning/app.js', root), 'utf8');
  assert.match(learningView, /experience\.recorded/);
  assert.match(learningView, /learning\.job\.started/);
  assert.match(learningView, /learning\.job\.done/);
  assert.match(learningView, /learning\.job\.error/);
  assert.doesNotMatch(learningView, /learning\.review\.started/);
});
