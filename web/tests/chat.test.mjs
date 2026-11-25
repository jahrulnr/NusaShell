// The controller against a scripted server: real chat.js, real store and
// turn projection, a fake socket and fetch.
import { calls, mockApi, sleep, sockets } from './env.mjs';
import { before, beforeEach, test } from 'node:test';
import assert from 'node:assert/strict';
import { state, subscribe } from '../lib/store.js';
import * as chat from '../lib/chat.js';
import { queuedFor } from '../lib/queued.js';
import { historyItems, liveHistory, liveSegments } from '../lib/turn.js';

let ws;
const sentTurns = () => ws.sent.filter((c) => c.type === 'turn.send');

// serveThread answers the reads a thread view makes for id.
function serveThread(id, { infoFails = 0, messages = [], info = {} } = {}, extra = {}) {
  let infoCalls = 0;
  mockApi({
    ...extra,
    [`GET /api/conversations/${id}/messages`]: () => [200, { messages, effectiveStart: 0, total: messages.length }],
    [`GET /api/conversations/${id}`]: () => (++infoCalls <= infoFails ? [404, { code: 'not_found', detail: 'no such thread' }] : { id, title: 'Thread', workspace: '', ...info }),
  });
}

// firstSend types into the home composer and sends.
function firstSend(text, sel = { providerId: 'p', modelId: 'm' }) {
  const home = chat.freshRoom();
  home.draft = '';
  home.sel = sel;
  assert.equal(chat.submit(home, text), true);
  return sentTurns().at(-1);
}

const opened = (cmd, conversationId, turnId = 't1') => ws.serve({ type: 'turn.opened', requestId: cmd.requestId, conversationId, turnId, seq: 0 });
const terminal = (cmd, type, conversationId, payload, turnId = 't1') =>
  ws.serve({ type, requestId: cmd.requestId, conversationId, turnId, seq: 1, payload });

before(() => {
  mockApi();
  chat.start();
  ws = sockets[0];
  ws.open();
});

beforeEach(() => {
  state.rooms = {};
  state.toasts = [];
  state.newProjectId = '';
  ws.sent.length = 0;
  chat.navigate('#/');
});

test('a first send goes out from the home view with the chosen model', () => {
  const cmd = firstSend('hello');
  assert.equal(cmd.conversationId, null);
  assert.deepEqual([cmd.payload.content, cmd.payload.providerId, cmd.payload.modelId], ['hello', 'p', 'm']);
});

test('a first send without a model asks for one and sends nothing', () => {
  state.prefs = { ...state.prefs, lastModel: undefined };
  const home = chat.freshRoom();
  assert.equal(chat.submit(home, 'hello'), false);
  assert.equal(sentTurns().length, 0);
  assert.equal(state.toasts.at(-1).kind, 'error');
});

test('the thread opens when the server mints it, and the home composer is empty again', async () => {
  serveThread('c_1');
  const cmd = firstSend('hello');
  opened(cmd, 'c_1');
  await sleep(20);
  assert.equal(location.hash, '#/c/c_1');
  assert.equal(state.rooms.c_1.live.userText, 'hello');
  assert.equal(chat.freshRoom().draft, '');
  assert.equal(chat.isBusy(state.rooms.c_1), true);
});

test('a finished first turn reads the thread and settles', async () => {
  serveThread('c_1', { messages: [{ id: 'm1', role: 'user', content: 'hello' }] });
  const cmd = firstSend('hello');
  opened(cmd, 'c_1');
  terminal(cmd, 'turn.completed', 'c_1', { userMessagePersisted: true });
  await sleep(40);
  const r = state.rooms.c_1;
  assert.equal(r.live, null);
  assert.equal(r.minted, null);
  assert.equal(r.infoStatus, 'ready');
  assert.equal(r.messages.length, 1);
  assert.equal(location.hash, '#/c/c_1');
});

test('the first read racing the server\'s save retries instead of showing "not found"', async () => {
  serveThread('c_1', { infoFails: 1 });
  const cmd = firstSend('hello');
  opened(cmd, 'c_1');
  await sleep(10);
  assert.notEqual(state.rooms.c_1.infoStatus, 'notfound');
  await sleep(300);
  assert.equal(state.rooms.c_1.infoStatus, 'ready');
});

test('a first message the server never saved returns to the home composer', async () => {
  serveThread('c_1', { infoFails: 99 });
  state.newProjectId = 'proj1';
  const cmd = firstSend('hello', { providerId: 'p', modelId: 'missing' });
  opened(cmd, 'c_1');
  await sleep(10);
  assert.equal(location.hash, '#/c/c_1');
  assert.equal(state.newProjectId, '');
  terminal(cmd, 'turn.failed', 'c_1', { status: 404, code: 'not_found', message: 'model missing not found', userMessagePersisted: false });
  await sleep(300);
  const home = chat.freshRoom();
  assert.equal(location.hash, '#/');
  assert.equal(state.rooms.c_1, undefined);
  assert.equal(home.draft, 'hello');
  assert.deepEqual([home.sel.providerId, home.sel.modelId], ['p', 'missing']);
  assert.equal(state.newProjectId, 'proj1');
  assert.deepEqual([state.toasts.at(-1).kind, state.toasts.at(-1).message], ['error', 'model missing not found']);
  assert.equal(chat.isBusy(home), false);
});

test('a first message canceled before it was saved comes back without an error toast', async () => {
  serveThread('c_1', { infoFails: 99 });
  const cmd = firstSend('hello');
  opened(cmd, 'c_1');
  terminal(cmd, 'turn.failed', 'c_1', { status: 408, code: 'canceled', message: 'send canceled', userMessagePersisted: false });
  await sleep(300);
  assert.equal(chat.freshRoom().draft, 'hello');
  assert.equal(location.hash, '#/');
  assert.equal(state.toasts.filter((t) => t.kind === 'error').length, 0);
});

test('a first turn that fails after the message was saved stays on its thread with the failure', async () => {
  serveThread('c_1', { messages: [{ id: 'm1', role: 'user', content: 'hello' }] });
  const cmd = firstSend('hello');
  opened(cmd, 'c_1');
  terminal(cmd, 'turn.failed', 'c_1', { status: 502, code: 'upstream', message: 'upstream down', userMessagePersisted: true });
  await sleep(40);
  const r = state.rooms.c_1;
  assert.equal(location.hash, '#/c/c_1');
  assert.deepEqual([r.failed.status, r.failed.message], [502, 'upstream down']);
  assert.equal(chat.freshRoom().draft, '');
  assert.equal(r.draft, '');
});

test('a later message an existing thread did not save comes back to that thread\'s draft', async () => {
  serveThread('c_2', { messages: [{ id: 'm1', role: 'user', content: 'first' }] });
  chat.navigate('#/c/c_2');
  await sleep(30);
  const r = state.rooms.c_2;
  assert.equal(chat.submit(r, 'again'), true);
  const cmd = sentTurns().at(-1);
  assert.equal(cmd.conversationId, 'c_2');
  opened(cmd, 'c_2', 't2');
  terminal(cmd, 'turn.failed', 'c_2', { status: 400, code: 'invalid', message: 'rejected', userMessagePersisted: false }, 't2');
  await sleep(40);
  assert.equal(location.hash, '#/c/c_2');
  assert.equal(r.draft, 'again');
  // the prompt never landed, so there is no turn to report: the draft is back
  // and the reason is said once, without a failure card offering a retry
  assert.equal(r.failed, null);
  assert.match(state.toasts.at(-1)?.message || '', /rejected/);
});

test('a command the server rejects outright gives the text back and says why', async () => {
  const cmd = firstSend('hello');
  ws.serve({ type: 'error', requestId: cmd.requestId, code: 'queue_full', message: 'too many active turns' });
  const home = chat.freshRoom();
  assert.equal(home.draft, 'hello');
  assert.equal(chat.isBusy(home), false);
  assert.equal(state.toasts.at(-1).message, 'too many active turns');
});

test('a message sent while the socket is down is kept and not queued as sent', () => {
  ws.readyState = 0;
  const home = chat.freshRoom();
  home.draft = '';
  home.sel = { providerId: 'p', modelId: 'm' };
  assert.equal(chat.submit(home, 'hello'), false);
  assert.equal(chat.isBusy(home), false);
  assert.match(state.toasts.at(-1).message, /Not connected/);
  ws.readyState = 1;
});

test('the project a new thread is aimed at rides the first send only', () => {
  state.newProjectId = 'proj1';
  const cmd = firstSend('hello');
  assert.equal(cmd.payload.projectId, 'proj1');
});

test('streamed deltas build the live turn and a completed frame ends it', async () => {
  serveThread('c_1');
  const cmd = firstSend('hello');
  opened(cmd, 'c_1');
  ws.serve({ type: 'turn.delta', conversationId: 'c_1', turnId: 't1', seq: 1, payload: { text: 'Hi ' } });
  ws.serve({ type: 'turn.delta', conversationId: 'c_1', turnId: 't1', seq: 2, payload: { text: 'there' } });
  assert.equal(state.rooms.c_1.live.segments[0].text, 'Hi there');
  ws.serve({ type: 'turn.completed', requestId: cmd.requestId, conversationId: 'c_1', turnId: 't1', seq: 3, payload: { userMessagePersisted: true } });
  await sleep(40);
  assert.equal(state.rooms.c_1.live, null);
});

