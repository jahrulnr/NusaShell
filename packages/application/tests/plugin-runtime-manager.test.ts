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
  const logMessages: string[] = [];
  const logger = {
    info: (message: string) => logMessages.push(message),
    warn: (message: string) => logMessages.push(message),
    error: (message: string) => logMessages.push(message),
    debug: (message: string) => logMessages.push(message),
  };
  const manager = new PluginRuntimeManager({
    pluginRepository,
    processAdapter,
    mcpClientFactory,
    eventDispatcher,
    clock,
    logger,
    toolCallTimeoutMs: 1000,
  });
  return { clock, eventDispatcher, pluginRepository, processAdapter, mcpClientFactory, manager, logMessages };
}

describe("PluginRuntimeManager", () => {
  it("persists autostart and starts only opted-in plugins", async () => {
    const { manager, pluginRepository } = setup();
    const optedIn = makePlugin("example.autostart");
    const onDemand = makePlugin("example.on-demand");
    pluginRepository.add(optedIn);
    pluginRepository.add(onDemand);

    const updated = await manager.setAutostart(optedIn.id, true);
    expect(updated.autostart).toBe(true);
    await manager.startAutostartPlugins();

    expect(await manager.getPluginState(optedIn.id)).toBe("running");
    expect(await manager.getPluginState(onDemand.id)).toBe("idle");
  });
  describe("listPlugins", () => {
    it("returns idle state for plugins without runtime entries", async () => {
      const { pluginRepository, manager } = setup();
      pluginRepository.add(makePlugin("nusashell.notes", {
        ui: {
          entry: "ui/mail.html",
          window: {
            mode: "fullscreen",
            defaultSize: { width: 1280, height: 800 },
            resizable: true,
          },
        },
        mcp: {
          transport: "stdio",
          command: "node",
          args: ["mcp/server.js"],
          keepAliveOnClose: true,
        },
      }));

      const views = await manager.listPlugins();

      expect(views).toHaveLength(1);
      expect(views[0]!.pluginId).toBe("nusashell.notes");
      expect(views[0]!.state).toBe("idle");
      expect(views[0]!.ui).toEqual({
        entry: "ui/mail.html",
        window: {
          mode: "fullscreen",
          defaultSize: { width: 1280, height: 800 },
          resizable: true,
        },
      });
      expect(views[0]!.keepAliveOnClose).toBe(true);
    });

    it("returns empty list when no plugins installed", async () => {
      const { manager } = setup();
      const views = await manager.listPlugins();
      expect(views).toHaveLength(0);
    });
  });

  describe("startPlugin", () => {
    it("merges host-provided runtime environment without mutating the manifest", async () => {
      const { pluginRepository, mcpClientFactory } = setup();
      const plugin = makePlugin("nusashell.notes", {
        mcp: {
          transport: "stdio",
          command: "node",
          args: ["server.js"],
          env: { MANIFEST_VALUE: "kept" },
        },
      });
      pluginRepository.add(plugin);
      const managerWithEnvironment = new PluginRuntimeManager({
        pluginRepository,
        processAdapter: new FakeProcessAdapter(),
        mcpClientFactory,
        eventDispatcher: new EventDispatcher(),
        clock: new FakeClock(),
        resolveRuntimeEnvironment: async (pluginId) =>
          pluginId === "nusashell.notes" ? { RUNTIME_SECRET: "injected" } : {},
      });

      await managerWithEnvironment.startPlugin(plugin.id);

      expect(mcpClientFactory.stdioCalls[0]?.env).toEqual({
        MANIFEST_VALUE: "kept",
        RUNTIME_SECRET: "injected",
      });
      expect(plugin.manifest.mcp.env).toEqual({ MANIFEST_VALUE: "kept" });
    });

    it("logs the connected MCP plugin id without a raw printf placeholder", async () => {
      const { pluginRepository, manager, logMessages } = setup();
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);

      expect(logMessages).toContain("MCP client connected (stdio) for plugin nusashell.notes");
      expect(logMessages.some((message) => message.includes("%s"))).toBe(false);
    });

    it("transitions idle -> starting -> running and publishes events", async () => {
      const { pluginRepository, manager, eventDispatcher } = setup();
      const plugin = makePlugin("nusashell.notes");
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
      const plugin = makePlugin("nusashell.notes", {}, false);
      pluginRepository.add(plugin);

      await expect(manager.startPlugin(plugin.id)).rejects.toMatchObject({
        code: "PLUGIN_DISABLED",
      });
    });

    it("is idempotent when already running", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      const view2 = await manager.startPlugin(plugin.id);

      expect(view2.state).toBe("running");
    });

    it("serializes concurrent start requests (single-flight)", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("nusashell.notes");
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
      const plugin = makePlugin("nusashell.notes");
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
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      const view = await manager.stopPlugin(plugin.id);
      expect(view.state).toBe("idle");
    });
  });

  describe("callTool", () => {
    it("calls the MCP client and returns result", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("nusashell.notes");
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
      const plugin = makePlugin("nusashell.notes");
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
      const plugin = makePlugin("nusashell.notes");
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
      const plugin = makePlugin("nusashell.notes");
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

    it("runs concurrent tool calls against the same plugin in parallel (not serial)", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      const client = mcpClientFactory.created[0]!;
      client.setToolDelay("tool_a", 60);
      client.setToolResult("tool_a", { a: 1 });
      client.setToolDelay("tool_b", 60);
      client.setToolResult("tool_b", { b: 2 });

      const start = Date.now();
      const [ra, rb] = await Promise.all([
        manager.callTool(plugin.id, {
          requestId: "00000000-0000-1000-8000-000000000005",
          toolName: "tool_a",
          args: {},
        }),
        manager.callTool(plugin.id, {
          requestId: "00000000-0000-1000-8000-000000000006",
          toolName: "tool_b",
          args: {},
        }),
      ]);
      const elapsed = Date.now() - start;

      expect(ra).toEqual({ a: 1 });
      expect(rb).toEqual({ b: 2 });
      // Parallel: combined wall time stays near one call's delay (~60ms),
      // not the serialized sum (~120ms).
      expect(elapsed).toBeLessThan(115);
      // Both reached the MCP client concurrently.
      expect(client.callLog.map((c) => c.name)).toEqual(["tool_a", "tool_b"]);
    });

    it("stop cancels in-flight concurrent tool calls (mutual exclusion with lifecycle)", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      mcpClientFactory.created[0]!.setToolDelay("slow_tool", 5000);

      const callPromise = manager.callTool(plugin.id, {
        requestId: "00000000-0000-1000-8000-000000000007",
        toolName: "slow_tool",
        args: {},
        timeoutMs: 10000,
      });
      const callResult = callPromise.then(
        () => ({ ok: true as const }),
        (err: unknown) => ({ ok: false as const, error: err }),
      );

      // Give the call a beat to become in-flight (state running) before stop.
      await new Promise((resolve) => setTimeout(resolve, 10));

      const stopped = await manager.stopPlugin(plugin.id);
      expect(stopped.state).toBe("idle");

      const result = await callResult;
      expect(result.ok).toBe(false);
      if (!result.ok) {
        expect((result.error as { code: string }).code).toBe("TOOL_CALL_CANCELLED");
      }
      expect(await manager.getPluginState(plugin.id)).toBe("idle");
    });
  });

  describe("MCP prompts and resources", () => {
    it("brokers prompt and resource capability calls for a running plugin", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("example.context");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      const client = mcpClientFactory.created[0]!;
      client.prompts.push({
        name: "summarize_note",
        description: "Summarize a note",
        arguments: [{ name: "note", required: true }],
      });
      client.setPromptResult("summarize_note", {
        description: "A note summary prompt",
        messages: [{ role: "user", content: { type: "text", text: "Summarize this note" } }],
      });
      client.resources.push({ uri: "notes://daily", name: "Daily notes", mimeType: "text/markdown" });
      client.resourceTemplates.push({ uriTemplate: "notes://{date}", name: "Note by date" });
      client.setResourceResult("notes://daily", {
        contents: [{ uri: "notes://daily", mimeType: "text/markdown", text: "# Today" }],
      });

      await expect(manager.listPrompts(plugin.id)).resolves.toEqual(client.prompts);
      await expect(manager.getPrompt(plugin.id, "summarize_note", { note: "Today" })).resolves.toEqual({
        description: "A note summary prompt",
        messages: [{ role: "user", content: { type: "text", text: "Summarize this note" } }],
      });
      await expect(manager.listResources(plugin.id)).resolves.toEqual(client.resources);
      await expect(manager.listResourceTemplates(plugin.id)).resolves.toEqual(client.resourceTemplates);
      await expect(manager.readResource(plugin.id, "notes://daily")).resolves.toEqual({
        contents: [{ uri: "notes://daily", mimeType: "text/markdown", text: "# Today" }],
      });
    });

    it("rejects prompt discovery when the plugin is not running", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("example.context");
      pluginRepository.add(plugin);

      await expect(manager.listPrompts(plugin.id)).rejects.toMatchObject({
        code: "PLUGIN_NOT_RUNNING",
      });
    });
  });

  describe("process crash", () => {
    it("transitions to crashed on unexpected process exit and publishes event", async () => {
      const { pluginRepository, manager, eventDispatcher, mcpClientFactory } = setup();
      const plugin = makePlugin("nusashell.notes");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);

      const events: string[] = [];
      eventDispatcher.onAny({
        handle: (event) => {
          events.push(event.type);
        },
      });

      mcpClientFactory.created[0]!.emitClose();

      // Wait for async crash handling
      await new Promise((resolve) => setTimeout(resolve, 10));

      const state = await manager.getPluginState(plugin.id);
      expect(state).toBe("crashed");
      expect(events).toContain("plugin.crashed");
    });

    it("cancels pending tool calls on crash", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("nusashell.notes");
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
      mcpClientFactory.created[0]!.emitClose();

      await expect(callPromise).rejects.toMatchObject({
        code: "PLUGIN_CRASHED",
      });
    });
  });

  describe("stopAll", () => {
    it("stops all running plugins", async () => {
      const { pluginRepository, manager } = setup();
      const pluginA = makePlugin("example.a");
      const pluginB = makePlugin("example.b");
      pluginRepository.add(pluginA);
      pluginRepository.add(pluginB);

      await manager.startPlugin(pluginA.id);
      await manager.startPlugin(pluginB.id);

      await manager.stopAll();

      expect(await manager.getPluginState(pluginA.id)).toBe("idle");
      expect(await manager.getPluginState(pluginB.id)).toBe("idle");
    });
  });

  describe("getPlugin manifest refresh", () => {
    it("returns updated command/env after repository save without restarting", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("example.native", {
        source: "native-mcp",
        mcp: {
          transport: "stdio",
          command: "npx",
          args: ["-y", "@playwright/mcp@latest"],
          env: { OLD: "1" },
        },
      });
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      const before = await manager.getPlugin(plugin.id);
      expect(before?.command).toBe("npx");
      expect(before?.env).toEqual({ OLD: "1" });

      const updated = makePlugin("example.native", {
        source: "native-mcp",
        mcp: {
          transport: "stdio",
          command: "npx",
          args: ["-y", "@playwright/mcp@latest"],
          env: {
            PATH: "/home/u/.nvm/versions/node/v24/bin:/usr/bin:/bin",
            OLD: "1",
          },
        },
      });
      await pluginRepository.save(updated);

      const after = await manager.getPlugin(plugin.id);
      expect(after?.command).toBe("npx");
      expect(after?.env).toEqual({
        PATH: "/home/u/.nvm/versions/node/v24/bin:/usr/bin:/bin",
        OLD: "1",
      });
      expect(after?.state).toBe("running");
    });
  });

  describe("stop during start / crashed", () => {
    it("aborts a hung connect and lands on idle", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("example.slow");
      pluginRepository.add(plugin);
      mcpClientFactory.nextConnectDelayMs = 5_000;

      const startPromise = manager.startPlugin(plugin.id);
      await new Promise((resolve) => setTimeout(resolve, 20));
      expect(await manager.getPluginState(plugin.id)).toBe("starting");

      const stopPromise = manager.stopPlugin(plugin.id);
      await Promise.allSettled([startPromise, stopPromise]);

      expect(await manager.getPluginState(plugin.id)).toBe("idle");
    });

    it("clears crashed to idle on stop", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("example.crash-clear");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id);
      mcpClientFactory.created[0]!.emitClose();
      await new Promise((resolve) => setTimeout(resolve, 10));
      expect(await manager.getPluginState(plugin.id)).toBe("crashed");

      const view = await manager.stopPlugin(plugin.id);
      expect(view.state).toBe("idle");
      expect(await manager.getPluginState(plugin.id)).toBe("idle");
    });

    it("ignores empty launch args and keeps the manifest script path", async () => {
      const { pluginRepository, manager, mcpClientFactory } = setup();
      const plugin = makePlugin("example.node-script");
      pluginRepository.add(plugin);

      await manager.startPlugin(plugin.id, { args: [] });
      expect(mcpClientFactory.stdioCalls[0]!.args).toEqual(["mcp/server.js"]);
      expect(await manager.getPluginState(plugin.id)).toBe("running");
    });

    it("fails fast when a launch override leaves node without a script", async () => {
      const { pluginRepository, manager } = setup();
      const plugin = makePlugin("example.flag-only");
      pluginRepository.add(plugin);

      await expect(manager.startPlugin(plugin.id, { args: ["--inspect"] })).rejects.toThrow(
        /requires a script path/,
      );
      expect(await manager.getPluginState(plugin.id)).toBe("crashed");
    });
  });
});
