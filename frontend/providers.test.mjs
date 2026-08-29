import assert from 'node:assert/strict';
import { test } from 'node:test';

import { BUILTIN_PROVIDERS, cacheTTLsFor, effectiveCacheTTL, mergeProviderRegistry } from './js/views/providers.js';

test('provider registry keeps the three built-in provider cards visible', () => {
  const providers = mergeProviderRegistry([]);

  assert.deepEqual(providers.map((provider) => provider.id), ['anthropic', 'openai', 'openrouter']);
  assert.equal(providers.find((provider) => provider.id === 'anthropic').driver, 'anthropic');
  assert.equal(providers.find((provider) => provider.id === 'anthropic').kind, 'messages');
  assert.equal(providers.find((provider) => provider.id === 'openai').driver, 'openai');
  assert.equal(providers.find((provider) => provider.id === 'openai').kind, 'responses');
  assert.equal(providers.find((provider) => provider.id === 'openrouter').driver, 'openrouter');
  assert.equal(providers.find((provider) => provider.id === 'openrouter').kind, 'chat');
  assert.equal(BUILTIN_PROVIDERS.length, 3);
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

  assert.equal(providers.length, 4);
  assert.equal(providers[2].id, 'openrouter');
  assert.equal(providers[2].kind, 'responses');
  assert.equal(providers[2].configured, true);
  assert.equal(providers[3].id, 'custom_1');
  assert.equal(providers[3].kind, 'messages');
  assert.equal(providers[3].driver, 'openrouter');
});

test('cache TTL chips use sendable values and keep a selected default', () => {
  assert.deepEqual(cacheTTLsFor({ kind: 'messages', driver: 'anthropic' }), ['5m', '1h']);
  assert.deepEqual(cacheTTLsFor({ kind: 'responses', driver: 'openai' }), ['30m']);
  assert.deepEqual(cacheTTLsFor({ kind: 'chat', driver: 'openrouter' }), ['5m', '1h']);
  assert.deepEqual(cacheTTLsFor({ kind: 'chat', driver: 'openai' }), ['30m']);
  assert.equal(effectiveCacheTTL({ kind: 'messages', driver: 'anthropic' }), '5m');
  assert.equal(effectiveCacheTTL({ kind: 'messages', driver: 'anthropic', cache_ttl: '1h' }), '1h');
  assert.equal(effectiveCacheTTL({ kind: 'responses', driver: 'openai' }), '30m');
});
