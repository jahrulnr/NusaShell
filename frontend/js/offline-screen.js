// Full-window offline state.
//
// Connection status comes from the WS lifecycle plus request-level verdicts
// emitted by rpc.js. The overlay covers every view (it lives at body level,
// outside .window) so a dead backend never leaves half-broken UI on screen:
//
//   offline       → cover immediately (HTTP verdict from app.info failure)
//   closed/error/ → cover only after the failure persists past GRACE_MS;
//   reconnecting    quick WS blips resolve before that and never flicker
//   rpc:error       backend request failure covers immediately
//   rpc:available   successful request hides the overlay
//   open          → hide instantly and cancel any pending timer

import { isPairingRequired, isRemoteAccessDisabled, on } from './rpc.js';

const DEFAULT_GRACE_MS = 10000;
// WS-level failure states: covered only once they persist past the grace
// window. ('offline' is the explicit HTTP verdict and skips the window.)
const GRACED_STATUSES = ['closed', 'error', 'reconnecting'];

export function initOfflineScreen({ graceMs = DEFAULT_GRACE_MS } = {}) {
  const screen = document.getElementById('offline-screen');
  if (!screen) return { show: () => {}, hide: () => {} };

  const retryBtn = document.getElementById('offline-retry-btn');
  let timer = null;

  function clearTimer() {
    if (!timer) return;
    clearTimeout(timer);
    timer = null;
  }

  function show() {
    // Never cover the screen while the pairing gate owns it.
    if (isPairingRequired() || isRemoteAccessDisabled()) return;
    clearTimer();
    screen.hidden = false;
  }

  function hide() {
    clearTimer();
    screen.hidden = true;
  }

  function onStatus(status) {
    if (status === 'open') {
      hide();
      return;
    }
    // When the backend requires device pairing, the pairing gate is the
    // dominant user-facing state. The backend is reachable — it just refuses
    // unpaired remote clients — so the offline overlay must stay hidden
    // regardless of the connection-status signal.
    if (isPairingRequired() || isRemoteAccessDisabled()) {
      hide();
      return;
    }
    if (status === 'offline') {
      // Explicit verdict (boot HTTP probe failed): no grace period.
      show();
      return;
    }
    if (!GRACED_STATUSES.includes(status)) {
      // connecting or unknown: keep waiting.
      return;
    }
    if (timer || !screen.hidden) return;
    timer = setTimeout(() => {
      timer = null;
      // Re-check pairing before showing: pairing may have become required
      // while the grace timer was pending.
      if (isPairingRequired() || isRemoteAccessDisabled()) {
        hide();
        return;
      }
      screen.hidden = false;
    }, graceMs);
  }

  window.addEventListener('nusashell:connection-status', (event) => onStatus(event.detail?.status));
  on('rpc:error', (detail) => {
    if (detail?.code === 'unavailable') onStatus('offline');
  });
  on('rpc:available', () => onStatus('open'));
  retryBtn?.addEventListener('click', () => location.reload());

  return { show, hide };
}
