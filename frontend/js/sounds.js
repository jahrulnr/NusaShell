// Sound notifications for agent turn events.
//
// Plays a notification sound when an agent turn completes or fails, gated
// by the SoundNotifications setting (default on). Sounds are served from
// the embedded /sounds/ endpoint and are preloaded on first use so the
// first turn-complete plays without a fetch delay.
//
// Browser autoplay policy: Audio elements created after a user gesture
// (clicking "send") are allowed to play. Since every agent turn is
// triggered by a user action, playback is permitted.

const SOUND_COMPLETE = '/sounds/notification.wav';
const SOUND_ERROR = '/sounds/notification-error.wav';

let completeAudio = null;
let errorAudio = null;
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
