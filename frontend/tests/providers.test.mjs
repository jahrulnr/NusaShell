import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import {
  BUILTIN_PROVIDERS,
  cacheTTLsFor,
  effectiveCacheTTL,
  reasoningSummariesFor,
  effectiveReasoningSummary,
  mergeProviderRegistry,
  renderUsageBar,
} from '../js/views/providers.js';

const providersSource = await readFile(new URL('../js/views/providers.js', import.meta.url), 'utf8');

test('provider registry keeps the four built-in provider cards visible', () => {
  const providers = mergeProviderRegistry([]);

  assert.deepEqual(providers.map((provider) => provider.id), ['anthropic', 'openai', 'openrouter', 'codex', 'gemini']);
  assert.equal(providers.find((provider) => provider.id === 'anthropic').driver, 'anthropic');
  assert.equal(providers.find((provider) => provider.id === 'anthropic').kind, 'messages');
  assert.equal(providers.find((provider) => provider.id === 'openai').driver, 'openai');
  assert.equal(providers.find((provider) => provider.id === 'openai').kind, 'responses');
  assert.equal(providers.find((provider) => provider.id === 'openrouter').driver, 'openrouter');
  assert.equal(providers.find((provider) => provider.id === 'openrouter').kind, 'chat');
  assert.equal(providers.find((provider) => provider.id === 'codex').driver, 'codex');
  assert.equal(providers.find((provider) => provider.id === 'codex').kind, 'codex');
  assert.equal(BUILTIN_PROVIDERS.length, 5);
});

test('provider registry preserves custom providers and selected API kinds', () => {
  const providers = mergeProviderRegistry([
    {
      id: 'custom_1',
      driver: 'openrouter',
      kind: 'messages',
      name: 'Private gateway',
      base_url: 'https://gateway.example/v1',
    },
    {
      id: 'openrouter',
      driver: 'openrouter',
      kind: 'responses',
      name: 'OpenRouter',
      base_url: 'https://openrouter.ai/api/v1',
      configured: true,
    },
  ]);

  assert.equal(providers.length, 6);
  assert.equal(providers[2].id, 'openrouter');
  assert.equal(providers[2].kind, 'responses');
  assert.equal(providers[2].configured, true);
  assert.equal(providers[5].id, 'custom_1');
  assert.equal(providers[5].kind, 'messages');
  assert.equal(providers[5].driver, 'openrouter');
});

test('gemini built-in and kind expose no cache TTL chips', () => {
  const gemini = BUILTIN_PROVIDERS.find((provider) => provider.id === 'gemini');
  assert.ok(gemini, 'gemini built-in card exists');
  assert.equal(gemini.kind, 'gemini');
  assert.equal(gemini.driver, 'gemini');
  assert.deepEqual(cacheTTLsFor({ kind: 'gemini', driver: 'gemini' }), []);
  assert.equal(effectiveCacheTTL({ kind: 'gemini', driver: 'gemini', cache_ttl: '30m' }), '');
});

