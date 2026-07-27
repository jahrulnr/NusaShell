import { WebSocket } from "ws";
import type { RequestMethod, EventType } from "@nusashell/contracts";

type EventHandler = (payload: unknown) => void;

export class WebSocketTestClient {
  private ws: WebSocket | null = null;
  private connected = false;
  private readonly pending = new Map<
    string,
    { resolve: (value: unknown) => void; reject: (error: unknown) => void; timer: ReturnType<typeof setTimeout> }
  >();
  private readonly eventHandlers = new Map<string, Set<EventHandler>>();

  constructor(private readonly url: string) {}

  connect(): Promise<void> {
    return new Promise((resolve, reject) => {
      this.ws = new WebSocket(this.url);

      this.ws.on("open", () => {
        this.connected = true;
        resolve();
      });

      this.ws.on("message", (data: Buffer) => {
        this.handleMessage(data.toString("utf-8"));
      });

      this.ws.on("close", () => {
        this.connected = false;
        this.rejectAllPending(new Error("Connection closed"));
      });

      this.ws.on("error", (err: Error) => {
        if (!this.connected) {
          reject(err);
        }
      });
    });
  }

  async disconnect(): Promise<void> {
    if (!this.ws) return;
    await new Promise<void>((resolve) => {
      const ws = this.ws;
      if (!ws) {
        resolve();
        return;
      }
      if (ws.readyState === WebSocket.CLOSED || ws.readyState === WebSocket.CLOSING) {
        resolve();
        return;
      }
      ws.once("close", () => resolve());
      ws.close();
    });
    this.connected = false;
  }

  forceClose(): void {
    if (this.ws) {
      this.ws.removeAllListeners("close");
      this.ws.terminate();
      this.connected = false;
      this.rejectAllPending(new Error("Force closed"));
    }
  }

  get isConnected(): boolean {
    return this.connected;
  }

  request<TResult = unknown>(
    method: RequestMethod,
    payload: Record<string, unknown>,
    timeoutMs = 5000,
  ): Promise<TResult> {
    const id = `test_${crypto.randomUUID()}`;
    const message = JSON.stringify({ kind: "request", id, method, payload });

    return new Promise<TResult>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`Request ${method} timed out after ${timeoutMs}ms`));
      }, timeoutMs);

      this.pending.set(id, {
        resolve: resolve as (value: unknown) => void,
        reject,
        timer,
      });

      this.ws?.send(message);
    });
  }

  onEvent(eventType: EventType, handler: EventHandler): () => void {
    const set = this.eventHandlers.get(eventType) ?? new Set();
    set.add(handler);
    this.eventHandlers.set(eventType, set);
    return () => {
      set.delete(handler);
      if (set.size === 0) {
        this.eventHandlers.delete(eventType);
      }
    };
  }

  private handleMessage(data: string): void {
    let parsed: unknown;
    try {
      parsed = JSON.parse(data);
    } catch {
      return;
    }
    if (!parsed || typeof parsed !== "object") return;

    const msg = parsed as Record<string, unknown>;

    if (msg.kind === "response") {
      const id = msg.id as string;
      const entry = this.pending.get(id);
      if (entry) {
        clearTimeout(entry.timer);
        this.pending.delete(id);
        if (msg.ok) {
          entry.resolve(msg.result);
        } else {
          entry.reject(msg.error);
        }
      }
    } else if (msg.kind === "event") {
      const event = msg.event as string;
      const handlers = this.eventHandlers.get(event);
      if (handlers) {
        for (const handler of handlers) {
          handler(msg.payload);
        }
      }
    }
  }

  private rejectAllPending(error: Error): void {
    for (const [, entry] of this.pending) {
      clearTimeout(entry.timer);
      entry.reject(error);
    }
    this.pending.clear();
  }
}
