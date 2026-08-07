import { describe, expect, it, afterEach } from "vitest";
import { NusaClient } from "../src/client/nusa-client.js";
import { nodeConnectionFactory } from "./test-connection-factory.js";
import { WebSocketServer, MessageRouter } from "@nusashell/transport-ws";
import { CommandBus, QueryBus } from "@nusashell/application";

function makeServer(port: number): WebSocketServer {
  const queryBus = new QueryBus();
  const commandBus = new CommandBus();
  const router = new MessageRouter({ commandBus, queryBus });
  return new WebSocketServer(router, { port });
}

describe("NusaClient subscribe-after-connect", () => {
  let server: WebSocketServer;
  let client: NusaClient;
  const port = 9160;

  afterEach(async () => {
    try { await client.disconnect(); } catch {}
    try { await server.stop(); } catch {}
  });

  it("rejects subscribe called before connect and does not register server-side", async () => {
    server = makeServer(port);
    await server.start();

    client = new NusaClient({
      url: `ws://127.0.0.1:${port}`,
      connectionFactory: nodeConnectionFactory,
    });

    // subscribe before connect must reject — the connection is not open yet.
    await expect(client.subscribe(["agent.tool_call_start"])).rejects.toThrow();

    await client.connect();

    // No subscription should be registered on the server.
    const session = server.sessionRegistry.all[0]!;
    expect(server.subscriptionRegistry.isSubscribed(session.id, "agent.tool_call_start")).toBe(false);
  });

  it("establishes server-side subscription when subscribe is called after connect", async () => {
    server = makeServer(port + 1);
    await server.start();

    client = new NusaClient({
      url: `ws://127.0.0.1:${port + 1}`,
      connectionFactory: nodeConnectionFactory,
    });

    await client.connect();

    // subscribe after connect succeeds and registers on the server.
    await client.subscribe(["agent.tool_call_start"]);

    const session = server.sessionRegistry.all[0]!;
    expect(server.subscriptionRegistry.isSubscribed(session.id, "agent.tool_call_start")).toBe(true);
  });

  it("does not auto-subscribe on initial connect (only on reconnect)", async () => {
    server = makeServer(port + 2);
    await server.start();

    client = new NusaClient({
      url: `ws://127.0.0.1:${port + 2}`,
      connectionFactory: nodeConnectionFactory,
    });

    await client.connect();

    // After initial connect, no subscription should exist until explicitly
    // calling subscribe(). The NusaClient onOpen only resubscribes on
    // reconnect, not on the first connect.
    const session = server.sessionRegistry.all[0]!;
    expect(server.subscriptionRegistry.isSubscribed(session.id, "agent.tool_call_start")).toBe(false);
    expect(server.subscriptionRegistry.isSubscribed(session.id, "agent.tool_call_start")).toBe(false);
  });
});
