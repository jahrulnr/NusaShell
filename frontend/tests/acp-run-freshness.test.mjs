// Freshness rules for the conversation-scoped ACP run stream. The subagent
// view is driven through its public API (bindSubagents, setSubagentConversation)
// with a controllable EventSource and RPC stub. Assertions read the dock header
// (live/settled counts), not private state.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { after, before, test } from 'node:test';
import { JSDOM } from 'jsdom';

const indexHTML = await readFile(new URL('../index.html', import.meta.url), 'utf8');
const dom = new JSDOM(indexHTML, { url: 'http://localhost/', pretendToBeVisual: true });
const { window } = dom;
const previous = {
  window: globalThis.window,
  document: globalThis.document,
  HTMLElement: globalThis.HTMLElement,
  localStorage: globalThis.localStorage,
  fetch: globalThis.fetch,
  EventSource: globalThis.EventSource,
};

const ago = (ms) => new Date(Date.now() - ms).toISOString();
const T_OLD = ago(20000);
const T_MID = ago(10000);
const T_NEW = ago(5000);

let activeConversation = '';
let listHandler = null; // (conversationId) => runs[] | Promise<runs[]>
const sources = [];

class FakeEventSource {
  constructor(url) {
    this.url = url;
    this.readyState = 0;
    this.listeners = new Map();
    sources.push(this);
  }
  addEventListener(type, fn) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(fn);
  }
  close() { this.readyState = 2; }
  open() { this.readyState = 1; this.onopen?.(); }
  send(frame) {
    for (const fn of this.listeners.get('acp.run') || []) fn({ data: JSON.stringify(frame) });
  }
}

function rpcResult(result) {
  return { ok: true, json: async () => ({ ok: true, result }) };
}

// settle turns a handler result or thrown error into an RPC HTTP response, so
// failures travel through the real rpc.js error path.
async function settle(handler) {
  try {
    return rpcResult(await handler());
  } catch (error) {
    return { ok: false, status: 500, json: async () => ({ ok: false, error: { message: error.message } }) };
  }
}

let getHandler = async () => { throw new Error('no get fixture'); };

const sub = {};
const rpcModule = {};
const intervals = new Set();
const nativeSetInterval = globalThis.setInterval;
const nativeClearInterval = globalThis.clearInterval;
const tick = () => new Promise((resolve) => setTimeout(resolve, 5));

function run(id, status, updatedAt, extra = {}) {
  return { id, conversation_id: activeConversation, agent_id: 'acp_dev', status, prompt: 'work', created_at: T_OLD, updated_at: updatedAt, ...extra };
}

const dockMeta = () => document.getElementById('acp-dock-meta').textContent;

before(async () => {
  globalThis.setInterval = (...args) => {
    const handle = nativeSetInterval(...args);
    intervals.add(handle);
    return handle;
  };
  globalThis.clearInterval = (handle) => {
    intervals.delete(handle);
    nativeClearInterval(handle);
  };
  globalThis.window = window;
  globalThis.document = window.document;
  globalThis.HTMLElement = window.HTMLElement;
  globalThis.localStorage = window.localStorage;
  globalThis.EventSource = FakeEventSource;
  globalThis.fetch = async (_url, init) => {
    const body = JSON.parse(init.body);
    if (body.method === 'acp.runs.list') {
      return settle(async () => ({ runs: (await listHandler?.(body.payload.conversation_id)) ?? [] }));
    }
    if (body.method === 'acp.runs.get') {
      return settle(async () => ({ run: await getHandler(body.payload.id) }));
    }
    return rpcResult({ agents: [] });
  };
  Object.assign(rpcModule, await import('../js/rpc.js'));
  Object.assign(sub, await import('../js/views/agent/subagents.js'));
  sub.bindSubagents({ getActiveConversationId: () => activeConversation });
});

after(() => {
  for (const handle of intervals) nativeClearInterval(handle);
  globalThis.setInterval = nativeSetInterval;
  globalThis.clearInterval = nativeClearInterval;
  globalThis.window = previous.window;
  globalThis.document = previous.document;
  globalThis.HTMLElement = previous.HTMLElement;
  globalThis.localStorage = previous.localStorage;
  globalThis.fetch = previous.fetch;
  globalThis.EventSource = previous.EventSource;
  dom.window.close();
});

function enterRoom(conversationId) {
  activeConversation = conversationId;
  sub.setSubagentConversation(conversationId);
  return sources.at(-1);
}

