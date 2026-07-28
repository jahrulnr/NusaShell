import { describe, expect, it, afterEach, beforeEach } from "vitest";
import { NusaClient } from "../src/client/nusa-client.js";
import { WebSocketServer, MessageRouter } from "@nusashell/transport-ws";
import { CommandBus, QueryBus } from "@nusashell/application";

function makeServer(port: number): WebSocketServer {
  const queryBus = new QueryBus();
  queryBus.register("list-plugins", {
    handle: async () => ({
      plugins: [
        { pluginId: "com.example.notes", name: "Notes", version: "1.0.0", icon: "📝", installPath: "/plugins/notes", state: "idle", enabled: true },
      ],
    }),
  } as never);

  const commandBus = new CommandBus();
  const router = new MessageRouter({ commandBus, queryBus });
  return new WebSocketServer(router, { port });
}

async function waitFor(predicate: () => boolean, timeoutMs = 5000): Promise<void> {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) {
      throw new Error(`waitFor timed out after ${timeoutMs}ms`);
    }
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
}

describe("NusaClient reconnect integration", () => {
  let server: WebSocketServer;
  let client: NusaClient;
  const basePort = 9140;

  afterEach(async () => {
    try { await client.disconnect(); } catch {}
    try { await server.stop(); } catch {}
  });

  it("auto-reconnects after server restart", async () => {
    server = makeServer(basePort);
    await server.start();

    client = new NusaClient({
      url: `ws://127.0.0.1:${basePort}`,
      reconnect: { enabled: true, initialDelayMs: 50, maxDelayMs: 200, jitterMs: 0 },
    });
    await client.connect();
    expect(client.isConnected).toBe(true);

    // Kill server
    await server.stop();

    await waitFor(() => client.isReconnecting, 1000);
    expect(client.isReconnecting).toBe(true);

    // Restart server
    server = makeServer(basePort);
    await server.start();

    await waitFor(() => client.isConnected, 5000);
    expect(client.isConnected).toBe(true);
    expect(client.isReconnecting).toBe(false);

    // Verify requests work after reconnect
    const result = await client.plugins.list();
    expect(result.plugins).toHaveLength(1);
  });

  it("preserves event handlers after reconnect (resubscribe)", async () => {
    server = makeServer(basePort + 1);
    await server.start();

    client = new NusaClient({
      url: `ws://127.0.0.1:${basePort + 1}`,
      reconnect: { enabled: true, initialDelayMs: 50, maxDelayMs: 200, jitterMs: 0 },
    });
    await client.connect();

    // Subscribe and register an event handler
    await client.subscribe(["plugin.started"]);
    let receivedPayload: unknown = null;
    client.on("plugin.started", (payload: unknown) => {
      receivedPayload = payload;
    });

    // Kill and restart server
    await server.stop();
    await waitFor(() => client.isReconnecting, 1000);

    server = makeServer(basePort + 1);
    await server.start();

    await waitFor(() => client.isConnected, 5000);

    // Server-side subscription should be restored after auto-resubscribe
    const session = server.sessionRegistry.all[0]!;
    expect(server.subscriptionRegistry.isSubscribed(session.id, "plugin.started")).toBe(true);

    // Send an event from the server — handler should still work
    session.sendEvent({
      kind: "event",
      event: "plugin.started",
      sequence: 1,
      payload: {
        pluginId: "com.example.notes",
        state: "running",
        pid: 999,
        timestamp: new Date().toISOString(),
      },
    });

    await waitFor(() => receivedPayload !== null, 2000);
    expect(receivedPayload).toMatchObject({ pluginId: "com.example.notes", state: "running" });
  });

  it("auto-resubscribe restores server-side subscriptions after reconnect", async () => {
    server = makeServer(basePort + 6);
    await server.start();

    client = new NusaClient({
      url: `ws://127.0.0.1:${basePort + 6}`,
      reconnect: { enabled: true, initialDelayMs: 50, maxDelayMs: 200, jitterMs: 0 },
    });
    await client.connect();

    // Subscribe to specific event types
    await client.subscribe(["plugin.started", "plugin.stopped"]);

    // Verify server-side registry has the subscriptions
    const sessionBefore = server.sessionRegistry.all[0]!;
    expect(server.subscriptionRegistry.isSubscribed(sessionBefore.id, "plugin.started")).toBe(true);
    expect(server.subscriptionRegistry.isSubscribed(sessionBefore.id, "plugin.stopped")).toBe(true);

    // Kill and restart server
    await server.stop();
    await waitFor(() => client.isReconnecting, 1000);

    server = makeServer(basePort + 6);
    await server.start();

    await waitFor(() => client.isConnected, 5000);

    // After reconnect, new session should have subscriptions restored
    const sessionAfter = server.sessionRegistry.all[0]!;
    expect(sessionAfter.id).not.toBe(sessionBefore.id);
    expect(server.subscriptionRegistry.isSubscribed(sessionAfter.id, "plugin.started")).toBe(true);
    expect(server.subscriptionRegistry.isSubscribed(sessionAfter.id, "plugin.stopped")).toBe(true);
  });

  it("fires onReconnect callback after successful reconnect", async () => {
    server = makeServer(basePort + 2);
    await server.start();

    let reconnected = false;
    client = new NusaClient({
      url: `ws://127.0.0.1:${basePort + 2}`,
      reconnect: { enabled: true, initialDelayMs: 50, maxDelayMs: 200, jitterMs: 0 },
    });
    client.onReconnect(() => { reconnected = true; });
    await client.connect();

    await server.stop();
    await waitFor(() => client.isReconnecting, 1000);

    server = makeServer(basePort + 2);
    await server.start();

    await waitFor(() => reconnected, 5000);
    expect(reconnected).toBe(true);
    expect(client.isConnected).toBe(true);
  });

  it("fires onReconnectFailed after maxAttempts exhausted", async () => {
    server = makeServer(basePort + 3);
    await server.start();

    let reconnectFailed = false;
    client = new NusaClient({
      url: `ws://127.0.0.1:${basePort + 3}`,
      reconnect: {
        enabled: true,
        maxAttempts: 2,
        initialDelayMs: 10,
        maxDelayMs: 20,
        jitterMs: 0,
      },
    });
    client.onReconnectFailed(() => { reconnectFailed = true; });
    await client.connect();

    // Kill server and don't restart
    await server.stop();

    await waitFor(() => reconnectFailed, 5000);
    expect(reconnectFailed).toBe(true);
    expect(client.isReconnecting).toBe(false);
    expect(client.isConnected).toBe(false);
  });

  it("explicit disconnect does NOT trigger reconnect", async () => {
    server = makeServer(basePort + 4);
    await server.start();

    client = new NusaClient({
      url: `ws://127.0.0.1:${basePort + 4}`,
      reconnect: { enabled: true, initialDelayMs: 50, maxDelayMs: 200, jitterMs: 0 },
    });
    await client.connect();
    expect(client.isConnected).toBe(true);

    await client.disconnect();
    expect(client.isConnected).toBe(false);
    expect(client.isReconnecting).toBe(false);

    // Wait a bit to ensure no reconnect attempt
    await new Promise((resolve) => setTimeout(resolve, 200));
    expect(client.isReconnecting).toBe(false);
    expect(client.isConnected).toBe(false);
  });

  it("rejects pending requests on disconnect but new requests work after reconnect", async () => {
    server = makeServer(basePort + 5);
    await server.start();

    client = new NusaClient({
      url: `ws://127.0.0.1:${basePort + 5}`,
      reconnect: { enabled: true, initialDelayMs: 50, maxDelayMs: 200, jitterMs: 0 },
      defaultTimeoutMs: 10000,
    });
    await client.connect();

    // Start a request that will be pending when connection drops
    const listPromise = client.plugins.list();

    // Kill server immediately (request still pending)
    await server.stop();

    // The pending request should be rejected
    await expect(listPromise).rejects.toThrow();

    // Restart server
    server = makeServer(basePort + 5);
    await server.start();

    await waitFor(() => client.isConnected, 5000);

    // New request should work
    const result = await client.plugins.list();
    expect(result.plugins).toHaveLength(1);
  });
});
