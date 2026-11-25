// The popup-class invariant, with the model picker as its reproducer: an
// action inside a panel must not cost the panel itself — same node, same
// scroll, focus still inside, and no structural removal observed anywhere
// in it. The removal observer is what gives this teeth: before the B-062
// fix, picking a model reset the room's session options and the section's
// content was destroyed and rebuilt across the network round-trip — the
// mutation proof for this test is exactly that revert, and it goes red
// naming the destroyed DIVs.
import { mockApi, sleep, sockets } from './env.mjs';
import { installDOM, doc, MutationObserver } from './dom.mjs';
installDOM();
import { before, beforeEach, test } from 'node:test';
import assert from 'node:assert/strict';
import { html, render } from '/vendor/preact/htm-preact.mjs';
import { state } from '../lib/store.js';
import * as chat from '../lib/chat.js';
import { ModelPicker } from '../components/pickers.js';

let ws;
const OPTS = [];

before(() => {
  mockApi();
  chat.start();
  ws = sockets[0];
  ws.open();
});

beforeEach(() => {
  state.rooms = {};
  state.route = { view: 'home', id: '' };
  ws.sent.length = 0;
  doc.body.textContent = '';
  chat.navigate('#/');
});

test('a model pick inside the panel keeps the panel, its scroll, its focus, and every control', async () => {
  mockApi({
    // the answer comes late on purpose: with an instant mock the loading
    // states collapse into one render and a reset leaves no observable trace —
    // the real bug was only ever visible across a network round-trip
    'GET /api/conversations/c_inv/agent-options': async () => { await sleep(40); return { data: OPTS, status: 'empty' }; },

    'PUT /api/conversations/c_inv/agent-options': { data: OPTS, status: 'ready' },
    [`PATCH /api/conversations/c_inv`]: (call) => ({ id: 'c_inv', title: 'Room', workspace: '', ...call.body }),
    'GET /api/conversations/c_inv/messages': { messages: [], effectiveStart: 0, total: 0 },
    'GET /api/conversations/c_inv': { id: 'c_inv', title: 'Room', workspace: '', agentId: 'devin', modelId: 'adaptive' },
    'GET /api/approval-rules': { data: [] },
    // longer prefixes first: mockApi matches prefixes, so the agent's own
    // routes must come before the bare list route or it swallows them
    'GET /api/agents/devin/models': { data: [{ id: 'adaptive', name: 'Adaptive' }, { id: 'swe-2', name: 'SWE-2' }] },
    'GET /api/agents/devin/options': { data: OPTS, status: 'empty' },
    'GET /api/agents': { data: [{ id: 'devin', name: 'Devin' }] },
    'GET /api/providers': { data: [] },
    'GET /api/context': { estimate: 0, total: 0 },
  });
  chat.navigate('#/c/c_inv');
  await sleep(50);
  const r = state.rooms.c_inv;
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  render(html`<${ModelPicker} room=${r} />`, host);
  await Promise.all([chat.loadAgents(), chat.loadProviders()]);
  await sleep(20);

  // open the panel
  const btn = host.querySelector('button[aria-haspopup="dialog"]');
  assert.ok(btn, 'the picker button rendered');
  btn.click();
  await sleep(120);
  const dialog = doc.querySelector('[role="dialog"]');
  assert.ok(dialog, 'the panel is open');

  // focus inside the panel, scroll it, then act inside it
  const input = dialog.querySelector('input');
  if (input) input.focus();
  dialog.scrollTop = 37;
  const removals = [];
  const mo = new MutationObserver((recs) => {
    for (const rec of recs) for (const n of rec.removedNodes) removals.push(n.tagName || 'text');
  });
  mo.observe(dialog, { childList: true, subtree: true, attributes: true });
  const rows = dialog.querySelectorAll('button.w-full');
  const target = rows.find((b) => b.textContent.trim().startsWith('SWE-2')) || rows[1];
  assert.ok(target, 'a model row to pick');
  target.click();
  await sleep(80);
  mo.disconnect();

  assert.equal(doc.contains(dialog), true, 'the panel never left the document');
  assert.equal(doc.querySelector('[role="dialog"]'), dialog, 'same node, not a remount');
  assert.equal(dialog.scrollTop, 37, 'scroll did not jump');
  assert.equal(dialog.contains(doc.activeElement), true, 'focus is still inside the panel');
  // structural removals are the violation; a swapped SVG is the row's
  // checkmark moving to the pick — content, not the panel rebuilding
  const structural = removals.filter((t) => t !== 'text' && t !== 'SVG');
  assert.equal(structural.length, 0, `no structure was removed, but the observer saw: ${removals.slice(0, 6).join(', ')}`);
  render(null, host);
});
