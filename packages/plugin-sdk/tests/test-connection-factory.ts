import { WebSocketConnection, type WebSocketConnectionFactory } from "../src/client/websocket-connection.js";

/**
 * Node.js connection factory for tests.
 * Uses the `ws`-based WebSocketConnection.
 */
export const nodeConnectionFactory: WebSocketConnectionFactory = (url, callbacks) =>
  new WebSocketConnection(url, callbacks);
