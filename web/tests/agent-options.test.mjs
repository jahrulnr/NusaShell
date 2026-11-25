// Agent session options: one list per room, one writer, two controls that read
// it — the composer's chip and the picker's Session Mode. These tests hold the
// controller to that shape, because the bug being fixed was two copies drifting
// apart, not a single control misbehaving.
import { calls, mockApi, sleep, sockets } from './env.mjs';
import { before, beforeEach, test } from 'node:test';
import assert from 'node:assert/strict';
import { state, subscribe } from '../lib/store.js';
import * as chat from '../lib/chat.js';

let ws;

const MODE = (current) => ({
  id: 'mode', name: 'Session Mode', category: 'mode', type: 'select', current,
  choices: [{ value: 'plan', name: 'Plan' }, { value: 'code', name: 'Code' }, { value: 'bypass', name: 'Bypass Permissions' }],
});
const LIST = (current = 'code') => [MODE(current), { id: 'thinking', name: 'Thinking', type: 'boolean', current: 'false' }];

// serveRoom answers the reads a room view makes, plus its agent options. The
// option routes answer with the list a write produced, so the controller can be
// checked against what the server would really say next.
function serveRoom(id, { current = 'code', put = null, info = {} } = {}) {
  let mode = current;
  // mockApi matches a path prefix, so the longer paths come first: the option
  // routes live under the conversation route, not beside it.
  mockApi({
    [`GET /api/conversations/${id}/agent-options`]: () => [200, { data: LIST(mode) }],
    [`PUT /api/conversations/${id}/agent-options`]: (call) => {
      if (put) return put(call, mode);
      mode = call.body.value;
      return [200, { data: LIST(mode) }];
    },
    [`GET /api/conversations/${id}/messages`]: { messages: [{ id: 'm1', role: 'user', content: 'hi' }], effectiveStart: 0, total: 1 },
    [`GET /api/conversations/${id}`]: { id, title: 'Agent room', workspace: '', agentId: 'claude', ...info },
    'GET /api/approval-rules': { data: [] },
  });
}

const optionReads = (id) => calls.filter((c) => c.method === 'GET' && c.url.includes(`/${id}/agent-options`)).length;
const optionWrites = (id) => calls.filter((c) => c.method === 'PUT' && c.url.includes(`/${id}/agent-options`)).length;

before(() => {
  mockApi();
  chat.start();
  ws = sockets[0];
  ws.open();
});

beforeEach(() => {
  state.rooms = {};
  state.toasts = [];
  state.route = { view: 'home', id: '' };
  ws.sent.length = 0;
  chat.navigate('#/');
});

test('one write changes the list both controls read, and both name the same mode', async () => {
  serveRoom('c_ao1');
  chat.navigate('#/c/c_ao1');
  await sleep(40);
  const r = state.rooms.c_ao1;
  assert.equal(chat.sessionModeName(r), 'Code');
  calls.length = 0;

  const outcome = await chat.setSessionOption(r, 'mode', 'bypass');
  assert.equal(outcome, 'saved');
  assert.equal(optionWrites('c_ao1'), 1);

  // the room's list is what both controls read: the chip names it from
  // sessionModeName, the picker reads sessionOptionValue — same value, same list
  const mode = r.agentOptions.list.find((o) => o.id === 'mode');
  assert.equal(mode.current, 'bypass');
  assert.equal(chat.sessionOptionValue(r, mode), 'bypass');
  assert.equal(chat.sessionModeName(r), 'Bypass Permissions');
  assert.equal(r.agentOptions.list.length, 2, 'the write answers with the whole list');
});