test('a turn relayed from another thread keeps the answer above it, then shows the prompt the stream never carries', async () => {
  const messages = [
    { id: 'm1', role: 'user', content: 'kalo di windows harusnya X:/ ga sih?' },
    { id: 'm2', role: 'assistant', content: 'Betul — C:\\ cuma contoh.' },
  ];
  serveThread('c_1', { messages });
  chat.navigate('#/c/c_1');
  await sleep(40);
  const r = state.rooms.c_1;
  // Another thread (or a chat, or another browser) starts a turn here: this
  // tab never sent it and has no prompt of its own.
  ws.serve({ type: 'conversation.activity', conversationId: 'c_1', turnId: 't9', payload: { state: 'opened', kind: 'send', requestId: 'req-relay' } });
  ws.serve({ type: 'turn.opened', conversationId: 'c_1', turnId: 't9', seq: 0, payload: { startedAt: Date.now() } });
  await sleep(20);
  assert.equal(r.live.adopted, true);
  assert.ok(!r.live.userText);
  // The prompt is written just after the turn opens, so until this tab has
  // it, history still ends with the previous answer — which the transcript
  // must keep, not trim at the last prompt it happens to hold.
  let shown = liveHistory(historyItems(r.messages), r.live);
  assert.deepEqual(shown.map((x) => x.kind), ['user', 'text']);
  assert.equal(shown[1].text, 'Betul — C:\\ cuma contoh.');
  assert.equal(r.messages.some((m) => m.id === 'm3'), false);
  messages.push({ id: 'm3', role: 'user', content: '[From thread @lynx Riset (c_9)]\n\nGuard workspace' });
  ws.serve({ type: 'turn.delta', conversationId: 'c_1', turnId: 't9', seq: 1, payload: { text: 'Betul' } });
  await sleep(40);
  // The first streamed frame proves the write landed: the prompt joins the
  // transcript above the live output, and the answer above it is untouched.
  shown = liveHistory(historyItems(r.messages), r.live);
  assert.deepEqual(shown.map((x) => x.kind), ['user', 'text', 'user']);
  assert.equal(shown[2].msg.id, 'm3');
  assert.deepEqual(r.live.segments.map((s) => s.text), ['Betul']);
  ws.serve({ type: 'turn.completed', requestId: 'req-relay', conversationId: 'c_1', turnId: 't9', seq: 2, payload: { userMessagePersisted: true } });
  await sleep(40);
  assert.equal(r.live, null);
  assert.deepEqual(r.messages.map((m) => m.id), ['m1', 'm2', 'm3']);
});

// ---------- the model picked while a turn runs ----------

const pick = { providerId: 'p', modelId: 'm' };
const patches = () => calls.filter((c) => c.method === 'PATCH');
const contextReads = () => calls.filter((c) => c.method === 'GET' && c.url.startsWith('/api/context'));

// openBusyThread opens c_1 and starts a turn on it, leaving the turn running.
async function openBusyThread(extra = {}) {
  state.prefs = { ...state.prefs, lastModel: pick };
  serveThread('c_1', { messages: [{ id: 'm1', role: 'user', content: 'first' }] }, {
    'PATCH /api/conversations/c_1': (call) => ({ id: 'c_1', title: 'Thread', ...call.body }),
    'GET /api/context': { estimate: 50_000, total: 1_000_000 },
    ...extra,
  });
  chat.navigate('#/c/c_1');
  await sleep(40);
  const r = state.rooms.c_1;
  assert.equal(chat.submit(r, 'work'), true);
  const cmd = sentTurns().at(-1);
  opened(cmd, 'c_1', 't2');
  await sleep(10);
  assert.equal(chat.isBusy(r), true);
  return { r, cmd };
}

test('a model picked while a turn runs does not ask the busy room to bind it', async () => {
  const { r, cmd } = await openBusyThread();
  calls.length = 0;
  chat.setSelection(r, { providerId: 'p2', modelId: 'm2' });
  await sleep(40);
  // The room's worker is busy with the turn: a bind would wait it out and
  // time out, and the context meter would measure a model that is not running.
  assert.equal(patches().length, 0);
  assert.equal(contextReads().length, 0);
  assert.equal(state.toasts.filter((t) => t.kind === 'error').length, 0);
  assert.deepEqual([chat.selection(r).providerId, chat.selection(r).modelId], ['p2', 'm2']);

  // When the turn ends and nothing follows, the choice binds the room.
  terminal(cmd, 'turn.completed', 'c_1', { userMessagePersisted: true }, 't2');
  await sleep(60);
  assert.equal(patches().length, 1);
  assert.deepEqual(patches()[0].body, { providerId: 'p2', modelId: 'm2', effort: 'default', upstream: '' });
});

test('a message queued behind the turn carries the choice instead of a separate bind', async () => {
  const { r, cmd } = await openBusyThread();
  calls.length = 0;
  chat.setSelection(r, { providerId: 'p2', modelId: 'm2' });
  assert.equal(chat.submit(r, 'next'), true);
  terminal(cmd, 'turn.completed', 'c_1', { userMessagePersisted: true }, 't2');
  await sleep(60);
  const next = sentTurns().at(-1);
  assert.equal(next.payload.content, 'next');
  assert.deepEqual([next.payload.providerId, next.payload.modelId], ['p2', 'm2']);
  assert.equal(patches().length, 0, 'the next turn binds the room when it runs; a bind posted beside it would queue behind it');
});

test('the context meter follows the stream, keeps its size between turns, and asks again after a fold', async () => {
  const { r, cmd } = await openBusyThread();
  assert.equal(r.ctx.estimate, 50_000);
  const step = (seq, tokens) => ws.serve({ type: 'turn.step_finished', conversationId: 'c_1', turnId: 't2', seq, payload: { usage: { totalInputTokens: tokens } } });

  step(1, 223_000);
  assert.equal(r.ctx.live, 223_000);

  // A fold starts and ends: the size reported before it no longer describes
  // the history, so the meter drops it and reads the server's.
  calls.length = 0;
  ws.serve({ type: 'turn.phase', conversationId: 'c_1', turnId: 't2', seq: 2, payload: { phase: 'compacting' } });
  assert.equal(r.ctx.live, 223_000, 'still the last report while the fold runs');
  ws.serve({ type: 'turn.delta', conversationId: 'c_1', turnId: 't2', seq: 3, payload: { text: 'back' } });
  await sleep(40);
  assert.equal(contextReads().length, 1);
  assert.equal(r.ctx.live, null);

  step(4, 41_000);
  assert.equal(r.ctx.live, 41_000);

  // The turn ends: the server is asked for the window, and the provider's
  // own figure stays rather than giving way to the server's estimate.
  calls.length = 0;
  ws.serve({ type: 'turn.completed', requestId: cmd.requestId, conversationId: 'c_1', turnId: 't2', seq: 5, payload: { userMessagePersisted: true } });
  await sleep(60);
  assert.equal(contextReads().length, 1);
  assert.equal(r.ctx.live, 41_000);
});

test('an agent room shows what its agent reports about its own context', async () => {
  serveThread('c_a', { messages: [{ id: 'm1', role: 'user', content: 'first' }], info: { agentId: 'claude', modelId: 'opus' } }, {
    'GET /api/context': { estimate: 0, total: 200_000, live: 53_000, cost: { amount: 0.04, currency: 'USD' } },
  });
  chat.navigate('#/c/c_a');
  await sleep(40);
  const r = state.rooms.c_a;

  // The room asks by its id alone: the figures are the agent's, not those of
  // a provider model picked beside it.
  const read = new URL(contextReads().at(-1).url, 'http://x');
  assert.equal(read.pathname + '?' + read.searchParams, '/api/context?conversationId=c_a');
  assert.deepEqual([r.ctx.total, r.ctx.live, r.ctx.cost.currency], [200_000, 53_000, 'USD']);

  assert.equal(chat.submit(r, 'work'), true);
  const cmd = sentTurns().at(-1);
  opened(cmd, 'c_a', 't3');
  await sleep(10);

  // Each report replaces the last, the window included.
  ws.serve({ type: 'turn.context', conversationId: 'c_a', turnId: 't3', seq: 1, payload: { used: 61_000, size: 200_000, cost: { amount: 0.05, currency: 'USD' } } });
  assert.deepEqual([r.ctx.total, r.ctx.live, r.ctx.cost.amount], [200_000, 61_000, 0.05]);
  ws.serve({ type: 'turn.context', conversationId: 'c_a', turnId: 't3', seq: 2, payload: { used: 62_000, size: 200_000 } });
  assert.deepEqual([r.ctx.live, r.ctx.cost], [62_000, null]);

  // An agent's prompt result may carry no token count at all: what a step
  // reports is no measure of its context, so the agent's own figure stands.
  ws.serve({ type: 'turn.step_finished', conversationId: 'c_a', turnId: 't3', seq: 3, payload: { usage: { totalInputTokens: 0 } } });
  assert.equal(r.ctx.live, 62_000);
});

test('a notice from the agent shows as a toast of its severity', async () => {
  serveThread('c_n', { messages: [{ id: 'm1', role: 'user', content: 'first' }], info: { agentId: 'claude', modelId: 'opus' } }, {
    'GET /api/context': { estimate: 0, total: 0 },
  });
  chat.navigate('#/c/c_n');
  await sleep(40);
  const r = state.rooms.c_n;
  state.toasts = [];
  assert.equal(chat.submit(r, 'work'), true);
  opened(sentTurns().at(-1), 'c_n', 't7');
  await sleep(10);
  const notice = (seq, payload) => ws.serve({ type: 'turn.notice', conversationId: 'c_n', turnId: 't7', seq, payload });
  notice(1, { severity: 'warning', title: 'Heads up', text: 'The agent is slow.' });
  notice(2, { severity: 'info', text: 'Compacting.' });
  notice(3, { severity: 'error', title: 'Broken', text: 'Something failed.' });
  assert.deepEqual(state.toasts.map((t) => [t.kind, t.message]), [
    ['warning', 'Heads up: The agent is slow.'], ['info', 'Compacting.'], ['error', 'Broken: Something failed.'],
  ]);
});

