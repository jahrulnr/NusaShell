import { describe, expect, it, beforeAll, afterAll } from "vitest";
import { NusaClient } from "../src/client/nusa-client.js";
import { WebSocketServer } from "@nusashell/transport-ws";
import { MessageRouter } from "@nusashell/transport-ws";
import { CommandBus, QueryBus } from "@nusashell/application";
import { NusaClientError } from "../src/errors/nusa-client.error.js";

describe("NusaClient", () => {
  let server: WebSocketServer;
  let client: NusaClient;
  const port = 9133;

  beforeAll(async () => {
    const queryBus = new QueryBus();
    queryBus.register("list-plugins", {
      handle: async () => ({
        plugins: [
          { pluginId: "com.example.notes", name: "Notes", version: "1.0.0", state: "idle", enabled: true },
        ],
      }),
    } as never);

    const commandBus = new CommandBus();
    commandBus.register("start-plugin", {
      handle: async () => ({
        pluginId: "com.example.notes",
        state: "running",
      }),
    } as never);

    const router = new MessageRouter({ commandBus, queryBus });
    server = new WebSocketServer(router, { port });
    await server.start();

    client = new NusaClient({ url: `ws://127.0.0.1:${port}` });
    await client.connect();
  });

  afterAll(async () => {
    await client.disconnect();
    await server.stop();
  });

  it("connects and lists plugins", async () => {
    const result = await client.plugins.list();
    expect(result.plugins).toHaveLength(1);
    expect(result.plugins[0]!.pluginId).toBe("com.example.notes");
  });

  it("starts a plugin", async () => {
    const result = await client.plugins.start("com.example.notes");
    expect(result.pluginId).toBe("com.example.notes");
    expect(result.state).toBe("running");
  });

  it("receives events via event subscriber", async () => {
    const session = server.sessionRegistry.all[0]!;
    session.sendEvent({
      kind: "event",
      event: "plugin.started",
      payload: {
        pluginId: "com.example.notes",
        state: "running",
        pid: 12345,
        timestamp: new Date().toISOString(),
      },
    });

    const eventPromise = new Promise<unknown>((resolve) => {
      client.on("plugin.started", (payload) => resolve(payload));
    });

    session.sendEvent({
      kind: "event",
      event: "plugin.started",
      payload: {
        pluginId: "com.example.notes",
        state: "running",
        pid: 12345,
        timestamp: new Date().toISOString(),
      },
    });

    const payload = await eventPromise;
    expect(payload).toMatchObject({ pluginId: "com.example.notes", state: "running" });
  });

  it("rejects with NusaClientError on error response", async () => {
    const commandBus = new CommandBus();
    commandBus.register("stop-plugin", {
      handle: async () => {
        throw new (await import("@nusashell/application")).ApplicationError(
          "PLUGIN_NOT_FOUND",
          "Plugin not found",
        );
      },
    } as never);

    const queryBus = new QueryBus();
    const router = new MessageRouter({ commandBus, queryBus });
    const errorServer = new WebSocketServer(router, { port: port + 1 });
    await errorServer.start();

    const errorClient = new NusaClient({ url: `ws://127.0.0.1:${port + 1}` });
    await errorClient.connect();

    await expect(errorClient.plugins.stop("com.unknown")).rejects.toThrow(NusaClientError);

    await errorClient.disconnect();
    await errorServer.stop();
  });
});
