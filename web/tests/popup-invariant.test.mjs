// The popup-class invariant, with the model picker and the agent chip as
// reproducers. An action inside a panel must not cost the panel itself: same
// node, same scroll, focus still inside, one scroll owner, and no structural
// removal observed anywhere in it. A panel whose anchor hides closes with it and
// does not come back open by itself. The removal observer is what gives this
// teeth: before the B-062 fix, picking a model reset the room's session options
// and the section's content was destroyed and rebuilt across the network
// round-trip — the mutation proof for this test is exactly that revert, and it
// goes red naming the destroyed DIVs.
//
// Every wait is bounded and every case cleans up in a finally, so a broken
// contract fails by its assertion, naming what broke, and never leaves the next
// case a mounted panel to trip over.
//
// A scroll position written from a test proves nothing about overflow: a
// programmatic scrollTop lands even on overflow:hidden. The scroll contract is
// read from the overflow the panel declares, and the browser check stays the
// proof for the rendered CSS.
import { calls, mockApi, sleep, sockets } from './env.mjs';
import { installDOM, doc, MutationObserver, Ev } from './dom.mjs';
installDOM();
import { before, beforeEach, test } from 'node:test';
import assert from 'node:assert/strict';
import { html, render } from '/vendor/preact/htm-preact.mjs';
import { state } from '../lib/store.js';
import * as chat from '../lib/chat.js';
import { ModelPicker } from '../components/pickers.js';
import { AgentOptions, AgentOptionsNotice } from '../components/agentbar.js';
import { inLayout } from './viewport.mjs';

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

// until waits for a change the app makes on its own. Past its deadline it fails
// with the contract it was waiting for: a missing change is an assertion, not a
// test that runs out of time.
const until = async (ok, message, ms = 1000) => {
  const end = Date.now() + ms;
  while (!ok()) {
    if (Date.now() > end) assert.fail(message);
    await sleep(10);
  }
};

// computedOverflowY is the overflow a Tailwind utility class declares on an
// element. The shim has no stylesheet, so this reads the class contract, not
// the compiled CSS: a regression of the panel to overflow-auto fails here
// without a browser.
const OVERFLOW_UTILS = { 'overflow-hidden': 'hidden', 'overflow-auto': 'auto', 'overflow-y-auto': 'auto' };
const computedOverflowY = (el) => (el.className || '').split(/\s+/).map((c) => OVERFLOW_UTILS[c]).find(Boolean) ?? 'visible';
const scrollOwners = (dialog) => [dialog, ...dialog.querySelectorAll('*')].filter((n) => n !== dialog && /(^| )overflow-(y-)?auto( |$)/.test(n.className || ''));
const listScroller = (dialog) => dialog.querySelector('[data-scroll="list"]');
const controlBand = (dialog) => dialog.querySelector('[data-scroll="controls"]');