test('a refused write keeps the value the server stored, and says why once', async () => {
  serveRoom('c_ao2', {
    current: 'code',
    put: () => [502, { code: 'upstream_error', detail: 'the agent did not answer' }],
  });
  chat.navigate('#/c/c_ao2');
  await sleep(40);
  const r = state.rooms.c_ao2;
  calls.length = 0;
  state.toasts = [];

  const outcome = await chat.setSessionOption(r, 'mode', 'bypass');
  assert.equal(outcome, 'unknown', 'a 502 does not say whether it landed');
  assert.equal(state.toasts.length, 1, 'one reason, said once');
  assert.match(state.toasts[0].message, /Could not confirm Session Mode/);
  assert.ok(optionReads('c_ao2') >= 1, 'the list was read again from the server');
  assert.equal(chat.sessionModeName(r), 'Code', 'the stored value is what both controls show');

  // a refusal the server states is the other branch: it says why, and still
  // shows what is stored
  serveRoom('c_ao2', { current: 'code', put: () => [409, { code: 'conflict', detail: 'that mode is not offered now' }] });
  await chat.setSessionOption(r, 'mode', 'bypass');
  assert.equal(chat.sessionModeName(r), 'Code');
});

test('a stale pick meets the 400: the server\'s reason reaches the owner and the vanished value leaves the list', async () => {
  // B-030: a tab whose list predates an agent change can still show a value
  // the session no longer offers. PUT answers 400 naming the option and the
  // value; the read that follows no longer offers it either.
  const stale = LIST('code');
  const fresh = [
    { ...MODE('code'), choices: MODE('code').choices.filter((c) => c.value !== 'bypass') },
    { id: 'thinking', name: 'Thinking', type: 'boolean', current: 'false' },
  ];
  let list = stale;
  mockApi({
    'GET /api/conversations/c_ao6/agent-options': () => [200, { data: list }],
    'PUT /api/conversations/c_ao6/agent-options': () => {
      list = fresh;
      return [400, { code: 'invalid_argument', detail: 'option "mode" does not offer "bypass"' }];
    },
    'GET /api/conversations/c_ao6/messages': { messages: [{ id: 'm1', role: 'user', content: 'hi' }], effectiveStart: 0, total: 1 },
    'GET /api/conversations/c_ao6': { id: 'c_ao6', title: 'Stale tab', workspace: '', agentId: 'claude' },
    'GET /api/approval-rules': { data: [] },
  });
  chat.navigate('#/c/c_ao6');
  await sleep(40);
  const r = state.rooms.c_ao6;
  calls.length = 0;
  state.toasts = [];

  const outcome = await chat.setSessionOption(r, 'mode', 'bypass');
  assert.equal(outcome, 'refused');
  assert.equal(state.toasts.length, 1, 'one reason, said once');
  assert.ok(
    state.toasts[0].message.includes('option "mode" does not offer "bypass"'),
    'the server\'s words reach the owner, not a generic sentence',
  );
  assert.ok(optionReads('c_ao6') >= 1, 'the list was read again from the server');
  assert.equal(chat.sessionModeName(r), 'Code', 'the stored value is what both controls show');
  const mode = r.agentOptions.list.find((o) => o.id === 'mode');
  assert.ok(!mode.choices.some((c) => c.value === 'bypass'), 'the vanished value is out of the list the controls draw');
});

test('a thread that does not exist yet stages the pick and carries it on its first turn', async () => {
  mockApi({
    'GET /api/agents/claude/options': { data: LIST('code') },
    'GET /api/providers': { items: [], page: { total: 0 } },
    'GET /api/agents': { items: [{ id: 'claude', name: 'Claude' }], page: { total: 1 } },
  });
  const home = chat.freshRoom();
  chat.setSelection(home, { agentId: 'claude' });
  await chat.loadAgentOptions(home);
  assert.equal(home.agentOptions.list.length, 2, 'the agent probe stands in for the room');

  calls.length = 0;
  const outcome = await chat.setSessionOption(home, 'mode', 'plan');
  assert.equal(outcome, 'staged');
  assert.equal(calls.filter((c) => c.method === 'PUT').length, 0, 'nothing to write on the server yet');
  assert.equal(home.sel.agentOptions.mode, 'plan');

  // the first turn carries the staged picks in the wire's ordered form
  chat.submit(home, 'hello');
  const sent = ws.sent.filter((c) => c.type === 'turn.send').at(-1);
  assert.deepEqual(sent.payload.agentOptions, [{ id: 'mode', value: 'plan' }]);
});

