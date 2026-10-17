import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';
import { JSDOM } from 'jsdom';

const appURL = new URL('../page/conversation/app.js', import.meta.url);

function extractFn(source, name, { asyncFn = false } = {}) {
  const marker = asyncFn ? `async function ${name}(` : `function ${name}(`;
  const start = source.indexOf(marker);
  assert.ok(start >= 0, `app.js must define ${name}`);
  const end = source.indexOf('\n}', start);
  assert.ok(end > start, `${name} must close at column 0`);
  return source.slice(start, end + 2);
}

function harness(source, { status = 'idle', rpcResult = { status: 'started' }, rpcError = null } = {}) {
  const dom = new JSDOM('<button id="context-compact" type="button">Compact</button><textarea id="composer"></textarea>');
  const calls = [];
  const toasts = [];
  const context = {
    document: dom.window.document,
    activeConversation: { id: 'room-1', status },
    conversations: [{ id: 'room-1', status }, { id: 'room-2', status: 'idle' }],
    compactingRooms: new Set(),
    toast: (message, level) => { toasts.push({ message, level }); },
    rpc: async (method, payload) => {
      calls.push({ method, payload });
      if (rpcError) throw rpcError;
      return rpcResult;
    },
    syncComposerAction() {
      const compacting = context.compactingRooms.has(context.activeConversation?.id);
      dom.window.document.querySelector('#composer').placeholder = compacting ? 'Compacting context…' : 'Ask';
    },
  };
  context.syncCompactAffordances = undefined;
  const code = `${extractFn(source, 'syncCompactAffordances')}\n${extractFn(source, 'releaseCompactingRoom')}\n${extractFn(source, 'compactConversation', { asyncFn: true })}`;
  runInNewContext(code, context);
  return { ...context, calls, toasts, button: dom.window.document.querySelector('#context-compact'), composer: dom.window.document.querySelector('#composer') };
}

test('manual compact starts the existing conversation RPC and holds the room until it finishes', async () => {
  const source = await readFile(appURL, 'utf8');
  assert.match(source, /id="context-compact"/);
  assert.match(source, /id: 'compact', label: 'Compact'/);
  const page = harness(source);
  await page.compactConversation('room-1');
  assert.equal(page.calls.length, 1);
  assert.equal(page.calls[0].method, 'agent.conversations.compact');
  assert.equal(page.calls[0].payload.id, 'room-1');
  assert.equal(page.compactingRooms.has('room-1'), true);
  assert.equal(page.button.disabled, true);
  assert.equal(page.button.getAttribute('aria-busy'), 'true');
  assert.equal(page.composer.placeholder, 'Compacting context…');
  assert.equal(page.toasts.length, 0);
});

test('manual compact explains a room that has nothing to summarize or compacts on the provider', async () => {
  const source = await readFile(appURL, 'utf8');
  const empty = harness(source, { rpcResult: { status: 'nothing_to_compact' } });
  await empty.compactConversation('room-1');
  assert.equal(empty.compactingRooms.has('room-1'), false);
  assert.equal(empty.button.disabled, false);
  assert.match(empty.toasts[0].message, /Nothing older to compact/);

  const server = harness(source, { rpcResult: { status: 'server_managed' } });
  await server.compactConversation('room-2');
  assert.equal(server.compactingRooms.has('room-2'), false);
  assert.match(server.toasts[0].message, /compacts on the provider/);
});

test('a terminal compaction event releases a room held after started', async () => {
  const source = await readFile(appURL, 'utf8');
  const page = harness(source, { rpcResult: { status: 'started' } });
  await page.compactConversation('room-1');
  assert.equal(page.compactingRooms.has('room-1'), true);
  assert.equal(page.button.disabled, true);
  page.releaseCompactingRoom('room-1');
  assert.equal(page.compactingRooms.has('room-1'), false);
  assert.equal(page.button.disabled, false);
  assert.equal(page.composer.placeholder, 'Ask');
});

test('manual compact does not start while a turn is running', async () => {
  const source = await readFile(appURL, 'utf8');
  const page = harness(source, { status: 'running' });
  page.syncCompactAffordances();
  assert.equal(page.button.disabled, true);
  await page.compactConversation('room-1');
  assert.equal(page.calls.length, 0);
  assert.match(page.toasts[0].message, /Stop the running turn/);
});
