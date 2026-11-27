// An agent room without a stored model still runs a model: the one the
// agent marks current. The composer button and the picker's check have to
// name that model, not borrow the agent's name — an agent's name where a
// model name belongs is how an owner stops being able to tell what runs.
import { calls, mockApi, sleep, sockets } from './env.mjs';
import { before, beforeEach, test } from 'node:test';
import assert from 'node:assert/strict';
import { state } from '../lib/store.js';
import * as chat from '../lib/chat.js';
import { agentModelLabel } from '../components/pickers.js';

let ws;

const AGENT = { id: 'devin', name: 'Devin' };
const LIST = [
  { id: 'swe-2-1-medium', name: 'SWE-2.1', current: false },
  { id: 'swe-2-high', name: 'SWE-2', current: true },
];
const storeWith = (list, status = 'ready') => ({
  agents: { items: [AGENT] },
  agentModels: { devin: { status, list, error: null } },
});

function serveThread(id, { info = {} } = {}, extra = {}) {
  mockApi({
    ...extra,
    [`GET /api/conversations/${id}/messages`]: { messages: [], effectiveStart: 0, total: 0 },
    [`GET /api/conversations/${id}`]: { id, title: 'Agent room', workspace: '', ...info },
    [`PATCH /api/conversations/${id}`]: (call) => ({ id, title: 'Agent room', workspace: '', runtime: call.body.agentId ? 'agent' : 'harness', ...call.body }),
    'GET /api/context': { estimate: 0, total: 0 },
  });
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
  state.route = { view: 'home', id: '' };
  ws.sent.length = 0;
  chat.navigate('#/');
});

test('a bound agent with no stored model names the model the agent marks current', () => {
  const s = storeWith(LIST);
  assert.equal(agentModelLabel(s, 'devin', ''), 'SWE-2', 'the running model, not the agent name');
  assert.equal(agentModelLabel(s, 'devin', 'swe-2-1-medium'), 'SWE-2.1', 'a pick still wins over current');
});

test('until the list can name a model the button says agent and state, not agent as model', () => {
  assert.equal(agentModelLabel(storeWith([]), 'devin', ''), 'Devin · default', 'a list that came back empty');
  assert.equal(agentModelLabel(storeWith([], 'loading'), 'devin', ''), 'Devin · default', 'a list still coming');
  assert.equal(agentModelLabel(storeWith([], 'error'), 'devin', ''), 'Devin · default', 'a list that failed');
  // a reload keeps the last list while it asks again: the name does not flicker away
  assert.equal(agentModelLabel(storeWith(LIST, 'loading'), 'devin', ''), 'SWE-2');
  // a full list without a current flag cannot name the model either
  const unmarked = { agents: { items: [AGENT] }, agentModels: { devin: { status: 'ready', list: LIST.map((m) => ({ ...m, current: false })), error: null } } };
  assert.equal(agentModelLabel(unmarked, 'devin', ''), 'Devin · default');
});

test('a model picked from the list is still written to the room', async () => {
  serveThread('c_am', { info: { runtime: 'agent', agentId: 'devin' } });
  chat.navigate('#/c/c_am');
  await sleep(40);
  const r = state.rooms.c_am;
  calls.length = 0;

  chat.setSelection(r, { runtime: 'agent', agentId: 'devin', providerId: '', modelId: 'swe-2-1-medium', effort: 'default', upstream: '' });
  await sleep(40);
  const patches = calls.filter((c) => c.method === 'PATCH');
  assert.equal(patches.length, 1);
  assert.deepEqual(patches[0].body, { agentId: 'devin', modelId: 'swe-2-1-medium' }, 'the pick reaches the room binding unchanged');
  assert.equal(chat.selection(r).modelId, 'swe-2-1-medium');
});