test('a newer completed list response recovers a run the stream left live', async () => {
  listHandler = () => [];
  const source = enterRoom('conv_fresh_newer');
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 1, runs: [run('acprun_n1', 'running', T_OLD)] });
  await tick();
  assert.match(dockMeta(), /1 live/);
  listHandler = () => [run('acprun_n1', 'completed', T_NEW)];
  sub.setSubagentConversation('conv_fresh_newer');
  await tick();
  assert.equal(dockMeta(), 'settled');
});

test('an equal-timestamp terminal list response replaces a streamed live run', async () => {
  listHandler = () => [];
  const source = enterRoom('conv_fresh_equal');
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 1, runs: [run('acprun_e1', 'running', T_MID)] });
  await tick();
  listHandler = () => [run('acprun_e1', 'failed', T_MID)];
  sub.setSubagentConversation('conv_fresh_equal');
  await tick();
  assert.equal(dockMeta(), 'settled');
});

test('a stale list response cannot overwrite a stream update that arrived during the request', async () => {
  listHandler = () => [];
  const source = enterRoom('conv_fresh_stale_list');
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 1, runs: [run('acprun_s1', 'running', T_OLD)] });
  await tick();
  let release;
  listHandler = () => new Promise((resolve) => { release = () => resolve([run('acprun_s1', 'running', T_OLD)]); });
  sub.setSubagentConversation('conv_fresh_stale_list');
  source.send({ type: 'acp.run.updated', seq: 2, run: run('acprun_s1', 'completed', T_MID) });
  release();
  await tick();
  assert.equal(dockMeta(), 'settled');
});

test('a fresher HTTP run is not regressed by an older queued stream frame', async () => {
  listHandler = () => [];
  const source = enterRoom('conv_fresh_regress');
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 1, runs: [run('acprun_r1', 'running', T_OLD)] });
  await tick();
  listHandler = () => [run('acprun_r1', 'completed', T_NEW)];
  sub.setSubagentConversation('conv_fresh_regress');
  await tick();
  assert.equal(dockMeta(), 'settled');
  source.send({ type: 'acp.run.updated', seq: 2, run: run('acprun_r1', 'running', T_MID) });
  await tick();
  assert.equal(dockMeta(), 'settled', 'older queued frame does not revive the run');
});

test('a terminal run is never revived to running by a newer-stamped list response', async () => {
  listHandler = () => [];
  const source = enterRoom('conv_fresh_terminal');
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 1, runs: [run('acprun_t1', 'completed', T_MID)] });
  await tick();
  listHandler = () => [run('acprun_t1', 'running', T_NEW)];
  sub.setSubagentConversation('conv_fresh_terminal');
  await tick();
  assert.equal(dockMeta(), 'settled');
});

test('waiting_permission stays live and is not classified as settled', async () => {
  listHandler = () => [];
  const source = enterRoom('conv_fresh_permission');
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 1, runs: [run('acprun_p1', 'waiting_permission', T_MID)] });
  await tick();
  assert.match(dockMeta(), /1 live/);
});

test('a missed incremental frame reopens a fresh snapshot without applying the gap frame', async () => {
  listHandler = () => [];
  const first = enterRoom('conv_fresh_gap');
  first.open();
  first.send({ type: 'acp.run.snapshot', seq: 1, runs: [run('acprun_g1', 'running', T_MID)] });
  await tick();
  const before = sources.length;
  first.send({ type: 'acp.run.updated', seq: 3, run: run('acprun_g1', 'completed', T_NEW) });
  await tick();
  assert.equal(sources.length, before + 1, 'a fresh stream replaces the gapped one');
  assert.equal(first.readyState, 2);
  assert.match(dockMeta(), /1 live/, 'gap frame is not applied');
  const fresh = sources.at(-1);
  fresh.open();
  fresh.send({ type: 'acp.run.snapshot', seq: 3, runs: [run('acprun_g1', 'completed', T_NEW)] });
  await tick();
  assert.equal(dockMeta(), 'settled', 'fresh snapshot heals the run');
});

test('frames from an obsolete stream are ignored after reopen', async () => {
  listHandler = () => [];
  const first = enterRoom('conv_fresh_obsolete');
  first.open();
  first.send({ type: 'acp.run.snapshot', seq: 1, runs: [run('acprun_o1', 'running', T_MID)] });
  await tick();
  first.send({ type: 'acp.run.updated', seq: 3, run: run('acprun_o1', 'running', T_MID) });
  await tick();
  sources.at(-1).open();
  first.send({ type: 'acp.run.updated', seq: 2, run: run('acprun_o1', 'completed', T_NEW) });
  await tick();
  assert.match(dockMeta(), /1 live/);
});

