import { describe, expect, it, afterEach } from "vitest";
import { createContainer } from "../src/container.js";
import { ShutdownCoordinator } from "../src/shutdown.js";
import { WebSocketTestClient, eventually } from "@nusashell/testing";

describe("ShutdownCoordinator", () => {
  let container: ReturnType<typeof createContainer>;

  afterEach(async () => {
    try { await container?.wsServer.stop(); } catch {}
  });

  it("router rejects commands after close()", async () => {
    container = createContainer({ port: 9160 });
    await container.wsServer.start();

    const client = new WebSocketTestClient(`ws://127.0.0.1:9160`);
    await client.connect();

    // Before close — requests work
    const result = await client.request("plugin.list", {}) as { plugins: unknown[] };
    expect(result.plugins).toEqual([]);

    // Close router (simulates shutdown step 2)
    container.router.close();
    expect(container.router.isClosed).toBe(true);

    // After close — requests rejected
    await expect(client.request("plugin.list", {}, 2000)).rejects.toThrow();

    await client.disconnect();
  });

  it("full shutdown sequence: stops WS, closes sessions, closes DB", async () => {
    container = createContainer({ port: 9161, dbPath: ":memory:" });
    await container.wsServer.start();

    const client = new WebSocketTestClient(`ws://127.0.0.1:9161`);
    await client.connect();

    expect(container.wsServer.sessionRegistry.all).toHaveLength(1);
    expect(container.db).toBeDefined();

    // Prevent process.exit from actually exiting
    const originalExit = process.exit;
    let exitCalled = false;
    process.exit = ((_code?: number) => { exitCalled = true; }) as never;

    const shutdown = new ShutdownCoordinator(container);
    await shutdown.shutdown();

    // Restore
    process.exit = originalExit;

    expect(exitCalled).toBe(true);
    expect(shutdown.isShuttingDown).toBe(true);
    expect(container.router.isClosed).toBe(true);
    expect(container.wsServer.sessionRegistry.all).toHaveLength(0);

    // Client should be disconnected after sessions cleared
    await eventually(() => !client.isConnected, 2000);
    expect(client.isConnected).toBe(false);
  });
});
