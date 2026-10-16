import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

import { agentTurnSound } from '../page/conversation/turn-sound.js';

const rooms = [{ id: 'room-1' }, { id: 'room-2' }];

test('enabled room without an error plays the completion sound', () => {
  assert.equal(agentTurnSound(true, { conversation_id: 'room-1' }, rooms), 'complete');
});

test('enabled room with payload.error plays the error sound', () => {
  assert.equal(agentTurnSound(true, { conversation_id: 'room-1', error: 'provider failed' }, rooms), 'error');
});

test('disabled sound notifications stay silent', () => {
  assert.equal(agentTurnSound(false, { conversation_id: 'room-1' }, rooms), 'silent');
});

test('headless turns stay silent', () => {
  assert.equal(agentTurnSound(true, {
    conversation_id: 'room-1',
    headless: true,
    error: 'background failed',
  }, rooms), 'silent');
});

test('a conversation missing from the room list stays silent', () => {
  assert.equal(agentTurnSound(true, { conversation_id: 'room-missing' }, rooms), 'silent');
});

test('the helper treats true as enabled', () => {
  assert.equal(agentTurnSound(true, { conversation_id: 'room-2', auto_continue: { should_continue: true } }, rooms), 'complete');
});

test('app cache treats a missing sound flag as enabled and refreshes after settings apply', async () => {
  const app = await readFile(new URL('../page/conversation/app.js', import.meta.url), 'utf8');
  assert.match(app, /sound_notifications !== false/);
  const startup = app.slice(app.indexOf('async function initializeLiveData'), app.indexOf('function syncFilters'));
  assert.match(startup, /refreshSoundNotifications\(/);
  const applied = app.slice(app.indexOf("on('settings.applied'"), app.indexOf("on('agent.turn.done'"));
  assert.match(applied, /refreshSoundNotifications\(/);
  assert.match(app, /rpc\('settings\.get'/);
  assert.match(app, /method === 'settings\.set'[\s\S]*refreshSoundNotifications\(/);
});

test('turn and ask handlers play sound, and the SSE round.done listener does not', async () => {
  const app = await readFile(new URL('../page/conversation/app.js', import.meta.url), 'utf8');
  const done = app.slice(app.indexOf("on('agent.turn.done'"), app.indexOf("on('agent.turn.error'"));
  const error = app.slice(app.indexOf("on('agent.turn.error'"), app.indexOf("on('agent.todo.updated'"));
  const ask = app.slice(app.indexOf("on('agent.ask.pending'"), app.indexOf("on('agent.ask.answered'"));
  const roundDone = app.slice(
    app.indexOf("source.addEventListener('round.done'"),
    app.indexOf('source.onerror'),
  );

  assert.match(done, /agentTurnSound\(/);
  assert.match(done, /playComplete\(true\)/);
  assert.match(done, /playError\(true\)/);
  // Hidden rooms stop at the early return — after the sidebar list refresh.
  assert.match(done, /agentTurnSound\([\s\S]*round\.conversation_id !== activeConversation\?\.id\) \{[\s\S]*?void refreshConversationRooms\(round\.conversation_id\);[\s\S]*?return;/);
  assert.match(error, /agentTurnSound\(/);
  assert.match(error, /playError\(true\)/);
  assert.match(error, /agentTurnSound\([\s\S]*round\.conversation_id !== activeConversation\?\.id\) \{[\s\S]*?return;/);
  assert.match(ask, /if \(payload\?\.conversation_id !== activeConversation\?\.id\) return;[\s\S]*playAsk\(/);
  assert.doesNotMatch(ask, /agentTurnSound\(/);
  assert.doesNotMatch(roundDone, /playComplete\(/);
  assert.doesNotMatch(roundDone, /playError\(/);
  assert.doesNotMatch(roundDone, /agentTurnSound\(/);
});
