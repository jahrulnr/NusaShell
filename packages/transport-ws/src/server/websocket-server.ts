import { WebSocketServer as WsServer, type WebSocket } from "ws";
import { randomUUID } from "node:crypto";
import { WebSocketSession } from "./websocket-session.js";
import { SessionRegistry } from "./session-registry.js";
import type { MessageRouter } from "../routing/message-router.js";

export interface WebSocketServerOptions {
  readonly port: number;
  readonly host?: string;
}

export class WebSocketServer {
  private readonly registry = new SessionRegistry();
  private server: WsServer | null = null;

  constructor(
    private readonly router: MessageRouter,
    private readonly options: WebSocketServerOptions,
  ) {}

  start(): Promise<void> {
    return new Promise((resolve) => {
      this.server = new WsServer({
        port: this.options.port,
        host: this.options.host ?? "0.0.0.0",
      });

      this.server.on("connection", (ws: WebSocket) => {
        const sessionId = randomUUID();
        const session = new WebSocketSession(sessionId, ws);
        this.registry.add(session);

        ws.on("message", async (data: Buffer) => {
          const raw = data.toString("utf-8");
          const response = await this.router.handle(raw);
          session.sendResponse(response);
        });

        ws.on("close", () => {
          this.registry.remove(sessionId);
        });

        ws.on("error", () => {
          this.registry.remove(sessionId);
        });
      });

      this.server.on("listening", () => resolve());
    });
  }

  stop(): Promise<void> {
    return new Promise((resolve, reject) => {
      if (!this.server) {
        resolve();
        return;
      }
      this.registry.clear();
      this.server.close((err) => {
        this.server = null;
        if (err) {
          reject(err);
        } else {
          resolve();
        }
      });
    });
  }

  get sessionRegistry(): SessionRegistry {
    return this.registry;
  }
}
