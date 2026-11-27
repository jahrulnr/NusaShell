// upstreamControl keeps the upstream select mounted across a model pick:
// the flicker the owner saw was the control unmounting for the loading
// state and remounting when the answer landed, which reads as the popup
// itself closing and reopening. The invariant: once a non-empty list ever
// arrived (`had`), no transition — loading, key change, or the frame in
// between — may unmount the control; only its content may change, and the
// content must never offer one model's upstreams on another.
import './env.mjs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { installDOM, doc } from './dom.mjs';
import { html, render } from '/vendor/preact/htm-preact.mjs';
import { state } from '../lib/store.js';
import { upstreamControl, contextRing, ContextMeter } from '../components/pickers.js';

const UP = [{ slug: 'fast', name: 'Fastlane' }];

test('a pick in flight keeps the control mounted with only Auto to offer', () => {
  // The new model's fetch is still out: state already carries its key but
  // no list. The select stays (show) and cannot offer the old model's
  // upstreams (list empty — 'Auto' is the only option, matching the value
  // every pick resets to).
  const ctl = upstreamControl({ status: 'loading', key: 'p|B', list: [], had: true }, 'p|B');
  assert.equal(ctl.show, true);
  assert.deepEqual(ctl.list, []);
  assert.equal(ctl.loading, true);
});

test('the stale frame between pick and effect never shows another key\'s list', () => {
  // One render happens with the new selection but the old upstream state.
  // The control must not unmount (it was visible a frame ago) and must not
  // offer model A's upstreams on model B.
  const ctl = upstreamControl({ status: 'ready', key: 'p|A', list: UP, had: true }, 'p|B');
  assert.equal(ctl.show, true);
  assert.deepEqual(ctl.list, []);
});

test('a fetched list renders for the key it belongs to', () => {
  const ctl = upstreamControl({ status: 'ready', key: 'p|A', list: UP, had: true }, 'p|A');
  assert.equal(ctl.show, true);
  assert.deepEqual(ctl.list, UP);
  assert.equal(ctl.loading, false);
});

test('a model without upstreams keeps the control once it was ever shown', () => {
  // Ready and empty is a definitive answer, but unmounting now would flicker
  // the footer on every pick that lands on such a model. The control stays;
  // 'Auto' alone is the honest content — it is what the room runs.
  const ctl = upstreamControl({ status: 'ready', key: 'p|B', list: [], had: true }, 'p|B');
  assert.equal(ctl.show, true);
  assert.deepEqual(ctl.list, []);
});

test('before the first answer ever arrives there is nothing to show', () => {
  // No upstreams have ever been seen in this panel session: no dead control
  // for providers that simply have none — neither while asking nor when the
  // answer is empty.
  assert.equal(upstreamControl({ status: 'loading', key: 'p|A', list: [], had: false }, 'p|A').show, false);
  assert.equal(upstreamControl({ status: 'ready', key: 'p|A', list: [], had: false }, 'p|A').show, false);
});

test('a failed read mounts the control before any list has arrived', () => {
  const ctl = upstreamControl({ status: 'error', key: 'p|A', list: [], had: false }, 'p|A');
  assert.equal(ctl.show, true);
  assert.equal(ctl.error, true);
  assert.deepEqual(ctl.list, []);
  // Another model's failure does not mount this one's control.
  assert.equal(upstreamControl({ status: 'error', key: 'p|A', list: [], had: false }, 'p|B').show, false);
});

test('a load or a failure does not keep the previous model\'s upstreams', () => {
  assert.deepEqual(upstreamControl({ status: 'loading', key: 'p|B', list: UP, had: true }, 'p|B').list, []);
  assert.deepEqual(upstreamControl({ status: 'error', key: 'p|B', list: UP, had: true }, 'p|B').list, []);
});

// The footer ring. Absent is not zero: an agent room with no report is a
// hollow ring, a provider room with no window is nothing, and the percent
// is the ratio, not a cap at 100.
const room = (info, ctx, sel = {}) => ({ id: 'c_1', sel, info, ctx });

test('an agent room with no report is a hollow ring, and a planted zero is not a count', () => {
  state.agents.items = [{ id: 'devin', name: 'Devin' }];
  const hollow = 'Context not reported by Devin. Nothing has arrived from it yet.';
  assert.deepEqual(contextRing(room({ runtime: 'agent', agentId: 'devin' }, null)), { hollow: true, text: hollow });
  assert.deepEqual(contextRing(room({ runtime: 'agent', agentId: 'devin' }, { estimate: 0, total: 0 })), { hollow: true, text: hollow });
  // A size with no live report is still not "0 / N". estimate 0 is the placeholder.
  assert.equal(contextRing(room({ runtime: 'agent', agentId: 'devin' }, { estimate: 0, total: 200_000 })).hollow, true);
  assert.equal(contextRing(room({ runtime: 'agent', agentId: 'devin' }, { estimate: 0, total: 200_000 })).text.includes('0%'), false);
});

