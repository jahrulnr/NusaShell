// Runtime tests for historical tool-card ownership and round-start idempotency.
// The real agent view is booted once against an index.html fixture with a
// stubbed RPC/EventSource layer, then driven through the public runtime event
// API (rpc.js emit). Assertions read the rendered thread, not source text.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { after, before, test } from 'node:test';
import { JSDOM } from 'jsdom';

const indexHTML = await readFile(new URL('../index.html', import.meta.url), 'utf8');
const dom = new JSDOM(indexHTML, { url: 'http://localhost/', pretendToBeVisual: true });
const { window } = dom;

const conversations = new Map();
const activeTurns = new Map();

// roundStreams maps "run:message" to the controller of the live per-round SSE
// response, so tests push frames exactly as the server would.
const roundStreams = new Map();
const encoder = new TextEncoder();

function roundStreamResponse(url) {
  const params = new URL(url, 'http://localhost').searchParams;
  const key = `${params.get('run_id')}:${params.get('message_id')}`;
  let controller;
  const body = new ReadableStream({ start(c) { controller = c; } });
  roundStreams.set(key, controller);
  return { ok: true, status: 200, body };
}

function sendRoundFrame(runId, messageId, event, data) {
  const controller = roundStreams.get(`${runId}:${messageId}`);
  if (!controller) throw new Error(`no open round stream for ${runId}:${messageId}`);
  controller.enqueue(encoder.encode(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`));
}

function rpcResult(result) {
  return { ok: true, json: async () => ({ ok: true, result }) };
}

class FakeEventSource {
  constructor(url) { this.url = url; this.readyState = 0; }
  close() { this.readyState = 2; }
  addEventListener() {}
  removeEventListener() {}
}

const agent = {};
const rpcModule = {};
// Running tool cards start 1s elapsed timers; track them so the file exits.
const intervals = new Set();
const nativeSetInterval = globalThis.setInterval;
const nativeClearInterval = globalThis.clearInterval;
const nativeSetTimeout = globalThis.setTimeout;

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
  globalThis.requestAnimationFrame = (cb) => setTimeout(cb, 0);
  globalThis.cancelAnimationFrame = (id) => clearTimeout(id);
  globalThis.EventSource = FakeEventSource;
  window.HTMLElement.prototype.scrollIntoView = function scrollIntoView() {};
  window.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  globalThis.fetch = async (url, init = {}) => {
    // Per-round SSE is a GET with no body; answer it before any RPC parsing.
    if (String(url).startsWith('/stream?')) return roundStreamResponse(String(url));
    const body = JSON.parse(init.body);
    if (body.method === 'agent.tools.contracts') return rpcResult({ version: 1, tools: [] });
    if (body.method === 'agent.conversations.list') {
      return rpcResult({ conversations: [...conversations.values()].map((snapshot) => snapshot.conversation) });
    }
    if (body.method === 'agent.turns.active') {
      return rpcResult(activeTurns.get(body.payload.id) ?? { active: false });
    }
    if (body.method === 'agent.conversations.get') {
      const snapshot = conversations.get(body.payload.id);
      if (!snapshot) throw new Error(`unknown conversation ${body.payload.id}`);
      return rpcResult(snapshot);
    }
    return rpcResult({});
  };
  Object.assign(rpcModule, await import('../js/rpc.js'));
  Object.assign(agent, await import('../js/views/agent.js'));
  await agent.initAgent();
});

after(async () => {
  for (const controller of roundStreams.values()) {
    try { controller.close(); } catch { /* already closed */ }
  }
  // The view module keeps async callbacks (stream retries, round fallbacks)
  // that read the DOM globals. This file runs in its own process, so the
  // globals stay installed; only the elapsed-timer intervals are released.
  await new Promise((resolve) => nativeSetTimeout(resolve, 100));
  for (const handle of intervals) nativeClearInterval(handle);
  globalThis.setInterval = nativeSetInterval;
  globalThis.clearInterval = nativeClearInterval;
});

const emit = (type, payload) => rpcModule.emit(type, payload);
const tick = () => new Promise((resolve) => setTimeout(resolve, 5));
const thread = () => document.getElementById('agent-thread');
const cardsFor = (id) => [...thread().querySelectorAll(`[data-tool-call-id="${id}"]`)];

function user(id, content, extra = {}) {
  return { id, role: 'user', content, created_at: new Date().toISOString(), ...extra };
}

function assistantWithCalls(id, calls) {
  return {
    id,
    role: 'assistant',
    created_at: new Date().toISOString(),
    steps: [{ type: 'tool_calls', tool_calls: calls }],
  };
}

async function openFixture(id, messages) {
  conversations.set(id, {
    conversation: { id, title: `Room ${id}`, workspace: '', status: 'idle', model: '', effort: 'auto' },
    messages,
    chunk_count: 0,
  });
  await agent.openConversationExternal(id);
  await tick();
}

test('a late subagent completion for a historical card patches it in place after a later round and a steer', async () => {
  const conv = 'conv_late_subagent';
  await openFixture(conv, [
    user('u1', 'delegate work'),
    assistantWithCalls('a1', [{ id: 'call_hist_sub', name: 'subagent', args: { prompt: 'long task', agent_id: 'acp_dev' }, status: 'running', output: '' }]),
    user('steer1', 'also check tests', { steer: true }),
    assistantWithCalls('a2', [{ id: 'call_hist_exec', name: 'exec', args: { command: 'ls' }, status: 'ok', output: 'listing' }]),
  ]);
  const historicalNode = cardsFor('call_hist_sub')[0].closest('.agent-message');
  assert.ok(historicalNode, 'historical subagent card is mounted');

  emit('agent.turn.started', { run_id: 'run_late_1', message_id: 'a3', round: 1, conversation_id: conv });
  emit('agent.tool.started', { run_id: 'run_late_1', tool_call_id: 'call_now_exec', name: 'exec', args: { command: 'pwd' }, conversation_id: conv });
  await tick();
  emit('agent.tool.completed', {
    run_id: 'run_late_1',
    tool_call_id: 'call_hist_sub',
    name: 'subagent',
    args: { prompt: 'long task', agent_id: 'acp_dev' },
    status: 'ok',
    output: 'Subagent run acprun_historic123 completed. Full result delivered in the subagent_result tool call.',
    conversation_id: conv,
  });
  await tick();

  const cards = cardsFor('call_hist_sub');
  assert.equal(cards.length, 1, 'exactly one card for the historical call');
  assert.equal(cards[0].closest('.agent-message'), historicalNode, 'the card stays in its original assistant node');
  assert.equal(cards[0].dataset.runId, 'acprun_historic123', 'completion output patched the historical card');
  assert.equal(cardsFor('call_now_exec').length, 1);
  assert.equal(cardsFor('call_now_exec')[0].closest('.agent-message')?.dataset.messageIds?.includes('a3') ?? false, true, 'current tool stays in the current round');
});

test('a late generic completion for a historical terminal updates it instead of appending a copy', async () => {
  const conv = 'conv_late_generic';
  await openFixture(conv, [
    user('u1', 'list files'),
    assistantWithCalls('a1', [{ id: 'call_hist_term', name: 'exec', args: { command: 'du -sh' }, status: 'running', output: '' }]),
    assistantWithCalls('a2', [{ id: 'call_next_term', name: 'exec', args: { command: 'true' }, status: 'ok', output: 'ok' }]),
  ]);
  const historicalNode = cardsFor('call_hist_term')[0].closest('.agent-message');

  emit('agent.turn.started', { run_id: 'run_late_2', message_id: 'a3', round: 1, conversation_id: conv });
  emit('agent.tool.started', { run_id: 'run_late_2', tool_call_id: 'call_cur_term', name: 'exec', args: { command: 'date' }, conversation_id: conv });
  await tick();
  emit('agent.tool.completed', {
    run_id: 'run_late_2',
    tool_call_id: 'call_hist_term',
    name: 'exec',
    args: { command: 'du -sh' },
    status: 'ok',
    output: 'late-output-marker',
    conversation_id: conv,
  });
  await tick();

  const cards = cardsFor('call_hist_term');
  assert.equal(cards.length, 1, 'no copy of the historical terminal');
  assert.equal(cards[0].closest('.agent-message'), historicalNode);
  assert.match(cards[0].textContent, /late-output-marker/);
  assert.equal(cards[0].dataset.status, 'ok');
});

test('a late started event for an already-settled historical card does not turn it running or change the current round', async () => {
  const conv = 'conv_late_started';
  await openFixture(conv, [
    user('u1', 'run tool'),
    assistantWithCalls('a1', [{ id: 'call_settled', name: 'exec', args: { command: 'echo hi' }, status: 'ok', output: 'hi' }]),
  ]);
  const card = cardsFor('call_settled')[0];
  assert.equal(card.dataset.status, 'ok');

  emit('agent.turn.started', { run_id: 'run_late_3', message_id: 'a2', round: 1, conversation_id: conv });
  emit('agent.tool.started', { run_id: 'run_late_3', tool_call_id: 'call_settled', name: 'exec', args: { command: 'echo hi' }, conversation_id: conv });
  await tick();

  assert.equal(cardsFor('call_settled').length, 1);
  assert.equal(cardsFor('call_settled')[0], card, 'same card node');
  assert.equal(card.dataset.status, 'ok', 'settled card keeps its status');
  assert.equal(card._elapsedTimer ?? null, null, 'no elapsed timer was attached to the historical card');
});

const roundCount = () => thread().querySelectorAll('.agent-round').length;

test('a duplicate turn.started after a tool-only round does not open another round or drop the running tool', async () => {
  const conv = 'conv_dup_start';
  await openFixture(conv, [user('u1', 'run tools')]);
  emit('agent.turn.started', { run_id: 'run_dup_1', message_id: 'a_dup_1', round: 1, conversation_id: conv });
  emit('agent.tool.started', { run_id: 'run_dup_1', tool_call_id: 'call_dup_tool', name: 'exec', args: { command: 'ls' }, conversation_id: conv });
  await tick();
  const rounds = roundCount();
  const card = cardsFor('call_dup_tool')[0];

  emit('agent.turn.started', { run_id: 'run_dup_1', message_id: 'a_dup_1', round: 1, conversation_id: conv });
  await tick();

  assert.equal(roundCount(), rounds, 'duplicate start does not add a round');
  assert.equal(cardsFor('call_dup_tool').length, 1);
  assert.equal(cardsFor('call_dup_tool')[0], card, 'running tool card is not remounted');
});

test('a stale round start after a newer round does not rewind the run', async () => {
  const conv = 'conv_stale_start';
  await openFixture(conv, [user('u1', 'two rounds')]);
  emit('agent.turn.started', { run_id: 'run_stale_1', message_id: 'm_s1', round: 1, conversation_id: conv });
  emit('agent.tool.started', { run_id: 'run_stale_1', tool_call_id: 'call_s1', name: 'exec', args: { command: 'a' }, conversation_id: conv });
  emit('agent.turn.started', { run_id: 'run_stale_1', message_id: 'm_s2', round: 2, conversation_id: conv });
  emit('agent.tool.started', { run_id: 'run_stale_1', tool_call_id: 'call_s2', name: 'exec', args: { command: 'b' }, conversation_id: conv });
  await tick();
  const rounds = roundCount();

  emit('agent.turn.started', { run_id: 'run_stale_1', message_id: 'm_s1', round: 1, conversation_id: conv });
  await tick();

  assert.equal(roundCount(), rounds, 'stale round 1 start opens no section');
  assert.equal(cardsFor('call_s1').length, 1);
  assert.equal(cardsFor('call_s2').length, 1);
  assert.equal(cardsFor('call_s2')[0].isConnected, true, 'current round tool stays mounted');
});

test('a turn.started for a persisted historical message is ignored', async () => {
  const conv = 'conv_hist_start';
  await openFixture(conv, [
    user('u1', 'hello'),
    { id: 'hist_done', role: 'assistant', created_at: new Date().toISOString(), steps: [{ type: 'text', content: 'finished answer' }] },
  ]);
  const node = thread().querySelector('.agent-message.assistant[data-message-ids~="hist_done"]');
  const rounds = roundCount();
  const dots = thread().querySelectorAll('.agent-thinking-dots').length;

  emit('agent.turn.started', { run_id: 'run_hist_start_1', message_id: 'hist_done', round: 1, conversation_id: conv });
  await tick();

  assert.equal(roundCount(), rounds, 'historical start adds no round');
  assert.equal(thread().querySelectorAll('.agent-thinking-dots').length, dots, 'no placeholder dots');
  assert.equal(thread().querySelector('.agent-message.assistant[data-message-ids~="hist_done"]'), node);
});

test('a duplicate start for the snapshot-adopted current round after a tool-only round is ignored', async () => {
  const conv = 'conv_adopt_start';
  activeTurns.set(conv, { active: true, run_id: 'run_adopt_1', message_id: 'adopt_m1' });
  conversations.set(conv, {
    conversation: { id: conv, title: 'Adopted', workspace: '', status: 'running', model: '', effort: 'auto' },
    messages: [user('u1', 'adopt me')],
    chunk_count: 0,
  });
  await agent.openConversationExternal(conv);
  await tick();
  emit('agent.tool.started', { run_id: 'run_adopt_1', tool_call_id: 'call_adopt_tool', name: 'exec', args: { command: 'ls' }, conversation_id: conv });
  await tick();
  const rounds = roundCount();

  emit('agent.turn.started', { run_id: 'run_adopt_1', message_id: 'adopt_m1', round: 1, conversation_id: conv });
  await tick();

  assert.equal(roundCount(), rounds, 'adopted round is not reopened');
  assert.equal(cardsFor('call_adopt_tool').length, 1);
  activeTurns.delete(conv);
});

test('a deferred next-round start applies once after the queued steer is applied', async () => {
  const conv = 'conv_deferred_start';
  await openFixture(conv, [user('u1', 'steer me')]);
  emit('agent.turn.started', { run_id: 'run_def_1', message_id: 'm_d1', round: 1, conversation_id: conv });
  emit('agent.tool.started', { run_id: 'run_def_1', tool_call_id: 'call_d1', name: 'exec', args: { command: 'ls' }, conversation_id: conv });
  await tick();
  emit('agent.steer.queued', { conversation_id: conv, steer_id: 'steer_d', text: 'change plan' });
  emit('agent.turn.started', { run_id: 'run_def_1', message_id: 'm_d2', round: 2, conversation_id: conv });
  await tick();
  const beforeApply = roundCount();

  emit('agent.steer.applied', { conversation_id: conv, steer_id: 'steer_d', text: 'change plan' });
  await tick();
  await tick();

  assert.equal(roundCount(), beforeApply + 1, 'deferred round opens exactly once');
  const steer = thread().querySelector('.agent-message.user.agent-steer');
  const round = thread().querySelector('.agent-message.assistant[data-message-ids~="m_d2"] .agent-round');
  assert.ok(steer && round, 'steer and deferred round are mounted');
  assert.ok(steer.compareDocumentPosition(round) & window.Node.DOCUMENT_POSITION_FOLLOWING, 'new round follows the steer');

  emit('agent.turn.started', { run_id: 'run_def_1', message_id: 'm_d2', round: 2, conversation_id: conv });
  await tick();
  assert.equal(roundCount(), beforeApply + 1, 'applied start is not applied twice');
});

test('a subagent card that settles before a steer keeps its round, and the later async completion patches that card', async () => {
  const conv = 'conv_steer_sub';
  await openFixture(conv, [user('u1', 'delegate and steer')]);
  emit('agent.turn.started', { run_id: 'run_ss_1', message_id: 'm_ss1', round: 1, conversation_id: conv });
  emit('agent.tool.started', { run_id: 'run_ss_1', tool_call_id: 'call_ss_sub', name: 'subagent', args: { prompt: 'p', agent_id: 'acp_dev' }, conversation_id: conv });
  await tick();
  emit('agent.tool.completed', {
    run_id: 'run_ss_1',
    tool_call_id: 'call_ss_sub',
    name: 'subagent',
    args: { prompt: 'p', agent_id: 'acp_dev' },
    status: 'ok',
    output: 'Subagent run acprun_ssfirst1 completed. Full result delivered in the subagent_result tool call.',
    conversation_id: conv,
  });
  await tick();
  const card = cardsFor('call_ss_sub')[0];
  assert.equal(card.dataset.standalone, 'true');
  const firstNode = card.closest('.agent-message');

  emit('agent.steer.queued', { conversation_id: conv, steer_id: 'ss_1', text: 'also check' });
  emit('agent.steer.applied', { conversation_id: conv, steer_id: 'ss_1', text: 'also check' });
  await tick();
  const steer = thread().querySelector('.agent-message.user.agent-steer');
  assert.ok(steer, 'steer bubble is mounted');
  assert.ok(card.isConnected && card.closest('.agent-round'), 'settled subagent card keeps its round after the steer seal');
  assert.ok(card.compareDocumentPosition(steer) & window.Node.DOCUMENT_POSITION_FOLLOWING, 'card stays before the steer');

  emit('agent.turn.started', { run_id: 'run_ss_1', message_id: 'm_ss2', round: 2, conversation_id: conv });
  await tick();
  emit('agent.tool.completed', {
    run_id: 'run_ss_1',
    tool_call_id: 'call_ss_sub',
    name: 'subagent',
    args: { prompt: 'p', agent_id: 'acp_dev' },
    status: 'ok',
    output: 'Subagent run acprun_ssdone1 completed. Full result delivered in the subagent_result tool call.',
    conversation_id: conv,
  });
  await tick();

  const cards = cardsFor('call_ss_sub');
  assert.equal(cards.length, 1, 'one card for the async completion');
  assert.equal(cards[0].dataset.runId, 'acprun_ssdone1');
  assert.equal(cards[0].closest('.agent-message'), firstNode, 'the card stays in its original round');
});

test('a delayed started event after a completed current terminal does not revive status, output, or timer', async () => {
  const conv = 'conv_settled_generic';
  await openFixture(conv, [user('u1', 'run then settle')]);
  emit('agent.turn.started', { run_id: 'run_set_1', message_id: 'm_set1', round: 1, conversation_id: conv });
  emit('agent.tool.started', { run_id: 'run_set_1', tool_call_id: 'call_set_term', name: 'exec', args: { command: 'build' }, conversation_id: conv });
  await tick();
  emit('agent.tool.completed', { run_id: 'run_set_1', tool_call_id: 'call_set_term', name: 'exec', args: { command: 'build' }, status: 'ok', output: 'final-build-output', conversation_id: conv });
  await tick();
  const card = cardsFor('call_set_term')[0];
  assert.equal(card.dataset.status, 'ok');

  emit('agent.tool.started', { run_id: 'run_set_1', tool_call_id: 'call_set_term', name: 'exec', args: { command: 'build' }, conversation_id: conv });
  await tick();

  assert.equal(cardsFor('call_set_term').length, 1);
  assert.equal(cardsFor('call_set_term')[0], card);
  assert.equal(card.dataset.status, 'ok', 'settled status is not reset to running');
  assert.equal(card._elapsedTimer ?? null, null, 'no elapsed timer is started');
  assert.match(card.textContent, /final-build-output/);
});

test('a delayed started event after a completed standalone subagent card does not revive it', async () => {
  const conv = 'conv_settled_sub';
  await openFixture(conv, [user('u1', 'delegate and settle')]);
  emit('agent.turn.started', { run_id: 'run_set_2', message_id: 'm_set2', round: 1, conversation_id: conv });
  emit('agent.tool.started', { run_id: 'run_set_2', tool_call_id: 'call_set_sub', name: 'subagent', args: { prompt: 'p', agent_id: 'acp_dev' }, conversation_id: conv });
  await tick();
  emit('agent.tool.completed', {
    run_id: 'run_set_2',
    tool_call_id: 'call_set_sub',
    name: 'subagent',
    args: { prompt: 'p', agent_id: 'acp_dev' },
    status: 'ok',
    output: 'Subagent run acprun_setdone1 completed. Full result delivered in the subagent_result tool call.',
    conversation_id: conv,
  });
  await tick();
  const card = cardsFor('call_set_sub')[0];
  assert.equal(card.dataset.status, 'ok');
  assert.equal(card.dataset.runId, 'acprun_setdone1');

  emit('agent.tool.started', { run_id: 'run_set_2', tool_call_id: 'call_set_sub', name: 'subagent', args: { prompt: 'p', agent_id: 'acp_dev' }, conversation_id: conv });
  await tick();

  assert.equal(cardsFor('call_set_sub').length, 1);
  assert.equal(cardsFor('call_set_sub')[0], card);
  assert.equal(card.dataset.status, 'ok');
  assert.equal(card.dataset.runId, 'acprun_setdone1');
});

// waitFor polls explicit rendered state until it holds, failing with a label
// instead of depending on a fixed delay.
async function waitFor(predicate, label) {
  const deadline = Date.now() + 2000;
  while (!predicate()) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${label}`);
    await tick();
  }
}