test('a room with no agent report yet has no meter to show', async () => {
  serveThread('c_b', { info: { agentId: 'claude', modelId: 'opus' } }, {
    'GET /api/context': { estimate: 0, total: 0 },
  });
  chat.navigate('#/c/c_b');
  await sleep(40);
  assert.equal(state.rooms.c_b.ctx.total, 0);
});

// ---------- the effort a room keeps ----------

test('a thread opens with the effort stored on it, as after a reload or a server restart', async () => {
  serveThread('c_1', { info: { providerId: 'p', modelId: 'm', effort: 'xhigh', upstream: 'Alpha' } });
  chat.navigate('#/c/c_1');
  await sleep(40);
  const sel = chat.selection(state.rooms.c_1);
  assert.deepEqual([sel.providerId, sel.modelId, sel.effort, sel.upstream], ['p', 'm', 'xhigh', 'Alpha']);

  // A thread that stores none shows the model's own.
  serveThread('c_2', { info: { providerId: 'p', modelId: 'm' } });
  chat.navigate('#/c/c_2');
  await sleep(40);
  assert.equal(chat.selection(state.rooms.c_2).effort, 'default');
});

test('picking an effort saves it on the thread without rebinding its model', async () => {
  serveThread('c_1', { info: { providerId: 'p', modelId: 'm' } }, {
    'PATCH /api/conversations/c_1': (call) => ({ id: 'c_1', providerId: 'p', modelId: 'm', ...call.body }),
    'GET /api/context': { estimate: 1, total: 1000 },
  });
  chat.navigate('#/c/c_1');
  await sleep(40);
  const r = state.rooms.c_1;
  calls.length = 0;
  chat.setSelection(r, { effort: 'high' });
  await sleep(40);
  assert.equal(patches().length, 1);
  assert.deepEqual(patches()[0].body, { effort: 'high', upstream: '' });
  // The thread answers with what it stored, and that is what the picker reads.
  assert.equal(chat.selection(r).effort, 'high');
  assert.equal(r.sel.effort, undefined);
});

// The server refuses payload fields it does not know, and an edit or a retry
// takes no agent: it runs on the thread's own runtime, which the picker has
// already bound. Sending the agent anyway failed every edit in an agent thread
// with "turn.edit payload is invalid".
test('an edit or a retry in an agent thread carries no agent, a send does', async () => {
  serveThread('c_ag', { messages: [{ id: 'm1', role: 'user', content: 'first' }], info: { agentId: 'claude', modelId: 'opus' } }, {
    'GET /api/context': { estimate: 0, total: 0 },
  });
  chat.navigate('#/c/c_ag');
  await sleep(40);
  const r = state.rooms.c_ag;

  assert.equal(chat.editMessage(r, 'm1', 'first, reworded'), true);
  const edit = ws.sent.findLast((c) => c.type === 'turn.edit');
  assert.deepEqual(edit.payload, { messageId: 'm1', content: 'first, reworded' });

  r.pendingOpen = null;
  chat.retry(r);
  const resume = ws.sent.findLast((c) => c.type === 'turn.resume');
  assert.deepEqual(resume.payload, {});

  // A send still names the agent.
  r.pendingOpen = null;
  r.sel = { agentId: 'claude', modelId: 'opus' };
  assert.equal(chat.submit(r, 'next'), true);
  const send = sentTurns().at(-1);
  assert.deepEqual([send.payload.agentId, send.payload.modelId], ['claude', 'opus']);
});

test('an edit in a provider thread still carries the picker\'s model and effort', async () => {
  serveThread('c_pv', { messages: [{ id: 'm1', role: 'user', content: 'first' }], info: { providerId: 'p', modelId: 'm' } }, {
    'GET /api/context': { estimate: 0, total: 1000 },
  });
  chat.navigate('#/c/c_pv');
  await sleep(40);
  const r = state.rooms.c_pv;
  assert.equal(chat.editMessage(r, 'm1', 'reworded'), true);
  const edit = ws.sent.findLast((c) => c.type === 'turn.edit');
  assert.deepEqual([edit.payload.providerId, edit.payload.modelId, edit.payload.effort], ['p', 'm', 'default']);
  assert.equal('agentId' in edit.payload, false);
});

test('every turn from the web names its effort, "default" included', async () => {
  state.prefs = { ...state.prefs, lastModel: undefined }; // an earlier pick is not this test's
  const cmd = firstSend('hello');
  assert.equal(cmd.payload.effort, 'default');
  state.rooms = {};
  const home = chat.freshRoom();
  home.draft = '';
  home.sel = { providerId: 'p', modelId: 'm', effort: 'max' };
  chat.submit(home, 'again');
  assert.equal(sentTurns().at(-1).payload.effort, 'max');
});

// ---------- deleting a thread ----------

const thread = (id, extra = {}) => ({ id, title: 'T ' + id, updatedAt: '2026-01-01T00:00:00Z', ...extra });
const deleteCalls = () => calls.filter((c) => c.method === 'DELETE');
const listPage = (items, total = items.length) => ({ data: items, page: { offset: 0, limit: 30, total, hasMore: false } });

function seedThreads(items, projectItems = {}) {
  state.convs = { ...state.convs, items, total: items.length, status: 'ready' };
  state.projectThreads = projectItems;
  state.rooms = {};
  state.deleting = [];
  state.toasts = [];
}

test('a deleted thread leaves every list that held it, the project\'s own included', async () => {
  const a = thread('c_a'), b = thread('c_b', { projectId: 'p1' });
  seedThreads([a], { p1: [b] }); // b lives only in its project's list
  mockApi({
    'DELETE /api/conversations/c_b': [204, null],
    'GET /api/conversations': () => listPage([a]),
    'GET /api/projects': { data: [] },
  });
  await chat.removeRoom(chat.room('c_b'));
  assert.deepEqual(state.projectThreads.p1, []);
  assert.deepEqual(state.convs.items.map((c) => c.id), ['c_a']);
  assert.deepEqual(state.toasts.map((t) => [t.kind, t.message]), [['info', 'Thread deleted.']]);
  assert.deepEqual(state.deleting, []);
});

test('deleting an open thread goes home, and counts are asked for again', async () => {
  const a = thread('c_open');
  seedThreads([a]);
  mockApi({
    'DELETE /api/conversations/c_open': [204, null],
    'GET /api/conversations': () => listPage([]),
    'GET /api/projects': { data: [] },
    'GET /api/conversations/c_open': thread('c_open'),
  });
  chat.navigate('#/c/c_open');
  await sleep(20);
  await chat.removeRoom(state.rooms.c_open);
  assert.equal(location.hash, '#/');
  assert.equal(state.convs.total, 0);
  assert.ok(calls.some((c) => c.method === 'GET' && c.url.startsWith('/api/projects')), 'the projects\' thread counts were not refreshed');
});

test('a thread the server no longer has counts as deleted', async () => {
  seedThreads([thread('c_gone')]);
  mockApi({
    'DELETE /api/conversations/c_gone': [404, { code: 'not_found', detail: 'conversation c_gone not found' }],
    'GET /api/conversations': () => listPage([]),
    'GET /api/projects': { data: [] },
  });
  await chat.removeRoom(chat.room('c_gone'));
  assert.deepEqual(state.convs.items, []);
  assert.deepEqual(state.toasts.map((t) => [t.kind, t.message]), [['info', 'Thread deleted.']], 'a 404 on delete is the outcome asked for, not an error');
});

test('a delete that fails keeps the thread, says why, and can be tried again', async () => {
  seedThreads([thread('c_keep')]);
  mockApi({ 'DELETE /api/conversations/c_keep': [500, { code: 'internal', detail: 'disk is read-only' }] });
  await chat.removeRoom(chat.room('c_keep'));
  assert.deepEqual(state.convs.items.map((c) => c.id), ['c_keep']);
  assert.equal(state.toasts[0].kind, 'error');
  assert.match(state.toasts[0].message, /Delete failed: .*disk is read-only/);
  assert.deepEqual(state.deleting, []);
  await chat.removeRoom(chat.room('c_keep'));
  assert.equal(deleteCalls().length, 2);
});

test('a thread being deleted shows it, a second delete is not started, and a slow one is announced', async () => {
  seedThreads([thread('c_slow')]);
  mockApi({
    'DELETE /api/conversations/c_slow': async () => { await sleep(600); return [204, null]; },
    'GET /api/conversations': () => listPage([]),
    'GET /api/projects': { data: [] },
  });
  const first = chat.removeRoom(chat.room('c_slow'));
  assert.deepEqual(state.deleting, ['c_slow']);
  await chat.removeRoom(chat.room('c_slow')); // ignored: already on its way
  await sleep(450);
  assert.deepEqual(state.toasts.map((t) => t.message), ['Deleting thread…'], 'nothing told the owner the delete was under way');
  await first;
  assert.equal(deleteCalls().length, 1);
  assert.deepEqual(state.toasts.map((t) => t.message), ['Thread deleted.'], 'the progress notice must give way to the result');
  assert.deepEqual(state.deleting, []);
});

// ---------- the thread list keeps what is paged in, but not what is gone ----------

test('a refresh that saw the whole list drops a thread the server no longer lists', async () => {
  const keep = [thread('c_1'), thread('c_2'), thread('c_3')];
  seedThreads([keep[0], thread('c_deleted_elsewhere'), keep[1], keep[2]]);
  mockApi({ 'GET /api/conversations': () => listPage(keep) });
  await chat.loadConversations();
  assert.deepEqual(state.convs.items.map((c) => c.id), ['c_1', 'c_2', 'c_3']);
  assert.equal(state.convs.total, 3);
});

