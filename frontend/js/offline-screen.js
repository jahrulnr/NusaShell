// Full-window offline state.
//
// Single source of truth is the `nusashell:connection-status` event emitted
// by app.js setConnection(). The overlay covers every view (it lives at body
// level, outside .window) so a dead backend never leaves half-broken UI on
// screen:
//
//   offline       → cover immediately (HTTP verdict from app.info failure)
//   closed/error/ → cover only after the failure persists past GRACE_MS;
//   reconnecting    quick WS blips resolve before that and never flicker
//   open          → hide instantly and cancel any pending timer

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
      screen.hidden = false;
    }, graceMs);
  }

  window.addEventListener('nusashell:connection-status', (event) => onStatus(event.detail?.status));
  retryBtn?.addEventListener('click', () => location.reload());

  return { show, hide };
}
