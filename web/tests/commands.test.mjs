// Native slash commands against a scripted server: the catalog the room
// reads, the "/" menu's rows, and the dispatch that turns a draft's first
// line into an action instead of a turn.
import { calls, mockApi, sleep, sockets } from './env.mjs';
import { before, beforeEach, test } from 'node:test';
import assert from 'node:assert/strict';
import { state } from '../lib/store.js';
import * as chat from '../lib/chat.js';
import { parseCommand, isNativeCommand, commandChoices, catalogRow, firstEnabled, stepRow } from '../lib/commands.js';

let ws;
const sentTurns = () => ws.sent.filter((c) => c.type === 'turn.send');
const commandPosts = () => calls.filter((c) => c.method === 'POST' && c.url.endsWith('/commands'));
const catalogReads = () => calls.filter((c) => c.method === 'GET' && c.url.startsWith('/api/commands'));

const native = (name, extra = {}) => ({ name, description: `${name} the room`, hint: '', source: 'native', enabled: true, reason: '', ...extra });
const agent = (name, extra = {}) => ({ name, description: `${name} with the agent`, hint: `/${name}`, source: 'agent', enabled: true, reason: '', ...extra });
const catalog = (...rows) => ({ data: rows });

// serveRoom answers the reads a thread view makes — for every id given, plus
// the catalog — and overlays `extra` (a command call, for one). Room routes
// come before the list route, which is a prefix of every thread path.
function serveRoom(ids, extra = {}, rows = [native('compact'), native('clear'), native('fork')]) {
  const table = {};
  for (const id of [].concat(ids)) {
    table[`GET /api/conversations/${id}/messages`] = { messages: [{ id: 'm_' + id, role: 'user', content: 'first' }], effectiveStart: 0, total: 1 };
    table[`GET /api/conversations/${id}`] = { id, title: id === 'c_1' ? 'Thread' : 'Thread (fork)', providerId: 'p', modelId: 'm', workspace: '' };
  }
  Object.assign(table, {
    'GET /api/commands': () => catalog(...rows),
    'GET /api/context': { estimate: 0, total: 200_000 },
    'GET /api/conversations': { data: [], total: 0, hasMore: false },
    'GET /api/projects': { data: [] },
  });
  mockApi({ ...table, ...extra });
}

async function openThread(id = 'c_1', extra = {}, rows) {
  serveRoom([id], extra, rows);
  chat.navigate('#/c/' + id);
  await sleep(30);
  return state.rooms[id];
}

before(() => {
  mockApi();
  chat.start();
  ws = sockets[0];
  ws.open();
});

beforeEach(() => {
  state.rooms = {};
  state.toasts = [];
  state.confirm = null;
  state.newProjectId = '';
  state.agents.items = [];
  ws.sent.length = 0;
  chat.navigate('#/');
});

// ---------- recognition ----------

test('a command is the draft\'s first line; a path or prose is not', () => {
  assert.deepEqual(parseCommand('  /compact  '), { name: 'compact', args: '' });
  assert.deepEqual(parseCommand('/clear extra'), { name: 'clear', args: 'extra' });
  // Text under the command counts as tokens: the server refuses it instead of
  // the command quietly swallowing the owner's message.
  assert.deepEqual(parseCommand('/compact\nhello'), { name: 'compact', args: 'hello' });
  assert.equal(parseCommand('/home/user/file.go'), null);
  assert.equal(parseCommand('hello /compact'), null);
  assert.equal(parseCommand('no command here'), null);

  const room = { catalog: { status: 'ready', items: [native('compact')] } };
  assert.equal(isNativeCommand(room, 'compact'), true);
  assert.equal(isNativeCommand(room, 'Compact'), false, 'names are case-sensitive');
  assert.equal(isNativeCommand(room, 'review'), false);
  // A native command the server adds later is reserved without a client change.
  assert.equal(isNativeCommand({ catalog: { items: [native('rewind')] } }, 'rewind'), true);
  // clear and fork stay reserved until the catalog lists them. compact does not:
  // putting it back on a client list would make the empty-catalog line true.
  assert.equal(isNativeCommand({ catalog: { items: [] } }, 'clear'), true);
  assert.equal(isNativeCommand({ catalog: { items: [] } }, 'fork'), true);
  assert.equal(isNativeCommand({ catalog: { items: [] } }, 'compact'), false);
  // The catalog's first row decides. A later native row does not override it,
  // and an agent that claims a thread command wins.
  assert.equal(isNativeCommand({ catalog: { items: [agent('compact'), native('compact')] } }, 'compact'), false);
  assert.equal(isNativeCommand({ catalog: { items: [agent('clear')] } }, 'clear'), false);
});