test('a refresh that filled its page keeps the threads paged in beyond it', async () => {
  const paged = Array.from({ length: 130 }, (_, i) => thread('c_' + i));
  seedThreads(paged);
  state.convs.total = 200;
  mockApi({ 'GET /api/conversations': (call) => {
    const limit = Number(new URL(call.url, 'http://x').searchParams.get('limit'));
    return { data: paged.slice(0, limit), page: { offset: 0, limit, total: 200, hasMore: true } };
  } });
  await chat.loadConversations();
  assert.equal(state.convs.items.length, 130, 'the reader\'s list shrank under them');
  assert.equal(calls.at(-1).url.includes('limit=100'), true, 'a refresh asks for what the reader has paged in, up to the cap');
});

// ---------- malformed routes, Stop, retry and the queue ----------

// liveRoom opens id and starts a turn on it, leaving the turn running.
async function liveRoom(id) {
  serveThread(id, { messages: [{ id: 'm1', role: 'user', content: 'first' }] }, {
    'GET /api/context': { estimate: 0, total: 1000 },
  });
  chat.navigate('#/c/' + id);
  await sleep(40);
  const r = state.rooms[id];
  assert.equal(chat.submit(r, 'work'), true);
  const cmd = sentTurns().at(-1);
  opened(cmd, id, 't_run');
  await sleep(10);
  assert.ok(r.live, 'a turn is running');
  return { r, cmd };
}
const cancels = () => ws.sent.filter((c) => c.type === 'turn.cancel');
const resumes = () => ws.sent.filter((c) => c.type === 'turn.resume');
const watches = () => ws.sent.filter((c) => c.type === 'turn.watch');

test('a malformed conversation hash shows home and skips the metadata read', async () => {
  calls.length = 0;
  state.sidebarOpen = true;
  chat.navigate('#/c/%E0%A4%A');
  await sleep(0);
  assert.deepEqual(state.route, { view: 'home' });
  assert.equal(state.sidebarOpen, false);
  assert.equal(calls.some((c) => c.url.includes('conversations/%')), false, 'no thread read for the undecodable id');

  // A well-formed escape still reaches its thread.
  serveThread('c_1');
  chat.navigate('#/c/%63_1');
  await sleep(20);
  assert.deepEqual(state.route, { view: 'chat', id: 'c_1' });
  await sleep(40);
  assert.equal(state.rooms.c_1.infoStatus, 'ready');
});

// A path URL is what an address looks like once the hash is gone: a
// thread link has to survive that, and anything else needs an answer
// instead of the home screen under a dead address.
test('a path-style thread link is taken over and an unknown path is not found', async () => {
  serveThread('c_path');
  location.pathname = '/c/c_path';
  chat.navigate('#/');
  await sleep(30);
  assert.equal(location.pathname, '/', 'the address is rewritten to a routable one');
  assert.equal(location.hash, '#/c/c_path');
  assert.deepEqual(state.route, { view: 'chat', id: 'c_path' });
  assert.equal(state.rooms.c_path.infoStatus, 'ready');

  location.pathname = '/nope/nested';
  chat.navigate('#/');
  await sleep(10);
  assert.equal(location.pathname, '/');
  assert.deepEqual(state.route, { view: 'missing', path: '/nope/nested' });

  // A thread-shaped path with no id is not a thread link either.
  location.pathname = '/c/';
  chat.navigate('#/');
  await sleep(10);
  assert.deepEqual(state.route, { view: 'missing', path: '/c/' });
});

test('Stop is not marked while the socket is down', async () => {
  const { r } = await liveRoom('c_stopoff');
  const ready = ws.readyState;
  ws.readyState = 3;
  try {
    chat.cancel(r);
    assert.equal(!!r.live.canceling, false, 'the user must be able to press Stop once reconnected');
    assert.equal(cancels().length, 0);
    assert.equal(state.toasts.at(-1).kind, 'error');
  } finally {
    ws.readyState = ready;
  }
});

test('a rejected Stop unlocks and says why; an acked one stays pending until the terminal', async () => {
  const { r, cmd } = await liveRoom('c_stoprej');

  chat.cancel(r);
  chat.cancel(r); // a second press sends nothing
  assert.equal(cancels().length, 1);
  assert.equal(cancels()[0].turnId, 't_run');
  assert.equal(r.live.canceling, true);

  ws.serve({ type: 'error', requestId: cancels()[0].requestId, code: 'turn_idle', message: 'no turn is running' });
  assert.equal(!!r.live.canceling, false, 'the rejection frees Stop again');
  assert.equal(state.toasts.at(-1).message, 'no turn is running');

  chat.cancel(r);
  assert.equal(cancels().length, 2);
  const second = cancels()[1];
  ws.serve({ type: 'turn.cancel_ack', requestId: second.requestId });
  await sleep(10);
  assert.equal(r.live.canceling, true, 'the ack leaves Stop pending for the terminal');

  terminal(cmd, 'turn.completed', 'c_stoprej', { userMessagePersisted: true }, 't_run');
  await sleep(40);
  assert.equal(r.live, null);
});

test('an unacked Stop unlocks on reconnect without a replay; an acked one stays pending', async () => {
  const a = await liveRoom('c_stoprc');
  chat.cancel(a.r);
  assert.equal(cancels().length, 1);

  const b = await liveRoom('c_stoprc2');
  chat.cancel(b.r);
  const acked = cancels().at(-1);
  ws.serve({ type: 'turn.cancel_ack', requestId: acked.requestId });
  await sleep(10);

  assert.equal(cancels().length, 2, 'both cancels were sent');
  ws.sent.length = 0;
  ws.open(); // the reconnect reattaches every live turn
  await sleep(10);

  assert.equal(cancels().length, 0, 'a cancel with no ack is not replayed');
  assert.equal(!!a.r.live.canceling, false, 'the unacked Stop is a button again');
  assert.equal(b.r.live.canceling, true, 'the acked cancel stays pending');
  assert.equal(watches().filter((w) => w.turnId === 't_run' && w.conversationId === 'c_stoprc').length, 1, 'the turn is rewatched');
});

test('retry while the socket is unavailable sends nothing and keeps the failure', async () => {
  const r = chat.room('c_retryoff');
  r.failed = { status: 502, code: 'upstream', message: 'upstream down' };
  r.held = true;
  const ready = ws.readyState;
  ws.readyState = 3;
  try {
    chat.retry(r);
    assert.equal(resumes().length, 0);
    assert.equal(r.pendingOpen, null);
    assert.equal(r.failed.message, 'upstream down');
    assert.equal(r.held, true);
  } finally {
    ws.readyState = ready;
  }
});

test('retry clears the failure only once the server accepts; a reject restores it', async () => {
  serveThread('c_retry');
  const r = chat.room('c_retry');
  r.failed = { status: 502, code: 'upstream', message: 'upstream down' };
  r.held = true;

  chat.retry(r);
  chat.retry(r); // busy now: no second resume
  assert.equal(resumes().length, 1);
  const first = resumes()[0];
  assert.equal(r.pendingOpen?.requestId, first.requestId);
  // The original failure is retained while the resume is unconfirmed.
  assert.equal(r.failed.message, 'upstream down');
  assert.equal(r.held, true);

  ws.serve({ type: 'error', requestId: first.requestId, code: 'no_turn', message: 'nothing to resume' });
  assert.equal(r.pendingOpen, null);
  assert.equal(r.failed.message, 'upstream down', 'the rejection keeps the failure card');
  assert.equal(r.held, true);

  chat.retry(r);
  assert.equal(resumes().length, 2);
  const second = resumes()[1];
  opened(second, 'c_retry', 't_res');
  await sleep(10);
  assert.equal(r.failed, null);
  assert.equal(r.held, false);
  assert.equal(r.pendingOpen, null);
});

test('a resume in flight when the connection drops keeps the failure and explains itself', async () => {
  serveThread('c_retrydc');
  const r = chat.room('c_retrydc');
  r.failed = { status: 502, code: 'upstream', message: 'upstream down' };
  r.held = true;
  chat.retry(r);
  assert.equal(r.pendingOpen?.kind, 'resume');

  ws.sent.length = 0;
  ws.open(); // reconnect: the unconfirmed resume is not replayed
  await sleep(10);
  assert.equal(r.pendingOpen, null);
  assert.equal(resumes().length, 0);
  assert.equal(r.failed.message, 'upstream down');
  assert.equal(r.held, true);
  assert.ok(state.toasts.some((t) => /server confirmed the retry/.test(t.message)));
});

test('a queued item unavailable at drain keeps the queue, the failure and the hold', async () => {
  const r = chat.room('c_queuedoff');
  r.queue.push({ id: 'q1', text: 'next', references: [], files: [] });
  r.failed = { status: 500, code: 'x', message: 'boom' };
  r.held = true;

  const ready = ws.readyState;
  ws.readyState = 3;
  try {
    chat.resumeQueue(r);
    assert.equal(r.queue.length, 1, 'the item goes back to the head of the queue');
    assert.equal(r.queue[0].id, 'q1');
    assert.equal(r.failed.message, 'boom');
    assert.equal(r.held, true);
    assert.equal(sentTurns().length, 0);
  } finally {
    ws.readyState = ready;
  }

  chat.resumeQueue(r);
  assert.equal(r.queue.length, 0);
  assert.equal(r.held, false);
  assert.equal(sentTurns().length, 1);
  assert.equal(sentTurns()[0].payload.content, 'next');
});

