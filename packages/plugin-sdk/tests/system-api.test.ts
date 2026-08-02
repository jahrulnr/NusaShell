import { describe, expect, it, afterEach } from "vitest";
import { NusaClient } from "../src/client/nusa-client.js";
import { nodeConnectionFactory } from "./test-connection-factory.js";
import { WebSocketServer, MessageRouter } from "@nusashell/transport-ws";
import { CommandBus, QueryBus, SystemPingHandler, SystemVersionHandler } from "@nusashell/application";

function makeServer(port: number): WebSocketServer {
  const queryBus = new QueryBus();
  queryBus.register("system-ping", new SystemPingHandler());
  queryBus.register("system-version", new SystemVersionHandler("0.0.9"));

  const commandBus = new CommandBus();
  const router = new MessageRouter({ commandBus, queryBus });
  return new WebSocketServer(router, { port });
}

describe("SystemApi", () => {
  let server: WebSocketServer;
  let client: NusaClient;
  const port = 9170;

  afterEach(async () => {
    try { await client.disconnect(); } catch {}
    try { await server.stop(); } catch {}
  });

  it("ping returns pong with timestamp", async () => {
    server = makeServer(port);
    await server.start();

    client = new NusaClient({ url: `ws://127.0.0.1:${port}`, connectionFactory: nodeConnectionFactory });
    await client.connect();

    const result = await client.system.ping();
    expect(result.pong).toBe(true);
    expect(result.timestamp).toBeDefined();
    expect(new Date(result.timestamp).getTime()).not.toBeNaN();
  });

  it("version returns version and name", async () => {
    server = makeServer(port + 1);
    await server.start();

    client = new NusaClient({ url: `ws://127.0.0.1:${port + 1}`, connectionFactory: nodeConnectionFactory });
    await client.connect();

    const result = await client.system.version();
    expect(result.version).toBe("0.0.9");
    expect(result.name).toBe("NusaShell");
  });
});