test('a high snapshot sequence and a reset epoch are valid cursors', async () => {
  listHandler = () => [];
  const source = enterRoom('conv_fresh_highseq');
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 500, runs: [run('acprun_h1', 'running', T_MID)] });
  await tick();
  source.send({ type: 'acp.run.updated', seq: 501, run: run('acprun_h1', 'completed', T_NEW) });
  await tick();
  assert.equal(dockMeta(), 'settled');
});

// heldFirstList holds the first list request open and answers later requests
// with the current server state, so a stale in-flight response can be released
// after the connection moved on.
function heldFirstList(staleRuns, currentRuns) {
  let calls = 0;
  let release;
  listHandler = () => {
    calls += 1;
    if (calls > 1) return currentRuns();
    return new Promise((resolve) => { release = () => resolve(staleRuns()); });
  };
  return () => release();
}

test('an in-flight list response from before a same-source reconnect is discarded', async () => {
  listHandler = () => [];
  const source = enterRoom('conv_fresh_reconnect');
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 1, runs: [run('acprun_c1', 'running', T_OLD)] });
  await tick();
  assert.match(dockMeta(), /1 live/);
  const releaseStale = heldFirstList(
    () => [run('acprun_c1', 'completed', T_NEW)],
    () => [run('acprun_c1', 'running', T_OLD)],
  );
  sub.setSubagentConversation('conv_fresh_reconnect');
  source.open();
  source.open();
  releaseStale();
  await tick();
  assert.match(dockMeta(), /1 live/, 'stale response from before the reconnect is discarded');
});

test('an in-flight list response from a previous room visit is discarded (ABA)', async () => {
  listHandler = () => [run('acprun_aba1', 'running', T_OLD)];
  enterRoom('conv_aba_a');
  await tick();
  const releaseStale = heldFirstList(
    () => [run('acprun_aba1', 'completed', T_NEW)],
    () => [run('acprun_aba1', 'running', T_OLD)],
  );
  sub.setSubagentConversation('conv_aba_a');
  enterRoom('conv_aba_b');
  enterRoom('conv_aba_a');
  releaseStale();
  await tick();
  assert.match(dockMeta(), /1 live/, 'response from the earlier visit is discarded');
});

// deferred returns a promise plus its settle functions, for held get responses.
function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

const panelFor = (runId) => document.querySelector(`#acp-drawer .acp-run-panel[data-run-id="${runId}"]`);
const drawerText = () => document.getElementById('acp-drawer')?.textContent || '';
const RETRY_TEXT = 'Unable to load this transcript. Click the card again to retry.';

test('a stale get after a same-source reopen is discarded and the selected transcript loads without a click', async () => {
  listHandler = () => [];
  const conv = 'conv_get_reopen';
  const source = enterRoom(conv);
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 1, runs: [] });
  await tick();
  const held = deferred();
  let calls = 0;
  getHandler = async (id) => {
    calls += 1;
    if (calls === 1) return held.promise;
    return run(id, 'completed', T_NEW);
  };
  sub.openDrawer('acprun_gr1');
  await tick();
  source.open();
  source.open();
  held.reject(new Error('stale failure'));
  await tick();
  await tick();
  assert.equal(calls, 2, 'a fresh get is issued for the current generation');
  assert.ok(panelFor('acprun_gr1'), 'transcript panel is mounted');
  assert.equal(drawerText().includes('Loading transcript'), false);
  assert.equal(drawerText().includes(RETRY_TEXT), false);
  sub.closeAcpOverlays();
});

test('a stale get from an earlier room visit is discarded (ABA) and the current visit keeps its own result', async () => {
  listHandler = () => [];
  const conv = 'conv_get_aba';
  enterRoom(conv);
  await tick();
  const staleHeld = deferred();
  let calls = 0;
  getHandler = async (id) => {
    calls += 1;
    if (calls === 1) return staleHeld.promise;
    return run(id, 'running', T_NEW, { conversation_id: conv });
  };
  sub.openDrawer('acprun_aba_g');
  await tick();
  enterRoom('conv_get_aba_other');
  enterRoom(conv);
  sub.openDrawer('acprun_aba_g');
  await tick();
  staleHeld.resolve(run('acprun_aba_g', 'completed', T_NEW, { conversation_id: conv }));
  await tick();
  await tick();
  assert.match(dockMeta(), /1 live/, 'the stale completed response does not settle the run');
  assert.ok(panelFor('acprun_aba_g'));
  sub.closeAcpOverlays();
});

