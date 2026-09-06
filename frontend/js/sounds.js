// Sound notifications for interactive Agent-room turn events.
//
// Plays a notification sound when a listed Agent room completes or fails, or
// when an ask_question card is waiting for an answer, gated by the
// SoundNotifications setting (default on). Background learning jobs and
// other headless turns stay silent: their ding is the same wav as a chat
// completion and makes the user think the conversation they are watching
// just finished.
//
// Sounds are served from the embedded /sounds/ endpoint and are preloaded
// on first use so the first turn-complete plays without a fetch delay.
//
// Browser autoplay policy: Audio elements created after a user gesture
// (clicking "send") are allowed to play. Since every interactive agent
// turn is triggered by a user action, playback is permitted.

const SOUND_COMPLETE = '/sounds/notification.wav';
const SOUND_ERROR = '/sounds/notification-error.wav';
const SOUND_ASK = '/sounds/notification-ask_question.wav';

let completeAudio = null;
let errorAudio = null;
let askAudio = null;
let preloaded = false;

// preload lazily creates the Audio elements so the browser fetches the
// wav files ahead of the first playback. Called on the first turn event.
// No-ops in environments without Audio (e.g. Node.js test runner).
function preload() {
  if (preloaded) return;
  preloaded = true;
  if (typeof Audio === 'undefined') return;
  completeAudio = new Audio(SOUND_COMPLETE);
  completeAudio.preload = 'auto';
  errorAudio = new Audio(SOUND_ERROR);
  errorAudio.preload = 'auto';
  askAudio = new Audio(SOUND_ASK);
  askAudio.preload = 'auto';
}

// shouldPlayAgentTurnSound is the room gate for completion/error dings.
// Only a visible Agent room (sidebar list) may play. Headless learning and
// pipeline transcripts share the Agent-room wav, so they stay silent even
// if they somehow appear in the list.
export function shouldPlayAgentTurnSound(soundEnabled, details = {}) {
  if (!soundEnabled) return false;
  if (details.headless) return false;
  const conversationId = details.conversationId;
  if (!conversationId) return false;
  const rooms = details.rooms;
  if (!Array.isArray(rooms)) return false;
  return rooms.some((room) => room && room.id === conversationId);
}

// playComplete plays the turn-complete notification. Silently no-ops when
// the setting is disabled or playback fails (e.g. browser blocked audio).
export function playComplete(soundEnabled) {
  if (!soundEnabled) return;
  preload();
  if (!completeAudio) return;
  completeAudio.currentTime = 0;
  completeAudio.play().catch(() => {
    // Autoplay blocked or fetch failed — silently ignore. The next user
    // gesture will re-enable playback.
  });
}

// playError plays the turn-error notification. Same gating as playComplete.
export function playError(soundEnabled) {
  if (!soundEnabled) return;
  preload();
  if (!errorAudio) return;
  errorAudio.currentTime = 0;
  errorAudio.play().catch(() => {
    // Autoplay blocked or fetch failed — silently ignore.
  });
}

// playAsk plays the ask_question-pending notification, announcing that an
// interactive question card is waiting for an answer. Same gating as
// playComplete.
export function playAsk(soundEnabled) {
  if (!soundEnabled) return;
  preload();
  if (!askAudio) return;
  askAudio.currentTime = 0;
  askAudio.play().catch(() => {
    // Autoplay blocked or fetch failed — silently ignore.
  });
}
