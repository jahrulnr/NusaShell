// A room's waiting count is a room notice, not turn state: the last frame
// wins, zero means gone, and the line that shows it re-renders itself.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import './env.mjs';
import { setQueued, queuedFor, watchQueued } from '../lib/queued.js';

test('the last notice wins, and zero clears the room', () => {
  assert.equal(queuedFor('q_1'), 0);
  assert.equal(setQueued('q_1', 3), true);
  assert.equal(queuedFor('q_1'), 3);
  assert.equal(setQueued('q_1', 0), true, 'the queue drained');
  assert.equal(queuedFor('q_1'), 0);
  // a repeated number costs nothing and announces nothing
  assert.equal(setQueued('q_2', 2), true);
  assert.equal(setQueued('q_2', 2), false);
  // frames carry no seq: whatever arrives last is the current depth
  setQueued('q_2', 5);
  setQueued('q_2', 1);
  assert.equal(queuedFor('q_2'), 1);
  // a count is per room
  assert.equal(queuedFor('q_3'), 0);
  // nonsense from the wire never becomes a number on screen
  setQueued('q_4', -2);
  assert.equal(queuedFor('q_4'), 0);
  setQueued('q_4', 'oops');
  assert.equal(queuedFor('q_4'), 0);
  setQueued('', 4);
});

test('a watcher hears every change until it stops watching', () => {
  const heard = [];
  const off = watchQueued((id, n) => heard.push([id, n]));
  setQueued('q_5', 2);
  setQueued('q_5', 2);
  setQueued('q_5', 0);
  off();
  setQueued('q_5', 7);
  assert.deepEqual(heard, [['q_5', 2], ['q_5', 0]]);
});
