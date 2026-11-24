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
});

test('the catalog lists native commands first, one row per name, native winning a collision', () => {
  state.agents.items = [{ id: 'claude', name: 'Claude' }];
  const room = {
    info: { agentId: 'claude' },
    catalog: { status: 'ready', items: [agent('review'), agent('compact'), native('compact'), native('fork', { enabled: false, reason: 'Nothing to fork yet' })] },
  };
  const rows = commandChoices(room);
  assert.deepEqual(rows.map((c) => [c.name, c.source, c.enabled]), [
    ['compact', 'NusaShell', true],
    ['fork', 'NusaShell', false],
    ['review', 'Claude', true],
  ]);
  assert.equal(rows[1].reason, 'Nothing to fork yet');
  assert.equal(rows[1].icon, 'branch');
  assert.equal(rows[0].native, true);
  assert.equal(rows[2].native, false);
  assert.deepEqual(commandChoices(room, 'fo').map((c) => c.name), ['fork']);
  assert.deepEqual(commandChoices(room, 'zz').map((c) => c.name), []);
  assert.equal(catalogRow(room, 'compact').source, 'native');
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

test('Enter on /compact runs the command and never sends a turn', async () => {
  const r = await openThread('c_1', {
    'POST /api/conversations/c_1/commands': () => ({ command: 'compact', conversation: { id: 'c_1', title: 'Thread', providerId: 'p', modelId: 'm' } }),
  });
  r.draft = '/compact';
  assert.equal(chat.submit(r, '/compact'), false, 'a command is not a send; the caller keeps the draft');

  await sleep(30);
  const posts = commandPosts();
  assert.equal(posts.length, 1);
  assert.equal(posts[0].url, '/api/conversations/c_1/commands');
  assert.deepEqual(posts[0].body, { name: 'compact', args: '', confirm: false });
  assert.equal(sentTurns().length, 0, 'no turn may carry a recognized command');
  assert.equal(r.draft, '', 'the 200 spent the draft');
  assert.equal(r.command, '');
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
  assert.equal(r.draft, '/clear', 'the draft waits on the answer');

  state.confirm.resolve(false);
  state.confirm = null;
  await sleep(20);
  assert.equal(posts, 1, 'cancelling sends nothing');
  assert.equal(r.draft, '/clear');
  assert.equal(r.command, '');

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
  assert.equal(r.draft, '/compact');
});

test('a busy room refuses the command and keeps the draft', async () => {
  const r = await openThread('c_1', {
    'POST /api/conversations/c_1/commands': () => [409, { code: 'busy', detail: 'Wait for the turn to finish' }],
  });
  r.draft = '/compact';
  chat.submit(r, '/compact');
  await sleep(30);
  assert.deepEqual([state.toasts.at(-1).kind, state.toasts.at(-1).message], ['error', 'Wait for the turn to finish']);
  assert.equal(r.draft, '/compact');
  assert.equal(r.command, '');
  assert.equal(sentTurns().length, 0);
});

test('a server without the command endpoint answers, and the draft survives it', async () => {
  const r = await openThread('c_1');
  r.draft = '/compact';
  chat.submit(r, '/compact');
  await sleep(30);
  // No route: the call is a 404 problem+json, which is a failure like any other.
  assert.equal(state.toasts.at(-1).kind, 'error');
  assert.match(state.toasts.at(-1).message, /\/compact failed: /);
  assert.equal(r.draft, '/compact');
  assert.equal(r.command, '');
  assert.equal(sentTurns().length, 0);
});

test('a command in the composer needs a thread first', async () => {
  mockApi({});
  const home = chat.freshRoom();
  home.draft = '/compact';
  chat.submit(home, '/compact');
  await sleep(20);
  assert.equal(commandPosts().length, 0);
  assert.deepEqual([state.toasts.at(-1).kind, state.toasts.at(-1).message], ['info', 'Send a message first.']);
  assert.equal(home.draft, '/compact');
  assert.equal(sentTurns().length, 0);
});
