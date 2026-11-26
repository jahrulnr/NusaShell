// Room names in a tool row (BL-10). A room the client has loaded reads by its own
// label or title, a room it has not reads as a handle, and two handles that share
// their last six characters stay apart. The row is built by toolRoom with a fake
// room book; the registry is checked through the client's own state, which fills
// from the lists, the rooms opened, and the tool calls.
import { mockApi } from './env.mjs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { state } from '../lib/store.js';
import * as chat from '../lib/chat.js';
import { toolRoom, shortHandle } from '../lib/turn.js';

// book is a fake room book: the named rooms are loaded, and the ids given are
// among the ids the client has met.
const book = (named = {}, ids = []) => {
  const among = new Set([...ids, ...Object.keys(named)]);
  return (id) => ({ label: named[id]?.label || '', title: named[id]?.title || '', handle: shortHandle(id, among) });
};
const WAIT = 'c_RERJLLW6MB3K2DIOBIC6MYZ3CO';
const waiting = (id) => JSON.stringify({ conversationId: id });

test('a loaded room reads by its label or its title, not by the tail of its id', () => {
  assert.deepEqual(toolRoom('conversation_wait', waiting(WAIT), '', book({ [WAIT]: { label: 'jackal', title: 'Builder' } })).names,
    [{ label: 'jackal', title: 'Builder', short: '' }]);
  assert.deepEqual(toolRoom('conversation_wait', waiting(WAIT), '', book({ [WAIT]: { title: 'Planner' } })).names,
    [{ label: '', title: 'Planner', short: '' }]);
});

test('a room the client has not loaded stays a handle: the last six characters of its id', () => {
  assert.deepEqual(toolRoom('conversation_wait', waiting(WAIT), '', book({})).names, [{ label: '', title: '', short: '@MYZ3CO' }]);
});

test('a loaded room reads the same while its call runs and after its answer lands', () => {
  const known = book({ [WAIT]: { label: 'jackal', title: 'Builder' } });
  const running = toolRoom('conversation_wait', waiting(WAIT), '', known).names;
  // the answer names the room differently: the loaded name still reads
  const landed = toolRoom('conversation_wait', waiting(WAIT), JSON.stringify({ thread: { id: WAIT, label: 'jackal-old', title: 'Old' } }), known).names;
  assert.deepEqual(running, [{ label: 'jackal', title: 'Builder', short: '' }]);
  assert.deepEqual(landed, running, 'the name does not move when the answer arrives');
});

test('an answer names a room the client has not loaded, and only that row changes', () => {
  assert.equal(toolRoom('conversation_wait', waiting(WAIT), '', book({})).names[0].short, '@MYZ3CO');
  assert.deepEqual(toolRoom('conversation_wait', waiting(WAIT), JSON.stringify({ thread: { id: WAIT, label: 'otter', title: '' } }), book({})).names,
    [{ label: 'otter', title: '', short: '' }]);
});

test('two ids that share their last six characters read apart', () => {
  const a = 'c_QQQQQQQ1ABCDEF', b = 'c_QQQQQQQ2ABCDEF';
  assert.equal(book({}, [a, b])(a).handle, '@1ABCDEF');
  assert.equal(book({}, [a, b])(b).handle, '@2ABCDEF');
});

test('a handle changes only when a new id collides with it', () => {
  const a = 'c_QQQQQQQ1ABCDEF', b = 'c_QQQQQQQ2ABCDEF', c = 'c_ZZZZZZZZZZZZZZZZZZ9XYZ', d = 'c_XXXXXXX1ABCDEF';
  assert.equal(book({}, [a, b])(a).handle, '@1ABCDEF');
  assert.equal(book({}, [a, b, c])(a).handle, '@1ABCDEF', 'an unrelated id leaves it as it was');
  assert.equal(book({}, [a, b, d])(a).handle, '@Q1ABCDEF', 'a colliding id lengthens it, and only it');
  assert.equal(book({}, [a, b, d])(d).handle, '@X1ABCDEF');
});

test('a handle is computed over the ids the client knows, never over the id alone', () => {
  const a = 'c_QQQQQQQ1ABCDEF';
  assert.equal(shortHandle(a, new Set([a])), '@ABCDEF', 'no rival: six characters');
});

test('a loaded room is named from the open room, the recent list, or a project list, in place', () => {
  state.rooms = { c_OPEN1: { info: { id: 'c_OPEN1', label: 'open', title: '' } } };
  state.convs.items = [{ id: 'c_LIST1', label: '', title: 'Recent' }];
  state.projectThreads = { p1: [{ id: 'c_PROJ1', label: 'pm', title: 'Plan' }] };
  assert.equal(chat.knownRoom('c_OPEN1').label, 'open');
  assert.equal(chat.knownRoom('c_LIST1').title, 'Recent');
  assert.equal(chat.knownRoom('c_PROJ1').label, 'pm');
  const none = chat.knownRoom('c_NONE1');
  assert.deepEqual([none.label, none.title], ['', '']);
  assert.equal(none.handle, '@_NONE1');
});

test('the registry is add-only: a room deleted later keeps its id, so no handle shrinks back', () => {
  const y = 'c_YYYYYYYYYYYYYYYY1ABCDEF', d = 'c_DDDDDDDDDDDDDDDD1ABCDEF';
  chat.room(d);
  const withD = chat.knownRoom(y).handle;
  assert.equal(withD, '@Y1ABCDEF', 'the opened room lengthens the handle it collides with');
  delete state.rooms[d];
  assert.equal(chat.knownRoom(y).handle, withD, 'the deleted room keeps its id in the registry');
});

// The registry is one add-only set for the whole file, so each test that reads it
// uses a suffix no earlier test registered.
test('a page of history registers the rooms its tool calls name', async () => {
  const h = 'c_HHHHHHHHHHHHHHHH3PQRSTU', y = 'c_TTTTTTTTTTTTTTTT3PQRSTU';
  mockApi({
    'GET /api/conversations/c_HIST1/messages': {
      messages: [{ id: 'm1', role: 'assistant', content: '', toolCalls: [{ id: 'k1', name: 'conversation_send', status: 'success',
        input: JSON.stringify({ conversationId: h, text: 'hi' }), output: '' }] }],
      effectiveStart: 0,
      total: 1,
    },
  });
  const r = chat.room('c_HIST1');
  assert.equal(chat.knownRoom(y).handle, '@PQRSTU', 'before the history is read, y is six characters');
  await chat.loadHistory(r, { force: true });
  assert.equal(chat.knownRoom(y).handle, '@T3PQRSTU', 'the room the history names collides with y');
});
