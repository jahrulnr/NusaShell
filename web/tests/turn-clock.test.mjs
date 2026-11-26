// A live turn's elapsed time comes from the server's start (turn.opened), never
// from the tab's clock: until the server has said when the turn started, the
// transcript shows a dash.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { newTurn, applyFrame, turnSeconds } from '../lib/turn.js';

test('a turn with no server start has no elapsed time: a dash, never a guess', () => {
  const t = newTurn({ turnId: 't1', kind: 'send' });
  assert.equal(t.startedAt, null);
  assert.equal(turnSeconds(t, 1700000005000), null);
});

test('the elapsed time counts from the server start, once the start is known', () => {
  const t = newTurn({ turnId: 't1', kind: 'send', startedAt: 1700000000000 });
  assert.equal(turnSeconds(t, 1700000042900), 42);
});

test('the start arrives with turn.opened, and the elapsed time follows it', () => {
  const t = newTurn({ turnId: 't2', kind: 'send' });
  assert.equal(turnSeconds(t, 1700000003000), null, 'before turn.opened: no time');
  applyFrame(t, { type: 'turn.opened', seq: 0, payload: { startedAt: 1700000000000 } });
  assert.equal(turnSeconds(t, 1700000003000), 3);
});
