import { WebSocketServer as WsServer, type WebSocket } from "ws";
import { randomUUID } from "node:crypto";
import type { LoggerPort } from "@nusashell/application";
import { WebSocketSession } from "./websocket-session.js";
import { SessionRegistry } from "./session-registry.js";
import { ClientSubscriptionRegistry } from "../events/client-subscription-registry.js";
import type { MessageRouter } from "../routing/message-router.js";
import type { ResponseEnvelope } from "@nusashell/contracts";
import { isSupportedVersion } from "@nusashell/contracts";

export interface WebSocketServerOptions {
  readonly port: number;
  readonly host?: string;
  readonly logger?: LoggerPort;
}

export class WebSocketServer {
  private readonly registry = new SessionRegistry();
  private readonly subscriptions = new ClientSubscriptionRegistry();
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
          let parsed: unknown;
          try {
            parsed = JSON.parse(raw);
          } catch {
            session.sendResponse({
              kind: "response",
              id: "",
              ok: false,
              error: { code: "INVALID_REQUEST", message: "Invalid JSON" },
            });
            return;
          }

          const msg = parsed as Record<string, unknown>;
          if (msg && msg.kind === "request") {
            const version = msg.protocolVersion as string | undefined;
            if (version !== undefined && !isSupportedVersion(version)) {
              session.sendResponse({
                kind: "response",
                id: (msg.id as string) ?? "",
                ok: false,
                error: {
                  code: "UNSUPPORTED_VERSION",
                  message: `Unsupported protocol version: ${version}`,
                },
              });
              session.close();
              return;
            }
          }

          if (msg && msg.kind === "request" && (msg.method === "subscribe" || msg.method === "unsubscribe")) {
            const response = this.handleSubscription(sessionId, msg);
            session.sendResponse(response);
            return;
          }

          // Handle concurrently — don't let a slow command (e.g. plugin.start)
          // block subsequent queries (e.g. plugin.get) on the same session
          void this.router.handle(raw).then((response) => {
            session.sendResponse(response);
          });
        });

        ws.on("close", () => {
          this.registry.remove(sessionId);
          this.subscriptions.clear(sessionId);
        });

        ws.on("error", (err) => {
          this.options.logger?.error("WebSocket session error sessionId=%s: %s", sessionId, err);
          this.registry.remove(sessionId);
          this.subscriptions.clear(sessionId);
        });
      });

      this.server.on("listening", () => resolve());
    });
  }

  private handleSubscription(sessionId: string, msg: Record<string, unknown>): ResponseEnvelope {
    const id = (msg.id as string) ?? "";
    const method = msg.method as "subscribe" | "unsubscribe";
    const payload = (msg.payload as { eventTypes?: string[] }) ?? {};
    const eventTypes = payload.eventTypes ?? ["*"];

    if (method === "subscribe") {
      this.subscriptions.subscribe(sessionId, eventTypes);
    } else {
      this.subscriptions.unsubscribe(sessionId, eventTypes);
    }

    return {
      kind: "response",
      id,
      ok: true,
      result: { subscribed: method === "subscribe" ? eventTypes : [] },
    };
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

  get subscriptionRegistry(): ClientSubscriptionRegistry {
    return this.subscriptions;
  }
}
