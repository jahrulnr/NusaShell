// Reconnect and broadcast storms must not turn into request storms:
// a cheap reattach, visibility-gated broadcasts, and deduped loaders.
import { calls, mockApi, sleep, sockets } from './env.mjs';
import { before, beforeEach, test } from 'node:test';
import assert from 'node:assert/strict';
import { state } from '../lib/store.js';
import * as chat from '../lib/chat.js';
import { backoffDelay, wasStable } from '../lib/socket.js';

let ws;
const roomReads = (id) => calls.filter((c) => c.url.includes(`/${id}`));
const hits = (path) => calls.filter((c) => c.url.startsWith(path)).length;

// reconnect drops the socket and opens the next one once it exists.
async function reconnect() {
  ws.onclose();
  await sleep(900); // first backoff step is ~0.5s
  ws = sockets.at(-1);
  ws.open();
  await sleep(30);
}

function serveRoom(id, { info = {}, messages = [], defer = false } = {}) {
  let release;
  const gate = defer ? new Promise((r) => { release = r; }) : null;
  mockApi({
    [`GET /api/conversations/${id}/messages`]: () => (gate ? gate.then(() => [200, { messages, effectiveStart: 0, total: messages.length }]) : [200, { messages, effectiveStart: 0, total: messages.length }]),
    [`GET /api/conversations/${id}`]: { id, title: 'T', workspace: '', ...info },
    'GET /api/conversations': { data: [{ id, title: 'T' }], page: { total: 1 } },
    'GET /api/context': { estimate: 10, total: 100 },
  });
  return { release: () => release?.() };
}

before(() => {
  mockApi();
  chat.start();
  ws = sockets[0];
  ws.open();
});

beforeEach(() => {
  state.rooms = {};
  state.toasts = [];
  state.route = { view: 'home' };
  ws.sent.length = 0;
  chat.navigate('#/');
});

test('reconnect with a loaded, idle room costs one catch-up read per window', async () => {
  serveRoom('c_vis', { messages: [{ id: 'm1', role: 'user', content: 'hi' }] });
  chat.navigate('#/c/c_vis');
  await sleep(50);
  assert.equal(state.rooms.c_vis.hist.status, 'ready');
  calls.length = 0;

  await reconnect();
  // Nothing immediate: the reconnect itself fires no room reads.
  assert.equal(roomReads('c_vis').length, 0, calls.map((c) => c.url));

  // Inside the refresh window the stale-marked visible room catches up
  // once — a reply that landed while the socket was down shows — and
  // the list read stays single.
  await sleep(5600);
  assert.equal(hits('/api/conversations/c_vis/messages'), 1, calls.map((c) => c.url));
  assert.equal(calls.filter((c) => c.url === '/api/conversations/c_vis').length, 1, calls.map((c) => c.url));
  assert.equal(state.rooms.c_vis.messages.at(-1).content, 'hi');
  const listReads = calls.filter((c) => c.url.startsWith('/api/conversations?'));
  assert.ok(listReads.length <= 1, calls.map((c) => c.url));
});

test('activity for another room reads nothing of that room', async () => {
  serveRoom('c_other');
  const other = chat.room('c_other');
  other.infoStatus = 'ready';
  other.hist.status = 'ready';
  calls.length = 0;

  ws.serve({ type: 'conversation.activity', conversationId: 'c_other', payload: { state: 'completed', turnId: 't_x' } });
  await sleep(50);
  assert.equal(roomReads('c_other').length, 0, calls.map((c) => c.url));
  assert.equal(other.histStale, true);

  ws.serve({ type: 'conversation.updated', conversationId: 'c_other' });
  await sleep(50);
  assert.equal(roomReads('c_other').length, 0, calls.map((c) => c.url));
});

test('two quick reconnects share one room load', async () => {
  const { release } = serveRoom('c_new', { defer: true });
  state.route = { view: 'chat', id: 'c_new' };
  chat.room('c_new'); // never loaded: statuses are 'loading'

  ws.onclose();
  await sleep(900);
  ws = sockets.at(-1);
  ws.open();
  ws.onclose();
  await sleep(900);
  ws = sockets.at(-1);
  ws.open();
  await sleep(30);
  release();
  await sleep(50);

  assert.equal(hits('/api/conversations/c_new/messages'), 1, calls.map((c) => c.url));
  assert.equal(calls.filter((c) => c.url === '/api/conversations/c_new').length, 1, calls.map((c) => c.url));
});

