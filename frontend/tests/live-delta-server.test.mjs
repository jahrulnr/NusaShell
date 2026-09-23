// Smoke test for frontend/testdata/live-delta-server.mjs — the fake backend
// serves the embedded frontend, boot RPCs, WebSocket lifecycle notifications,
// and replayable per-round SSE data for a synthetic multi-round turn.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { startLiveDeltaServer } from '../testdata/live-delta-server.mjs';

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

async function readRoundStream(base, runID, messageID) {
  const url = new URL('/stream', base);
  url.searchParams.set('run_id', runID);
  url.searchParams.set('message_id', messageID);
  const response = await fetch(url);
  assert.equal(response.status, 200);
  assert.match(response.headers.get('content-type') || '', /^text\/event-stream/);
  const blocks = (await response.text()).split(/\r?\n\r?\n/).filter((block) => block.trim() && !block.startsWith(':'));
  return blocks.map((block) => {
    let type = '';
    let data = '';
    for (const line of block.split(/\r?\n/)) {
      if (line.startsWith('event:')) type = line.slice(6).trim();
      else if (line.startsWith('data:')) data += line.slice(5).trimStart();
    }
    return { type, payload: JSON.parse(data) };
  });
}

test('live-delta-server serves boot RPCs, WS lifecycle signals, and multi-round SSE', { skip: hasNativeWS ? false : 'needs a native WebSocket client (Node >= 22)' }, async () => {
  const { server, port, clients, close } = await startLiveDeltaServer({ port: 0, rounds: 3, speedMs: 1, chunkSize: 64 });
  const base = `http://127.0.0.1:${port}`;

  try {
    const page = await fetch(`${base}/`);
    assert.equal(page.status, 200);
    assert.match(await page.text(), /<html/i);

    const { conversations } = await rpc(base, 'agent.conversations.list');
    assert.equal(conversations[0].id, 'conv_live_delta');
    const { models } = await rpc(base, 'ai.models.list');
    assert.equal(models.length, 1);
    assert.deepEqual(await rpc(base, 'agent.unknown.method'), {});

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
    await waitFor(events, () => events.some((event) => event.type === 'agent.turn.done'));

    const starts = events.filter((event) => event.type === 'agent.turn.started').map((event) => event.payload);
    assert.equal(starts.length, 3);
    assert.ok(events.some((event) => event.type === 'agent.tool.started'));
    assert.ok(!events.some((event) => ['agent.message.delta', 'agent.reasoning.delta', 'agent.tool.delta'].includes(event.type)));

    for (let i = 0; i < starts.length; i++) {
      const frames = await readRoundStream(base, starts[i].run_id, starts[i].message_id);
      const deltas = frames.filter((frame) => frame.type === 'round.delta').map((frame) => frame.payload);
      const done = frames.find((frame) => frame.type === 'round.done')?.payload;
      assert.ok(deltas.some((frame) => frame.kind === 'reasoning' && frame.text), `round ${i + 1} reasoning comes from SSE`);
      assert.ok(deltas.some((frame) => frame.kind === 'tool' && frame.tool_call_id), `round ${i + 1} tool frames come from SSE`);
      assert.ok(deltas.some((frame) => frame.kind === 'text' && frame.text.includes(`Round ${i + 1}`)), `round ${i + 1} text comes from SSE`);
      assert.ok(done, `round ${i + 1} has a terminal SSE frame`);
      for (let j = 0; j < deltas.length; j++) {
        assert.equal(deltas[j].seq, j + 1, `round ${i + 1} delta sequence is contiguous`);
      }
      if (i + 1 < starts.length) {
        assert.equal(done.next?.message_id, starts[i + 1].message_id, `round ${i + 1} chains to the next SSE round`);
      } else {
        assert.equal(done.next, undefined, 'final round has no next reference');
      }
    }

    ws.close();
    await waitFor([], () => clients.size === 0, 2000);
    assert.equal(clients.size, 0, 'client list is cleaned up after close');
  } finally {
    await close();
  }
});
