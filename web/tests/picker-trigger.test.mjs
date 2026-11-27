// G10 on the model trigger. A keyboard click (detail 0) toggles the panel
// from the state now: closed opens, open closes. A pointer click uses the
// state recorded at pointerdown, so a close that lands between pointerdown
// and click — light dismiss — is not undone.
import { mockApi, sleep, sockets } from './env.mjs';
import { installDOM, doc, Ev } from './dom.mjs';

installDOM();
import { before, beforeEach, test } from 'node:test';
import assert from 'node:assert/strict';
import { html, render } from '/vendor/preact/htm-preact.mjs';
import { state } from '../lib/store.js';
import * as chat from '../lib/chat.js';
import { ModelPicker } from '../components/pickers.js';

let ws;

const room = () => ({
  id: 'c_sp',
  sel: { runtime: 'harness', providerId: 'or', modelId: 'alpha', effort: 'default', upstream: '', workspace: '' },
  info: { runtime: 'harness', providerId: 'or', modelId: 'alpha' },
  agentOptions: { status: 'idle', list: [], error: null, at: 0, key: '', failures: 0, toastId: null },
});

before(() => {
  mockApi({
    'GET /api/providers': { data: [{ id: 'or', name: 'OpenRouter', models: [{ id: 'alpha', name: 'Alpha' }] }] },
    'GET /api/agents': { data: [] },
    'GET /api/context': { estimate: 0, total: 0 },
    'GET /api/approval-rules': { data: [] },
  });
  chat.start();
  ws = sockets[0];
  ws.open();
});

beforeEach(() => {
  state.rooms = {};
  state.toasts = [];
  state.agentModels = {};
  state.agents = { items: [], status: 'ready', error: null };
  state.providers = { items: [{ id: 'or', name: 'OpenRouter', models: [{ id: 'alpha', name: 'Alpha' }] }], status: 'ready', error: null };
  state.route = { view: 'home', id: '' };
  ws.sent.length = 0;
  chat.navigate('#/');
});

const fire = (el, type, props) => {
  const e = new Ev(type, { bubbles: true });
  Object.assign(e, props);
  el.dispatchEvent(e);
  return e;
};

const mount = () => {
  const r = room();
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  render(html`<${ModelPicker} room=${r} />`, host);
  const trigger = host.querySelector('button[aria-haspopup="dialog"]');
  const open = () => !!doc.querySelector('[role="dialog"]');
  return { host, trigger, open };
};

test('a keyboard click uses the current state', async () => {
  const { host, trigger, open } = mount();
  try {
    assert.equal(open(), false);
    fire(trigger, 'click', { detail: 0 });
    await sleep(20);
    assert.equal(open(), true, 'Space or Enter on a closed trigger opens');
    fire(trigger, 'click', { detail: 0 });
    await sleep(20);
    assert.equal(open(), false, 'Space or Enter on an open trigger closes');
  } finally {
    render(null, host);
    host.remove();
  }
});

test('a pointer click does not reopen after a light dismiss', async () => {
  const { host, trigger, open } = mount();
  try {
    fire(trigger, 'click', { detail: 0 });
    await sleep(20);
    assert.equal(open(), true);
    fire(trigger, 'PointerDown', {});
    fire(doc, 'keydown', { key: 'Escape' });
    await sleep(20);
    assert.equal(open(), false, 'the panel closed before the click');
    fire(trigger, 'click', { detail: 1 });
    await sleep(20);
    assert.equal(open(), false, 'the click after light dismiss opened the panel again');
    fire(trigger, 'click', { detail: 0 });
    await sleep(20);
    assert.equal(open(), true);
    fire(trigger, 'PointerDown', {});
    fire(doc, 'keydown', { key: 'Escape' });
    await sleep(20);
    assert.equal(open(), false);
    fire(trigger, 'click', { detail: 0 });
    await sleep(20);
    assert.equal(open(), true, 'a keyboard click after light dismiss uses the closed state and opens');
  } finally {
    render(null, host);
    host.remove();
  }
});
