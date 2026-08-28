// Smoke test for frontend/testdata/live-delta-server.mjs — the fake backend
// used to watch the live-delta DOM in a real browser. Verifies the server
// serves the frontend, answers boot RPCs, and streams a multi-round fake
// turn (turn.started rounds + message/tool deltas) over the WebSocket.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { startLiveDeltaServer } from './testdata/live-delta-server.mjs';

const hasNativeWS = typeof globalThis.WebSocket === 'function';

async function rpc(base, method, payload = {}) {
  const res = await fetch(`${base}/rpc/${method.replaceAll('.', '/')}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ method, payload }),
  });
  const body = await res.json();
  if (!res.ok) throw new Error(body?.error?.message || `HTTP ${res.status}`);
  return body.result;
}

function waitFor(events, predicate, timeoutMs = 25000) {
  return new Promise((resolve, reject) => {
    const deadline = Date.now() + timeoutMs;
    const timer = setInterval(() => {
      if (predicate()) { clearInterval(timer); resolve(); return; }
      if (Date.now() > deadline) { clearInterval(timer); reject(new Error('timed out waiting for stream events')); }
    }, 25);
  });
}

test('live-delta-server serves the frontend, boot RPCs, and a multi-round stream', { skip: hasNativeWS ? false : 'needs a native WebSocket client (Node >= 22)' }, async () => {
  const { server, port, clients, close } = await startLiveDeltaServer({ port: 0, rounds: 3, speedMs: 1, chunkSize: 64 });
  const base = `http://127.0.0.1:${port}`;

  try {
    // Frontend is served.
    const page = await fetch(`${base}/`);
    assert.equal(page.status, 200);
    assert.match(await page.text(), /<html/i);

    // Boot RPCs answer.
    const { conversations } = await rpc(base, 'agent.conversations.list');
    assert.equal(conversations[0].id, 'conv_live_delta');
    const { models } = await rpc(base, 'ai.models.list');
    assert.equal(models.length, 1);

    // Unknown RPCs degrade to an empty result (boot never hard-fails).
    assert.deepEqual(await rpc(base, 'agent.unknown.method'), {});

    // WebSocket connects and receives the synthetic multi-round turn.
    const events = [];
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws`);
    await new Promise((resolve, reject) => {
      ws.addEventListener('open', resolve, { once: true });
      ws.addEventListener('error', reject, { once: true });
    });
    ws.addEventListener('message', (e) => {
      try { events.push(JSON.parse(e.data)); } catch { /* ignore */ }
    });

    await rpc(base, 'agent.turns.start', { conversation_id: 'conv_live_delta' });

    await waitFor(events, () => {
      const types = new Set(events.map((e) => e.type));
      return types.has('agent.message.delta') && types.has('agent.tool.delta')
        && events.some((e) => e.type === 'agent.turn.started' && (e.payload?.round ?? 1) >= 2);
    });

    // Every delta is bound to the conversation so the UI can route it.
    const delta = events.find((e) => e.type === 'agent.message.delta');
    assert.equal(delta.payload.conversation_id, 'conv_live_delta');
    assert.ok(delta.payload.text.length > 0);

    // The turn settles.
    await waitFor(events, () => events.some((e) => e.type === 'agent.turn.done'));
    assert.ok(events.some((e) => e.type === 'agent.turn.started' && e.payload.round === 3), 'third round announced');

    ws.close();
    await waitFor([], () => clients.size === 0, 2000);
    assert.equal(clients.size, 0, 'client list is cleaned up after close');
  } finally {
    await close();
  }
});