test('a queue head refused before open keeps its place and the queue stays held', async () => {
  const r = chat.room('c_qhead');
  const a = { id: 'qa', text: 'first', references: [], files: [] };
  const b = { id: 'qb', text: 'second', references: [], files: [] };
  r.queue.push(a, b);
  r.held = true;
  r.draft = 'keep me';

  chat.resumeQueue(r);
  assert.equal(sentTurns().length, 1);
  assert.equal(sentTurns()[0].payload.content, 'first');

  ws.serve({ type: 'error', requestId: sentTurns()[0].requestId, code: 'queue_full', message: 'too many active turns' });
  await sleep(10);
  assert.equal(sentTurns().length, 1, 'a refused head is not replayed');
  assert.equal(r.draft, 'keep me', 'a queued send refused before open does not touch the draft');
  assert.deepEqual(r.queue, [a, b], 'the refused head is back first, before later items');
  assert.equal(r.held, true);

  chat.resumeQueue(r);
  assert.equal(sentTurns().length, 2);
  assert.equal(sentTurns()[1].payload.content, 'first', 'Send next retries the head, not a later item');
});

test('a socket drop during terminal refresh holds the queue until Send next', async () => {
  const { r, cmd } = await liveRoom('c_gate');
  const next = { id: 'qn', text: 'after', references: [], files: [] };
  r.queue.push(next);

  // Let the terminal begin its refresh, but finish it after the socket drops.
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  mockApi({
    [`GET /api/conversations/c_gate/messages`]: () => gate.then(() => ({ messages: [{ id: 'm1', role: 'user', content: 'first' }], effectiveStart: 0, total: 1 })),
    [`GET /api/conversations/c_gate`]: { id: 'c_gate', title: 'Thread', workspace: '' },
    'GET /api/context': { estimate: 0, total: 1000 },
  });
  terminal(cmd, 'turn.completed', 'c_gate', { userMessagePersisted: true }, 't_run');
  const ready = ws.readyState;
  ws.readyState = 3;
  try {
    release();
    await sleep(40);
  } finally {
    ws.readyState = ready;
  }

  assert.equal(!!r.live, false, 'the completed turn is no longer live');
  assert.equal(chat.isBusy(r), false);
  assert.equal(sentTurns().filter((c) => c.payload.content === 'after').length, 0, 'nothing dispatched while disconnected');
  assert.deepEqual(r.queue, [next], 'the queued head is kept');
  assert.equal(r.hist.status, 'ready');
  assert.equal(r.held, true);

  chat.resumeQueue(r);
  assert.equal(sentTurns().at(-1).payload.content, 'after');
  assert.equal(r.held, false);
});

test('a replayed terminal confirms a retry whose opening reply was lost', async () => {
  serveThread('c_replay', { messages: [{ id: 'm1', role: 'user', content: 'first' }] }, {
    'GET /api/context': { estimate: 0, total: 1000 },
  });
  chat.navigate('#/c/c_replay');
  await sleep(40);
  const r = state.rooms.c_replay;
  const failure = { status: 502, code: 'upstream', message: 'upstream down' };
  r.failed = failure;
  r.held = true;

  chat.retry(r);
  const resumeCmd = resumes().at(-1);
  assert.equal(r.pendingOpen?.requestId, resumeCmd.requestId);

  // On reconnect the thread's list row reports the retry still running;
  // the debounced refresh picks it up and rewatches.
  serveThread('c_replay', { messages: [{ id: 'm1', role: 'user', content: 'first' }], info: { activeTurnId: 't_replay', activeTurnKind: 'edit' } }, {
    'GET /api/context': { estimate: 0, total: 1000 },
    'GET /api/conversations': () => listPage([thread('c_replay', { activeTurnId: 't_replay', activeTurnKind: 'edit' })]),
  });
  ws.sent.length = 0;
  ws.open(); // reconnect: the unconfirmed resume is remembered, not replayed
  await sleep(5600);
  assert.equal(r.pendingOpen, null);
  assert.equal(resumes().length, 0);
  assert.equal(r.unconfirmedRetry?.requestId, resumeCmd.requestId);
  assert.equal(r.failed, failure);

  // The room rewatches the reported turn; replay carries the original ids.
  const w = watches().at(-1);
  assert.equal(w.turnId, 't_replay');
  assert.equal(w.afterSeq, -1);

  // The replayed turn.opened keeps the resume's requestId; onOpened ignores
  // it (the request is gone) because watch() already prepared the live turn.
  opened(resumeCmd, 'c_replay', 't_replay');
  await sleep(10);
  assert.equal(r.live?.turnId, 't_replay');
  assert.equal(r.failed, failure, 'the watched turn hides the card but the failure is still on file');

  // The terminal replays the original requestId: proof the resume was accepted.
  ws.serve({ type: 'turn.completed', requestId: resumeCmd.requestId, conversationId: 'c_replay', turnId: 't_replay', seq: 1, payload: {} });
  await sleep(40);
  assert.equal(r.live, null);
  assert.equal(r.failed, null, 'the confirmed retry clears the stale failure');
  assert.equal(r.unconfirmedRetry, null);
});

test('steer carries the full draft — references, uploads, annotations — and restores them on rejection', async () => {
  const { r, cmd } = await openBusyThread();
  const refs = [{ kind: 'conversation', id: 'c_dog', label: '@dog', start: 4, end: 8 }];
  const files = [
    { key: 'k1', status: 'ready', id: 'u_1', name: 'shot.png', mediaType: 'image/png' },
    { key: 'k2', status: 'ready', pickId: 'pk_1', marker: 1, screenshot: true, name: '[1] <button>' },
  ];
  assert.equal(chat.submit(r, '  é @dog  ', refs, files, 't2'), true);
  const steer = ws.sent.at(-1);
  assert.equal(steer.type, 'turn.steer');
  assert.equal(steer.turnId, 't2');
  assert.equal(steer.payload.content, 'é @dog');
  assert.deepEqual(steer.payload.references, [{ kind: 'conversation', id: 'c_dog', label: '@dog', start: 3, end: 7 }]);
  assert.deepEqual(steer.payload.uploads, ['u_1']);
  assert.deepEqual(steer.payload.annotations, [{ pickId: 'pk_1', screenshot: true }]);
  assert.equal(r.steers.length, 1);

  // A rejected steer returns the whole input to the draft — text,
  // selections at their editor spans, and files.
  r.draft = 'newer @cat';
  r.refs = [{ kind: 'conversation', id: 'c_cat', label: '@cat', start: 6, end: 10 }];
  r.files = [];
  ws.serve({ type: 'error', requestId: steer.requestId, code: 'invalid_argument', message: 'nope' });
  assert.equal(r.draft, 'é @dog\nnewer @cat');
  const dr = r.refs.find((x) => x.id === 'c_dog');
  const cr = r.refs.find((x) => x.id === 'c_cat');
  assert.ok(dr && cr, 'both selections restored');
  assert.deepEqual([dr.start, dr.end], [2, 6], '@dog back at its editor span');
  assert.deepEqual([cr.start, cr.end], [13, 17], 'newer draft ref shifted past the restored text');
  assert.equal(r.files.length, 2);
  assert.equal(r.files[0].id, 'u_1');
  assert.equal(r.files[1].pickId, 'pk_1');
});

test('an unconsumed attachment-only steer returns to the draft, not the queue', async () => {
  const { r, cmd } = await openBusyThread();
  const files = [{ key: 'k1', status: 'ready', id: 'u_1', name: 'shot.png', mediaType: 'image/png' }];
  assert.equal(chat.submit(r, '', [], files, 't2'), true);
  const steer = ws.sent.at(-1);
  assert.equal(steer.type, 'turn.steer');
  assert.equal(steer.payload.content, '');
  assert.deepEqual(steer.payload.uploads, ['u_1']);
  terminal(cmd, 'turn.completed', 'c_1', { userMessagePersisted: true, steering: [{ inputId: steer.requestId, state: 'pending' }] }, 't2');
  await sleep(60);
  assert.equal(r.steers.length, 0);
  assert.deepEqual(r.files, files);
  assert.equal(r.queue.length, 0);
  assert.equal(sentTurns().length, 1, 'steer must not start a new turn');
  assert.match(state.toasts.at(-1).message, /back in the draft/);
});

test('a terminal snapshot keeps an unconfirmed steer for a late rejection', async () => {
  const { r, cmd } = await openBusyThread();
  const files = [{ key: 'k1', status: 'ready', id: 'u_1', name: 'shot.png' }];
  assert.equal(chat.submit(r, '', [], files, 't2'), true);
  const steer = ws.sent.at(-1);
  terminal(cmd, 'turn.completed', 'c_1', { userMessagePersisted: true }, 't2');
  assert.equal(r.steers[0]?.state, 'unknown');
  assert.equal(r.queue.length, 0);
  ws.serve({ type: 'error', requestId: steer.requestId, code: 'not_found', message: 'no live turn with that id' });
  assert.equal(r.steers.length, 0);
  assert.deepEqual(r.files, files);
  await sleep(40);
});

