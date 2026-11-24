// The follow decision on its own: which gestures and positions keep the
// transcript pinned to its bottom, and which must never take the position
// away from the reader.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  BACK, newFollow, followAway, followToward, followScrolled,
  followWrote, followResized, followHeld, followJump,
} from '../lib/follow.js';

test('a slow scroll up is not undone by the scroll event that follows it', () => {
  const f = newFollow();
  // A wheel step up releases at once …
  assert.equal(followAway(f), true);
  assert.equal(f.stick, false);
  // … and the small step it lands on — still within reach of the bottom — is
  // not the reader coming back. This is the tick that used to re-arm the pin.
  assert.equal(followScrolled(f, 3), false);
  assert.equal(followScrolled(f, BACK - 1), false);
  assert.equal(f.stick, false);
  // More slow steps stay away.
  for (const dist of [9, 20, 40, 60]) assert.equal(followScrolled(f, dist), false);
  assert.equal(f.stick, false);
});

test('scrolling back down to the bottom follows again', () => {
  const f = newFollow();
  followAway(f);
  followScrolled(f, 200);
  assert.equal(f.stick, false);
  // Downward intent, then the step that lands near the bottom.
  followToward(f);
  assert.equal(followScrolled(f, 40), false, 'not yet');
  followToward(f);
  assert.equal(followScrolled(f, BACK), true);
  assert.equal(f.stick, true);
});

test('a wheel tick down at the very bottom follows again', () => {
  const f = newFollow();
  followAway(f);
  assert.equal(f.stick, false);
  followToward(f);
  assert.equal(followScrolled(f, 0), true, 'the reader asked for the bottom');
});

test('a position this page wrote never re-arms the pin', () => {
  const f = newFollow();
  followAway(f);
  // a prepend restored, or a pin written for another reason
  followWrote(f);
  assert.equal(followScrolled(f, 0), false);
  assert.equal(f.stick, false);
  // the same press the page itself made cannot stand in for the reader either
  followHeld(f, true);
  followWrote(f);
  assert.equal(followScrolled(f, 0), false);
});

test('a contact area that changed under the reader does not re-arm it', () => {
  const f = newFollow();
  followToward(f);
  followAway(f);
  // a keyboard closing makes the page taller: the reader is suddenly closer
  // to the bottom without having moved
  followResized(f);
  assert.equal(followScrolled(f, 0), false);
  assert.equal(f.stick, false);
  // their next gesture is heard again
  followToward(f);
  assert.equal(followScrolled(f, 0), true);
});

test('a position clearly away from the bottom releases, however it got there', () => {
  const f = newFollow();
  assert.equal(followScrolled(f, 400), true);
  assert.equal(f.stick, false);
  // already away: nothing to report
  assert.equal(followScrolled(f, 500), false);
});

test('a finger or button held on the transcript blocks the pin', () => {
  const f = newFollow();
  followHeld(f, true);
  assert.equal(f.held, true);
  followHeld(f, false);
  assert.equal(f.held, false);
});

test('the chip follows the reader back, and a new thread starts at its bottom', () => {
  const f = newFollow();
  followAway(f);
  assert.equal(followJump(f), true);
  assert.equal(f.stick, true);
  followAway(f);
  assert.equal(followJump(f), true);
});