test('the menu follows the catalog, and a native row does not replace an agent row', () => {
  state.agents.items = [{ id: 'claude', name: 'Claude' }];
  const room = {
    info: { agentId: 'claude' },
    catalog: { status: 'ready', items: [agent('compact'), native('clear'), native('fork', { enabled: false, reason: 'Nothing to fork yet' })] },
  };
  const rows = commandChoices(room);
  assert.deepEqual(rows.map((c) => [c.name, c.source, c.enabled, c.icon]), [
    ['compact', 'Claude', true, 'bot'],
    ['clear', 'NusaShell', true, 'slash'],
    ['fork', 'NusaShell', false, 'slash'],
  ]);
  assert.equal(rows[2].reason, 'Nothing to fork yet');
  assert.equal(rows[0].native, false);
  const both = commandChoices({
    info: { agentId: 'claude' },
    catalog: { items: [agent('compact'), native('compact'), native('fork')] },
  });
  assert.deepEqual(both.map((c) => [c.name, c.native]), [
    ['compact', false],
    ['compact', true],
    ['fork', true],
  ]);
  assert.deepEqual(commandChoices(room, 'fo').map((c) => c.name), ['fork']);
  assert.deepEqual(commandChoices(room, 'zz').map((c) => c.name), []);
  assert.equal(catalogRow(room, 'compact').source, 'agent');
});

test('the keyboard walks past a row that cannot run', () => {
  const rows = [native('compact', { enabled: false }), native('clear'), native('fork')];
  assert.equal(firstEnabled(rows), 1);
  assert.equal(stepRow(rows, 1, 1), 2);
  assert.equal(stepRow(rows, 2, 1), 1, 'a disabled row is never the selection');
  assert.equal(stepRow(rows, 1, -1), 2);
  assert.equal(stepRow([native('compact', { enabled: false })], 0, 1), 0, 'nothing to move to stays put');
});

// ---------- dispatch ----------

test('the room\'s catalog is read by conversation', async () => {
  await openThread('c_1');
  const read = catalogReads().at(-1);
  assert.equal(read.url, '/api/commands?conversationId=c_1');
});

test('an agent-reported compact is a turn, and a hardcoded compact would send it to runCommand', async () => {
  const r = await openThread('c_1', {}, [agent('compact'), native('clear'), native('fork')]);
  assert.equal(isNativeCommand(r, 'compact'), false);
  assert.equal(isNativeCommand(r, 'clear'), true);
  r.draft = '/compact';
  assert.equal(chat.submit(r, '/compact'), true, 'the agent receives /compact as a turn');
  await sleep(30);
  assert.equal(commandPosts().length, 0, 'it must not call the native command endpoint');
  assert.equal(sentTurns().at(-1).payload.content, '/compact');
});

test('Enter on /compact runs the command and never sends a turn', async () => {
  const r = await openThread('c_1', {
    'POST /api/conversations/c_1/commands': () => ({ command: 'compact', conversation: { id: 'c_1', title: 'Thread', providerId: 'p', modelId: 'm' } }),
  });
  r.draft = '/compact';
  assert.equal(chat.submit(r, '/compact'), false, 'a command is not a send');
  assert.equal(r.command?.phase, 'running', 'it becomes a receipt, not a turn');
  assert.equal(r.command?.text, '/compact');

  await sleep(30);
  const posts = commandPosts();
  assert.equal(posts.length, 1);
  assert.equal(posts[0].url, '/api/conversations/c_1/commands');
  assert.deepEqual(posts[0].body, { name: 'compact', args: '', confirm: false });
  assert.equal(sentTurns().length, 0, 'no turn may carry a recognized command');
  assert.equal(r.draft, '', 'the command is spent from the draft by submit');
  assert.equal(r.command, null, 'and the receipt clears when it settles');
  assert.equal(state.toasts.at(-1).message, 'History compacted.');
});

