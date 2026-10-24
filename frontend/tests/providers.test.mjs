import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import {
  filterProviders,
  providerKindLabel,
  providerStatusLabel,
} from '../page/providers/model.js';
import {
  cacheTTLsFor,
  effectiveCacheTTL,
  effectiveReasoningSummary,
  reasoningSummariesFor,
} from '../js/views/providers.js';
import { createProvidersPage } from '../page/providers/app.js';

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

const configured = {
  id: 'prov_tokenrouter',
  kind: 'chat',
  name: 'TokenRouter',
  base_url: 'https://api.tokenrouter.example/v1',
  enabled: true,
  configured: true,
  has_api_key: true,
  models: [{ id: 'model-a', provider_id: 'prov_tokenrouter', provider_name: 'TokenRouter', context: 128000 }],
};

test('provider model labels keep configured-only semantics', () => {
  const phantom = { id: 'anthropic', name: 'Anthropic', configured: false, kind: 'messages' };
  assert.equal(providerKindLabel(configured), 'Chat');
  assert.equal(providerStatusLabel(configured), 'Ready');
  assert.equal(providerStatusLabel(phantom), 'Needs setup');
  assert.deepEqual(filterProviders([phantom, configured], 'token'), [configured]);
  assert.deepEqual(filterProviders([phantom, configured], '', 'ready'), [configured]);
});

test('experimental Providers route exposes Providers and ACP tabs without default provider cards', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const css = await readFile(new URL('providers.css', root), 'utf8');
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const pageApp = await readFile(new URL('page/providers/app.js', root), 'utf8');
  const { document } = new JSDOM(html).window;
  const page = document.querySelector('#providers-view');

  assert.ok(page);
  assert.equal(page.getAttribute('aria-label'), 'Providers workspace');
  assert.deepEqual([...page.querySelectorAll('[data-providers-tab]')].map((tab) => tab.textContent.trim()), ['Providers', 'ACP']);
  assert.ok(page.querySelector('#providers-list'));
  assert.ok(page.querySelector('#acp-list'));
  assert.ok(page.querySelector('#providers-add'));
  assert.ok(page.querySelector('#acp-add'));
  assert.equal(page.querySelector('#providers-tab-hint'), null);
  assert.doesNotMatch(page.textContent, /Configured LLM connections/);
  assert.doesNotMatch(pageApp, /BUILTIN_PROVIDERS|mergeProviderRegistry/);
  assert.match(app, /providersPage\?\.refresh/);
  assert.match(app, /providers-active/);
  assert.match(pageApp, /ai\.providers\.list/);
  assert.match(pageApp, /acp\.agents\.list/);
  assert.match(pageApp, /ai\.providers\.test/);
  assert.match(pageApp, /acp\.agents\.probe/);
  assert.match(css, /\.providers-tabs\s*\{/);
  assert.match(css, /\.providers-workspace\s*\{/);
  assert.match(css, /\.shell\.providers-active ~ \.ss-content\.slim-select\s*\{[^}]*z-index:\s*1100 !important;/);
});

test('Providers page renders only backend records and patches a provider test in place', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  const rpcClient = async (method, payload) => {
    calls.push({ method, payload });
    if (method === 'ai.providers.list') return {
      providers: [
        { id: 'anthropic', kind: 'messages', name: 'Anthropic', configured: false, enabled: true, models: [] },
        configured,
      ],
    };
    if (method === 'ai.providers.test') return { ok: true, latency_ms: 42, models: 1 };
    if (method === 'ai.providers.import-models') return { models: configured.models };
    if (method === 'acp.agents.list') return { agents: [] };
    throw new Error(`unexpected RPC ${method}`);
  };
  let page;
  try {
    page = createProvidersPage(dom.window.document.querySelector('#providers-view'), { rpcClient });
    page.init();
    await page.refresh();

    assert.equal(dom.window.document.querySelectorAll('#providers-list .providers-list-item').length, 1);
    assert.equal(dom.window.document.querySelector('#providers-list').textContent.includes('Anthropic'), false);
    dom.window.document.querySelector('.providers-list-row')?.click();
    const detail = dom.window.document.querySelector('#providers-detail');
    const row = dom.window.document.querySelector('.providers-list-item');
    const status = dom.window.document.querySelector('#providers-detail-status');
    assert.equal(detail.hidden, false);
    assert.equal(dom.window.document.querySelector('#providers-detail-title').textContent, 'TokenRouter');
    const testButton = dom.window.document.querySelector('#providers-test');
    testButton.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(status.textContent, 'Connected · 42 ms');
    assert.equal(detail, dom.window.document.querySelector('#providers-detail'));
    assert.equal(row, dom.window.document.querySelector('.providers-list-item'));
    assert.equal(calls.filter(({ method }) => method === 'ai.providers.list').length, 1);
    assert.ok(calls.some(({ method, payload }) => method === 'ai.providers.test' && payload.id === configured.id));
  } finally {
    page?.destroy();
    restore();
  }
});