test('a completed turn reloads history once even with the activity echo', async () => {
  serveRoom('c_vis', { messages: [{ id: 'm1', role: 'user', content: 'hi' }] });
  chat.navigate('#/c/c_vis');
  await sleep(50);
  calls.length = 0;

  const r = state.rooms.c_vis;
  r.live = { turnId: 't1', lastSeq: 0, segments: [], terminal: null, phase: null, n: 0 };
  ws.serve({ type: 'turn.completed', requestId: 'req-t', conversationId: 'c_vis', turnId: 't1', seq: 1, payload: { userMessagePersisted: true } });
  ws.serve({ type: 'conversation.activity', conversationId: 'c_vis', payload: { state: 'completed', turnId: 't1' } });
  await sleep(80);
  assert.equal(hits('/api/conversations/c_vis/messages'), 1, calls.map((c) => c.url));
  assert.ok(hits('/api/context') <= 1, calls.map((c) => c.url));
});

test('a terminal during an in-flight history load refetches once more', async () => {
  // The first read hangs; the turn completes while it is in flight.
  let release;
  const gate = new Promise((r) => { release = r; });
  const page = { messages: [{ id: 'm1', role: 'user', content: 'hi' }, { id: 'm2', role: 'assistant', content: 'done' }], effectiveStart: 0, total: 2 };
  let msgsCalls = 0;
  mockApi({
    'GET /api/conversations/c_vis/messages': () => (msgsCalls++ === 0 ? gate.then(() => [200, page]) : [200, page]),
    'GET /api/conversations/c_vis': { id: 'c_vis', title: 'T' },
    'GET /api/conversations': { data: [{ id: 'c_vis', title: 'T' }], page: { total: 1 } },
    'GET /api/context': { estimate: 10, total: 100 },
  });
  chat.navigate('#/c/c_vis');
  await sleep(30);
  const r = state.rooms.c_vis;
  r.live = { turnId: 't1', lastSeq: 0, segments: [], terminal: null, phase: null, n: 0, kind: 'send' };
  ws.serve({ type: 'turn.completed', requestId: 'req-t', conversationId: 'c_vis', turnId: 't1', seq: 1, payload: { userMessagePersisted: true } });
  await sleep(30);
  release();
  await sleep(80);

  // The stale need outlived the old flight: the follow-up fetched the
  // messages the terminal wrote.
  assert.equal(msgsCalls, 2, calls.map((c) => c.url));
  assert.equal(r.messages.at(-1).content, 'done');
  assert.equal(r.histStale, false);
});

test('a failed send that persisted nothing does not reload history or context', async () => {
  serveRoom('c_vis');
  chat.navigate('#/c/c_vis');
  await sleep(50);
  calls.length = 0;

  const r = state.rooms.c_vis;
  r.live = { turnId: 't1', lastSeq: 0, segments: [], terminal: null, phase: null, n: 0, kind: 'send' };
  ws.serve({ type: 'turn.failed', requestId: 'req-t', conversationId: 'c_vis', turnId: 't1', seq: 1, payload: { status: 500, code: 'internal', message: 'boom', userMessagePersisted: false } });
  await sleep(80);
  assert.equal(hits('/api/conversations/c_vis/messages'), 0, calls.map((c) => c.url));
  assert.equal(hits('/api/context'), 0, calls.map((c) => c.url));
});

test('backoff resets only after a stable link, grows, and caps', () => {
  // A link that lived past the stable mark restarts the streak.
  for (let i = 0; i < 50; i++) {
    const d = backoffDelay(9, 20_000);
    assert.ok(d >= 375 && d <= 750, `stable link delay ${d}`);
  }
  // Short-lived links escalate.
  assert.ok(backoffDelay(0, 100) <= 750);
  assert.ok(backoffDelay(3, 100) >= 3000, 'streak 3 pays ~4s');
  for (let i = 0; i < 50; i++) assert.ok(backoffDelay(30, 0) <= 18_750, 'capped');
});

test('a socket that closed before opening proves no stability', () => {
  // openedAt 0 is the never-opened marker: it must count against the
  // streak, or a refused connection reconnects at 500 ms forever.
  assert.equal(wasStable(0, 5000), false);
  assert.equal(wasStable(Date.now(), 5000), false);
  assert.equal(wasStable(Date.now() - 6000, 5000), true);
});
