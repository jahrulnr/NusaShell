// The Select's resync is what a refused pick depends on: slim-select marks
// the choice at once, so when the server keeps the old value the control
// must be pushed back onto it — and `value` never moved to ask for that.
import './env.mjs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { selectResync } from '../components/ui.js';

const OPTS = [
  { value: 'accept-edits', text: 'Code' },
  { value: 'bypass', text: 'Bypass permissions' },
];

const harness = () => {
  const el = { _value: undefined, _data: undefined };
  const pushed = [];
  const ss = { setData: (d) => pushed.push(d) };
  const selected = () => pushed.at(-1)?.find((o) => o.selected)?.value;
  return { el, ss, pushed, selected };
};

test('a refused pick is pushed back onto the value the server kept', () => {
  const { el, ss, pushed, selected } = harness();
  // The mount push marks the stored value.
  selectResync(el, ss, 'accept-edits', OPTS);
  assert.equal(selected(), 'accept-edits');
  // slim-select's afterChange marks the pick before the answer comes back.
  el._value = 'bypass';
  // The PUT is refused: `value` stays accept-edits and the props' deps never
  // moved — divergence alone has to ask for the push.
  assert.equal(selectResync(el, ss, 'accept-edits', OPTS), true);
  assert.equal(selected(), 'accept-edits', 'the control shows the stored value, not the failed pick');
  assert.equal(el._value, 'accept-edits');
});

test('a value that actually changed still syncs, and an accepted pick costs no push', () => {
  const { el, ss, pushed, selected } = harness();
  selectResync(el, ss, 'accept-edits', OPTS);
  // An external change — another tab, a server answer — still reaches the display.
  assert.equal(selectResync(el, ss, 'bypass', OPTS), true);
  assert.equal(selected(), 'bypass');
  // The owner picks bypass and the server takes it: value arrives at what
  // slim-select already shows; pushing again would be churn.
  pushed.length = 0;
  el._value = 'bypass';
  assert.equal(selectResync(el, ss, 'bypass', OPTS), false);
  assert.equal(pushed.length, 0);
});

test('a steady control does not rewrite itself every render', () => {
  const { el, ss, pushed } = harness();
  selectResync(el, ss, 'accept-edits', OPTS);
  pushed.length = 0;
  for (let i = 0; i < 5; i++) assert.equal(selectResync(el, ss, 'accept-edits', OPTS), false);
  assert.equal(pushed.length, 0, 'renders with no divergence push nothing');
  // ...but a new option list is a real change and does push.
  assert.equal(selectResync(el, ss, 'accept-edits', [...OPTS, { value: 'plan', text: 'Plan' }]), true);
});

test('grouped options mark the stored value inside their group', () => {
  const { el, ss, pushed, selected } = harness();
  const grouped = [{ label: 'Session', options: OPTS }];
  el._value = 'bypass';
  selectResync(el, ss, 'accept-edits', grouped);
  const flat = pushed.at(-1).flatMap((e) => e.options || [e]);
  assert.equal(flat.find((o) => o.selected)?.value, 'accept-edits');
});
