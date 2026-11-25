// Settling an install without a network: the page must reload on the version
// the server answers with, and must never wait without a bound.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import './env.mjs';
import { settleInstall, INSTALL_POLL_FAILS } from '../lib/install.js';

// scripted answers each call from a list, holding the last value; an Error
// entry rejects. Every test carries a timeout of its own: an unbounded settle
// is a regression, and it must fail the suite rather than hang it.
function scripted(values, label) {
  let i = 0;
  const calls = { n: 0 };
  const fn = async () => {
    calls.n += 1;
    if (calls.n > 500) throw new Error(`${label}: called ${calls.n} times — the settle is not bounded`);
    const v = values[Math.min(i, values.length - 1)];
    i += 1;
    if (v instanceof Error) throw v;
    return v;
  };
  return { fn, calls };
}

const running = { state: 'running', phase: 'download', version: 'v2' };

function harness({ progress, version, from = 'v1' }) {
  const sleeps = [];
  const progressCalls = scripted(progress, 'progress');
  const versionCalls = scripted(version, 'version');
  const reloads = [];
  const seen = [];
  const run = settleInstall({
    progress: progressCalls.fn,
    version: versionCalls.fn,
    // a real tick, not a microtask: an unbounded loop must yield so the
    // per-test timeout can fail it instead of starving the event loop
    sleep: async (ms) => { sleeps.push(ms); await new Promise((r) => setTimeout(r, 0)); },
    reload: () => reloads.push('reload'),
    onProgress: (p) => seen.push(p.state),
    from,
  });
  return {
    run,
    sleeps,
    reloads,
    seen,
    progressCalls: progressCalls.calls,
    versionCalls: versionCalls.calls,
  };
}

test('a reexec that forgets the install state still reloads on the new version', { timeout: 10_000 }, async () => {
  // the old process reports progress, then is replaced: the new one answers
  // `idle` and the install state is gone with the process that held it
  const h = harness({
    progress: [running, running, { state: 'idle' }],
    version: [{ current: 'v1' }, { current: 'v1' }, { current: 'v2' }],
  });
  const out = await h.run;
  assert.equal(out.outcome, 'reloaded');
  assert.equal(out.version, 'v2');
  assert.deepEqual(h.reloads, ['reload'], 'the page is reloaded exactly once');
});

test('a server that drops mid-poll is waited for, and the new version reloads', { timeout: 10_000 }, async () => {
  const down = new Error('connection refused');
  const h = harness({
    progress: [running, down, down, down, down, down],
    version: [down, down, { current: 'v2' }],
  });
  const out = await h.run;
  assert.equal(out.outcome, 'reloaded');
  assert.equal(out.version, 'v2');
  assert.deepEqual(h.reloads, ['reload']);
  assert.ok(h.progressCalls.n <= INSTALL_POLL_FAILS + 1, 'the failing reads stop at the bound');
});

test('a failed install is reported and never reloaded away', { timeout: 10_000 }, async () => {
  const h = harness({
    progress: [running, { state: 'failed', error: 'checksum mismatch' }],
    version: [{ current: 'v1' }, { current: 'v2' }],
  });
  const out = await h.run;
  assert.equal(out.outcome, 'failed');
  assert.equal(out.error, 'checksum mismatch');
  assert.deepEqual(h.reloads, [], 'a failure stays on screen');
  assert.equal(h.versionCalls.n, 0, 'no point asking which version runs after a failure');
});

test('a version that never changes ends as a timeout, not a spin', { timeout: 10_000 }, async () => {
  const h = harness({
    progress: [{ state: 'succeeded' }],
    version: [{ current: 'v1' }],
  });
  const out = await h.run;
  assert.equal(out.outcome, 'timeout');
  assert.equal(out.version, 'v1');
  assert.deepEqual(h.reloads, []);
  assert.ok(h.sleeps.length < 200, `bounded: ${h.sleeps.length} waits`);
});

test('an install still running at the poll limit hands over to the version wait', { timeout: 10_000 }, async () => {
  const h = harness({
    progress: [running],
    version: [{ current: 'v2' }],
  });
  const out = await h.run;
  assert.equal(out.outcome, 'reloaded', 'a slow install that lands still reloads');
  assert.ok(h.progressCalls.n < 1000, 'the poll itself is bounded');
});

test('with no known version the first answer is the anchor', { timeout: 10_000 }, async () => {
  const h = harness({
    progress: [{ state: 'idle' }],
    version: [{ current: 'v1' }, { current: 'v1' }, { current: 'v2' }],
    from: '',
  });
  const out = await h.run;
  assert.equal(out.outcome, 'reloaded');
  assert.equal(out.version, 'v2');
  assert.deepEqual(h.reloads, ['reload']);
});

test('the same version answering over and over never reloads', { timeout: 10_000 }, async () => {
  const h = harness({
    progress: [running, { state: 'idle' }],
    version: [{ current: 'v1' }],
    from: 'v1',
  });
  const out = await h.run;
  assert.equal(out.outcome, 'timeout');
  assert.deepEqual(h.reloads, []);
});
