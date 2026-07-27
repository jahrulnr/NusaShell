import { describe, expect, it, afterAll, beforeAll } from "vitest";
import { WebSocket } from "ws";
import { WebSocketServer } from "../src/server/websocket-server.js";
import { MessageRouter } from "../src/routing/message-router.js";
import { CommandBus, QueryBus } from "@nusashell/application";

describe("WebSocketServer", () => {
  let server: WebSocketServer;
  let ws: WebSocket;
  const port = 9132;

  beforeAll(async () => {
    const queryBus = new QueryBus();
    queryBus.register("list-plugins", {
      handle: async () => ({
        plugins: [
          { pluginId: "com.example.notes", name: "Notes", version: "1.0.0", state: "idle", enabled: true },
        ],
      }),
    } as never);

    const router = new MessageRouter({ commandBus: new CommandBus(), queryBus });
    server = new WebSocketServer(router, { port });
    await server.start();
  });

  afterAll(async () => {
    if (ws && ws.readyState === ws.OPEN) {
      ws.close();
    }
    await server.stop();
  });

  it("accepts a connection and responds to plugin.list", async () => {
    await new Promise<void>((resolve, reject) => {
      ws = new WebSocket(`ws://127.0.0.1:${port}`);
      ws.on("open", () => {
        ws.send(JSON.stringify({
          kind: "request",
          id: "req_001",
          method: "plugin.list",
          payload: {},
        }));
      });
      ws.on("message", (data: Buffer) => {
        try {
          const response = JSON.parse(data.toString("utf-8"));
          expect(response.kind).toBe("response");
          expect(response.id).toBe("req_001");
          expect(response.ok).toBe(true);
          expect(response.result.plugins).toHaveLength(1);
          resolve();
        } catch (err) {
          reject(err);
        }
      });
      ws.on("error", reject);
      setTimeout(() => reject(new Error("timeout")), 5000);
    });
  });

  it("returns error for invalid JSON", async () => {
    await new Promise<void>((resolve, reject) => {
      const client = new WebSocket(`ws://127.0.0.1:${port}`);
      client.on("open", () => {
        client.send("not valid json");
      });
      client.on("message", (data: Buffer) => {
        try {
          const response = JSON.parse(data.toString("utf-8"));
          expect(response.ok).toBe(false);
          expect(response.error.code).toBe("INVALID_REQUEST");
          resolve();
        } catch (err) {
          reject(err);
        } finally {
          client.close();
        }
      });
      client.on("error", reject);
      setTimeout(() => reject(new Error("timeout")), 5000);
    });
  });
});