test('a name outside the catalog is ordinary text and still becomes a turn', async () => {
  const r = await openThread('c_1');
  assert.equal(chat.submit(r, '/tidak-ada'), true);
  const cmd = sentTurns().at(-1);
  assert.equal(cmd.payload.content, '/tidak-ada');
  assert.equal('references' in cmd.payload, false);
  assert.equal(commandPosts().length, 0);

  // A path is not a command either.
  r.pendingOpen = null;
  assert.equal(chat.submit(r, '/home/user/notes.md'), true);
  assert.equal(sentTurns().at(-1).payload.content, '/home/user/notes.md');
  assert.equal(commandPosts().length, 0);
});

test('clear asks first, retries with the confirm word, and keeps the draft when cancelled', async () => {
  let posts = 0;
  let cleared = false;
  serveRoom(['c_1'], {
    'GET /api/conversations/c_1/messages': () => ({ messages: cleared ? [] : [{ id: 'm1', role: 'user', content: 'first' }], effectiveStart: 0, total: cleared ? 0 : 1 }),
    'POST /api/conversations/c_1/commands': (call) => {
      posts++;
      if (!call.body.confirm) return [409, { code: 'confirmation_required', detail: 'clear requires confirm' }];
      cleared = true;
      return { command: 'clear', conversation: { id: 'c_1', title: 'Thread', providerId: 'p', modelId: 'm' } };
    },
  });
  chat.navigate('#/c/c_1');
  await sleep(30);
  const r = state.rooms.c_1;

  r.draft = '/clear';
  chat.submit(r, '/clear');
  await sleep(20);
  assert.equal(posts, 1);
  assert.equal(state.confirm?.title, 'Clear this thread?');
  assert.equal(state.confirm?.confirm, 'Clear');
  assert.equal(state.confirm?.danger, true);
  assert.equal(r.draft, '', 'the command left the box when it was sent');

  state.confirm.resolve(false);
  state.confirm = null;
  await sleep(20);
  assert.equal(posts, 1, 'cancelling sends nothing');
  assert.equal(r.draft, '/clear', 'and the word comes back to the box');
  assert.equal(r.command, null);

  chat.submit(r, '/clear');
  await sleep(20);
  state.confirm.resolve(true);
  state.confirm = null;
  await sleep(40);
  // One call to learn the command needs confirming, one more with the word.
  assert.equal(posts, 3);
  assert.deepEqual(commandPosts().at(-1).body, { name: 'clear', args: '', confirm: true });
  assert.equal(r.draft, '');
  assert.deepEqual(r.messages, [], 'the emptied history is read back');
  assert.equal(state.toasts.at(-1).message, 'Thread cleared.');
});

test('fork opens the new thread the server answers with', async () => {
  // The new room's own reads are served too: the fork is opened at once.
  serveRoom(['c_1', 'c_2'], {
    'POST /api/conversations/c_1/commands': () => ({ command: 'fork', conversation: { id: 'c_2', title: 'Thread (fork)', providerId: 'p', modelId: 'm' } }),
  });
  chat.navigate('#/c/c_1');
  await sleep(30);
  const r = state.rooms.c_1;
  r.draft = '/fork';
  chat.submit(r, '/fork');
  await sleep(400);

  assert.equal(sentTurns().length, 0);
  assert.equal(location.hash, '#/c/c_2');
  assert.equal(state.rooms.c_2.info.title, 'Thread (fork)');
  assert.equal(state.rooms.c_2.infoStatus, 'ready');
  assert.equal(r.draft, '');
  assert.equal(state.toasts.at(-1).message, 'Thread forked.');
  assert.ok(calls.some((c) => c.method === 'GET' && c.url.startsWith('/api/projects')), 'the project counts learn about the fork');
});

