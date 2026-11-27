// A browser recording attaches to a message like a file does: it rides
// the composer chip into turn.send's recordingId, survives queueing and
// restore, and never leaves through a steer.
import { mockApi, sockets } from './env.mjs';
import { before, beforeEach, test } from 'node:test';
import assert from 'node:assert/strict';
import { state } from '../lib/store.js';
import * as chat from '../lib/chat.js';
import { viewOf, applyView } from '../lib/views.js';

let ws;
const sentTurns = () => ws.sent.filter((c) => c.type === 'turn.send');

function serveThread(id, { messages = [] } = {}) {
  mockApi({
    [`GET /api/conversations/${id}/messages`]: () => [200, { messages, effectiveStart: 0, total: messages.length }],
    [`GET /api/conversations/${id}`]: () => ({ id, title: 'Thread', workspace: '' }),
  });
}

const opened = (cmd, conversationId, turnId = 't1') => ws.serve({ type: 'turn.opened', requestId: cmd.requestId, conversationId, turnId, seq: 0 });

before(() => {
  mockApi();
  chat.start();
  ws = sockets[0];
  ws.open();
});

beforeEach(() => {
  state.rooms = {};
  state.toasts = [];
  state.newProjectId = '';
  ws.sent.length = 0;
  chat.navigate('#/');
});

test('submit sends the attached recording as recordingId, text or none', () => {
  const home = chat.freshRoom();
  home.sel = { runtime: 'harness', providerId: 'p', modelId: 'm' };
  home.recording = { id: 'rec_1', status: 'completed', label: 'Recording noon' };
  assert.equal(chat.submit(home, 'what happened'), true);
  let cmd = sentTurns().at(-1);
  assert.equal(cmd.payload.recordingId, 'rec_1');
  assert.equal(cmd.payload.content, 'what happened');

  // A recording alone is a message — clear the in-flight send so the
  // home composer submits another instead of queueing.
  home.pendingOpen = null;
  home.recording = { id: 'rec_2', status: 'completed' };
  assert.equal(chat.submit(home, ''), true);
  cmd = sentTurns().at(-1);
  assert.deepEqual([cmd.payload.content, cmd.payload.recordingId], ['', 'rec_2']);
});

test('the pending user bubble keeps the recording chip', () => {
  const home = chat.freshRoom();
  home.sel = { runtime: 'harness', providerId: 'p', modelId: 'm' };
  home.recording = { id: 'rec_3', status: 'interrupted' };
  assert.equal(chat.submit(home, 'see this'), true);
  assert.equal(home.pendingOpen?.recording?.id, 'rec_3');
  // A committed turn carries it on live for the transcript chip.
  opened(sentTurns().at(-1), 'c_1');
  assert.equal(state.rooms.c_1.live.userRecording?.id, 'rec_3');
});

test('a queued message carries its recording into the eventual send', () => {
  serveThread('c_1');
  const r = chat.room('c_1');
  r.id = 'c_1';
  r.sel = { runtime: 'harness', providerId: 'p', modelId: 'm' };
  r.live = { turnId: 't1', kind: 'send', terminal: false }; // busy
  r.recording = { id: 'rec_4', status: 'completed' };
  assert.equal(chat.submit(r, 'queued'), true);
  assert.equal(r.queue.length, 1);
  assert.equal(r.queue[0].recording.id, 'rec_4');

  r.live = null; // turn ends → the held queue drains oldest first
  chat.resumeQueue(r);
  const cmd = sentTurns().at(-1);
  assert.equal(cmd.payload.recordingId, 'rec_4');
});

test('restoring a queued message brings its recording chip back', () => {
  const r = chat.room('c_9');
  r.queue.push({ id: 'q1', text: 'later', references: [], files: [], recording: { id: 'rec_5', status: 'completed' } });
  chat.restoreQueued(r, 'q1');
  assert.equal(r.recording?.id, 'rec_5');
  assert.equal(r.draft, 'later');
});

test('a recording cannot steer — the draft and chip stay put', () => {
  const r = chat.room('c_1');
  r.id = 'c_1';
  r.live = { turnId: 't1', kind: 'send', terminal: false, steerable: true };
  r.recording = { id: 'rec_6', status: 'recording' };
  r.draft = 'mid-turn note';
  assert.equal(chat.submit(r, 'mid-turn note', [], [], 't1'), false);
  assert.equal(r.recording?.id, 'rec_6');
  assert.equal(r.draft, 'mid-turn note');
  assert.equal(sentTurns().length, 0);
});

test('a rejected send restores the recording chip with the draft', async () => {
  const home = chat.freshRoom();
  home.sel = { runtime: 'harness', providerId: 'p', modelId: 'm' };
  home.recording = { id: 'rec_7', status: 'completed' };
  assert.equal(chat.submit(home, 'take it'), true);
  const cmd = sentTurns().at(-1);
  // Simulate the composer clearing it on submit, as deliver() does.
  home.recording = null;
  ws.serve({ type: 'error', requestId: cmd.requestId, code: 'invalid_argument', message: 'nope' });
  assert.equal(home.recording?.id, 'rec_7');
  assert.equal(home.draft, 'take it');
});

test('viewOf keeps the chip; applyView restores a recording-only view', () => {
  const r = chat.room('c_v');
  r.recording = { id: 'rec_8', status: 'completed', label: 'Recording 09:00' };
  const doc = viewOf(r);
  assert.deepEqual(doc.recording, { id: 'rec_8', status: 'completed', label: 'Recording 09:00' });

  const fresh = chat.room('c_w');
  applyView(fresh, doc);
  assert.equal(fresh.recording?.id, 'rec_8');
  // An existing chip wins over a kept view's.
  fresh.recording = { id: 'rec_mine', status: 'recording' };
  applyView(fresh, doc);
  assert.equal(fresh.recording.id, 'rec_mine');
});
