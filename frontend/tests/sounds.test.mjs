import assert from 'node:assert/strict';
import test from 'node:test';

import { shouldPlayAgentTurnSound } from '../js/sounds.js';

test('interactive Agent rooms may play the turn-complete sound', () => {
  assert.equal(shouldPlayAgentTurnSound(true, {
    conversationId: 'conv_chat',
    rooms: [{ id: 'conv_chat' }],
  }), true);
});

test('background learning and other headless turns stay silent', () => {
  assert.equal(shouldPlayAgentTurnSound(true, {
    conversationId: 'conv_learning',
    rooms: [{ id: 'conv_learning' }],
    headless: true,
  }), false, 'a learning job must not share the Agent-room completion ding');
});

test('hidden transcripts that are not in the room list stay silent', () => {
  assert.equal(shouldPlayAgentTurnSound(true, {
    conversationId: 'conv_learning',
    rooms: [{ id: 'conv_chat' }],
    activeId: 'conv_chat',
  }), false);
});

test('disabled sound notifications stay silent even for a listed room', () => {
  assert.equal(shouldPlayAgentTurnSound(false, {
    conversationId: 'conv_chat',
    rooms: [{ id: 'conv_chat' }],
  }), false);
});