test('round.done next opens the next round before a delayed old turn.started, and the next round text survives it', async () => {
  const conv = 'conv_transport_next';
  await openFixture(conv, [user('u1', 'two rounds over SSE')]);
  emit('agent.turn.started', { run_id: 'run_tr_1', message_id: 'm_tr1', round: 1, conversation_id: conv });
  await waitFor(() => roundStreams.has('run_tr_1:m_tr1'), 'round 1 stream');
  sendRoundFrame('run_tr_1', 'm_tr1', 'round.delta', { seq: 1, kind: 'text', text: 'first round text' });
  sendRoundFrame('run_tr_1', 'm_tr1', 'round.done', { last_seq: 1, next: { run_id: 'run_tr_1', message_id: 'm_tr2', round: 2 } });
  await waitFor(() => roundStreams.has('run_tr_1:m_tr2'), 'round 2 stream');
  sendRoundFrame('run_tr_1', 'm_tr2', 'round.delta', { seq: 1, kind: 'text', text: 'second round text' });
  await waitFor(() => thread().textContent.includes('second round text'), 'second round text painted');
  const rounds = roundCount();

  emit('agent.turn.started', { run_id: 'run_tr_1', message_id: 'm_tr1', round: 1, conversation_id: conv });
  await tick();

  assert.equal(roundCount(), rounds, 'delayed old start opens no round');
  assert.match(thread().textContent, /first round text/);
  assert.match(thread().textContent, /second round text/);
});

