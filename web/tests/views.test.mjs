// A thread's kept view: what is restored into a room, what is saved, and
// that a draft typed before the view arrived is never overwritten.
import { calls, mockApi, sleep } from './env.mjs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { emit, state } from '../lib/store.js';
import { room } from '../lib/chat.js';
import { applyView, loadView, startViews, viewOf } from '../lib/views.js';

startViews();

test('a view keeps only what another page could send', () => {
  const r = room('c_keep');
  r.draft = 'see @a.go please';
  r.refs = [{ start: 4, end: 9, label: '@a.go', kind: 'file', value: 'a.go' }, { start: 0, end: 3, label: 'xyz' }];
  r.quotes = [{ key: 'q1', text: 'a line', reply: 'why?', from: null }];
  r.files = [
    { key: 'f1', status: 'ready', id: 'up_1', name: 'a.png', size: 3, mediaType: 'image/png', preview: 'blob:x' },
    { key: 'f2', status: 'uploading', name: 'b.png' },
    { key: 'f3', status: 'ready', pickId: 'pk_1', marker: 1, comment: 'this', screenshot: true, kind: 'element', label: '<a>', name: '[1] <a>' },
  ];
  r.pane = 'files';
  r.browserUrl = 'https://example.com/';
  assert.deepEqual(viewOf(r), {
    draft: 'see @a.go please',
    refs: [{ start: 4, end: 9, label: '@a.go', kind: 'file', value: 'a.go' }],
    quotes: [{ key: 'q1', text: 'a line', reply: 'why?', from: null }],
    files: [
      { key: 'f1', id: 'up_1', name: 'a.png', size: 3, mediaType: 'image/png' },
      { key: 'f3', pickId: 'pk_1', marker: 1, comment: 'this', screenshot: true, kind: 'element', label: '<a>', name: '[1] <a>' },
    ],
    pane: 'files',
    browserUrl: 'https://example.com/',
  });
});

test('a kept view fills an untouched room, and what the owner did here wins', () => {
  const doc = { draft: 'half a thought', quotes: [{ key: 'q', text: 'x', reply: '' }], files: [{ key: 'f', id: 'up_2', name: 'n' }], pane: 'terminal' };
  const empty = room('c_fill');
  applyView(empty, doc);
  assert.equal(empty.draft, 'half a thought');
  assert.equal(empty.caret, 'half a thought'.length);
  assert.equal(empty.files[0].status, 'ready');
  assert.equal(empty.pane, 'terminal');

  const typed = room('c_typed');
  typed.draft = 'mine';
  typed.pane = null; // closed on purpose
  applyView(typed, doc);
  assert.equal(typed.draft, 'mine');
  assert.deepEqual(typed.quotes, []);
  assert.equal(typed.pane, null);

  const phone = room('c_phone');
  applyView(phone, doc, false);
  assert.equal(phone.pane, undefined, 'a narrow screen does not reopen the pane over the transcript');
  assert.equal(viewOf(phone).pane, 'terminal', 'but keeps it for the wide screen that left it open');
  phone.pane = null;
  assert.equal(viewOf(phone).pane, undefined, 'closing it on purpose forgets it');
});

test('changes are saved after a pause, and only once the kept view was read', async () => {
  let release;
  mockApi({
    'GET /api/conversations/c_save/view': () => new Promise((ok) => { release = () => ok({ view: { document: { draft: 'kept' }, updatedAt: 1 } }); }),
    'PUT /api/conversations/c_save/view': (req) => ({ view: { document: req.body.document, updatedAt: 2 } }),
  });
  const r = room('c_save');
  const loading = loadView(r);
  emit();
  await sleep(800);
  assert.equal(calls.filter((c) => c.method === 'PUT').length, 0, 'nothing is saved before the kept view arrives');
  release();
  await loading;
  assert.equal(r.draft, 'kept');
  emit();
  await sleep(800);
  assert.equal(calls.filter((c) => c.method === 'PUT').length, 0, 'an unchanged view is not written back');

  r.draft = 'kept, and more';
  emit();
  await sleep(100);
  r.draft = 'kept, and more words';
  emit();
  await sleep(900);
  const puts = calls.filter((c) => c.method === 'PUT');
  assert.equal(puts.length, 1, 'a burst of typing is one save');
  assert.deepEqual(puts[0].body, { document: { draft: 'kept, and more words' } });

  r.draft = '';
  emit();
  await sleep(900);
  assert.deepEqual(calls.filter((c) => c.method === 'PUT').at(-1).body, { document: {} }, 'a sent draft clears the kept one');
  assert.ok(state.rooms.c_save.view.ready);
});
