import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import './env.mjs';
import { state, toast, dismissToast } from '../lib/store.js';

// One toast is on screen at a time. Two stacked toasts covered a button on
// Settings at 390px, so a notice replaces the one showing. An action toast (an
// Undo) is the exception: no notice replaces it, and what arrives waits for it.
// The nav drawer and a toast both sit at the top left, so while the drawer is open
// nothing is on screen: the toast that was shown is held, and comes back on close.
const shown = () => state.toasts.map((t) => t.message);
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const undo = { label: 'Undo', run() {} };

// Every toast a test makes is taken down after it, so the toasts one test left
// waiting or held cannot show up in the next.
const made = [];
const make = (...args) => {
  const id = toast(...args);
  made.push(id);
  return id;
};
afterEach(() => {
  state.sidebarOpen = false;
  for (const id of made.splice(0)) dismissToast(id);
  state.toasts = [];
});

test('a new notice replaces the one on screen rather than stacking under it', () => {
  make('first', 'error', 0);
  make('second', 'error', 0);
  assert.deepEqual(shown(), ['second']);
});

test("a replaced notice's timer does not take down the notice that replaced it", async () => {
  make('first', 'info', 20);
  make('second', 'info', 0);
  await wait(60);
  assert.deepEqual(shown(), ['second']);
});

test('an action toast is not replaced by a notice', () => {
  make('Replaced the draft.', 'info', 0, undo);
  make('Could not save.', 'error', 0);
  assert.deepEqual(shown(), ['Replaced the draft.']);
});

test('a notice that arrives under an action shows once the action is used', () => {
  const u = make('Replaced the draft.', 'info', 0, undo);
  make('Could not save.', 'error', 0);
  dismissToast(u); // the Undo button takes its toast down first
  assert.deepEqual(shown(), ['Could not save.']);
});

test('the newest notice waiting behind an action is the one that shows', () => {
  const u = make('Replaced the draft.', 'info', 0, undo);
  make('first notice', 'info', 0);
  make('second notice', 'info', 0);
  dismissToast(u);
  assert.deepEqual(shown(), ['second notice']);
});

test('actions under an action wait their turn in order, and none is dropped', () => {
  const a = make('one', 'info', 0, undo);
  const b = make('two', 'info', 0, undo);
  const c = make('three', 'info', 0, undo);
  assert.deepEqual(shown(), ['one']);
  dismissToast(a);
  assert.deepEqual(shown(), ['two']);
  dismissToast(b);
  assert.deepEqual(shown(), ['three']);
  dismissToast(c);
  assert.deepEqual(shown(), []);
});

test('a waiting notice comes after the actions queued ahead of it', () => {
  const a = make('one', 'info', 0, undo);
  make('a notice', 'info', 0);
  const b = make('two', 'info', 0, undo);
  dismissToast(a);
  assert.deepEqual(shown(), ['two']);
  dismissToast(b);
  assert.deepEqual(shown(), ['a notice']);
});

test('a toast dismissed before it shows is cancelled', () => {
  const u = make('Replaced the draft.', 'info', 0, undo);
  const late = make('Could not save.', 'error', 0);
  dismissToast(late);
  dismissToast(u);
  assert.deepEqual(shown(), []);
});

test("an action's time limit hands the screen to the notice waiting behind it", async () => {
  make('Replaced the draft.', 'info', 20, undo);
  make('Could not save.', 'error', 0);
  await wait(60);
  assert.deepEqual(shown(), ['Could not save.']);
});

test('a queued action starts its time limit when it shows, not when it is queued', async () => {
  const a = make('one', 'info', 0, undo);
  make('two', 'info', 30, undo);
  await wait(50);
  dismissToast(a);
  assert.deepEqual(shown(), ['two']);
  await wait(60);
  assert.deepEqual(shown(), []);
});

test('an action may replace a notice on screen; a notice may replace a notice', () => {
  make('Could not save.', 'error', 0);
  make('Replaced the draft.', 'info', 0, undo);
  assert.deepEqual(shown(), ['Replaced the draft.']);
});

test('while the drawer is open nothing is on screen, however many toasts arrive', () => {
  state.sidebarOpen = true;
  make('Could not save.', 'error', 0);
  assert.deepEqual(shown(), []);
  make('Replaced the draft.', 'info', 0, undo);
  assert.deepEqual(shown(), []);
  make('Saved.', 'info', 0);
  assert.deepEqual(shown(), []);
});

test('a notice that arrives while the drawer is open shows on close with its full time', async () => {
  state.sidebarOpen = true;
  make('Could not save.', 'error', 100);
  await wait(40);
  state.sidebarOpen = false;
  assert.deepEqual(shown(), ['Could not save.']);
  await wait(70);
  assert.deepEqual(shown(), ['Could not save.'], 'its clock started when it appeared, not while it waited');
});

test('a toast on screen when the drawer opens is held, and comes back with the time it had left', async () => {
  make('Saved.', 'info', 120);
  await wait(30);
  state.sidebarOpen = true;
  await wait(150);
  assert.deepEqual(shown(), [], 'nothing shows while the drawer is open');
  state.sidebarOpen = false;
  assert.deepEqual(shown(), ['Saved.'], 'held, not run down while the drawer was open');
  await wait(60);
  assert.deepEqual(shown(), ['Saved.']);
  await wait(50);
  assert.deepEqual(shown(), [], 'the clock picked up from what was left (about 90 ms), not from zero');
});

test('a held action keeps its place, and a notice that arrives meanwhile waits behind it', () => {
  const u = make('Replaced the draft.', 'info', 0, undo);
  state.sidebarOpen = true;
  make('Could not save.', 'error', 0);
  assert.deepEqual(shown(), []);
  state.sidebarOpen = false;
  assert.deepEqual(shown(), ['Replaced the draft.']);
  dismissToast(u);
  assert.deepEqual(shown(), ['Could not save.']);
});

test('actions that arrive while the drawer is open queue in order, and none is dropped', () => {
  state.sidebarOpen = true;
  const a = make('one', 'info', 0, undo);
  const b = make('two', 'info', 0, undo);
  assert.deepEqual(shown(), []);
  state.sidebarOpen = false;
  assert.deepEqual(shown(), ['one']);
  dismissToast(a);
  assert.deepEqual(shown(), ['two']);
  dismissToast(b);
  assert.deepEqual(shown(), []);
});

test('while the drawer is open only the newest notice waits, and it shows on close', () => {
  state.sidebarOpen = true;
  make('first', 'info', 0);
  make('second', 'info', 0);
  make('third', 'info', 0);
  assert.deepEqual(shown(), []);
  state.sidebarOpen = false;
  assert.deepEqual(shown(), ['third']);
});

test('a held toast dismissed while the drawer is open does not come back', () => {
  const s = make('Saved.', 'info', 0);
  state.sidebarOpen = true;
  dismissToast(s);
  make('later', 'info', 0);
  state.sidebarOpen = false;
  assert.deepEqual(shown(), ['later']);
});

test('opening and closing the drawer again keeps the held notice and the newest one behind it', () => {
  const x1 = make('x1', 'info', 0);
  state.sidebarOpen = true;
  make('x2', 'info', 0);
  make('x3', 'info', 0);
  state.sidebarOpen = false;
  assert.deepEqual(shown(), ['x1'], 'the held notice is not replaced by one that arrived while it was held');
  state.sidebarOpen = true;
  state.sidebarOpen = false;
  assert.deepEqual(shown(), ['x1']);
  dismissToast(x1);
  assert.deepEqual(shown(), ['x3'], 'the newest waiting notice follows the held one');
});
