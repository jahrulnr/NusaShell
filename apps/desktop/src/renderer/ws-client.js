// Renderer host client — bridges to Electron main via IPC instead of the
// loopback WebSocket (Phase 2 of desktop-inprocess-ipc-plan).
//
// Exports match the former ws-client.js surface so call sites
// (agent-api.js, plugin-api.js, launcher.js, turn-event-helper.js, …)
// do not need to change. The WebSocket/NusaClient dependency is gone.

let onOpenCallback = null;
let onLogCallback = null;
let onConnectionChangeCallback = null;
let ready = false;

/**
 * Initialize the host client. In the IPC model the backend is in-process in
 * main, so there is no TCP connect step — "ready" is immediate once preload
 * has exposed `window.shell.backend`.
 *
 * @param {{ url?: string, onOpen?: (isOpen?: boolean) => void, onConnectionChange?: (state: "open" | "closed" | "reconnecting" | "failed") => void, onLog?: (level: string, message: string) => void }} config
 */
export function initWsClient(config) {
  onOpenCallback = config.onOpen ?? null;
  onLogCallback = config.onLog ?? null;
  onConnectionChangeCallback = config.onConnectionChange ?? null;
  // IPC is always "connected" once preload is loaded. Fire onOpen on next
  // microtask so launcher code that registers handlers before connectWs()
  // still sees them.
  ready = true;
  Promise.resolve().then(() => {
    onConnectionChangeCallback?.("open");
    onOpenCallback?.(true);
  });
}

/** No-op in IPC mode — the backend is in-process, no TCP connect needed. */
export function connectWs() {
  if (!ready) {
    ready = true;
    onConnectionChangeCallback?.("open");
    onOpenCallback?.(true);
  }
}

/**
 * Send a request to the backend via IPC.
 * @param {string} method
 * @param {unknown} payload
 * @param {number} timeoutMs
 * @returns {Promise<unknown>}
 */
export function sendRequest(method, payload, timeoutMs = 60000) {
  if (!window.shell?.backend) {
    return Promise.reject(new Error("Host backend bridge not available"));
  }
  log("debug", `IPC request ${method}`);
  return window.shell.backend.request(method, payload, { timeoutMs }).then((envelope) => {
    // The MessageRouter returns a ResponseEnvelope { kind, id, ok, result? | error? }.
    // Unwrap it so callers get the result value directly, matching the old
    // NusaClient.request behavior which returned the unwrapped result.
    if (envelope && typeof envelope === "object" && "ok" in envelope) {
      if (envelope.ok) return envelope.result;
      const err = new Error(envelope.error?.message ?? "Request failed");
      err.code = envelope.error?.code;
      err.details = envelope.error?.details;
      throw err;
    }
    // Some handlers may return raw values (e.g. legacy IPC paths); pass through.
    return envelope;
  });
}

/**
 * Subscribe to a backend event type via IPC.
 * @param {string} eventType
 * @param {(payload: unknown, sequence?: number) => void} handler
 * @returns {() => void} unsubscribe
 */
export function onEvent(eventType, handler) {
  if (!window.shell?.backend) {
    log("warn", `onEvent("${eventType}") called before backend bridge ready — handler dropped`);
    return () => {};
  }
  return window.shell.backend.onEvent(eventType, (payload, sequence) => handler(payload, sequence));
}

/**
 * No-op in IPC mode — IPC event fan-out is process-local; there is no
 * subscription registry to sync. Kept for call-site compatibility.
 */
export async function subscribe(_eventTypes) {
  // No-op: IpcEventBridge broadcasts to all windows automatically.
}

/** Always true in IPC mode once preload is loaded. */
export function isConnected() {
  return ready && Boolean(window.shell?.backend);
}

function log(level, message) {
  onLogCallback?.(level, message);
}
