import assert from 'node:assert/strict';
import test from 'node:test';

import { entriesAfter } from './js/log-tail.js';

test('log tail append only emits entries after the last rendered id', () => {
  const visible = [{ id: 'A' }, { id: 'B' }, { id: 'C' }, { id: 'D' }];
  assert.deepEqual(entriesAfter(visible, 'C').map((e) => e.id), ['D']);
  assert.deepEqual(entriesAfter(visible, null).map((e) => e.id), ['A', 'B', 'C', 'D']);
  assert.deepEqual(entriesAfter(visible, 'missing').map((e) => e.id), ['A', 'B', 'C', 'D']);
});