test('a provider room with no window draws nothing, even when an agent id is set', () => {
  assert.equal(contextRing(room({ runtime: 'harness', agentId: 'devin', providerId: 'local', modelId: 'm1' }, null)), null);
  assert.equal(contextRing(room({ runtime: 'harness', providerId: 'local', modelId: 'm1' }, { estimate: 0, total: 0 })), null);
  assert.equal(contextRing(room({ runtime: 'harness', providerId: 'local', modelId: 'm1' }, { estimate: 50_000 })), null);
});

test('a counted zero is the context endpoint, and a frame with no estimate is not a count', () => {
  const view = contextRing(room(
    { runtime: 'harness', providerId: 'local', modelId: 'm1' },
    { estimate: 0, total: 200_000, live: 224_000 },
  ));
  assert.equal(view.hollow, false);
  assert.equal(view.text, '0% (0 / 200K) context used · estimated');
  assert.equal(view.text.includes('Quota'), false);
  // turn.context on a room with no estimate leaves a window and no count.
  // live is the frame's used, and it is not the provider's figure.
  assert.equal(contextRing(room(
    { runtime: 'harness', providerId: 'local', modelId: 'm1' },
    { total: 200_000, live: 0, cost: null },
  )), null);
});

test('the percent is the ratio, and past the window it says over', () => {
  const view = contextRing(room(
    { runtime: 'harness', providerId: 'local', modelId: 'm1' },
    { estimate: 224_000, total: 200_000 },
  ));
  assert.equal(view.pct, 112);
  assert.equal(view.text, '112% (224K / 200K) context used · over');
  assert.equal(view.text.includes('100%'), false);
});

test('an agent report uses live, names the agent, and says quota is not reported', () => {
  state.agents.items = [];
  const view = contextRing(room(
    { runtime: 'agent', agentId: 'devin', modelId: 'gpt' },
    { estimate: 0, live: 106_000, total: 200_000, cost: { amount: 12.5, currency: 'credits' } },
  ));
  assert.equal(view.pct, 53);
  assert.equal(view.text, '53% (106K / 200K) context used · live · session cost 12.5 credits · Quota not reported by devin');
  const named = contextRing(room(
    { runtime: 'agent', agentId: 'devin' },
    { live: 0, total: 200_000, cost: { amount: 0.04, currency: 'USD' } },
  ));
  state.agents.items = [{ id: 'devin', name: 'Devin' }];
  const withName = contextRing(room(
    { runtime: 'agent', agentId: 'devin' },
    { live: 0, total: 200_000, cost: { amount: 0.04, currency: 'USD' } },
  ));
  const money = new Intl.NumberFormat(undefined, { style: 'currency', currency: 'USD', maximumFractionDigits: 4 }).format(0.04);
  assert.equal(withName.text, `0% (0 / 200K) context used · live · session cost ${money} · Quota not reported by Devin`);
  assert.equal(named.text.includes('gpt'), false);
  assert.equal(named.text.includes('Quota not reported by devin'), true);
});

test('the ring\'s name and tooltip are the same words, and a hollow ring has no arc', () => {
  installDOM();
  state.agents.items = [{ id: 'devin', name: 'Devin' }];
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  const draw = (info, ctx) => {
    render(html`<${ContextMeter} room=${room(info, ctx)} />`, host);
    return host;
  };
  draw({ runtime: 'agent', agentId: 'devin' }, { estimate: 0, total: 0 });
  const hollow = host.querySelector('[role="img"]');
  const tip = host.querySelector('[role="tooltip"]');
  assert.equal(hollow.getAttribute('aria-label'), tip.textContent);
  assert.equal(hollow.getAttribute('aria-label'), 'Context not reported by Devin. Nothing has arrived from it yet.');
  assert.equal(host.querySelectorAll('circle').length, 1);
  draw({ runtime: 'harness', providerId: 'local', modelId: 'm1' }, { estimate: 224_000, total: 200_000 });
  assert.equal(host.querySelector('[role="img"]').getAttribute('aria-label'), host.querySelector('[role="tooltip"]').textContent);
  assert.equal(host.querySelector('[role="img"]').getAttribute('aria-label').startsWith('112%'), true);
  assert.equal(host.querySelectorAll('circle').length, 2);
  draw({ runtime: 'harness', providerId: 'local', modelId: 'm1' }, null);
  assert.equal(host.querySelector('[role="img"]'), null);
  render(null, host);
  host.parentNode.removeChild(host);
});
