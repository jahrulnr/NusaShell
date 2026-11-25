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
import { upstreamControl } from '../components/pickers.js';

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