test('a command the room reports as unavailable is refused locally, saying why', async () => {
  const r = await openThread('c_1', {}, [native('compact', { enabled: false, reason: 'Nothing to compact yet' }), native('clear'), native('fork')]);
  r.draft = '/compact';
  chat.submit(r, '/compact');
  await sleep(20);
  assert.equal(commandPosts().length, 0, 'the server never has to refuse what its catalog already did');
  assert.deepEqual([state.toasts.at(-1).kind, state.toasts.at(-1).message], ['info', 'Nothing to compact yet']);
  assert.equal(r.draft, '/compact', 'a refusal nobody sent keeps the word');
  assert.equal(r.command, null);
});

test('a busy room refuses the command and keeps the draft', async () => {
  const r = await openThread('c_1', {
    'POST /api/conversations/c_1/commands': () => [409, { code: 'busy', detail: 'Wait for the turn to finish' }],
  });
  r.draft = '/compact';
  chat.submit(r, '/compact');
  await sleep(30);
  assert.deepEqual([state.toasts.at(-1).kind, state.toasts.at(-1).message], ['error', 'Wait for the turn to finish']);
  assert.equal(r.draft, '/compact', 'the box was free, so the word goes back to it');
  assert.equal(r.command, null);
  assert.equal(sentTurns().length, 0);
});

test('a server without the command endpoint answers, and the draft survives it', async () => {
  const r = await openThread('c_1');
  r.draft = '/compact';
  chat.submit(r, '/compact');
  await sleep(30);
  // No route: the call is a 404 problem+json — the server answered, so the
  // command certainly did not run, and the box gets its word back.
  assert.equal(state.toasts.at(-1).kind, 'error');
  assert.match(state.toasts.at(-1).message, /not found/i);
  assert.equal(r.draft, '/compact');
  assert.equal(r.command, null);
  assert.equal(sentTurns().length, 0);
});

test('a command in the composer needs a thread first', async () => {
  mockApi({});
  const home = chat.freshRoom();
  // No room yet: the catalog the server answers without a conversation id,
  // every native row off. compact is reserved because that row says so.
  home.catalog = {
    status: 'ready',
    items: [native('compact', { enabled: false, reason: 'Send a message first.' }), native('clear'), native('fork')],
  };
  home.draft = '/compact';
  chat.submit(home, '/compact');
  await sleep(20);
  assert.equal(commandPosts().length, 0);
  assert.deepEqual([state.toasts.at(-1).kind, state.toasts.at(-1).message], ['info', 'Send a message first.']);
  assert.equal(home.draft, '/compact');
  assert.equal(sentTurns().length, 0);
});

// ---------- /compact while it runs: the composer is free, the receipt is not ----------

const running = (name = 'compact') => ({ name, text: `/${name}`, args: '', phase: 'running', error: '', retry: true, startedAt: Date.now() });

test('a message typed while a command runs waits in the queue', async () => {
  const r = await openThread('c_1');
  r.command = running();
  assert.equal(chat.submit(r, 'hello'), true);
  assert.equal(r.queue.length, 1, 'queued, not sent into a refusal');
  assert.equal(sentTurns().length, 0, 'no turn.send while the command holds the room');

  // the same room, idle: it goes out as a turn again
  r.command = null;
  assert.equal(chat.submit(r, 'again'), true);
  assert.equal(sentTurns().length, 1);
});

test('the draft leaves the box once, when the command is sent', async () => {
  let release;
  const gate = new Promise((res) => { release = res; });
  const r = await openThread('c_1', {
    'POST /api/conversations/c_1/commands': async () => { await gate; return { command: 'compact' }; },
  });
  r.draft = '/compact';
  r.files = [{ key: 'k1', status: 'ready', id: 'u_1', name: 'a.png' }];
  r.refs = [{ start: 0, end: 8, label: '/compact', kind: 'command' }];
  assert.equal(chat.submit(r, '/compact'), false);
  assert.equal(r.draft, '', 'spent by submit, on the tick it was sent');
  assert.deepEqual(r.refs, []);
  assert.equal(r.files.length, 1, 'attachments belong to the next message');
  assert.equal(r.command?.phase, 'running');
  assert.equal(r.command?.text, '/compact');

  release();
  await sleep(50);
  assert.equal(r.draft, '', 'and not emptied a second time when the reply lands');
  assert.equal(r.command, null);
});

