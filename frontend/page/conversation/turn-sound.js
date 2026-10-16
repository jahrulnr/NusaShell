import { shouldPlayAgentTurnSound } from '../../js/sounds.js';

// agentTurnSound is the room gate for a turn completion or failure.
// Playback stays in the conversation page; this module does not touch DOM or Audio.
export function agentTurnSound(soundEnabled, payload, rooms) {
  const play = shouldPlayAgentTurnSound(soundEnabled, {
    conversationId: payload?.conversation_id || '',
    rooms,
    headless: payload?.headless,
  });
  if (!play) return 'silent';
  return payload?.error ? 'error' : 'complete';
}
