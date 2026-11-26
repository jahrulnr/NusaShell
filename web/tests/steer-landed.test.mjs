// A relayed steer whose history row has landed is drawn once: the tray lets go of
// it, and the transcript keeps the row. The row is matched by its inputId and by
// nothing else, so a steer with no row yet stays in the tray.
import { mockApi } from './env.mjs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as chat from '../lib/chat.js';
import { historyItems } from '../lib/turn.js';

// shown counts the copies of a text this tab draws: its tray entries, then the
// user rows of the history.
const shown = (r, text) => [
  ...r.steers.map((s) => s.text),
  ...historyItems(r.messages || []).filter((x) => x.kind === 'user').map((x) => x.msg.content),
].filter((t) => t === text).length;

test('a relayed steer whose row has landed is drawn once: the tray lets go of it', async () => {
  mockApi({
    'GET /api/conversations/c_relay/messages': {
      messages: [{ id: 'u1', role: 'user', content: 'relay text', inputId: 's1' }],
      effectiveStart: 0,
      total: 1,
    },
  });
  const r = chat.room('c_relay');
  r.steers = [{ inputId: 's1', text: 'relay text', state: 'pending', turnId: 't1', files: [], references: [] }];
  await chat.loadHistory(r, { force: true });
  assert.equal(shown(r, 'relay text'), 1, 'one copy: the history row, not the tray entry as well');
  assert.deepEqual(r.steers, []);
});

test('a steer with no history row yet stays in the tray', async () => {
  mockApi({
    'GET /api/conversations/c_wait/messages': {
      messages: [{ id: 'u2', role: 'user', content: 'earlier', inputId: 's0' }],
      effectiveStart: 0,
      total: 1,
    },
  });
  const r = chat.room('c_wait');
  r.steers = [{ inputId: 's9', text: 'still pending', state: 'pending', turnId: 't2', files: [], references: [] }];
  await chat.loadHistory(r, { force: true });
  assert.deepEqual(r.steers.map((s) => s.inputId), ['s9']);
  assert.equal(shown(r, 'still pending'), 1);
});

test('an ordinary owner steer, whose history row carries no inputId, is not released by the history', async () => {
  mockApi({
    'GET /api/conversations/c_own/messages': {
      messages: [{ id: 'a1', role: 'assistant', content: '', parts: [{ kind: 'steer', text: 'owner text' }] }],
      effectiveStart: 0,
      total: 1,
    },
  });
  const r = chat.room('c_own');
  r.steers = [{ inputId: 'own1', text: 'owner text', state: 'pending', turnId: 't3', files: [], references: [] }];
  await chat.loadHistory(r, { force: true });
  assert.deepEqual(r.steers.map((s) => s.inputId), ['own1'], 'a row without an inputId releases nothing; its own final frame does');
});
