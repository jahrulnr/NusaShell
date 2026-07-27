import { WebSocket } from "ws";
import { randomUUID } from "node:crypto";

export type ConnectionStatus = "disconnected" | "connecting" | "connected";

export interface WebSocketConnectionCallbacks {
  onMessage: (data: string) => void;
  onOpen: () => void;
  onClose: () => void;
  onError: (error: Error) => void;
}

export class WebSocketConnection {
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
      this.ws = new WebSocket(this.url);

      this.ws.on("open", () => {
        this.status = "connected";
        this.callbacks.onOpen();
        resolve();
      });

      this.ws.on("message", (data: Buffer) => {
        this.callbacks.onMessage(data.toString("utf-8"));
      });

      this.ws.on("close", () => {
        this.status = "disconnected";
        this.callbacks.onClose();
      });

      this.ws.on("error", (err: Error) => {
        if (this.status === "connecting") {
          this.status = "disconnected";
          reject(err);
        } else {
          this.callbacks.onError(err);
        }
      });
    });
  }

  send(data: string): void {
    if (this.ws && this.status === "connected") {
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

      ws.once("close", () => {
        this.status = "disconnected";
        resolve();
      });
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

export function generateRequestId(): string {
  return `req_${randomUUID()}`;
}
