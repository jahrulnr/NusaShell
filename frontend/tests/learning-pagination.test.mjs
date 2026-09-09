import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8');
const source = readFileSync(new URL('../js/views/learning.js', import.meta.url), 'utf8');

test('Learning log keeps a bounded page and exposes previous/next controls', () => {
  for (const id of ['learning-log-prev', 'learning-log-page', 'learning-log-next']) {
    assert.match(html, new RegExp(`id="${id}"`));
  }
  assert.match(source, /const LEARNING_LOG_PAGE_SIZE = 50/);
  assert.match(source, /rpc\('learning\.log', \{ limit: LEARNING_LOG_PAGE_SIZE, cursor \}\)/);
  assert.match(source, /state\.logEntries = res\.entries \|\| \[\]/);
  assert.doesNotMatch(source, /rpc\('learning\.log', \{ limit: 200 \}\)/);
});

test('Learning log navigation tracks stable backend cursors', () => {
  assert.match(source, /logCursors: \[0\]/);
  assert.match(source, /state\.logCursors\[state\.logPage \+ 1\] = res\.next_cursor/);
  assert.match(source, /prevBtn\.addEventListener\('click'/);
  assert.match(source, /nextBtn\.addEventListener\('click'/);
});