test('steer receipts after reconnect reconcile pending, then a failed snapshot', async () => {
  const { r } = await openBusyThread();
  const files = [{ key: 'k1', status: 'ready', id: 'u_1', name: 'shot.png' }];
  assert.equal(chat.submit(r, 'é @dog', [{ kind: 'conversation', id: 'c_dog', label: '@dog', start: 2, end: 6 }], files, 't2'), true);
  const steer = ws.sent.at(-1);

  ws.open(); // reconnect: the live turn is rewatched
  await sleep(40);
  const watch = ws.sent.findLast((x) => x.type === 'turn.watch');
  assert.equal(watch.turnId, 't2');
  ws.serve({
    type: 'turn.resync_required', requestId: watch.requestId, conversationId: 'c_1', turnId: 't2',
    payload: { turn: 'active', steering: [{ inputId: steer.requestId, state: 'pending' }] },
  });
  assert.equal(r.steers[0]?.state, 'pending');
  assert.equal(r.queue.length, 0);
  assert.equal(r.files.length, 0);

  ws.open(); // reconnect again: this time the snapshot is terminal
  await sleep(40);
  const watch2 = ws.sent.findLast((x) => x.type === 'turn.watch');
  const steering = [{ inputId: steer.requestId, state: 'claimed' }];
  r.draft = 'newer';
  ws.serve({
    type: 'turn.resync_required', requestId: watch2.requestId, conversationId: 'c_1', turnId: 't2',
    payload: { turn: 'failed', steering, terminal: { status: 502, code: 'upstream', message: 'upstream down', userMessagePersisted: true, steering } },
  });
  await sleep(60);
  assert.equal(r.failed?.status, 502);
  assert.equal(r.steers.length, 0);
  assert.equal(r.queue.length, 0);
  assert.equal(r.draft, 'é @dog\nnewer');
  assert.deepEqual(r.refs.map((x) => [x.id, x.start, x.end]), [['c_dog', 2, 6]]);
  assert.deepEqual(r.files, files);
  assert.equal(sentTurns().length, 1, 'no automatic send after a failed snapshot');
});

test('a never-saved first turn returns its queue, steers, and draft to home', async () => {
  serveThread('c_1', { infoFails: 99 });
  const cmd = firstSend('hello');
  opened(cmd, 'c_1');
  await sleep(10);
  const r = state.rooms.c_1;
  const files = [{ key: 'k1', status: 'ready', id: 'u_1', name: 'shot.png' }];
  assert.equal(chat.submit(r, '', [], files, 't1'), true);
  const steer = ws.sent.at(-1);
  assert.equal(chat.submit(r, 'later'), true, 'a later send queues behind the busy turn');
  r.draft = '  @cat  ';
  r.refs = [{ kind: 'conversation', id: 'c_cat', label: '@cat', start: 2, end: 6 }];

  terminal(cmd, 'turn.failed', 'c_1', {
    status: 502, code: 'upstream', message: 'upstream down', userMessagePersisted: false,
    steering: [{ inputId: steer.requestId, state: 'pending' }],
  }, 't1');
  await sleep(300);
  const home = chat.freshRoom();
  assert.equal(home.draft, 'hello\n  @cat  ');
  const cr = home.refs.find((x) => x.id === 'c_cat');
  assert.deepEqual([cr.start, cr.end], [8, 12]);
  assert.equal(home.held, true);
  assert.deepEqual(home.queue.map((q) => q.text), ['later']);
  assert.deepEqual(home.files, files);
  assert.equal(sentTurns().length, 1, 'no automatic send after the move home');
});

test('a quote is a Markdown blockquote, capped', () => {
  assert.equal(chat.quoteBlock('  one\r\n\r\ntwo  '), '> one\n>\n> two');
  assert.equal(chat.quoteBlock('   '), '');
  const long = chat.quoteBlock('x'.repeat(chat.QUOTE_LIMIT + 50));
  assert.equal(long, '> ' + 'x'.repeat(chat.QUOTE_LIMIT) + '…');
});

test('a quote rides above the draft and is written ahead of its text when sent', () => {
  const r = chat.room('c_quote');
  r.draft = '@cat look';
  r.refs = [{ kind: 'conversation', id: 'c_cat', label: '@cat', start: 0, end: 4 }];
  assert.equal(chat.addQuote(r, ' a\nb ', ' why? '), true);
  assert.equal(chat.addQuote(r, 'c'), true);
  assert.equal(chat.addQuote(r, ' '), false);
  assert.equal(r.draft, '@cat look', 'the draft text is untouched');
  assert.deepEqual(r.quotes.map((q) => [q.text, q.reply]), [['a\nb', 'why?'], ['c', '']]);
  assert.equal(r.caret, r.draft.length);
  const out = chat.quotedDraft(r.quotes, r.draft, r.refs);
  assert.equal(out.text, '> a\n> b\n\nwhy?\n\n> c\n\n@cat look');
  assert.equal(out.text.slice(out.refs[0].start, out.refs[0].end), '@cat');
  assert.equal(chat.quotedDraft(r.quotes, '', []).text, '> a\n> b\n\nwhy?\n\n> c', 'a quote alone is a message');
  assert.deepEqual(chat.quotedDraft([], 'x', r.refs), { text: 'x', refs: r.refs });
  chat.removeQuote(r, r.quotes[0].key);
  assert.deepEqual(r.quotes.map((q) => q.text), ['c']);
});

test('a side chat opens the new-thread composer on the origin runtime, project, and a link back', async () => {
  const origin = chat.room('c_side');
  origin.info = { id: 'c_side', title: 'Plan [v2]', agentId: 'claude', modelId: 'opus', projectId: 'p_1', workspace: '/w' };
  const home = chat.freshRoom();
  home.draft = 'unsent';
  assert.equal(chat.startSideChat(origin, 'the bit', 'and this?'), true);
  await sleep(5);
  assert.equal(location.hash, '#/');
  assert.equal(home.draft, '');
  assert.equal(chat.quotedDraft(home.quotes, home.draft).text, 'From [Plan \\[v2\\]](/#/c/c_side):\n\n> the bit\n\nand this?');
  assert.deepEqual(home.sel, { agentId: 'claude', modelId: 'opus' });
  assert.equal(state.newProjectId, 'p_1');
  assert.deepEqual(home.sideOf, { id: 'c_side', title: 'Plan [v2]' });
  assert.equal(sentTurns().filter((c) => c.payload?.content?.includes('the bit')).length, 0, 'nothing runs until the owner sends');
  // The replaced draft comes back with Undo.
  state.toasts.at(-1).action.run();
  assert.equal(home.draft, 'unsent');
  assert.deepEqual(home.quotes, []);
  assert.equal(home.sideOf, null);

  const plain = chat.room('c_side2');
  plain.info = { id: 'c_side2', title: 'API', providerId: 'p', modelId: 'm', effort: 'high', upstream: 'u', workspace: '/w2' };
  chat.startSideChat(plain, 'x');
  assert.deepEqual(home.sel, { providerId: 'p', modelId: 'm', effort: 'high', upstream: 'u', workspace: '/w2' });
  assert.equal(state.newProjectId, '');
  home.draft = ''; home.quotes = []; home.sel = {}; home.sideOf = null;
});

test('a new thread starts on the owner default, and a pick or a binding wins over it', () => {
  const saved = state.threadDefault;
  state.prefs.lastModel = { providerId: 'p_last', modelId: 'm_last', effort: 'high' };
  state.threadDefault = { agent: 'claude', provider: '', model: 'opus' };
  const home = chat.freshRoom();
  home.sel = {};
  assert.deepEqual([chat.selection(home).agentId, chat.selection(home).modelId, chat.selection(home).effort], ['claude', 'opus', 'default']);
  state.threadDefault = { agent: '', provider: 'or', model: 'gpt', effort: 'max' };
  assert.deepEqual([chat.selection(home).providerId, chat.selection(home).modelId, chat.selection(home).effort], ['or', 'gpt', 'max']);
  home.sel = { providerId: 'p2', modelId: 'm2' };
  assert.equal(chat.selection(home).providerId, 'p2', 'a pick in the composer wins');
  home.sel = {};
  const bound = chat.room('c_bound_default');
  bound.info = { id: 'c_bound_default', providerId: 'px', modelId: 'mx' };
  assert.equal(chat.selection(bound).providerId, 'px', 'a started thread keeps its own');
  state.threadDefault = null;
  assert.equal(chat.selection(home).providerId, 'p_last', 'unset falls back to the last pick');
  state.threadDefault = saved;
});

// The purge is honest in both directions: a confirmed DELETE empties the
// client's threads, lists, and route; a refused one keeps everything.
test('deleteAllRooms empties local thread state after the server confirms', async () => {
  chat.room('c_1');
  chat.room('c_2');
  state.convs = { items: [{ id: 'c_1' }], total: 1, hasMore: false, status: 'ready', error: null, more: false };
  state.projectThreads = { p1: [{ id: 'c_1' }] };
  mockApi({
    'GET /api/conversations/c_1/messages': () => [200, { messages: [], effectiveStart: 0, total: 0 }],
    'GET /api/conversations/c_1': () => ({ id: 'c_1', title: 'T', workspace: '' }),
    'DELETE /api/conversations': () => [200, { deleted: 2 }],
    'GET /api/conversations': () => ({ data: [], total: 0, hasMore: false }),
    'GET /api/projects': () => ({ data: [] }),
  });
  chat.navigate('#/c/c_1');
  await sleep(0); // let the route open the room
  const res = await chat.deleteAllRooms();
  assert.equal(res.deleted, 2);
  assert.ok(calls.some((c) => c.method === 'DELETE' && c.url === '/api/conversations' && c.body.confirm === 'DELETE'),
    'the confirm word went out');
  // Every thread room is gone; the fresh home composer that navigating
  // '#/' creates is not a thread.
  assert.deepEqual(Object.keys(state.rooms), ['new']);
  assert.deepEqual(state.convs.items, []);
  assert.deepEqual(state.projectThreads, {});
  await sleep(0);
  assert.equal(state.route.view, 'home');
});

test('a failed purge keeps every local thread trace', async () => {
  const r = chat.room('c_1');
  r.draft = 'keep me';
  state.convs = { items: [{ id: 'c_1' }], total: 1, hasMore: false, status: 'ready', error: null, more: false };
  mockApi({ 'DELETE /api/conversations': () => [500, { code: 'stuck', detail: 'uploads busy' }] });
  await assert.rejects(() => chat.deleteAllRooms());
  assert.ok(state.rooms.c_1, 'the room stays');
  assert.equal(state.rooms.c_1.draft, 'keep me');
  assert.equal(state.convs.items.length, 1);
});