test('a completion that arrives before the SSE initial tool frame is not revived by it, and trailing text still paints', async () => {
  const conv = 'conv_transport_completion_first';
  await openFixture(conv, [user('u1', 'complete first')]);
  emit('agent.turn.started', { run_id: 'run_cp_1', message_id: 'm_cp1', round: 1, conversation_id: conv });
  await waitFor(() => roundStreams.has('run_cp_1:m_cp1'), 'round stream');
  emit('agent.tool.completed', { run_id: 'run_cp_1', tool_call_id: 'call_cp', name: 'exec', args: { command: 'x' }, status: 'ok', output: 'ws-final-output', conversation_id: conv });
  await waitFor(() => cardsFor('call_cp').length === 1, 'completed card mounted');
  const card = cardsFor('call_cp')[0];

  sendRoundFrame('run_cp_1', 'm_cp1', 'round.delta', { seq: 1, kind: 'tool', tool_call_id: 'call_cp', name: 'exec', args: { command: 'x' } });
  sendRoundFrame('run_cp_1', 'm_cp1', 'round.delta', { seq: 2, kind: 'text', text: 'trailing text' });
  await waitFor(() => thread().textContent.includes('trailing text'), 'trailing text painted');

  assert.equal(cardsFor('call_cp').length, 1);
  assert.equal(cardsFor('call_cp')[0], card);
  assert.equal(card.dataset.status, 'ok', 'late SSE initial frame does not reset status');
  assert.match(card.textContent, /ws-final-output/);
  assert.equal(card._elapsedTimer ?? null, null);
});

