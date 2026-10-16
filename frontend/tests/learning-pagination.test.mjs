import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8');
const app = readFileSync(new URL('../page/learning/app.js', import.meta.url), 'utf8');

test('Learning log keeps a bounded page and exposes previous/next controls', () => {
  for (const id of ['learning-log-prev', 'learning-log-page', 'learning-log-next']) {
    assert.match(html, new RegExp(`id="${id}"`));
  }
  assert.match(app, /const LEARNING_LOG_PAGE_SIZE = 50/);
  assert.match(app, /rpcClient\('learning\.log', \{ limit: LEARNING_LOG_PAGE_SIZE, cursor \}\)/);
  assert.match(app, /state\.logEntries = resultList\(logResponse, 'entries'\)/);
  assert.doesNotMatch(app, /limit: 200/);
});

test('Learning log navigation tracks stable backend cursors', () => {
  assert.match(app, /logCursors: \[0\]/);
  assert.match(app, /state\.logCursors\[page\] = cursor/);
  assert.match(app, /if \(state\.logHasMore\) state\.logCursors\[page \+ 1\] = logResponse\.next_cursor/);
  assert.match(app, /find\('learning-log-prev'\)\?\.addEventListener\('click'/);
  assert.match(app, /find\('learning-log-next'\)\?\.addEventListener\('click'/);
  assert.match(app, /cursor: state\.logCursors\[state\.logPage - 1\] \|\| 0/);
  assert.match(app, /cursor: state\.logCursors\[state\.logPage \+ 1\] \|\| 0/);
});
