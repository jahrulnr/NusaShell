// The model picker reads a catalog's status. unknown is an answer: it shows
// the reason and the default, and it is not the empty-catalog row. known
// with data: [] is that empty catalog. A body that omits status is not one.
import { mockApi, sleep, sockets } from './env.mjs';
import { installDOM, doc } from './dom.mjs';

installDOM();
import { before, beforeEach, test } from 'node:test';
import assert from 'node:assert/strict';
import { html, render } from '/vendor/preact/htm-preact.mjs';
import { state } from '../lib/store.js';
import * as chat from '../lib/chat.js';
import { ModelPicker } from '../components/pickers.js';

let ws;

before(() => {
  mockApi();
  chat.start();
  ws = sockets[0];
  ws.open();
});

beforeEach(() => {
  state.rooms = {};
  state.toasts = [];
  state.agentModels = {};
  state.agents = { items: [], status: 'idle', error: null };
  state.providers = { items: [], status: 'idle', error: null };
  state.route = { view: 'home', id: '' };
  ws.sent.length = 0;
  chat.navigate('#/');
});

const openPicker = async (catalog) => {
  mockApi({
    'GET /api/conversations/c_cat/messages': { messages: [], effectiveStart: 0, total: 0 },
    'GET /api/conversations/c_cat': { id: 'c_cat', title: 'Room', workspace: '', agentId: 'devin', modelId: '' },
    'GET /api/conversations/c_cat/agent-options': { data: [], status: 'empty' },
    'GET /api/agents/devin/models': catalog,
    'GET /api/agents/devin/options': { data: [], status: 'empty' },
    'GET /api/agents': { data: [{ id: 'devin', name: 'Devin' }] },
    'GET /api/providers': { data: [] },
    'GET /api/context': { estimate: 0, total: 0 },
    'GET /api/approval-rules': { data: [] },
  });
  chat.navigate('#/c/c_cat');
  await sleep(40);
  const room = state.rooms.c_cat;
  await chat.loadAgents();
  await chat.loadAgentModels('devin', true);
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  render(html`<${ModelPicker} room=${room} />`, host);
  host.querySelector('button[aria-haspopup="dialog"]').click();
  await sleep(30);
  const dialog = doc.querySelector('[role="dialog"]');
  return { host, dialog, text: dialog ? dialog.textContent : '' };
};

const closePicker = (host) => {
  render(null, host);
  host.remove();
};

test('an unknown catalog shows the reason and the default, not an empty list', async () => {
  const { host, dialog, text } = await openPicker({
    status: 'unknown',
    source: 'cache',
    data: [],
    default: { id: 'grok-4.7', by: 'agent' },
    reason: 'the agent reported no model list',
  });
  try {
    assert.ok(dialog, 'the panel opened');
    const stored = state.agentModels.devin;
    assert.equal(stored.report, 'unknown');
    assert.deepEqual(stored.list, [], 'unknown stores no rows');
    assert.equal(/no models/i.test(text), false, 'unknown rendered as an empty list');
    assert.equal(text.includes('This agent lists no models'), false);
    assert.equal(text.includes('unknown'), true);
    assert.equal(text.includes('grok-4.7'), true);
    assert.equal(text.includes('the agent reported no model list'), true);
    assert.equal(text.includes('default: grok-4.7 (agent)'), true);
  } finally {
    closePicker(host);
  }
});

test('a known empty catalog stays the empty list', async () => {
  const { host, text } = await openPicker({ status: 'known', source: 'cache', data: [] });
  try {
    assert.equal(state.agentModels.devin.report, 'known');
    assert.deepEqual(state.agentModels.devin.list, []);
    assert.equal(text.includes('This agent lists no models'), true);
    assert.equal(text.includes('unknown'), false);
  } finally {
    closePicker(host);
  }
});

test('a body with no status is not an empty catalog', async () => {
  const { host, text } = await openPicker({ source: 'cache' });
  try {
    assert.equal(state.agentModels.devin.report, 'unknown');
    assert.equal(/no models/i.test(text), false, 'a body with no status rendered as an empty list');
    assert.equal(text.includes('unknown'), true);
    assert.equal(text.includes('default: unset'), true);
  } finally {
    closePicker(host);
  }
});

test('a list without status still shows its rows', async () => {
  const { host, text } = await openPicker({
    data: [{ id: 'swe-2', name: 'SWE-2', current: true }],
  });
  try {
    assert.equal(state.agentModels.devin.report, 'known');
    assert.equal(text.includes('SWE-2'), true);
    assert.equal(/no models/i.test(text), false);
    assert.equal(text.includes('unknown'), false);
  } finally {
    closePicker(host);
  }
});
