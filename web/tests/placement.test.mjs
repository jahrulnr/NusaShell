import { test } from 'node:test';
import assert from 'node:assert/strict';
import { chooseSide } from '../lib/placement.js';

test('a popover that fits where its caller wants it stays there', () => {
  assert.equal(chooseSide('top', 500, 120, 400), 'top');
  assert.equal(chooseSide('bottom', 120, 500, 130), 'bottom');
  // A small menu with less room below than the other side still fits below.
  assert.equal(chooseSide('bottom', 400, 180, 127), 'bottom');
});

test('a popover that does not fit opens on the other side when that is clearly better', () => {
  // A tall panel above a button with 268 free, where the other side has 520.
  assert.equal(chooseSide('top', 268, 520, 460), 'bottom');
  // The other side fits the content outright, even if only a little roomier.
  assert.equal(chooseSide('top', 268, 300, 290), 'bottom');
  // A menu at the foot of the window opens upward.
  assert.equal(chooseSide('bottom', 420, 90, 127), 'top');
});

test('a small difference is not worth moving for', () => {
  // Neither side fits and the other is only 20px roomier: stay.
  assert.equal(chooseSide('top', 268, 288, 460), 'top');
  assert.equal(chooseSide('top', 268, 308, 460), 'bottom');
  assert.equal(chooseSide('top', 150, 140, 460), 'top');
});
