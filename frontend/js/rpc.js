// Transport layer: HTTP /rpc, SSE /events, WebSocket /ws.
// All three transports speak the same event vocabulary.

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

export async function rpc(method, payload = {}, { timeoutMs = 60000 } = {}) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  let res;
  try {
    res = await fetch(`/rpc?event=${encodeURIComponent(method)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ method, payload }),
      signal: controller.signal,
    });
  } catch (err) {
    if (err?.name === 'AbortError') throw new Error(`RPC timed out: ${method}`);
    throw err;
  } finally {
    clearTimeout(timeout);
  }
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new Error(body.error?.message || `HTTP ${res.status}`);
    err.code = body.error?.code;
    throw err;
  }
  const rpcErr = toError(body);
  if (rpcErr) throw rpcErr;
  return body.result ?? {};
}

// ---- WebSocket (event stream: BE -> FE triggers) ----
let ws = null;
let wsStatus = 'idle';
let wsSeq = 0;
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

export function wsRpc(method, payload = {}, timeoutMs = 30000) {
  const wsConn = connectWS();
  return new Promise((resolve, reject) => {
    const id = ++wsSeq;
    const timer = setTimeout(() => {
      wsPending.delete(id);
      reject(new Error('WS RPC timed out'));
    }, timeoutMs);
    wsPending.set(id, {
      resolve: (v) => { clearTimeout(timer); resolve(v); },
      reject: (e) => { clearTimeout(timer); reject(e); },
    });
    const send = () => {
      if (wsConn.readyState !== WebSocket.OPEN) {
        wsPending.delete(id);
        reject(new Error('WebSocket is not connected'));
        return;
      }
      wsConn.send(JSON.stringify({ id, method, payload }));
    };
    if (wsConn.readyState === WebSocket.OPEN) send();
    else wsConn.addEventListener('open', send, { once: true });
  });
}

export function wsStatusNow() {
  return ws ? ws.readyState : (globalThis.WebSocket?.CLOSED ?? 3);
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
