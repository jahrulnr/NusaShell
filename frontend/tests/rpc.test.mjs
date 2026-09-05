import assert from 'node:assert/strict';
import { test } from 'node:test';

import { rpc } from '../js/rpc.js';

test('rpc maps fetch network failures to Backend unreachable', async () => {
  const original = globalThis.fetch;
  globalThis.fetch = async () => {
    throw new TypeError('NetworkError when attempting to fetch resource.');
  };
  try {
    await assert.rejects(() => rpc('automation.list'), { message: 'Backend unreachable' });
    await assert.rejects(() => rpc('acp.agents.list'), (err) => {
      assert.equal(err.message, 'Backend unreachable');
      assert.equal(err.code, 'unavailable');
      assert.equal(err.method, 'acp.agents.list');
      return true;
    });
  } finally {
    globalThis.fetch = original;
  }
});

test('rpc keeps HTTP error text from the backend', async () => {
  const original = globalThis.fetch;
  globalThis.fetch = async () => ({
    ok: false,
    status: 409,
    json: async () => ({ error: { code: 'conflict', message: 'Provider name is taken' } }),
  });
  try {
    await assert.rejects(() => rpc('ai.providers.save'), { message: 'Provider name is taken' });
  } finally {
    globalThis.fetch = original;
  }
});