// ---------- a rename against a read already in flight ----------

// A pull that started before a change must not put the old value back: the
// thread list and the room record are pulls, and with several rooms live one
// is almost always in flight when the owner renames.
function heldReads() {
  let release;
  const gate = new Promise((r) => { release = r; });
  return { gate, release };
}

test('a rename is not undone by a thread list that started before it', async () => {
  const { gate, release } = heldReads();
  let title = 'Old title';
  mockApi({
    'GET /api/conversations/c_1/messages': () => ({ messages: [], effectiveStart: 0, total: 0 }),
    'GET /api/conversations/c_1': () => ({ id: 'c_1', title, workspace: '' }),
    'GET /api/conversations': async () => {
      const seen = title; // the page as the server had it when the read began
      await gate;
      return { data: [{ id: 'c_1', title: seen, activeTurnId: null, activeTurnKind: null }], page: { total: 1 } };
    },
    'GET /api/context': { estimate: 1, total: 2 },
    'PATCH /api/conversations/c_1': (call) => { title = call.body.title; return { id: 'c_1', title, workspace: '' }; },
    'GET /api/projects': { data: [] },
  });
  chat.navigate('#/c/c_1');
  await sleep(30);
  const r = state.rooms.c_1;
  const pending = chat.loadConversations(); // holds the pre-rename page
  await sleep(10);
  await chat.rename(r, 'New title');
  assert.equal(r.info.title, 'New title');
  release();
  await pending;
  await sleep(40);
  assert.equal(r.info.title, 'New title', 'the room keeps the name it was given');
  const row = state.convs.items.find((x) => x.id === 'c_1');
  assert.equal(row?.title, 'New title', 'the list row shows the new name');
});

test('a rename is not undone by a room record read that started before it', async () => {
  const { gate, release } = heldReads();
  let title = 'Old title';
  mockApi({
    'GET /api/conversations/c_1/messages': () => ({ messages: [], effectiveStart: 0, total: 0 }),
    'GET /api/conversations/c_1': async () => {
      const seen = title; // the record as the server had it when the read began
      await gate;
      return { id: 'c_1', title: seen, workspace: '' };
    },
    'GET /api/conversations': { data: [{ id: 'c_1', title, activeTurnId: null, activeTurnKind: null }], page: { total: 1 } },
    'GET /api/context': { estimate: 1, total: 2 },
    'PATCH /api/conversations/c_1': (call) => { title = call.body.title; return { id: 'c_1', title, workspace: '' }; },
    'GET /api/projects': { data: [] },
  });
  chat.navigate('#/c/c_1'); // its record read is held at the gate
  await sleep(30);
  const r = state.rooms.c_1;
  await chat.rename(r, 'New title');
  release();
  await sleep(60);
  assert.equal(r.info.title, 'New title', 'the late record does not overwrite the rename');
  assert.equal(r.infoStatus, 'ready');
});

// Pulls keep coming; what they answer is compared with what is on screen. An
// answer that says the same thing writes nothing — no new row object, no
// header swap, no render — while a different answer is applied.
test('a pull that answers with what is already shown writes nothing', async () => {
  const thread = { id: 'c_1', title: 'Same title', activeTurnId: null, activeTurnKind: null };
  mockApi({
    'GET /api/conversations/c_1/messages': () => ({ messages: [], effectiveStart: 0, total: 0 }),
    'GET /api/conversations/c_1': () => ({ id: 'c_1', title: 'Same title', workspace: '', messageCount: 3 }),
    'GET /api/conversations': { data: [thread], page: { total: 1 } },
    'GET /api/context': { estimate: 1, total: 2 },
    'GET /api/projects': { data: [] },
  });
  chat.navigate('#/c/c_1');
  await sleep(40);
  await chat.loadConversations(); // settle the list on this mock's answer
  const r = state.rooms.c_1;
  const row = state.convs.items.find((x) => x.id === 'c_1');
  const info = r.info;
  await chat.loadConversations(); // the same answer again
  await sleep(20);
  assert.equal(state.convs.items.find((x) => x.id === 'c_1'), row, 'the list keeps its row object');
  r.infoStale = true;
  await chat.openRoom('c_1');
  await sleep(20);
  assert.equal(r.info, info, 'the header keeps its record object');
  assert.equal(r.info.title, 'Same title');
});

// ---------- a steer the store already holds ----------

// A tab that reloads mid-turn reads the room (the steered input is already a
// user message there) and then replays the journal, which carries the same
// steer. The transcript draws it once — from the record, annotations and all.
test('a replayed steer is drawn once, from the history row that carries its inputId', async () => {
  const messages = [
    { id: 'm1', role: 'user', content: 'mulai' },
    { id: 'm2', role: 'user', content: 'tambahan', inputId: 'req-1', annotations: [{ marker: 1, comment: 'catatan' }] },
  ];
  serveThread('c_1', { messages });
  chat.navigate('#/c/c_1');
  await sleep(40);
  const r = state.rooms.c_1;
  // another tab (or a reload) watches the running turn from its first frame
  ws.serve({ type: 'conversation.activity', conversationId: 'c_1', turnId: 't9', payload: { state: 'opened', kind: 'send', requestId: 'req-other' } });
  ws.serve({ type: 'turn.opened', conversationId: 'c_1', turnId: 't9', seq: 0, payload: { startedAt: Date.now() } });
  ws.serve({ type: 'turn.steer', conversationId: 'c_1', turnId: 't9', seq: 1, payload: { inputId: 'req-1', text: 'tambahan', final: true } });
  ws.serve({ type: 'turn.delta', conversationId: 'c_1', turnId: 't9', seq: 2, payload: { text: 'lanjut' } });
  await sleep(30);
  const hist = liveHistory(historyItems(r.messages), r.live);
  // the live bubble is gone, and the record row takes its place in the tail —
  // the text that follows the steer stays after the row, not before it
  assert.deepEqual(liveSegments(r.live, hist).map((s) => s.id), ['m2', 't9:2'], 'no second bubble for the same steer');
  const row = hist.find((x) => x.kind === 'user' && x.steerInput === 'req-1');
  assert.ok(row, 'the history row is the steer');
  assert.equal(row.msg.annotations.length, 1, 'its annotation is there');
  assert.equal(liveSegments(r.live, hist)[0].id, row.id, 'drawn from the record, in the live position');

  // the owner withdraws it: the live bubble goes, the record stays
  ws.serve({ type: 'turn.steer_recalled', conversationId: 'c_1', turnId: 't9', seq: 3, payload: { inputId: 'req-1' } });
  await sleep(20);
  assert.deepEqual(r.live.segments.filter((s) => s.kind === 'steer'), []);
  assert.ok(historyItems(r.messages).some((x) => x.steerInput === 'req-1'));
});

// The other direction: this tab watches a turn another tab steered. The store
// writes the steered input at once, this tab's first pull lands that row, and
// the journal replays the same steer after it — so the bubble would come back
// on top of its own record. It is not drawn, and the row keeps its
// annotation; a steer this tab did not send is never queued here either.
test('a steer this tab sent draws the annotation picks its frame does not carry', async () => {
  const { r } = await openBusyThread();
  const files = [
    { key: 'k1', status: 'ready', pickId: 'pk_1', marker: 1, comment: 'tombol ini', screenshot: true, name: '[1] <button>' },
  ];
  assert.equal(chat.submit(r, 'cek ini', [], files, 't2'), true);
  const steer = ws.sent.at(-1);
  // The fold writes the user message and the journal replays it: text plus the
  // pick's screenshot, never the comment. This tab still holds what it sent.
  ws.serve({
    type: 'turn.steer', conversationId: 'c_1', turnId: 't2', seq: 1,
    payload: { inputId: steer.requestId, text: 'cek ini', final: true, attachments: [{ uri: 'file:///shot.png', mediaType: 'image/png', modality: 'image', name: '[1] <button>' }] },
  });
  const seg = r.live.segments.find((s) => s.kind === 'steer' && s.inputId === steer.requestId);
  assert.ok(seg, 'the bubble is in the turn');
  assert.deepEqual(seg.annotations.map((a) => a.comment), ['tombol ini'], 'the bubble shows the mark it was sent with');
  assert.deepEqual(seg.attachments.map((a) => a.uri), ['file:///shot.png'], 'and the shot stays a plain upload');
  assert.equal(r.steers.length, 0, 'the final frame ends the queued chip');

  // Another tab's steer has no local copy: its bubble carries the shot alone,
  // and the comment arrives with the record row.
  ws.serve({
    type: 'turn.steer', conversationId: 'c_1', turnId: 't2', seq: 2,
    payload: { inputId: 'req-other', text: 'lihat', final: true, attachments: [{ uri: 'file:///other.png', mediaType: 'image/png', modality: 'image', name: '[2] <a>' }] },
  });
  const other = r.live.segments.find((s) => s.kind === 'steer' && s.inputId === 'req-other');
  assert.equal(other.annotations, undefined, 'nothing invented for a steer this tab never sent');
});

