// ACP EventSource recovery regression tests — a permanent (non-2xx) stream
// failure must reopen on a bounded delay instead of leaving the Subagents
// dock frozen, and a room switch must cancel the pending reopen so a stale
// retry cannot resubscribe to the previous room.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';

import { acceptSubagentStreamFrame } from '../page/conversation/subagent-state.js';

const root = new URL('../', import.meta.url);

async function extractFunctions(names) {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  return names.map((name) => {
    const start = app.indexOf(`function ${name}(`);
    assert.ok(start >= 0, `expected ${name} in app.js`);
    return app.slice(start, app.indexOf('\n}', start) + 2);
  }).join('\n');
}

function fakeTimers() {
  const timers = [];
  return {
    timers,
    setTimeout: (fn, delay) => {
      const timer = { fn, delay, cleared: false };
      timers.push(timer);
      return timer;
    },
    clearTimeout: (timer) => { if (timer) timer.cleared = true; },
    flush() { for (const timer of timers.splice(0)) if (!timer.cleared) timer.fn(); },
  };
}

function makeEventSource(sources) {
  return class FakeEventSource {
    static CONNECTING = 0;
    static OPEN = 1;
    static CLOSED = 2;
    constructor(url) {
      this.url = url;
      this.readyState = FakeEventSource.CONNECTING;
      this.listeners = new Map();
      sources.push(this);
    }
    addEventListener(type, fn) { this.listeners.set(type, fn); }
    emit(type, data) { this.listeners.get(type)?.({ data }); }
    close() { this.readyState = FakeEventSource.CLOSED; }
  };
}

async function streamHarness() {
  const sources = [];
  const timers = fakeTimers();
  const upserts = [];
  let bridgeCalls = 0;
  const code = await extractFunctions([
    'closeSubagentStream',
    'openSubagentStream',
    'scheduleSubagentStreamReopen',
  ]);
  const context = {
    EventSource: makeEventSource(sources),
    setTimeout: timers.setTimeout,
    clearTimeout: timers.clearTimeout,
    SUBAGENT_STREAM_REOPEN_MS: 5000,
    acceptSubagentStreamFrame,
    toSubagentView: (run) => run,
    upsertLiveSubagent: (run) => { upserts.push(run); return true; },
    renderSubagentsDock: () => {},
    loadSubagents: () => { bridgeCalls += 1; return Promise.resolve(); },
    liveSubagents: [],
    streamedSubagentIDs: new Set(),
    activeConversation: { id: 'conv-1' },
    subagentEventSource: null,
    subagentStreamConversationID: '',
    subagentStreamReopenTimer: null,
  };
  runInNewContext(code, context);
  return { context, sources, timers, upserts, bridge: () => bridgeCalls };
}

test('a permanently closed ACP stream bridges through the list RPC and reopens once', async () => {
  const { context, sources, timers, upserts, bridge } = await streamHarness();

  context.openSubagentStream('conv-1');
  assert.equal(sources.length, 1);
  const first = sources[0];
  assert.match(first.url, /\/stream\/acp\?conversation_id=conv-1/);

  // HTTP 503 (subscriber cap) permanently closes the source: readyState CLOSED.
  first.readyState = first.constructor.CLOSED;
  first.onerror();
  assert.equal(bridge(), 1, 'the closed stream falls back to acp.runs.list while reopening');
  assert.equal(timers.timers.length, 1, 'one bounded reopen is scheduled');
  assert.equal(timers.timers[0].delay, 5000);

  timers.flush();
  assert.equal(first.readyState, first.constructor.CLOSED);
  assert.equal(sources.length, 2, 'a fresh EventSource replaces the dead one');
  const second = sources[1];
  assert.equal(context.subagentEventSource, second);

  // The reopened stream delivers a fresh snapshot and repopulates the dock.
  second.readyState = second.constructor.OPEN;
  second.onopen();
  second.emit('acp.run', JSON.stringify({
    type: 'acp.run.snapshot',
    seq: 0,
    conversation_id: 'conv-1',
    runs: [{ id: 'acprun_1', conversation_id: 'conv-1', status: 'running' }],
  }));
  assert.deepEqual(upserts.map((run) => run.id), ['acprun_1']);
  assert.equal(bridge(), 2, 'the recovered stream refreshes the list snapshot');
});

test('a reconnecting (transient) error does not schedule a reopen', async () => {
  const { context, sources, timers, bridge } = await streamHarness();

  context.openSubagentStream('conv-1');
  const first = sources[0];
  first.readyState = first.constructor.CONNECTING;
  first.onerror();
  assert.equal(timers.timers.length, 0, 'EventSource retries network failures itself');
  assert.equal(bridge(), 0);
  assert.equal(context.subagentEventSource, first);
});

test('room switch cancels a pending reopen and rejects stale frames', async () => {
  const { context, sources, timers, upserts } = await streamHarness();

  context.openSubagentStream('conv-1');
  const first = sources[0];
  first.readyState = first.constructor.CLOSED;
  first.onerror();
  assert.equal(timers.timers.length, 1);

  // The room switch teardown path must cancel the retry, not just close the
  // socket — otherwise the dead room's stream resubscribes later.
  context.closeSubagentStream();
  assert.equal(context.subagentStreamReopenTimer, null);
  assert.equal(first.readyState, first.constructor.CLOSED);
  timers.flush();
  assert.equal(sources.length, 1, 'no reopen fires after the room change');

  // Frames arriving on the abandoned source are rejected once another room is
  // active, even if the source reference still lingers.
  context.openSubagentStream('conv-2');
  context.activeConversation = { id: 'conv-2' };
  const stale = sources[0];
  stale.emit('acp.run', JSON.stringify({
    type: 'acp.run.snapshot', seq: 1, conversation_id: 'conv-1', runs: [{ id: 'acprun_old', conversation_id: 'conv-1' }],
  }));
  assert.deepEqual(upserts, [], 'stale-room frames are dropped');
  assert.equal(context.subagentStreamConversationID, 'conv-2');
});

test('a scheduled reopen cannot hijack the newer room', async () => {
  const { context, sources, timers } = await streamHarness();

  context.openSubagentStream('conv-1');
  const first = sources[0];
  first.readyState = first.constructor.CLOSED;
  first.onerror();
  assert.equal(timers.timers.length, 1);

  // The switch path clears the timer; guard the residual case too: even if a
  // timer survived, its callback must not resubscribe the old room.
  context.closeSubagentStream();
  context.activeConversation = { id: 'conv-2' };
  context.openSubagentStream('conv-2');
  timers.flush();
  assert.equal(context.subagentStreamConversationID, 'conv-2');
  assert.equal(sources.at(-1).url.includes('conv-2'), true);
  assert.equal(sources.filter((source) => source.url.includes('conv-1')).length, 1);
});
