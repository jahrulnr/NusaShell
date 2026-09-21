// Transport layer: HTTP /rpc, WebSocket /ws.
// Both transports speak the same event vocabulary.

const listeners = new Map();

export function on(type, fn) {
  if (!listeners.has(type)) listeners.set(type, []);
  listeners.get(type).push(fn);
  return () => off(type, fn);
}

export function off(type, fn) {
  const fns = listeners.get(type);
  if (!fns) return;
  const i = fns.indexOf(fn);
  if (i >= 0) fns.splice(i, 1);
}

export function emit(type, payload) {
  const fns = listeners.get(type);
  if (fns) for (const fn of [...fns]) fn(payload);
}

function toError(res) {
  if (!res.ok) {
    const err = new Error(res.error?.message || `RPC failed (${res.error?.code || 'unknown'})`);
    err.code = res.error?.code;
    return err;
  }
  return null;
}

function rpcUnavailableError(method, cause) {
  const err = new Error('Backend unreachable');
  err.code = 'unavailable';
  err.method = method;
  err.cause = cause;
  return err;
}

function signalBackendUnavailable(method) {
  emit('rpc:error', { code: 'unavailable', method });
}

function proxyUnavailableError(method, error) {
  const err = rpcUnavailableError(method, error);
  if (error?.message) err.message = error.message;
  signalBackendUnavailable(method);
  return err;
}

// ---- Pairing-required shared state ----
// When the backend rejects a remote client with PAIRING_REQUIRED, the
// pairing gate must become the dominant user-facing state. This shared
// flag lets the offline-screen suppress its overlay (the backend is
// reachable, it just requires pairing) and lets the pairing gate show.
let pairingRequired = false;
let remoteAccessDisabled = false;

export function isPairingRequired() {
  return pairingRequired;
}

export function clearPairingRequired() {
  pairingRequired = false;
}

export function isRemoteAccessDisabled() {
  return remoteAccessDisabled;
}

export function clearRemoteAccessDisabled() {
  remoteAccessDisabled = false;
}

// isPairingRequiredError reports whether an error is a PAIRING_REQUIRED
// rejection from the backend. Used by view catches to suppress the expected
// error toast while the pairing gate is the dominant UI, without hiding
// genuine `unavailable` or unexpected errors.
export function isPairingRequiredError(err) {
	return err != null && err.code === 'PAIRING_REQUIRED';
}

export function isRemoteAccessDisabledError(err) {
  return err != null && err.code === 'REMOTE_ACCESS_DISABLED';
}

export async function rpc(method, payload = {}, { timeoutMs = 60000 } = {}) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  let res;
  try {
    res = await fetch(`/rpc/${method.replace(/\./g, '/')}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ method, payload }),
      signal: controller.signal,
    });
  } catch (err) {
    if (err?.name === 'AbortError') throw new Error(`RPC timed out: ${method}`);
    const unavailable = rpcUnavailableError(method, err);
    signalBackendUnavailable(method);
    throw unavailable;
  } finally {
    clearTimeout(timeout);
  }
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new Error(body.error?.message || `HTTP ${res.status}`);
    err.code = body.error?.code;
    if (err.code === 'proxy_error') throw proxyUnavailableError(method, err);
		if (err.code === 'PAIRING_REQUIRED') {
			pairingRequired = true;
			emit('rpc:error', { code: 'PAIRING_REQUIRED', method });
		}
		if (err.code === 'REMOTE_ACCESS_DISABLED') {
			remoteAccessDisabled = true;
			emit('rpc:error', { code: 'REMOTE_ACCESS_DISABLED', method });
		}
    throw err;
  }
  const rpcErr = toError(body);
  if (rpcErr) {
    if (rpcErr.code === 'proxy_error') throw proxyUnavailableError(method, rpcErr);
		if (rpcErr.code === 'PAIRING_REQUIRED') {
			pairingRequired = true;
			emit('rpc:error', { code: 'PAIRING_REQUIRED', method });
		}
		if (rpcErr.code === 'REMOTE_ACCESS_DISABLED') {
			remoteAccessDisabled = true;
			emit('rpc:error', { code: 'REMOTE_ACCESS_DISABLED', method });
		}
    throw rpcErr;
  }
  // A successful response clears the pairing-required flag (e.g. after
	// the user completed pairing and the session cookie is now valid).
	pairingRequired = false;
	remoteAccessDisabled = false;
  emit('rpc:available', { method });
  return body.result ?? {};
}

// ---- WebSocket (event stream: BE -> FE triggers) ----
let ws = null;
let wsStatus = 'idle';
const wsPending = new Map();
let wsOptions = {};
let reconnectTimer = null;
let reconnectDelay = 500;
// localStorage is browser-only; guards keep this module importable in
// Node-based unit tests (jsdom lacks a real storage global).
function lsGet(key, fallback = null) {
  try { return typeof localStorage !== 'undefined' ? localStorage.getItem(key) : fallback; }
  catch { return fallback; }
}
function lsSet(key, value) {
  try { if (typeof localStorage !== 'undefined') localStorage.setItem(key, value); } catch { /* noop */ }
}
let autoReconnect = lsGet('nusashell.autoReconnect') !== 'false';

function rejectPending(message) {
  const error = new Error(message);
  for (const pending of wsPending.values()) pending.reject(error);
  wsPending.clear();
}

function scheduleReconnect() {
  if (!autoReconnect || reconnectTimer || !wsOptions.onStatus) return;
  wsOptions.onStatus('reconnecting');
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connectWS(wsOptions);
  }, reconnectDelay);
  reconnectDelay = Math.min(reconnectDelay * 2, 8000);
}

function openWS() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const socket = new WebSocket(`${proto}://${location.host}/ws`);
  ws = socket;
  socket.onopen = () => {
    if (ws !== socket) return;
    wsStatus = 'open';
    reconnectDelay = 500;
    wsOptions.onStatus?.('open');
  };
  socket.onclose = () => {
    if (ws !== socket) return;
    wsStatus = 'closed';
    ws = null;
    rejectPending('WebSocket connection closed');
    wsOptions.onStatus?.('closed');
    scheduleReconnect();
  };
  socket.onerror = () => {
    if (ws !== socket) return;
    wsStatus = 'error';
    wsOptions.onStatus?.('error');
  };
  socket.onmessage = (e) => {
    if (ws !== socket) return;
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    if (msg.id !== undefined) {
      const pending = wsPending.get(msg.id);
      if (!pending) return;
      wsPending.delete(msg.id);
      if (msg.ok) pending.resolve(msg.result ?? {});
      else {
        const err = new Error(msg.error?.message || 'WS RPC failed');
        err.code = msg.error?.code;
        pending.reject(err);
      }
      return;
    }
    if (msg.type) emit(msg.type, msg.payload);
  };
  return socket;
}

export function connectWS(options = {}) {
  if (typeof options.onStatus === 'function') {
    wsOptions = { ...wsOptions, onStatus: options.onStatus };
  }
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return ws;
  return openWS();
}

export function autoReconnectEnabled() {
  return autoReconnect;
}

export function setAutoReconnect(enabled) {
  autoReconnect = Boolean(enabled);
  lsSet('nusashell.autoReconnect', String(autoReconnect));
  if (!autoReconnect && reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (autoReconnect && !ws) connectWS();
}

// Test/lifecycle hook: stop reconnect timers when the host is shutting down.
export function closeWS() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  wsOptions = {};
  rejectPending('WebSocket connection closed');
  if (ws) {
    const socket = ws;
    ws = null;
    socket.close();
  }
}