// The other direction: this tab watches a turn another tab steered. The store
// writes the steered input at once, this tab's first pull lands that row, and
// the journal replays the same steer after it — so the bubble would come back
// on top of its own record. It is not drawn, and the row keeps its
// annotation; a steer this tab did not send is never queued here either.
test('a steer from another tab is drawn once when its record lands before the frame', async () => {
  const messages = [
    { id: 'm1', role: 'user', content: 'mulai' },
    { id: 'm2', role: 'assistant', content: 'jalan' },
  ];
  serveThread('c_1', { messages });
  chat.navigate('#/c/c_1');
  await sleep(40);
  const r = state.rooms.c_1;
  ws.serve({ type: 'conversation.activity', conversationId: 'c_1', turnId: 't5', payload: { state: 'opened', kind: 'send', requestId: 'req-other' } });
  ws.serve({ type: 'turn.opened', conversationId: 'c_1', turnId: 't5', seq: 0, payload: { startedAt: Date.now() } });
  // the other tab's steer is in the store by the time this one reads the page
  messages.push({ id: 'm3', role: 'user', content: 'tahan', inputId: 'req-s', annotations: [{ marker: 1, comment: 'catatan' }] });
  messages.push({ id: 'm4', role: 'assistant', content: 'sebentar' });
  ws.serve({ type: 'turn.delta', conversationId: 'c_1', turnId: 't5', seq: 1, payload: { text: 'sebentar' } });
  await sleep(60);
  assert.ok(r.messages.some((m) => m.id === 'm3'), 'the row landed mid-turn');
  // the journal replay of that same steer arrives after it
  ws.serve({ type: 'turn.steer', conversationId: 'c_1', turnId: 't5', seq: 2, payload: { inputId: 'req-s', text: 'tahan', final: true } });
  await sleep(30);
  assert.deepEqual(r.steers, [], 'the watching tab queues nothing');
  const during = liveSegments(r.live, liveHistory(historyItems(r.messages), r.live));
  assert.ok(r.live.segments.some((s) => s.kind === 'steer' && s.inputId === 'req-s'), 'the replay is in the turn');
  assert.deepEqual(during.filter((s) => s.kind === 'steer'), [], 'but its record is what the transcript draws');

  // the turn ends: the row is all that is left
  ws.serve({ type: 'turn.completed', conversationId: 'c_1', turnId: 't5', seq: 3, payload: { userMessagePersisted: true } });
  await sleep(80);
  assert.equal(r.live, null, 'the turn is over');
  const after = liveHistory(historyItems(r.messages), r.live);
  const steers = after.filter((x) => x.steerInput === 'req-s');
  assert.equal(steers.length, 1, 'one row, not two');
  assert.equal(steers[0].msg.annotations.length, 1, 'the record keeps its annotation');
});

// A provider call is retried by what it persisted, not by what the tab
// watched: the failed attempt committed nothing, so its streamed tail must go
// before the next attempt streams over it.
test('a retry_wait starts the answer over instead of splicing two attempts', async () => {
  const { r } = await openBusyThread();
  ws.serve({ type: 'turn.delta', conversationId: 'c_1', turnId: 't2', seq: 1, payload: { text: 'setengah jawaban' } });
  ws.serve({ type: 'turn.phase', conversationId: 'c_1', turnId: 't2', seq: 2, payload: { phase: 'retry_wait', retryAfterMs: 2000 } });
  await sleep(20);
  assert.equal(r.live.phase.phase, 'retry_wait', 'the status line announces the pause');
  assert.deepEqual(r.live.segments, [], 'the abandoned attempt left nothing behind');
  ws.serve({ type: 'turn.delta', conversationId: 'c_1', turnId: 't2', seq: 3, payload: { text: 'jawaban yang utuh' } });
  await sleep(20);
  const items = liveSegments(r.live, liveHistory(historyItems(r.messages), r.live));
  assert.deepEqual(items.map((s) => s.text), ['jawaban yang utuh'], 'one answer, from the attempt that counts');
  assert.equal(r.live.phase, null, 'the pause ends with the first delta');
});

test('a failed turn keeps the message the provider sent', async () => {
  const { r, cmd } = await openBusyThread();
  terminal(cmd, 'turn.failed', 'c_1', { status: 502, code: 'upstream_error', message: 'upstream stream broken', userMessagePersisted: true }, 't2');
  await sleep(60);
  assert.deepEqual([r.failed.status, r.failed.code, r.failed.message], [502, 'upstream_error', 'upstream stream broken']);
});

test('a failure frame with no message keeps none, so the card can say so', async () => {
  const { r, cmd } = await openBusyThread();
  terminal(cmd, 'turn.failed', 'c_1', { status: 502, code: 'upstream_error', userMessagePersisted: true }, 't2');
  await sleep(60);
  assert.equal(r.failed.message, '', 'no invented explanation stands in for the provider');
  assert.deepEqual([r.failed.status, r.failed.code], [502, 'upstream_error']);
});

// A turn the server never ran is not a failed turn: nothing to report, nothing
// to retry, and the prompt goes back to the draft.
test('a shutdown failure restores the draft and leaves no card and no retry', async () => {
  const { r, cmd } = await openBusyThread();
  r.messages = [...r.messages, { id: 'a1', role: 'assistant', content: 'the answer that finished' }];
  terminal(cmd, 'turn.failed', 'c_1', { status: 503, code: 'unavailable', message: 'server shutting down', userMessagePersisted: false }, 't2');
  await sleep(60);
  assert.equal(r.failed, null, 'no failure card for a turn that never ran');
  assert.equal(r.draft, 'work', 'the prompt is back in the draft');
  assert.equal(chat.waitingPrompt(r), null);
  assert.match(state.toasts.at(-1)?.message || '', /shutting down/);
});

test('a real failure on an unanswered prompt keeps the card and its retry', async () => {
  const { r, cmd } = await openBusyThread();
  terminal(cmd, 'turn.failed', 'c_1', { status: 500, code: 'internal', message: 'boom', userMessagePersisted: true }, 't2');
  await sleep(60);
  assert.equal(r.failed.status, 500);
  assert.ok(chat.waitingPrompt(r), 'the prompt has no answer, so it can be sent again');
});

test('a failure after the answer finished offers nothing to retry', async () => {
  const { r, cmd } = await openBusyThread();
  terminal(cmd, 'turn.failed', 'c_1', { status: 500, code: 'internal', message: 'boom', userMessagePersisted: true }, 't2');
  await sleep(60);
  assert.equal(r.failed.status, 500, 'the failure is on file …');
  // … but the room ends with the answer to that prompt, and the card is
  // rendered on waitingPrompt (transcript.js), so neither it nor Retry shows.
  r.messages = [
    { id: 'm1', role: 'user', content: 'work' },
    { id: 'a1', role: 'assistant', content: 'done — nothing left to ask' },
  ];
  assert.equal(chat.waitingPrompt(r), null, 'no Retry for work that is done');
});

test('a tool-only step is not an answer to the prompt', () => {
  const room = {
    messages: [
      { id: 'm1', role: 'user', content: 'work' },
      { id: 'a1', role: 'assistant', content: '', parts: [{ kind: 'tool_call', toolCallId: 'c1' }] },
    ],
  };
  assert.equal(chat.waitingPrompt(room)?.id, 'm1', 'a step without text leaves the prompt unanswered');
  assert.equal(chat.waitingPrompt({ messages: [{ id: 'n1', role: 'user', userType: 'system', content: 'note' }] }), null);
  assert.equal(chat.waitingPrompt({ messages: [] }), null);
});

// The room's cross-thread queue: a notice, not a turn frame. It updates the
// line that shows it without touching the transcript (no store flush), and a
// stop tool's own answer feeds the same number rather than a second one.
test('a queued notice updates the room without re-rendering the transcript', async () => {
  serveThread('c_1');
  chat.navigate('#/c/c_1');
  await sleep(40);
  const r = state.rooms.c_1;
  let flushes = 0;
  const off = subscribe(() => { flushes += 1; });
  const messages = r.messages;

  ws.serve({ type: 'conversation.queued', conversationId: 'c_1', payload: { waiting: 3 } });
  await sleep(20);
  assert.equal(queuedFor('c_1'), 3, 'the line reads the count');
  assert.equal(flushes, 0, 'nothing in the store re-rendered');
  assert.equal(r.messages, messages, 'the transcript list is the same object');

  ws.serve({ type: 'conversation.queued', conversationId: 'c_1', payload: { waiting: 1 } });
  await sleep(20);
  assert.equal(queuedFor('c_1'), 1, 'the last notice wins');

  ws.serve({ type: 'conversation.queued', conversationId: 'c_1', payload: { waiting: 0 } });
  await sleep(20);
  assert.equal(queuedFor('c_1'), 0, 'drained: the line goes away');
  assert.equal(flushes, 0);
  off();
});

test('a stop tool answer names the same count, and the newer number wins', async () => {
  const { r } = await openBusyThread();
  ws.serve({ type: 'turn.tool_call', conversationId: 'c_1', turnId: 't2', seq: 10, payload: { callId: 'stop1', name: 'conversation_stop' } });
  ws.serve({ type: 'turn.tool_input', conversationId: 'c_1', turnId: 't2', seq: 11, payload: { callId: 'stop1', text: '{"thread":"c_2"}' } });
  ws.serve({ type: 'turn.tool_output', conversationId: 'c_1', turnId: 't2', seq: 12, payload: { callId: 'stop1', text: '{"threadId":"c_2","stopped":true,"waiting":2}' } });
  ws.serve({ type: 'turn.tool_result', conversationId: 'c_1', turnId: 't2', seq: 13, payload: { callId: 'stop1' } });
  await sleep(20);
  assert.equal(queuedFor('c_2'), 2, 'the stopped room keeps its queue count');

  // the room's own notice is the newer word: one number, no second line
  ws.serve({ type: 'conversation.queued', conversationId: 'c_2', payload: { waiting: 0 } });
  await sleep(20);
  assert.equal(queuedFor('c_2'), 0);
  assert.ok(r.live, 'the calling turn is untouched');
});
