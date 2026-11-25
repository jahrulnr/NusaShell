// A room's waiting count is a room notice, not turn state: the last frame
// wins, zero means gone, and the line that shows it re-renders itself.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import './env.mjs';
import { ownNote, queuedFor, queuedNote, setQueued, watchQueued } from '../lib/queued.js';

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

// The room's line and the composer's tray can be on screen together, holding
// two different numbers: the worker's depth for the room and this tab's own
// queued messages. Each says what it counts, so the smaller number never reads
// as a contradiction of the larger one.
test('the room count and the owner\'s queue never read as one number', () => {
  const room = queuedNote(3);
  assert.equal(room, '3 waiting to run');
  assert.ok(!/yours|queued/i.test(room), 'the room line does not claim whose jobs they are');

  const own = ownNote(1);
  assert.equal(own, '1 of yours queued — it sends when this turn ends');
  assert.ok(!/waiting/i.test(own), 'the tray does not use the room line\'s word');
  assert.match(ownNote(2, { command: true }), /^2 of yours queued — it sends when the command finishes$/);
  assert.match(ownNote(1, { held: true }), /^1 of yours queued — on hold$/);

  // the words that tell the two apart: one names who, the other names what
  assert.ok(room.includes('waiting') && own.includes('yours'));
});
