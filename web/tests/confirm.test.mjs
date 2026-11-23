import { test } from 'node:test';
import assert from 'node:assert/strict';
import './env.mjs';
import { state, askConfirm } from '../lib/store.js';

test('a question stays open until the owner answers it', async () => {
  const asked = askConfirm({ title: 'Delete it?', confirm: 'Delete', danger: true });
  assert.equal(state.confirm.title, 'Delete it?');
  assert.equal(state.confirm.confirm, 'Delete');
  assert.equal(state.confirm.danger, true);
  state.confirm.resolve(true);
  assert.equal(await asked, true);
});

test('a new question answers the one still open no', async () => {
  const first = askConfirm({ title: 'First?' });
  const second = askConfirm({ title: 'Second?' });
  assert.equal(await first, false);
  assert.equal(state.confirm.title, 'Second?');
  assert.equal(state.confirm.confirm, 'Confirm');
  state.confirm.resolve(false);
  assert.equal(await second, false);
});
