import { describe, expect, it, afterEach } from "vitest";
import { WebSocketServer } from "../src/server/websocket-server.js";
import { MessageRouter } from "../src/routing/message-router.js";
import { CommandBus, QueryBus } from "@nusashell/application";
import { WebSocketTestClient, eventually } from "@nusashell/testing";

function makeServer(port: number): WebSocketServer {
  const commandBus = new CommandBus();
  const queryBus = new QueryBus();

  // Register a slow query handler to simulate an in-flight request
  queryBus.register("list-plugins", {
    handle: async () => {
      await new Promise((resolve) => setTimeout(resolve, 300));
      return {
        plugins: [
          { pluginId: "com.example.notes", name: "Notes", version: "1.0.0", icon: "📝", installPath: "/plugins/notes", state: "idle", enabled: true },
        ],
      };
    },
  } as never);

  const router = new MessageRouter({ commandBus, queryBus });
  return new WebSocketServer(router, { port });
}

describe("client disconnect during active request (§15)", () => {
  let server: WebSocketServer;
  let client: WebSocketTestClient;
  const port = 9150;

  afterEach(async () => {
    try { await client.disconnect(); } catch {}
    try { await server.stop(); } catch {}
  });

  it("server survives client disconnect during an in-flight request", async () => {
    server = makeServer(port);
    await server.start();

    client = new WebSocketTestClient(`ws://127.0.0.1:${port}`);
    await client.connect();
    expect(client.isConnected).toBe(true);

    // Start a request that takes 300ms
    const requestPromise = client.request("plugin.list", {});

    // Disconnect client after 50ms (request still in-flight)
    await new Promise((resolve) => setTimeout(resolve, 50));
    client.forceClose();

    // The pending request should be rejected
    await expect(requestPromise).rejects.toThrow();

    // Server should still be running — new client can connect
    const client2 = new WebSocketTestClient(`ws://127.0.0.1:${port}`);
    await client2.connect();
    expect(client2.isConnected).toBe(true);

    // New request should succeed
    const result = await client2.request("plugin.list", {}) as { plugins: unknown[] };
    expect(result.plugins).toHaveLength(1);

    await client2.disconnect();
  });

  it("session is removed from registry on disconnect", async () => {
    server = makeServer(port + 1);
    await server.start();

    client = new WebSocketTestClient(`ws://127.0.0.1:${port + 1}`);
    await client.connect();

    expect(server.sessionRegistry.all).toHaveLength(1);

    client.forceClose();

    await eventually(() => server.sessionRegistry.all.length === 0, 2000);
    expect(server.sessionRegistry.all).toHaveLength(0);
  });
});