test('a refusal while the owner is typing leaves the receipt, not the draft', async () => {
  let release;
  const gate = new Promise((res) => { release = res; });
  const r = await openThread('c_1', {
    'POST /api/conversations/c_1/commands': async () => {
      await gate;
      return [400, { code: 'nothing_to_compact', detail: 'Nothing to compact yet' }];
    },
  });
  chat.submit(r, '/compact');
  r.draft = 'hello';
  release();
  await sleep(50);
  assert.equal(r.draft, 'hello', 'what the owner wrote is never touched');
  assert.equal(r.command?.phase, 'failed');
  assert.equal(r.command?.error, 'Nothing to compact yet');
  assert.equal(r.command?.retry, true, 'a named refusal can be retried');
  assert.equal(state.toasts.length, 0, 'the receipt says it; no toast on top');
});

test('an unanswered command puts its word back, and offers no retry', async () => {
  const r = await openThread('c_1', {
    'POST /api/conversations/c_1/commands': () => { throw new Error('offline'); },
  });
  chat.submit(r, '/compact');
  await sleep(50);
  assert.equal(r.draft, '/compact', 'the box was free, so the word comes back');
  assert.equal(r.command, null, 'no receipt is left behind');
  assert.equal(state.toasts.at(-1)?.message, 'Could not confirm /compact — check the thread.');
  assert.doesNotMatch(JSON.stringify(state.toasts.at(-1)), /retry/i);
});

test('an unanswered command while the box is busy keeps the receipt without retry', async () => {
  let release;
  const gate = new Promise((res) => { release = res; });
  const r = await openThread('c_1', {
    'POST /api/conversations/c_1/commands': async () => { await gate; throw new Error('offline'); },
  });
  chat.submit(r, '/clear');
  r.draft = 'hello';
  release();
  await sleep(50);
  assert.equal(r.draft, 'hello');
  assert.equal(r.command?.phase, 'failed');
  assert.equal(r.command?.retry, false, 'an unknown outcome is never resent blindly');
  assert.equal(state.toasts.length, 0);
});

test('the queue moves when the command settles, not before', async () => {
  let release;
  const gate = new Promise((res) => { release = res; });
  const r = await openThread('c_1', {
    'POST /api/conversations/c_1/commands': async () => { await gate; return { command: 'compact' }; },
  });
  chat.submit(r, '/compact');
  r.queue = [{ id: 'q1', text: 'queued hello', references: [], files: [], recording: null }];
  assert.equal(chat.resumeQueue(r), undefined);
  assert.equal(sentTurns().length, 0, 'nothing sends while the command runs');
  release();
  await sleep(60);
  assert.equal(sentTurns().length, 1, 'and it sends when the command is done');
  assert.equal(sentTurns()[0].payload.content, 'queued hello');
  assert.equal(r.queue.length, 0);
});

// A fork moves the route; the old room keeps whatever was queued for it. That
// queue must not run in a thread nobody is watching, and must not be orphaned
// either: coming back is what sends it.
test('a queue a fork left behind sends when the owner comes back', async () => {
  serveRoom(['c_1', 'c_2'], {
    'POST /api/conversations/c_1/commands': () => ({ command: 'fork', conversation: { id: 'c_2', title: 'Thread (fork)', providerId: 'p', modelId: 'm' } }),
  });
  chat.navigate('#/c/c_1');
  await sleep(30);
  const r = state.rooms.c_1;
  r.queue = [{ id: 'q1', text: 'left behind', references: [], files: [], recording: null }];

  r.draft = '/fork';
  chat.submit(r, '/fork');
  await sleep(400);
  assert.equal(location.hash, '#/c/c_2');
  assert.equal(sentTurns().length, 0, 'nothing runs in the room nobody is looking at');
  assert.equal(r.queue.length, 1, 'the queue waits for its room');

  // the tray says so when the owner is back — and the message goes
  chat.navigate('#/c/c_1');
  await sleep(80);
  assert.equal(sentTurns().length, 1, 'coming back releases it');
  assert.equal(sentTurns()[0].payload.content, 'left behind');
  assert.equal(r.queue.length, 0);
});