// serveModelRoom answers everything the model picker's panel reads for c_inv.
// The agent-options answer comes late on purpose: with an instant mock the
// loading states collapse into one render and a reset leaves no observable
// trace — the real bug was only ever visible across a network round-trip.
const serveModelRoom = (agentOptions = async () => { await sleep(40); return { data: OPTS, status: 'empty' }; }) => mockApi({
  'GET /api/conversations/c_inv/agent-options': agentOptions,

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

// openModelPanel navigates to c_inv, mounts the picker, and opens its panel.
const openModelPanel = async () => {
  chat.navigate('#/c/c_inv');
  await sleep(50);
  const r = state.rooms.c_inv;
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  render(html`<${ModelPicker} room=${r} />`, host);
  await Promise.all([chat.loadAgents(), chat.loadProviders()]);
  await sleep(20);
  host.querySelector('button[aria-haspopup="dialog"]').click();
  await sleep(120);
  return { r, host, dialog: doc.querySelector('[role="dialog"]') };
};

const modelPickKeepsPanel = async () => {
  serveModelRoom();
  chat.navigate('#/c/c_inv');
  await sleep(50);
  const r = state.rooms.c_inv;
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  let mo = null;
  try {
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
    // The panel is overflow:hidden. The model list scrolls, and the session
    // options sit in a second band so they do not leave with the list. Search
    // and the Providers / Agents footer stay outside both.
    assert.equal(computedOverflowY(dialog), 'hidden', 'the panel declares overflow:hidden, so a regression to auto is caught');
    const owners = scrollOwners(dialog);
    assert.equal(owners.length, 2, `the list and the controls band scroll, but ${owners.length} scroll inside the panel`);
    const body = listScroller(dialog);
    const band = controlBand(dialog);
    assert.ok(body && band, 'the model rows and the controls are separate scroll owners');
    assert.equal(owners.includes(body) && owners.includes(band), true);
    assert.equal(body.textContent.includes('no options') || body.textContent.includes('Asking the agent'), false);
    assert.equal(band.textContent.includes('no options') || band.textContent.includes('Asking the agent'), true);
    const input = dialog.querySelector('input');
    assert.ok(input && !body.contains(input) && !band.contains(input), 'the search field is chrome: it stays out of both scrolls');
    const links = [...dialog.querySelectorAll('a')].filter((a) => /^(Providers|Agents)$/.test(a.textContent.trim()));
    assert.ok(links.length >= 2 && links.every((a) => !body.contains(a) && !band.contains(a)), 'the Providers / Agents footer is chrome: it stays out of both scrolls');

    // focus inside the panel, scroll the body, then act inside it
    if (input) input.focus();
    body.scrollTop = 37;
    const removals = [];
    mo = new MutationObserver((recs) => {
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
    // The pick reloaded the room's data behind the panel; a refresh like that
    // must not move the scroll or steal the focus.
    assert.equal(body.scrollTop, 37, 'scroll did not jump');
    assert.equal(dialog.contains(doc.activeElement), true, 'focus is still inside the panel');
    // structural removals are the violation; a swapped SVG is the row's
    // checkmark moving to the pick — content, not the panel rebuilding
    const structural = removals.filter((t) => t !== 'text' && t !== 'SVG');
    assert.equal(structural.length, 0, `no structure was removed, but the observer saw: ${removals.slice(0, 6).join(', ')}`);
  } finally {
    mo?.disconnect();
    render(null, host);
    host.remove();
  }
};

// An anchor that hides while its panel is open takes the panel with it, and the
// panel does not come back open when the anchor shows again.
const agentChipFlow = async () => {
  const MODE = { id: 'thinking', name: 'Thinking', type: 'boolean', current: 'false' };
  let answer = { data: [MODE] };
  mockApi({
    'GET /api/conversations/c_chip/agent-options': () => [200, answer],
    'GET /api/conversations/c_chip/messages': { messages: [], effectiveStart: 0, total: 0 },
    'GET /api/conversations/c_chip': { id: 'c_chip', title: 'Room', workspace: '', agentId: 'devin', modelId: 'adaptive' },
    'GET /api/approval-rules': { data: [] },
    'GET /api/context': { estimate: 0, total: 0 },
  });
  chat.navigate('#/c/c_chip');
  await sleep(50);
  const r = state.rooms.c_chip;
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  const chip = () => host.querySelector('button[aria-expanded]');
  const panel = () => doc.querySelector('[role="dialog"]');
  try {
    render(html`<${AgentOptions} room=${r} />`, host);
    await chat.loadAgentOptions(r, { force: true });
    await until(() => chip(), 'the chip shows while the agent offers an option');
    chip().click();
    await until(() => panel(), 'the chip opens its panel');
    answer = { data: [], status: 'ready' };
    await chat.loadAgentOptions(r, { force: true });
    await until(() => chip() === null, 'a definitive empty answer hides the chip');
    assert.equal(panel(), null, 'the panel goes with its chip');
    answer = { data: [MODE] };
    await chat.loadAgentOptions(r, { force: true });
    await until(() => chip(), 'the chip shows again once the agent offers an option');
    assert.equal(panel(), null, 'and the panel does not reopen by itself');
  } finally {
    render(null, host);
    host.remove();
  }
};

// The composer's panel says "Applies to the next turn" only while the record
// parks a pick. An idle pick that supersedes the parked one leaves the record
// with no pending, so the panel returns to its plain line and the chip names
// the running pick. The record is the only source for that line.
const agentHintFollowsRecord = async () => {
  // The mode is drawn as a toggle here: the shim cannot mount slim-select, and
  // the hint and the chip read the record, not the control drawn for it.
  const option = (current) => ({ id: 'mode', name: 'Session Mode', category: 'mode', type: 'boolean', current,
    choices: [{ value: 'plan', name: 'Plan' }, { value: 'code', name: 'Code' }, { value: 'bypass', name: 'Bypass Permissions' }] });
  let mode = 'code';
  let parked = { options: [{ id: 'mode', value: 'plan' }], acceptedAt: '2026-11-25T23:15:04Z' };
  mockApi({
    'GET /api/conversations/c_hint/agent-options': () => [200, { data: [option(mode)] }],
    'PUT /api/conversations/c_hint/agent-options': (call) => { mode = call.body.value; parked = null; return [200, { data: [option(mode)] }]; },
    'GET /api/conversations/c_hint/messages': { messages: [], effectiveStart: 0, total: 0 },
    'GET /api/conversations/c_hint': () => ({ id: 'c_hint', title: 'Room', workspace: '', agentId: 'devin', ...(parked ? { pending: parked } : {}) }),
    'GET /api/approval-rules': { data: [] },
    'GET /api/context': { estimate: 0, total: 0 },
  });
  chat.navigate('#/c/c_hint');
  await sleep(50);
  const r = state.rooms.c_hint;
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  const chip = () => host.querySelector('button[aria-expanded]');
  const panel = () => doc.querySelector('[role="dialog"]');
  try {
    render(html`<${AgentOptions} room=${r} />`, host);
    await chat.loadAgentOptions(r, { force: true });
    await until(() => chip(), 'the chip shows while the agent offers an option');
    assert.match(chip().textContent, /Plan/, 'the chip names the parked pick while it waits');
    chip().click();
    await until(() => panel(), 'the chip opens its panel');
    assert.match(panel().textContent, /Applies to the next turn/, 'the parked pick is said to apply next turn');

    assert.equal(await chat.setSessionOption(r, 'mode', 'bypass'), 'saved');
    await until(() => !/Applies to the next turn/.test(panel().textContent), 'the superseded pick must stop applying next turn');
    assert.match(panel().textContent, /Kept for this thread/, 'the panel returns to its plain line');
    assert.match(chip().textContent, /Bypass Permissions/, 'the chip names the running pick');
  } finally {
    render(null, host);
    host.remove();
  }
};

// The chip's panel gives one of three answers, and a failed read is never a bare
// red line. A read that failed with no list shows the failure with its Retry, and
// the footer that describes a list stays hidden. A refresh that failed after a good
// read keeps the list and says it is from the last read. A transient empty answer
// says so, and has no footer.
const agentPanelAnswers = async () => {
  const MODE = { id: 'thinking', name: 'Thinking', type: 'boolean', current: 'false' };
  const FAILED = /Session options can't be read right now\./;
  const STALE = /the values below are from the last read/;
  const FOOTER = /Kept for this thread/;
  const FAIL = [503, { code: 'unavailable', detail: 'the agent is not answering' }];
  let answer = FAIL;
  let reads = 0;
  mockApi({
    'GET /api/conversations/c_panel/agent-options': () => { reads++; return answer; },
    'GET /api/conversations/c_panel/messages': { messages: [], effectiveStart: 0, total: 0 },
    'GET /api/conversations/c_panel': { id: 'c_panel', title: 'Room', workspace: '', agentId: 'devin' },
    'GET /api/approval-rules': { data: [] },
    'GET /api/context': { estimate: 0, total: 0 },
  });
  chat.navigate('#/c/c_panel');
  await sleep(50);
  const r = state.rooms.c_panel;
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  const chip = () => host.querySelector('button[aria-expanded]');
  const panel = () => doc.querySelector('[role="dialog"]');
  const retry = () => [...(panel()?.querySelectorAll('button') || [])].find((b) => b.textContent.trim() === 'Retry');
  try {
    render(html`<${AgentOptions} room=${r} />`, host);
    await chat.loadAgentOptions(r, { force: true });
    await until(() => chip(), 'the chip stays while the options cannot be read, so the owner can retry');
    chip().click();
    await until(() => panel(), 'the chip opens its panel');

    // A failed read with no list: the failure, its Retry, and no footer.
    assert.match(panel().textContent, FAILED, 'the failure says what cannot be read');
    assert.ok(retry(), 'the failed read offers Retry');
    assert.doesNotMatch(panel().textContent, FOOTER, 'with no list, the footer that describes a list stays hidden');

    // Retry is a fresh read; a good answer replaces the failure with the list.
    const before = reads;
    answer = [200, { data: [MODE] }];
    retry().click();
    await until(() => reads > before, 'Retry reads the options again');
    await until(() => !FAILED.test(panel().textContent), 'a good read replaces the failure');
    assert.match(panel().textContent, /Thinking/, 'the list is back');
    assert.match(panel().textContent, FOOTER, 'with a list, the footer is back');

    // A refresh that fails after a good read keeps the list, and says so.
    answer = FAIL;
    await chat.loadAgentOptions(r, { force: true });
    await until(() => STALE.test(panel().textContent), 'a failed refresh says the list is from the last read');
    assert.match(panel().textContent, /Thinking/, 'the last read stays on screen');
    assert.match(panel().textContent, FOOTER, 'the footer still describes that list');
    assert.ok(retry(), 'the stale list offers Retry');

    // The agent's transient empty answer: its own line, no footer, no stale warning.
    answer = [200, { data: [], status: 'empty' }];
    await chat.loadAgentOptions(r, { force: true });
    await until(() => /reports no options right now/.test(panel().textContent), 'a transient empty answer says so');
    const refresh = () => [...(panel()?.querySelectorAll('button') || [])].find((b) => b.textContent.trim() === 'Refresh');
    assert.ok(refresh(), 'an empty answer offers Refresh, because nothing failed');
    assert.equal(retry(), undefined, 'an empty answer does not say Retry; Retry is for a failed read');
    assert.doesNotMatch(panel().textContent, FOOTER, 'an empty list has no footer');
    assert.doesNotMatch(panel().textContent, STALE, 'a good answer clears the stale warning');
  } finally {
    render(null, host);
    host.remove();
  }
};

// The surface registers in the commit that mounts it, not a frame later: a read
// already in flight can fail the moment the panel opens, and it must find the
// panel registered. Checked synchronously, with no wait, right after the mount.
const registersAtMount = async () => {
  mockApi({
    'GET /api/conversations/c_mount/agent-options': [200, { data: [{ id: 'thinking', name: 'Thinking', type: 'boolean', current: 'false' }] }],
    'GET /api/conversations/c_mount/messages': { messages: [], effectiveStart: 0, total: 0 },
    'GET /api/conversations/c_mount': { id: 'c_mount', title: 'Room', workspace: '', agentId: 'devin' },
    'GET /api/approval-rules': { data: [] },
    'GET /api/context': { estimate: 0, total: 0 },
  });
  chat.navigate('#/c/c_mount');
  await sleep(50);
  const r = state.rooms.c_mount;
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  try {
    render(html`<${AgentOptionsNotice} room=${r} />`, host);
    assert.equal(chat.optionsShownInline(r), true, 'registered by the time the mount returns');
  } finally {
    render(null, host);
    host.remove();
  }
  assert.equal(chat.optionsShownInline(r), false, 'unmounting releases the surface');
};

// An open panel is the inline surface for its room's options outage: the outage is
// said there, not in a toast, and a Retry that fails again adds to the count the
// panel shows. A closed panel releases the outage, so the next one is announced.
const outageInPanel = async () => {
  const MODE = { id: 'thinking', name: 'Thinking', type: 'boolean', current: 'false' };
  const FAIL = [503, { code: 'unavailable', detail: 'the agent is not answering' }];
  let answer = [200, { data: [MODE] }];
  let reads = 0;
  mockApi({
    'GET /api/conversations/c_outage/agent-options': () => { reads++; return answer; },
    'GET /api/conversations/c_outage/messages': { messages: [], effectiveStart: 0, total: 0 },
    'GET /api/conversations/c_outage': { id: 'c_outage', title: 'Room', workspace: '', agentId: 'devin' },
    'GET /api/approval-rules': { data: [] },
    'GET /api/context': { estimate: 0, total: 0 },
  });
  chat.navigate('#/c/c_outage');
  await sleep(50);
  const r = state.rooms.c_outage;
  state.toasts = [];
  const notices = () => state.toasts.filter((t) => t.message.startsWith('Could not refresh agent options'));
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  const chip = () => host.querySelector('button[aria-expanded]');
  const panel = () => doc.querySelector('[role="dialog"]');
  const retry = () => [...(panel()?.querySelectorAll('button') || [])].find((b) => b.textContent.trim() === 'Retry');
  try {
    render(html`<${AgentOptions} room=${r} />`, host);
    await chat.loadAgentOptions(r, { force: true });
    await until(() => chip(), 'the chip shows');
    chip().click();
    await until(() => panel(), 'the chip opens its panel');
    await until(() => chat.optionsShownInline(r), 'the open panel registers as the inline surface');

    // The outage starts while the panel is open: the panel says so, no toast does.
    answer = FAIL;
    await chat.loadAgentOptions(r, { force: true });
    await until(() => /the values below are from the last read/.test(panel().textContent), 'the panel says the list is from the last read');
    assert.match(panel().textContent, /Failed 1 time in a row/, 'the panel counts the failure');
    assert.equal(notices().length, 0, 'the open panel carries the outage; no toast repeats it');

    // A Retry that fails again changes the count in the panel, not the toasts.
    const before = reads;
    retry().click();
    await until(() => reads > before, 'Retry reads the options again');
    await until(() => /Failed 2 times in a row/.test(panel().textContent), 'each failed Retry adds to the count');
    assert.equal(notices().length, 0, 'a failed Retry adds no toast');

    // Closing the panel releases the outage: the next one, with no panel open, is announced.
    chip().click();
    await until(() => !panel(), 'the chip closes its panel');
    await until(() => !chat.optionsShownInline(r), 'a closed panel no longer carries the outage');
    answer = [200, { data: [MODE] }];
    await chat.loadAgentOptions(r, { force: true });
    answer = FAIL;
    await chat.loadAgentOptions(r, { force: true });
    assert.equal(notices().length, 1, 'a new outage with no panel open gets its toast');
  } finally {
    render(null, host);
    host.remove();
  }
};

// The model picker's agent section answers a failed read the same way: the failure
// and its Retry, and that Retry is a fresh read.
const pickerAgentFailure = async () => {
  const FAIL = [503, { code: 'unavailable', detail: 'the agent is not answering' }];
  let answer = FAIL;
  let reads = 0;
  serveModelRoom(() => { reads++; return answer; });
  const { host, dialog } = await openModelPanel();
  const panel = () => doc.querySelector('[role="dialog"]');
  const retry = () => [...(panel()?.querySelectorAll('button') || [])].find((b) => b.textContent.trim() === 'Retry');
  try {
    assert.ok(dialog, 'the panel is open');
    assert.match(dialog.textContent, /Session options can't be read right now\./, 'the failure says what cannot be read');
    assert.ok(retry(), 'the failed read offers Retry');
    const before = reads;
    answer = [200, { data: OPTS, status: 'empty' }];
    retry().click();
    await until(() => reads > before, 'Retry reads the options again');
    await until(() => !/can't be read right now/.test(panel()?.textContent || ''), 'a good read replaces the failure');
  } finally {
    render(null, host);
    host.remove();
  }
};

// Typing in the search field makes the filtered list the target, so the body
// jumps back to its top; the typing itself never moves the focus.
const searchStartsAtTop = async () => {
  serveModelRoom();
  const { host, dialog } = await openModelPanel();
  try {
    assert.ok(dialog, 'the panel is open');
    const body = dialog.querySelector('[data-scroll="list"]');
    assert.ok(body, 'the scroll body exists');
    const input = dialog.querySelector('input');
    input.focus();
    body.scrollTop = 55;
    input.value = 'swe';
    input.dispatchEvent(new Ev('input', { bubbles: true }));
    await sleep(30);
    const live = doc.querySelector('[role="dialog"] [data-scroll="list"]');
    assert.equal(live, body, 'a filter must not rebuild the list scroller');
    assert.equal(live.scrollTop, 0, 'a changed filter starts the body at its top');
    assert.equal(doc.activeElement, input, 'typing never moves the focus');
  } finally {
    render(null, host);
    host.remove();
  }
};

// Reasoning effort is chrome for the chosen model. Scrolling the catalog
// must not take it with the rows: it lives in the band, and that band is a
// different scroll node from the list.
const effortStaysWithTheList = async () => {
  const models = [
    { id: 'm1', name: 'Alpha', listed: true, supportedEfforts: ['low', 'high'] },
    ...Array.from({ length: 8 }, (_, i) => ({ id: `m${i + 2}`, name: `Model ${i + 2}`, listed: true })),
  ];
  mockApi({
    'GET /api/providers/or/upstreams': { data: [{ slug: 'fast', name: 'Fastlane' }] },
    'GET /api/providers': { data: [{ id: 'or', name: 'OpenRouter', models }] },
    'GET /api/agents': { data: [] },
    'GET /api/conversations/c_eff/agent-options': { data: [], status: 'empty' },
    'GET /api/conversations/c_eff/messages': { messages: [], effectiveStart: 0, total: 0 },
    'GET /api/conversations/c_eff': { id: 'c_eff', title: 'Room', workspace: '', providerId: 'or', modelId: 'm1', effort: 'low' },
    'GET /api/approval-rules': { data: [] },
    'GET /api/context': { estimate: 0, total: 0 },
  });
  chat.navigate('#/c/c_eff');
  await sleep(50);
  const r = state.rooms.c_eff;
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  try {
    render(html`<${ModelPicker} room=${r} />`, host);
    await chat.loadProviders();
    await sleep(20);
    host.querySelector('button[aria-haspopup="dialog"]').click();
    await until(() => doc.body.textContent.includes('Reasoning effort'), 'the effort control did not render');
    const dialog = doc.querySelector('[role="dialog"]');
    const list = listScroller(dialog);
    const effort = [...dialog.querySelectorAll('span')].find((el) => el.textContent.trim() === 'Reasoning effort');
    assert.ok(list && effort, 'the list and the effort control both rendered');
    assert.equal(list.contains(effort), false, 'the effort control rides the model list');
    const band = controlBand(dialog);
    assert.ok(band && band.contains(effort), 'the effort control has no band of its own');
    assert.match(band.className, /overflow-y-auto/);
    assert.equal(band.style.maxHeight, 'calc(var(--panel-room) * 0.4)');
    list.scrollTop = 200;
    assert.equal(list.scrollTop, 200);
    assert.equal(band.scrollTop, 0, 'scrolling the list also scrolled the controls');
    assert.equal(list.contains(effort), false);
  } finally {
    render(null, host);
    host.remove();
  }
};

// A new room has no id. Its session options are the agent's own list, and
// they stay in the band: there is no thread yet, so this picker is the only
// place those controls exist.
const freshRoomOptionsStay = async () => {
  const saved = state.threadDefault;
  const options = [
    { id: 'mode', name: 'Session Mode', category: 'mode', type: 'select', current: 'agent', choices: [{ value: 'agent', name: 'Agent' }, { value: 'plan', name: 'Plan' }] },
    { id: 'effort', name: 'Effort', type: 'select', current: 'high', choices: [{ value: 'high', name: 'Extra High' }] },
  ];
  mockApi({
    'GET /api/agents/cursor/options': { data: options, status: 'ready' },
    'GET /api/agents/cursor/models': { data: [{ id: 'opus', name: 'Opus', current: true }, { id: 'sonnet', name: 'Sonnet' }] },
    'GET /api/agents': { data: [{ id: 'cursor', name: 'Cursor' }] },
    'GET /api/providers': { data: [] },
    'GET /api/context': { estimate: 0, total: 0 },
  });
  state.threadDefault = { agent: 'cursor', provider: '', model: 'opus' };
  const r = chat.freshRoom();
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  // A label is matched whole: "Model 2" contains "Mode" as a substring.
  const labelled = (node, text) => [...node.querySelectorAll('span')].some((el) => el.textContent.trim() === text);
  try {
    assert.equal(r.id, null);
    await chat.loadProviders();
    render(html`<${ModelPicker} room=${r} />`, host);
    await chat.loadAgents();
    host.querySelector('button[aria-haspopup="dialog"]').click();
    await until(() => {
      const band = doc.querySelector('[role="dialog"]') && controlBand(doc.querySelector('[role="dialog"]'));
      return !!band && labelled(band, 'Session Mode') && labelled(band, 'Effort');
    }, 'the new room did not show the agent options in the controls band');
    const dialog = doc.querySelector('[role="dialog"]');
    const list = listScroller(dialog);
    const band = controlBand(dialog);
    assert.ok(list && band, 'the new room splits the list from the controls');
    assert.equal(labelled(list, 'Session Mode'), false, 'the agent option rides the model list');
    assert.equal(labelled(list, 'Effort'), false, 'the agent effort rides the model list');
    assert.ok(calls.some((call) => call.url === '/api/agents/cursor/options'), 'a new room reads the agent options, not a thread');
    assert.equal(calls.some((call) => call.url.includes('/conversations/')), false);
  } finally {
    render(null, host);
    host.remove();
    state.threadDefault = saved;
  }
};

test('reasoning effort stays put while the model list scrolls', effortStaysWithTheList);
test('a new room keeps the agent options out of the model list', freshRoomOptionsStay);
test('the panel says a pick applies next turn only while the record parks it', agentHintFollowsRecord);
test('the options panel answers a read, an empty answer, and a failure, and a failure offers Retry', agentPanelAnswers);
test('the model picker offers Retry for a failed options read, and the retry reads again', pickerAgentFailure);
test('an open options panel carries the outage inline: no toast repeats it, and each failed Retry counts in the panel', outageInPanel);
test('the inline surface registers in the commit that mounts the panel, before any read can answer', registersAtMount);

for (const layout of ['sheet', 'desktop']) {
  test(`a model pick inside the panel keeps the panel, its scroll, its focus, and every control (${layout})`, () => inLayout(layout, modelPickKeepsPanel));
  test(`a panel whose chip hides closes with it, and does not reopen when the chip returns (${layout})`, () => inLayout(layout, agentChipFlow));
  test(`typing in the search field starts the body at its top (${layout})`, () => inLayout(layout, searchStartsAtTop));
}
