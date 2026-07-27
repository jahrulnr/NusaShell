import { describe, expect, it } from "vitest";
import { PluginId } from "@nusashell/domain";
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

describe("PluginRuntimeManager", () => {
  describe("listPlugins", () => {
    it("returns idle state for plugins without runtime entries", async () => {
      const { pluginRepository, manager } = setup();
      pluginRepository.add(makePlugin("com.example.notes"));

      const views = await manager.listPlugins();

      expect(views).toHaveLength(1);
      expect(views[0]!.pluginId).toBe("com.example.notes");
      expect(views[0]!.state).toBe("idle");
    });

    it("returns empty list when no plugins installed", async () => {
      const { manager } = setup();
      const views = await manager.listPlugins();
      expect(views).toHaveLength(0);
    });
  });

  describe("startPlugin", () => {
    it("transitions idle -> starting -> running and publishes events", async () => {
      const { pluginRepository, manager, eventDispatcher } = setup();
      const plugin = makePlugin("com.example.notes");
      pluginRepository.add(plugin);

      const events: string[] = [];
      eventDispatcher.onAny({
        handle: (event) => {
          events.push(event.type);
        },
      });

      const view = await manager.startPlugin(plugin.id);

      expect(view.state).toBe("running");
      expect(events).toContain("plugin.state_changed");
      expect(events).toContain("plugin.started");
    });

    it("throws PLUGIN_NOT_FOUND for unknown plugin", async () => {
      const { manager } = setup();
      const idResult = PluginId.create("com.unknown.plugin");
      if (!idResult.ok) throw new Error("bad id");

      await expect(manager.startPlugin(idResult.value)).rejects.toMatchObject({
        code: "PLUGIN_NOT_FOUND",
      });
    });

    it("throws PLUGIN_DISABLED for disabled plugin", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("com.example.notes", {}, false);
      pluginRepository.add(plugin);

      await expect(manager.startPlugin(plugin.id)).rejects.toMatchObject({
        code: "PLUGIN_DISABLED",
      });
    });

    it("is idempotent when already running", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("com.example.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      const view2 = await manager.startPlugin(plugin.id);

      expect(view2.state).toBe("running");
    });

    it("serializes concurrent start requests (single-flight)", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("com.example.notes");
      pluginRepository.add(plugin);

      const [a, b] = await Promise.all([
        manager.startPlugin(plugin.id),
        manager.startPlugin(plugin.id),
      ]);

      expect(a.state).toBe("running");
      expect(b.state).toBe("running");
    });
  });

  describe("stopPlugin", () => {
    it("transitions running -> stopping -> idle and publishes stopped event", async () => {
      const { pluginRepository, manager, eventDispatcher } = setup();
      const plugin = makePlugin("com.example.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);

      const events: string[] = [];
      eventDispatcher.onAny({
        handle: (event) => {
          events.push(event.type);
        },
      });

      const view = await manager.stopPlugin(plugin.id);
      expect(view.state).toBe("idle");
      expect(events).toContain("plugin.stopped");
    });

    it("is a no-op when already idle", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("com.example.notes");
      pluginRepository.add(plugin);

      const view = await manager.stopPlugin(plugin.id);
      expect(view.state).toBe("idle");
    });
  });

  describe("callTool", () => {
    it("calls the MCP client and returns result", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("com.example.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);

      const client = mcpClientFactory.created[0]!;
      client.setToolResult("create_note", { id: "note-1" });

      const result = await manager.callTool(plugin.id, {
        requestId: "00000000-0000-1000-8000-000000000001",
        toolName: "create_note",
        args: { text: "hello" },
      });

      expect(result).toEqual({ id: "note-1" });
      expect(client.callLog).toHaveLength(1);
      expect(client.callLog[0]!.name).toBe("create_note");
    });

    it("publishes tool.call_completed event", async () => {
      const { pluginRepository, manager, eventDispatcher, mcpClientFactory } = setup();
      const plugin = makePlugin("com.example.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      mcpClientFactory.created[0]!.setToolResult("create_note", { ok: true });

      const events: string[] = [];
      eventDispatcher.onAny({
        handle: (event) => {
          events.push(event.type);
        },
      });

      await manager.callTool(plugin.id, {
        requestId: "00000000-0000-1000-8000-000000000002",
        toolName: "create_note",
        args: {},
      });

      expect(events).toContain("tool.call_completed");
    });

    it("throws when plugin is not running", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("com.example.notes");
      pluginRepository.add(plugin);

      await expect(
        manager.callTool(plugin.id, {
          requestId: "00000000-0000-1000-8000-000000000003",
          toolName: "create_note",
          args: {},
        }),
      ).rejects.toMatchObject({
        code: "INVALID_RUNTIME_TRANSITION",
      });
    });

    it("rejects with timeout when tool call exceeds timeout", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("com.example.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      mcpClientFactory.created[0]!.setToolDelay("slow_tool", 5000);

      await expect(
        manager.callTool(plugin.id, {
          requestId: "00000000-0000-1000-8000-000000000004",
          toolName: "slow_tool",
          args: {},
          timeoutMs: 50,
        }),
      ).rejects.toMatchObject({
        code: "TOOL_CALL_TIMEOUT",
      });
    });
  });

  describe("process crash", () => {
    it("transitions to crashed on unexpected process exit and publishes event", async () => {
      const { pluginRepository, manager, eventDispatcher, processAdapter } = setup();
      const plugin = makePlugin("com.example.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);

      const events: string[] = [];
      eventDispatcher.onAny({
        handle: (event) => {
          events.push(event.type);
        },
      });

      const handle = processAdapter.handles[0]!;
      handle.emitExit(1);

      // Wait for async crash handling
      await new Promise((resolve) => setTimeout(resolve, 10));

      const state = await manager.getPluginState(plugin.id);
      expect(state).toBe("crashed");
      expect(events).toContain("plugin.crashed");
    });

    it("cancels pending tool calls on crash", async () => {
      const { pluginRepository, manager, mcpClientFactory, processAdapter } = setup();
      const plugin = makePlugin("com.example.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      mcpClientFactory.created[0]!.setToolDelay("slow_tool", 5000);

      const callPromise = manager.callTool(plugin.id, {
        requestId: "00000000-0000-1000-8000-000000000005",
        toolName: "slow_tool",
        args: {},
        timeoutMs: 10000,
      });

      // Let the tool call start executing before crashing
      await new Promise((resolve) => setTimeout(resolve, 10));
      processAdapter.handles[0]!.emitExit(1);

      await expect(callPromise).rejects.toMatchObject({
        code: "PLUGIN_CRASHED",
      });
    });
  });

  describe("stopAll", () => {
    it("stops all running plugins", async () => {
      const { pluginRepository, manager } = setup();
      const pluginA = makePlugin("com.example.a");
      const pluginB = makePlugin("com.example.b");
      pluginRepository.add(pluginA);
      pluginRepository.add(pluginB);

      await manager.startPlugin(pluginA.id);
      await manager.startPlugin(pluginB.id);

      await manager.stopAll();

      expect(await manager.getPluginState(pluginA.id)).toBe("idle");
      expect(await manager.getPluginState(pluginB.id)).toBe("idle");
    });
  });
});