test('cache TTL chips use sendable values and keep a selected default', () => {
  assert.deepEqual(cacheTTLsFor({ kind: 'messages', driver: 'anthropic' }), ['5m', '1h', 'off']);
  assert.deepEqual(cacheTTLsFor({ kind: 'responses', driver: 'openai' }), ['30m', 'off']);
  assert.deepEqual(cacheTTLsFor({ kind: 'codex', driver: 'codex' }), ['30m', 'off']);
  assert.deepEqual(cacheTTLsFor({ kind: 'chat', driver: 'openrouter' }), ['5m', '1h', 'off']);
  assert.deepEqual(cacheTTLsFor({ kind: 'chat', driver: 'openai' }), ['30m', 'off']);
  assert.deepEqual(cacheTTLsFor({ kind: 'messages', cache_ttls: ['5m', '1h'] }), ['5m', '1h', 'off']);
  assert.equal(effectiveCacheTTL({ kind: 'messages', driver: 'anthropic' }), '5m');
  assert.equal(effectiveCacheTTL({ kind: 'messages', driver: 'anthropic', cache_ttl: '1h' }), '1h');
  assert.equal(effectiveCacheTTL({ kind: 'messages', driver: 'anthropic', cache_ttl: 'off' }), 'off');
  assert.equal(effectiveCacheTTL({ kind: 'responses', driver: 'openai' }), '30m');
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

test('Codex detail wires OAuth, import, multi-account, usage, and runtime RPCs', () => {
  for (const method of [
    'ai.codex.login',
    'ai.codex.import',
    'ai.codex.logout',
    'ai.codex.accounts.list',
    'ai.codex.accounts.switch',
    'ai.codex.refresh-circuits',
    'ai.codex.runtime.status',
    'ai.codex.runtime.download',
    'ai.codex.usage',
  ]) {
    assert.match(providersSource, new RegExp(`rpc\\('${method.replace(/\./g, '\\.')}'`));
  }
  assert.match(providersSource, /id: 'codex-login-btn'/);
  assert.match(providersSource, /id: 'codex-import-cli-btn'/);
  assert.match(providersSource, /id: 'codex-refresh-circuits-btn'/);
  assert.match(providersSource, /id: 'codex-account-list'/);
  assert.match(providersSource, /id: 'codex-runtime-status'/);
  assert.match(providersSource, /id: 'codex-runtime-download-btn'/);
  assert.match(providersSource, /Sign in with ChatGPT/);
  assert.match(providersSource, /Import from Codex CLI/);
  // OAuth + Import are primary; paste is only an optional fallback.
  assert.match(providersSource, /optional fallback/);
  assert.match(providersSource, /prefer Sign in \/ Import from CLI/);
  assert.doesNotMatch(providersSource, /paste the Codex OAuth access token/);
});

test('Codex usage bar colors by remaining quota', () => {
  const dom = new JSDOM('<!doctype html><html><body></body></html>');
  globalThis.document = dom.window.document;
  try {
    const ok = renderUsageBar('Session', { used_percent: 40, remaining_percent: 60, reset_at: 0 });
    assert.ok(ok.querySelector('.codex-usage-bar-fill.ok'));
    const warning = renderUsageBar('Weekly', { used_percent: 75, remaining_percent: 25, reset_at: 0 });
    assert.ok(warning.querySelector('.codex-usage-bar-fill.warning'));
    const critical = renderUsageBar('Session', { used_percent: 95, remaining_percent: 5, reset_at: 0 });
    assert.ok(critical.querySelector('.codex-usage-bar-fill.critical'));
    assert.match(ok.textContent, /40%/);
    assert.match(ok.textContent, /60% left/);
  } finally {
    delete globalThis.document;
  }
});

test('Codex detail render shows accounts and runtime panels', async () => {
  const dom = new JSDOM(`<!doctype html><html><body>
    <button id="add-provider-btn" type="button"></button>
    <div id="provider-registry"></div>
    <div id="provider-detail" hidden></div>
    <div id="provider-acp-section"></div>
    <button id="add-acp-agent-btn" type="button"></button>
    <div id="toast-container"></div>
  </body></html>`, { url: 'http://localhost/' });
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  globalThis.HTMLElement = dom.window.HTMLElement;
  if (!dom.window.requestAnimationFrame) {
    dom.window.requestAnimationFrame = (cb) => { cb(); return 0; };
  }
  globalThis.requestAnimationFrame = (cb) => { cb(); return 0; };

  const calls = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (_url, init) => {
    const body = JSON.parse(init.body);
    calls.push(body.method);
    if (body.method === 'ai.providers.list') {
      return {
        ok: true,
        json: async () => ({
          ok: true,
          result: {
            providers: [{
              id: 'codex',
              driver: 'codex',
              kind: 'codex',
              name: 'Codex',
              base_url: 'https://chatgpt.com/backend-api/codex',
              enabled: true,
              configured: true,
              has_api_key: true,
              reasoning_summaries: ['auto', 'concise', 'detailed', 'none'],
              models: [{ id: 'gpt-5.4' }],
            }],
          },
        }),
      };
    }
    if (body.method === 'ai.codex.usage') {
      return {
        ok: true,
        json: async () => ({
          ok: true,
          result: {
            accounts: [{
              account_id: 'acc-1',
              email: 'user@example.com',
              name: 'User',
              active: true,
              plan: 'plus',
              primary_window: { used_percent: 20, remaining_percent: 80 },
            }],
          },
        }),
      };
    }
    if (body.method === 'ai.codex.runtime.status') {
      return {
        ok: true,
        json: async () => ({
          ok: true,
          result: { installed: true, version: '0.1.0', path: '/tmp/codex' },
        }),
      };
    }
    return { ok: true, json: async () => ({ ok: true, result: {} }) };
  };

  try {
    // Fresh module instance so module-level state is not shared across tests.
    const mod = await import(`../js/views/providers.js?codex-detail=${Date.now()}`);
    await mod.initProviders();
    const cards = [...document.querySelectorAll('.provider-registry-card')];
    const codexCard = cards.find((card) => card.querySelector('h2')?.textContent === 'Codex');
    assert.ok(codexCard, 'Codex registry card present');
    codexCard.querySelector('.provider-configure').click();
    await new Promise((r) => setTimeout(r, 40));

    assert.ok(document.getElementById('codex-accounts-card'), 'accounts card present');
    assert.ok(document.getElementById('codex-runtime-card'), 'runtime card present');
    assert.ok(document.getElementById('codex-login-btn'));
    assert.ok(document.getElementById('codex-import-cli-btn'));
    assert.ok(document.getElementById('codex-refresh-circuits-btn'));
    assert.ok(document.getElementById('codex-runtime-download-btn'));
    const summaryPicker = document.getElementById('provider-reasoning-summary');
    assert.ok(summaryPicker, 'reasoning summary picker present');
    assert.deepEqual(
      [...summaryPicker.querySelectorAll('button')].map((button) => button.textContent),
      ['auto', 'concise', 'detailed', 'none'],
    );
    assert.equal(summaryPicker.querySelector('[aria-pressed="true"]')?.textContent, 'auto');
    assert.match(document.getElementById('codex-account-list').textContent, /user@example\.com/);
    assert.match(document.getElementById('codex-runtime-status').textContent, /v0\.1\.0/);
    assert.ok(calls.includes('ai.codex.usage'));
    assert.ok(calls.includes('ai.codex.runtime.status'));
    assert.match(document.getElementById('provider-detail').textContent, /ChatGPT OAuth/);
  } finally {
    globalThis.fetch = originalFetch;
    delete globalThis.window;
    delete globalThis.document;
    delete globalThis.HTMLElement;
  }
});