// The stored pick list changed wire form: an ordered [{id, value}] array is
// what the server writes now, while the {id: value} object still decodes.
// A turn carries the array in the order the picker draws, and both forms
// read into the same values for the controls.
test('staged picks ride the first turn as the ordered array the picker shows', async () => {
  mockApi({
    'GET /api/agents/claude/options': { data: LIST('code') },
    'GET /api/providers': { items: [], page: { total: 0 } },
    'GET /api/agents': { items: [{ id: 'claude', name: 'Claude' }], page: { total: 1 } },
  });
  const home = chat.freshRoom();
  chat.setSelection(home, { agentId: 'claude' });
  await chat.loadAgentOptions(home);

  // picked in reverse of the display order on purpose: the map's own order
  // is thinking-then-mode, the picker's list is mode-then-thinking
  await chat.setSessionOption(home, 'thinking', 'true');
  await chat.setSessionOption(home, 'mode', 'plan');

  chat.submit(home, 'hello');
  const sent = ws.sent.filter((c) => c.type === 'turn.send').at(-1);
  assert.deepEqual(sent.payload.agentOptions, [
    { id: 'mode', value: 'plan' },
    { id: 'thinking', value: 'true' },
  ], 'the body is an array in the order the picker draws, not the order clicked');
});

test('a stored pick list reads the same in array form and in object form', async () => {
  mockApi({
    'GET /api/agents/claude/options': { data: LIST('code') },
    'GET /api/providers': { items: [], page: { total: 0 } },
    'GET /api/agents': { items: [{ id: 'claude', name: 'Claude' }], page: { total: 1 } },
  });
  const home = chat.freshRoom();
  chat.setSelection(home, { agentId: 'claude' });
  await chat.loadAgentOptions(home);
  const mode = home.agentOptions.list.find((o) => o.id === 'mode');

  // the array the server writes now
  chat.setSelection(home, { agentOptions: [{ id: 'mode', value: 'plan' }] });
  assert.equal(chat.sessionOptionValue(home, mode), 'plan');
  assert.equal(chat.sessionModeName(home), 'Plan');

  // the object older state carries — a tab staged before this change, for one
  chat.setSelection(home, { agentOptions: { mode: 'bypass' } });
  assert.equal(chat.sessionOptionValue(home, mode), 'bypass');
  assert.equal(chat.sessionModeName(home), 'Bypass Permissions');
});

test('nothing is written while a turn runs, and the controls say when it applies', async () => {
  serveRoom('c_ao4');
  chat.navigate('#/c/c_ao4');
  await sleep(40);
  const r = state.rooms.c_ao4;
  r.settling = true; // a turn owns this room right now
  calls.length = 0;

  const outcome = await chat.setSessionOption(r, 'mode', 'bypass');
  assert.equal(outcome, 'busy');
  assert.equal(optionWrites('c_ao4'), 0, 'no write is sent while the room is busy');
  assert.equal(chat.sessionModeName(r), 'Code');
  r.settling = false;
});

test('a change announced by the server is read once, not once per control', async () => {
  serveRoom('c_ao5');
  chat.navigate('#/c/c_ao5');
  await sleep(40);
  calls.length = 0;
  let flushes = 0;
  const off = subscribe(() => { flushes += 1; });

  ws.serve({ type: 'conversation.updated', conversationId: 'c_ao5', payload: { state: 'option' } });
  await sleep(30);
  assert.equal(optionReads('c_ao5'), 1, 'one read for the change, however many controls show it');
  assert.ok(flushes > 0, 'and the list the controls read was announced');
  off();
});
