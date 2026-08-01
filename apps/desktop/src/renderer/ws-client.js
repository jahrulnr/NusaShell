// Lightweight browser-native WebSocket client for the launcher renderer.
// Extracted from launcher.js — now backed by NusaClient from @nusashell/plugin-sdk
// with a BrowserWebSocketConnection adapter for the Electron renderer context.
//
// This is a temporary compatibility shim that preserves the original
// sendRequest/onEvent/subscribe/connectWs exports so launcher.js and
// other renderer modules can use NusaClient without rewriting all
// call sites. Eventually the renderer should use NusaClient directly.

import { NusaClient, BrowserWebSocketConnection } from "@nusashell/plugin-sdk";

let client = null;
let onOpenCallback = null;
let onLogCallback = null;
let wsUrl = null;

/**
 * Initialize the WebSocket client (creates NusaClient, registers callbacks).
 * Event handlers can be registered via onEvent() before connectWs() is called.
 * @param {{ url: string, onOpen?: (isOpen?: boolean) => void, onLog?: (level: string, message: string) => void }} config
 */
export function initWsClient(config) {
  wsUrl = config.url;
  onOpenCallback = config.onOpen ?? null;
  onLogCallback = config.onLog ?? null;

  client = new NusaClient({
    url: wsUrl,
    reconnect: { enabled: true, maxAttempts: Infinity, initialDelayMs: 1000, maxDelayMs: 1000, backoffFactor: 1, jitterMs: 0 },
    connectionFactory: (url, callbacks) => new BrowserWebSocketConnection(url, callbacks),
  });

  client.onReconnect(() => {
    log("info", `WebSocket reconnected to ${wsUrl}`);
    onOpenCallback?.(true);
  });

  client.onReconnectFailed(() => {
    log("error", "WebSocket reconnection failed");
    onOpenCallback?.(false);
  });
}

export function connectWs() {
  if (!client) return;
  client.connect().then(() => {
    log("info", `WebSocket connected to ${wsUrl}`);
    onOpenCallback?.(true);
  }).catch((error) => {
    log("error", `WebSocket connection failed: ${error?.message ?? error}`);
  });
}

export function sendRequest(method, payload, timeoutMs = 10000) {
  if (!client || !client.isConnected) {
    return Promise.reject(new Error("Not connected"));
  }
  log("debug", `WebSocket request ${method}`);
  return client.request(method, payload, timeoutMs);
}

export function onEvent(eventType, handler) {
  if (!client) return () => {};
  return client.on(eventType, (payload, sequence) => handler(payload, sequence));
}

export async function subscribe(eventTypes) {
  if (!client) return;
  const types = eventTypes ?? ["*"];
  await client.subscribe(types);
}

export function isConnected() {
  return client?.isConnected ?? false;
}

function log(level, message) {
  onLogCallback?.(level, message);
}
