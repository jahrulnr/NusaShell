import type { ConnectionStatus, IWebSocketConnection, WebSocketConnectionCallbacks } from "./connection-types.js";

/**
 * Browser-compatible WebSocket connection using the native browser WebSocket API.
 *
 * This is the browser counterpart to `WebSocketConnection` (which uses the
 * Node.js `ws` package). It implements the same `IWebSocketConnection` interface
 * so `NusaClient` can work in Electron renderer / browser contexts.
 *
 * Uses `crypto.randomUUID()` (available in modern browsers and Node.js 20+)
 * instead of `node:crypto`.
 */
export class BrowserWebSocketConnection implements IWebSocketConnection {
  private ws: WebSocket | null = null;
  private status: ConnectionStatus = "disconnected";

  constructor(
    private readonly url: string,
    private readonly callbacks: WebSocketConnectionCallbacks,
  ) {}

  connect(): Promise<void> {
    if (this.status === "connected" || this.status === "connecting") {
      return Promise.resolve();
    }

    this.status = "connecting";

    return new Promise((resolve, reject) => {
      try {
        this.ws = new WebSocket(this.url);
      } catch (err) {
        this.status = "disconnected";
        reject(err instanceof Error ? err : new Error(String(err)));
        return;
      }

      this.ws.onopen = () => {
        this.status = "connected";
        this.callbacks.onOpen();
        resolve();
      };

      this.ws.onmessage = (event: MessageEvent) => {
        const data = typeof event.data === "string" ? event.data : String(event.data);
        this.callbacks.onMessage(data);
      };

      this.ws.onclose = () => {
        this.status = "disconnected";
        this.callbacks.onClose();
      };

      this.ws.onerror = () => {
        const error = new Error(`WebSocket error connecting to ${this.url}`);
        if (this.status === "connecting") {
          this.status = "disconnected";
          reject(error);
        } else {
          this.callbacks.onError(error);
        }
      };
    });
  }

  send(data: string): void {
    if (this.ws && this.status === "connected" && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(data);
    }
  }

  disconnect(): Promise<void> {
    return new Promise((resolve) => {
      if (!this.ws) {
        this.status = "disconnected";
        resolve();
        return;
      }

      const ws = this.ws;
      this.ws = null;

      if (ws.readyState === WebSocket.CLOSED || ws.readyState === WebSocket.CLOSING) {
        this.status = "disconnected";
        resolve();
        return;
      }

      ws.onclose = () => {
        this.status = "disconnected";
        resolve();
      };
      ws.close();
    });
  }

  get isConnected(): boolean {
    return this.status === "connected";
  }

  get connectionStatus(): ConnectionStatus {
    return this.status;
  }
}
