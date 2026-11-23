// The one multiplexed turn socket. Reconnects with capped, jittered
// backoff; a disconnect never cancels a turn server-side, so the
// controller re-watches live turns from their last seq on every (re)open.

let seq = 0;
export const newRequestId = () => `r${Date.now().toString(36)}${(++seq).toString(36)}`;

// A link that stayed up this long proved the server reachable — the next
// drop starts the backoff over. A short-lived one (killed for a full
// queue, a proxy flap) counts against the retry pace instead.
const STABLE_MS = 10_000;
const BASE_MS = 500;
const MAX_MS = 15_000;

// wasStable reports whether a connection that just closed had proven
// stable — it must have actually opened first (openedAt 0 means it died
// before onopen, which counts against the streak, never for it).
export const wasStable = (openedAt, stableMs = STABLE_MS) => openedAt > 0 && Date.now() - openedAt >= stableMs;

// backoffDelay answers how long to wait before retry #fails. openFor is
// how long the lost connection had been open: a stable link resets the
// streak. The jitter keeps a fleet of reconnecting tabs from marching in
// step.
export function backoffDelay(fails, openFor) {
  const streak = openFor >= STABLE_MS ? 0 : fails;
  const base = Math.min(BASE_MS * 2 ** streak, MAX_MS);
  return base * (0.75 + Math.random() * 0.5);
}

export class TurnSocket {
  constructor({ onFrame, onState, onOpen }) {
    this.onFrame = onFrame;
    this.onState = onState;
    this.onOpen = onOpen;
    this.ws = null;
    this.fails = 0;
    this.openedAt = 0;
    this.stableTimer = null;
    this.connect();
  }

  get open() { return this.ws?.readyState === WebSocket.OPEN; }

  connect() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(`${proto}//${location.host}/api/ws`);
    this.ws = ws;
    this.onState('connecting');
    ws.onopen = () => {
      this.openedAt = Date.now();
      this.onState('open');
      this.onOpen();
      // The streak clears only after the link proves stable — a socket
      // that dies in seconds keeps pacing its retries instead of
      // restarting the backoff at every open.
      this.stableTimer = setTimeout(() => { this.fails = 0; }, STABLE_MS);
    };
    ws.onmessage = (ev) => {
      let frame;
      try { frame = JSON.parse(ev.data); } catch { return; }
      this.onFrame(frame);
    };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      clearTimeout(this.stableTimer);
      const openFor = this.openedAt ? Date.now() - this.openedAt : 0;
      const delay = backoffDelay(this.fails, openFor);
      this.fails++;
      this.onState('closed');
      setTimeout(() => this.connect(), delay);
    };
  }

  // send returns false when the socket can't carry the command right now;
  // callers keep the user's text instead of guessing delivery.
  send(cmd) {
    if (!this.open) return false;
    this.ws.send(JSON.stringify(cmd));
    return true;
  }
}