test('a deferred next-round start keeps its queued tool lifecycle and output until the steer applies', async () => {
  const conv = 'conv_transport_deferred';
  await openFixture(conv, [user('u1', 'steer while tooling')]);
  emit('agent.turn.started', { run_id: 'run_df_1', message_id: 'm_df1', round: 1, conversation_id: conv });
  await waitFor(() => roundStreams.has('run_df_1:m_df1'), 'round 1 stream');
  sendRoundFrame('run_df_1', 'm_df1', 'round.delta', { seq: 1, kind: 'text', text: 'before steer' });
  await waitFor(() => thread().textContent.includes('before steer'), 'pre-steer text');

  emit('agent.steer.queued', { conversation_id: conv, steer_id: 'st_df', text: 'pivot' });
  emit('agent.turn.started', { run_id: 'run_df_1', message_id: 'm_df2', round: 2, conversation_id: conv });
  emit('agent.tool.started', { run_id: 'run_df_1', tool_call_id: 'call_df', name: 'exec', args: { command: 'ls' }, conversation_id: conv });
  emit('agent.tool.completed', { run_id: 'run_df_1', tool_call_id: 'call_df', name: 'exec', args: { command: 'ls' }, status: 'ok', output: 'deferred-output', conversation_id: conv });
  await tick();
  assert.equal(cardsFor('call_df').length, 0, 'nothing is mounted before the steer applies');

  emit('agent.steer.applied', { conversation_id: conv, steer_id: 'st_df', text: 'pivot' });
  await waitFor(() => cardsFor('call_df').length === 1, 'queued tool mounted after the steer');
  const card = cardsFor('call_df')[0];
  const steer = thread().querySelector('.agent-message.user.agent-steer');
  const roundTwo = thread().querySelector('.agent-message.assistant[data-message-ids~="m_df2"]');
  assert.equal(card.dataset.status, 'ok');
  assert.match(card.textContent, /deferred-output/);
  assert.equal(card.closest('.agent-message'), roundTwo, 'the queued tool belongs to the deferred round');
  assert.ok(steer.compareDocumentPosition(roundTwo) & window.Node.DOCUMENT_POSITION_FOLLOWING, 'the deferred round follows the steer');
});