test('ACP tab renders an empty state and keeps the list separate from providers', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const rpcClient = async (method) => {
    if (method === 'ai.providers.list') return { providers: [] };
    if (method === 'acp.agents.list') return { agents: [] };
    throw new Error(`unexpected RPC ${method}`);
  };
  let page;
  try {
    page = createProvidersPage(dom.window.document.querySelector('#providers-view'), { rpcClient });
    page.init();
    await page.refresh();
    dom.window.document.querySelector('[data-providers-tab="acp"]').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(page.state.tab, 'acp');
    assert.equal(dom.window.document.querySelector('#acp-empty').hidden, false);
    assert.equal(dom.window.document.querySelector('#providers-panel').hidden, true);
    assert.equal(dom.window.document.querySelector('#acp-panel').hidden, false);
  } finally {
    page?.destroy();
    restore();
  }
});

test('ACP detail aligns model preference actions and authentication card', async () => {
  const css = await readFile(new URL('providers.css', root), 'utf8');
  assert.match(css, /#acp-models-list \.providers-model-item\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)\s+auto\s+auto;/);
  assert.match(css, /#acp-auth-card\s*\{[^}]*margin-top:\s*0;/);
  assert.match(css, /#providers-detail\s*\{[^}]*display:\s*flex;[^}]*height:\s*100%;[^}]*min-height:\s*100%;/);
  assert.match(css, /#providers-detail \.providers-model-list\s*\{[^}]*max-height:\s*none;[^}]*flex:\s*1;/);
});

// Ported from production frontend/tests/providers.test.mjs — these helpers
// stay live through page/providers/app.js imports; built-in-card and Codex
// detail markup assertions were dropped (the new page has no default cards).
test('gemini kind exposes no cache TTL chips', () => {
  assert.deepEqual(cacheTTLsFor({ kind: 'gemini' }), []);
  assert.equal(effectiveCacheTTL({ kind: 'gemini', cache_ttl: '30m' }), '');
});

test('provider cache TTL surfaces show the sendable value without an upstream qualifier', async () => {
  for (const path of ['js/views/providers.js', 'page/providers/app.js', 'providers.css']) {
    const source = await readFile(new URL(path, root), 'utf8');
    assert.doesNotMatch(source, /via upstream/, `${path} dropped the upstream qualifier`);
    assert.doesNotMatch(source, /provider-cache-ttl-note/, `${path} dropped the upstream note style`);
  }
});

test('cache TTL chips use sendable values and keep a selected default', () => {
  assert.deepEqual(cacheTTLsFor({ kind: 'messages' }), ['5m', '1h', 'off']);
  assert.deepEqual(cacheTTLsFor({ kind: 'responses' }), ['30m', 'off']);
  assert.deepEqual(cacheTTLsFor({ kind: 'codex' }), ['30m', 'off']);
  assert.deepEqual(cacheTTLsFor({ kind: 'chat' }), ['5m', '1h', 'off']);
  assert.deepEqual(cacheTTLsFor({ kind: 'messages', cache_ttls: ['5m', '1h'] }), ['5m', '1h', 'off']);
  assert.equal(effectiveCacheTTL({ kind: 'messages' }), '5m');
  assert.equal(effectiveCacheTTL({ kind: 'messages', cache_ttl: '1h' }), '1h');
  assert.equal(effectiveCacheTTL({ kind: 'messages', cache_ttl: 'off' }), 'off');
  assert.equal(effectiveCacheTTL({ kind: 'responses' }), '30m');
});

test('Codex reasoning summary chips expose supported verbosity levels', () => {
  assert.deepEqual(reasoningSummariesFor({ kind: 'codex' }), []);
  assert.deepEqual(reasoningSummariesFor({
    kind: 'codex',
    reasoning_summaries: ['auto', 'concise', 'detailed', 'none'],
  }), ['auto', 'concise', 'detailed', 'none']);
  assert.deepEqual(reasoningSummariesFor({ kind: 'responses' }), []);
  assert.equal(effectiveReasoningSummary({ kind: 'codex' }), '');
  assert.equal(effectiveReasoningSummary({
    kind: 'codex',
    reasoning_summaries: ['auto', 'concise', 'detailed', 'none'],
  }), 'auto');
  assert.equal(effectiveReasoningSummary({
    kind: 'codex',
    reasoning_summaries: ['auto', 'concise', 'detailed', 'none'],
    reasoning_summary: 'detailed',
  }), 'detailed');
});

test('Codex runtime is a sibling card and device sign-in cancels without later polling', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  let resolveStart;
  const rpcClient = async (method) => {
    calls.push(method);
    if (method === 'ai.providers.list') return { providers: [{ id: 'codex', kind: 'codex', name: 'Codex', configured: true, models: [] }, configured] };
    if (method === 'acp.agents.list') return { agents: [] };
    if (method === 'ai.codex.accounts.list') return { accounts: [] };
    if (method === 'ai.codex.runtime.status') return { installed: true, version: '1.0' };
    if (method === 'ai.codex.device.start') return new Promise((resolve) => { resolveStart = resolve; });
    throw new Error(`unexpected RPC ${method}`);
  };
  let page;
  try {
    page = createProvidersPage(dom.window.document.querySelector('#providers-view'), { rpcClient });
    page.init(); await page.refresh(); page.selectProvider('codex');
    const account = dom.window.document.querySelector('#providers-codex-card');
    const runtime = dom.window.document.querySelector('#codex-runtime-card');
    assert.equal(account.nextElementSibling, runtime);
    assert.equal(runtime.classList.contains('providers-card'), true);
    assert.equal(runtime.hidden, false);
    dom.window.document.querySelector('#providers-codex-login').click();
    assert.equal(dom.window.document.querySelector('#providers-device-login').hidden, false);
    page.closeOverlays();
    resolveStart({ device_auth_id: 'device', user_code: 'CODE', verification_url: 'https://auth.openai.com/codex/device', expires_at: Date.now() / 1000 + 900, interval_seconds: 1 });
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(dom.window.document.querySelector('#providers-device-login').hidden, true);
    assert.equal(dom.window.document.querySelector('#providers-codex-login').disabled, false);
    assert.equal(calls.includes('ai.codex.login'), false);
    assert.equal(calls.includes('ai.codex.device.poll'), false);
    page.selectProvider(configured.id);
    assert.equal(runtime.hidden, true);
  } finally { page?.destroy(); restore(); }
});

test('Device sign-in shows code and link, polls once at a time, then refreshes accounts', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  let polls = 0;
  let accountLoads = 0;
  let page;
  const rpcClient = async (method) => {
    if (method === 'ai.providers.list') return { providers: [{ id: 'codex', kind: 'codex', name: 'Codex', configured: true, models: [] }] };
    if (method === 'acp.agents.list') return { agents: [] };
    if (method === 'ai.codex.accounts.list') { accountLoads++; return { accounts: [] }; }
    if (method === 'ai.codex.runtime.status') return { installed: true };
    if (method === 'ai.codex.device.start') return { device_auth_id: 'device', user_code: 'ABCD-EFGH', verification_url: 'https://auth.openai.com/codex/device', expires_at: Date.now() / 1000 + 900, interval_seconds: 1 };
    if (method === 'ai.codex.device.poll') { polls++; return { status: 'complete', account_id: 'connected' }; }
    throw new Error(method);
  };
  try {
    page = createProvidersPage(dom.window.document.querySelector('#providers-view'), { rpcClient });
    page.init(); await page.refresh(); page.selectProvider('codex');
    dom.window.document.querySelector('#providers-codex-login').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    const panel = dom.window.document.querySelector('#providers-device-login');
    assert.equal(panel.querySelector('code').textContent, 'ABCD-EFGH');
    assert.equal(panel.querySelector('a').target, '_blank');
    assert.equal(polls, 0);
    const before = accountLoads;
    await new Promise((resolve) => setTimeout(resolve, 1100));
    assert.equal(polls, 1);
    assert.equal(panel.hidden, true);
    assert.ok(accountLoads > before);
  } finally { page?.destroy(); restore(); }
});

test('ACP model search filters names and tiers without changing the preferred model', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const agent = { id: 'worker', name: 'Worker', transport: 'stdio', command: 'worker', preferred_model_id: 'one', models: [
    { id: 'one', name: 'Claude Medium', tier: 'fast' },
    { id: 'two', name: 'Other model', tier: 'deep' },
  ] };
  const writes = [];
  let page;
  try {
    page = createProvidersPage(dom.window.document.querySelector('#providers-view'), { rpcClient: async (method) => {
      if (method === 'ai.providers.list') return { providers: [] };
      if (method === 'acp.agents.list') return { agents: [agent] };
      writes.push(method); throw new Error(method);
    } });
    page.init(); await page.refresh(); page.selectAcp('worker');
    const field = dom.window.document.querySelector('#acp-model-search');
    const search = async (value) => { field.value = value; field.dispatchEvent(new dom.window.Event('input')); await new Promise((resolve) => setTimeout(resolve, 220)); };
    await search('deep');
    assert.equal(dom.window.document.querySelectorAll('#acp-models-list [role="listitem"]').length, 1);
    assert.match(dom.window.document.querySelector('#acp-models-list').textContent, /Other model/);
    await search('no match');
    assert.match(dom.window.document.querySelector('#acp-models-list').textContent, /No model matches/);
    await search('');
    assert.equal(dom.window.document.querySelectorAll('#acp-models-list [role="listitem"]').length, 2);
    assert.equal(agent.preferred_model_id, 'one');
    assert.deepEqual(writes, []);
  } finally { page?.destroy(); restore(); }
});

test('Codex accounts show both actual usage periods, boundaries, and missing snapshots', async () => {
  const dom = new JSDOM(await readFile(new URL('index.html', root), 'utf8'), { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const accounts = [
    { account_id: 'active', active: true, primary_window: { used_percent: 0, limit_window_seconds: 18000, reset_at: 1800000000 }, weekly_window: { used_percent: 100, limit_window_seconds: 604800, reset_at: 1800000000 } },
    { account_id: 'backup', primary_window: { used_percent: 25, limit_window_seconds: 2592000 }, weekly_window: { used_percent: 40 } },
    { account_id: 'missing' },
  ];
  let page;
  try {
    page = createProvidersPage(dom.window.document.querySelector('#providers-view'), { rpcClient: async method => {
      if (method === 'ai.providers.list') return { providers: [{ id: 'codex', kind: 'codex', name: 'Codex', configured: true, models: [] }] };
      if (method === 'ai.codex.usage') return { accounts };
      if (method === 'acp.agents.list') return { agents: [] };
      if (method === 'ai.codex.runtime.status') return {};
      throw new Error(`Unexpected RPC ${method}`);
    } });
    page.init(); await page.refresh(); page.selectProvider('codex');
    await new Promise(resolve => setTimeout(resolve, 20));
    const rows = [...dom.window.document.querySelectorAll('#providers-account-list [role="listitem"]')];
    assert.equal(rows.length, 3);
    assert.match(rows[0].textContent, /5-hour100% available0% usedResets/);
    assert.match(rows[0].textContent, /Weekly0% available100% usedResets/);
    assert.deepEqual([...rows[0].querySelectorAll('[role="meter"]')].map(e => e.getAttribute('aria-valuenow')), ['0', '100']);
    assert.match(rows[1].textContent, /Monthly \(30 days\)75% available/);
    assert.match(rows[1].textContent, /Additional window60% available/);
    assert.match(rows[1].textContent, /Reset time unavailable/);
    assert.match(rows[2].textContent, /Usage unavailable/);
    assert.equal(rows[2].querySelector('[role="meter"]'), null);
  } finally { page?.destroy(); restore(); dom.window.close(); }
});