test('a failing list response does not block the selected transcript: the get loads it', async () => {
  listHandler = async () => { throw new Error('list unavailable'); };
  const conv = 'conv_get_nolist';
  const source = enterRoom(conv);
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 1, runs: [] });
  await tick();
  getHandler = async (id) => run(id, 'completed', T_NEW, { conversation_id: conv });
  sub.openDrawer('acprun_nl1');
  await tick();
  await tick();
  assert.ok(panelFor('acprun_nl1'), 'transcript loads from the get alone');
  assert.equal(drawerText().includes(RETRY_TEXT), false);
  listHandler = () => [];
  sub.closeAcpOverlays();
});

test('a genuine unsuperseded get failure shows the retry text and is not retried automatically', async () => {
  listHandler = () => [];
  const conv = 'conv_get_fail';
  const source = enterRoom(conv);
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 1, runs: [] });
  await tick();
  let calls = 0;
  getHandler = async () => {
    calls += 1;
    throw new Error('run not found');
  };
  sub.openDrawer('acprun_gf1');
  await tick();
  await tick();
  assert.match(drawerText(), /Unable to load this transcript/);
  assert.equal(calls, 1);
  await new Promise((resolve) => setTimeout(resolve, 30));
  assert.equal(calls, 1, 'no unbounded retry');
  sub.closeAcpOverlays();
});

test('a stream update for the run during a get request makes the later get rejection harmless', async () => {
  listHandler = () => [];
  const conv = 'conv_get_stream';
  const source = enterRoom(conv);
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 1, runs: [] });
  await tick();
  const held = deferred();
  getHandler = () => held.promise;
  sub.openDrawer('acprun_sx1');
  await tick();
  source.send({ type: 'acp.run.updated', seq: 2, run: run('acprun_sx1', 'running', T_NEW, { conversation_id: conv }) });
  await tick();
  held.reject(new Error('get failed after stream update'));
  await tick();
  await tick();
  assert.ok(panelFor('acprun_sx1'), 'streamed run stays visible');
  assert.equal(drawerText().includes(RETRY_TEXT), false, 'stale get error is not shown');
  sub.closeAcpOverlays();
});

test('a snapshot that omits the run makes the in-flight get stale, and a fresh get loads it', async () => {
  listHandler = () => [];
  const conv = 'conv_get_snapshot';
  const source = enterRoom(conv);
  source.open();
  source.send({ type: 'acp.run.snapshot', seq: 1, runs: [] });
  await tick();
  const held = deferred();
  let calls = 0;
  getHandler = async (id) => {
    calls += 1;
    if (calls === 1) return held.promise;
    return run(id, 'completed', T_NEW, { conversation_id: conv });
  };
  sub.openDrawer('acprun_so1');
  await tick();
  source.send({ type: 'acp.run.snapshot', seq: 5, runs: [] });
  held.reject(new Error('stale get failure'));
  await tick();
  await tick();
  assert.equal(calls, 2);
  assert.ok(panelFor('acprun_so1'), 'fresh get loads the run after the snapshot');
  assert.equal(drawerText().includes(RETRY_TEXT), false);
  sub.closeAcpOverlays();
});

for (const live of ['running', 'waiting_permission']) {
  for (const terminal of ['completed', 'failed', 'cancelled']) {
    test(`a strictly older ${terminal} list response cannot settle a streamed ${live} run`, async () => {
      const conv = `conv_older_${live}_${terminal}`;
      const id = `acprun_old_${live.replace('_', '')}${terminal}`;
      listHandler = () => [];
      const source = enterRoom(conv);
      source.open();
      source.send({ type: 'acp.run.snapshot', seq: 1, runs: [run(id, live, T_NEW, { conversation_id: conv, prompt: 'live-prompt' })] });
      await tick();
      sub.openDrawer(id);
      await tick();
      listHandler = () => [run(id, terminal, T_OLD, { conversation_id: conv, prompt: 'stale-terminal-prompt' })];
      sub.setSubagentConversation(conv);
      await tick();
      await tick();
      assert.match(dockMeta(), /1 live/, 'the newer live run stays live');
      assert.ok(panelFor(id)?.textContent.includes('live-prompt'), 'streamed content is kept');
      assert.equal(panelFor(id)?.textContent.includes('stale-terminal-prompt'), false);
      listHandler = () => [];
      sub.closeAcpOverlays();
    });
  }
}
