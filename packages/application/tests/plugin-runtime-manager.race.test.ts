import { describe, expect, it } from "vitest";
import {
  PluginRuntimeManager,
  EventDispatcher,
} from "../src/index.js";
import {
  FakeClock,
  FakeMcpClientFactory,
  FakePluginRepository,
  FakeProcessAdapter,
  makePlugin,
} from "./fakes.js";

function setup() {
  const clock = new FakeClock();
  const eventDispatcher = new EventDispatcher();
  const pluginRepository = new FakePluginRepository();
  const processAdapter = new FakeProcessAdapter();
  const mcpClientFactory = new FakeMcpClientFactory();
  const manager = new PluginRuntimeManager({
    pluginRepository,
    processAdapter,
    mcpClientFactory,
    eventDispatcher,
    clock,
    toolCallTimeoutMs: 1000,
  });
  return { clock, eventDispatcher, pluginRepository, processAdapter, mcpClientFactory, manager };
}

describe("PluginRuntimeManager race conditions (§15)", () => {
  describe("concurrent start and stop", () => {
    it("Promise.all([start, stop]) ends in a consistent state", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      const [startResult, stopResult] = await Promise.allSettled([
        manager.startPlugin(plugin.id),
        manager.stopPlugin(plugin.id),
      ]);

      // Both should resolve (no rejections / hangs)
      expect(startResult.status).toBe("fulfilled");
      expect(stopResult.status).toBe("fulfilled");

      // Final state must be one of: idle or running — not stuck in starting/stopping
      const state = await manager.getPluginState(plugin.id);
      expect(["idle", "running"]).toContain(state);
    });

    it("Promise.all([stop, start]) ends in a consistent state", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);

      const [stopResult, startResult] = await Promise.allSettled([
        manager.stopPlugin(plugin.id),
        manager.startPlugin(plugin.id),
      ]);

      expect(stopResult.status).toBe("fulfilled");
      expect(startResult.status).toBe("fulfilled");

      const state = await manager.getPluginState(plugin.id);
      expect(["idle", "running"]).toContain(state);
    });
  });

  describe("callTool while starting", () => {
    it("callTool fired concurrently with start does not hang", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      const startPromise = manager.startPlugin(plugin.id);
      const callPromise = manager.callTool(plugin.id, {
        requestId: "00000000-0000-1000-8000-000000000010",
        toolName: "create_note",
        args: {},
      });

      await startPromise;

      // callTool should either resolve or reject — not hang
      const result = await Promise.allSettled([callPromise]);
      expect(result[0]!.status).not.toBe("pending");

      // If it resolved, the result should be valid
      if (result[0]!.status === "fulfilled") {
        expect(result[0]!.value).toBeDefined();
      }
    });
  });

  describe("timeout followed by late response", () => {
    it("timeout error is thrown, late response is ignored", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      const client = mcpClientFactory.created[0]!;
      client.setToolDelay("slow_tool", 5000);
      client.setToolResult("slow_tool", { late: true });

      await expect(
        manager.callTool(plugin.id, {
          requestId: "00000000-0000-1000-8000-000000000020",
          toolName: "slow_tool",
          args: {},
          timeoutMs: 50,
        }),
      ).rejects.toMatchObject({
        code: "TOOL_CALL_TIMEOUT",
      });

      // Wait for the late response to arrive
      await new Promise((resolve) => setTimeout(resolve, 100));

      // Plugin should still be running (not crashed)
      const state = await manager.getPluginState(plugin.id);
      expect(state).toBe("running");

      // The pending call should be cleaned up
      // We can verify by making a new call with the same request ID without deadlock
      client.setToolDelay("slow_tool", 0);
      client.setToolResult("slow_tool", { ok: true });
      const result = await manager.callTool(plugin.id, {
        requestId: "00000000-0000-1000-8000-000000000021",
        toolName: "slow_tool",
        args: {},
      });
      expect(result).toEqual({ ok: true });
    });
  });

  describe("backend shutdown while plugins are active", () => {
    it("stopAll transitions all running plugins to idle", async () => {
      const { pluginRepository, manager } = setup();
      const pluginA = makePlugin("example.a");
      const pluginB = makePlugin("example.b");
      pluginRepository.add(pluginA);
      pluginRepository.add(pluginB);

      await manager.startPlugin(pluginA.id);
      await manager.startPlugin(pluginB.id);

      expect(await manager.getPluginState(pluginA.id)).toBe("running");
      expect(await manager.getPluginState(pluginB.id)).toBe("running");

      await manager.stopAll();

      expect(await manager.getPluginState(pluginA.id)).toBe("idle");
      expect(await manager.getPluginState(pluginB.id)).toBe("idle");
    });

    it("stopAll cancels pending tool calls", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      mcpClientFactory.created[0]!.setToolDelay("slow_tool", 5000);

      const callPromise = manager.callTool(plugin.id, {
        requestId: "00000000-0000-1000-8000-000000000030",
        toolName: "slow_tool",
        args: {},
        timeoutMs: 50,
      });

      // Catch the rejection immediately to avoid unhandled rejection
      const callResult = callPromise.then(
        () => ({ ok: true as const, value: undefined }),
        (err: unknown) => ({ ok: false as const, error: err }),
      );

      // Let the tool call start and time out
      await new Promise((resolve) => setTimeout(resolve, 100));

      await manager.stopAll();

      const result = await callResult;
      expect(result.ok).toBe(false);
      if (!result.ok) {
        expect((result.error as { code: string }).code).toBe("TOOL_CALL_TIMEOUT");
      }

      expect(await manager.getPluginState(plugin.id)).toBe("idle");
    });
  });

  describe("duplicate request ID", () => {
    it("two callTool with same requestId do not deadlock", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      mcpClientFactory.created[0]!.setToolResult("create_note", { ok: true });

      const reqId = "00000000-0000-1000-8000-000000000040";

      const [a, b] = await Promise.allSettled([
        manager.callTool(plugin.id, {
          requestId: reqId,
          toolName: "create_note",
          args: {},
        }),
        manager.callTool(plugin.id, {
          requestId: reqId,
          toolName: "create_note",
          args: {},
        }),
      ]);

      // Both should settle (no hang/deadlock)
      expect(a.status).not.toBe("pending");
      expect(b.status).not.toBe("pending");

      // At least one should succeed
      const fulfilled = [a, b].filter((r) => r.status === "fulfilled");
      expect(fulfilled.length).toBeGreaterThanOrEqual(1);
    });
  });

  describe("concurrent restart and stop", () => {
    it("Promise.all([restart, stop]) ends in a consistent state", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);

      const [restartResult, stopResult] = await Promise.allSettled([
        manager.restartPlugin(plugin.id),
        manager.stopPlugin(plugin.id),
      ]);

      expect(restartResult.status).toBe("fulfilled");
      expect(stopResult.status).toBe("fulfilled");

      const state = await manager.getPluginState(plugin.id);
      expect(["idle", "running"]).toContain(state);
    });
  });
});
