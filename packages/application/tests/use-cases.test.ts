import { describe, expect, it } from "vitest";
import {
  CallToolHandler,
  CommandBus,
  EventDispatcher,
  ListPluginsHandler,
  QueryBus,
  StartPluginHandler,
  StopPluginHandler,
  PluginRuntimeManager,
} from "../src/index.js";
import {
  FakeClock,
  FakeMcpClientFactory,
  FakePluginRepository,
  FakeProcessAdapter,
  makePlugin,
} from "./fakes.js";

function setupBus() {
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

  const commandBus = new CommandBus();
  commandBus.register("start-plugin", new StartPluginHandler(manager));
  commandBus.register("stop-plugin", new StopPluginHandler(manager));
  commandBus.register("call-tool", new CallToolHandler(manager));

  const queryBus = new QueryBus();
  queryBus.register("list-plugins", new ListPluginsHandler(manager));

  return {
    clock,
    eventDispatcher,
    pluginRepository,
    processAdapter,
    mcpClientFactory,
    manager,
    commandBus,
    queryBus,
  };
}

describe("StartPluginHandler via CommandBus", () => {
  it("starts a plugin and returns running state", async () => {
    const { commandBus, pluginRepository } = setupBus();
    pluginRepository.add(makePlugin("com.example.notes"));

    const result = await commandBus.execute({
      kind: "start-plugin",
      pluginId: "com.example.notes",
    });

    expect(result).toEqual({
      pluginId: "com.example.notes",
      state: "running",
    });
  });

  it("rejects invalid plugin id", async () => {
    const { commandBus } = setupBus();

    await expect(
      commandBus.execute({
        kind: "start-plugin",
        pluginId: "",
      }),
    ).rejects.toMatchObject({ code: "PLUGIN_NOT_FOUND" });
  });
});

describe("StopPluginHandler via CommandBus", () => {
  it("stops a running plugin", async () => {
    const { commandBus, pluginRepository } = setupBus();
    pluginRepository.add(makePlugin("com.example.notes"));

    await commandBus.execute({
      kind: "start-plugin",
      pluginId: "com.example.notes",
    });

    const result = await commandBus.execute({
      kind: "stop-plugin",
      pluginId: "com.example.notes",
    });

    expect(result).toEqual({
      pluginId: "com.example.notes",
      state: "idle",
    });
  });
});

describe("CallToolHandler via CommandBus", () => {
  it("calls a tool on a running plugin", async () => {
    const { commandBus, pluginRepository, mcpClientFactory } = setupBus();
    pluginRepository.add(makePlugin("com.example.notes"));

    await commandBus.execute({
      kind: "start-plugin",
      pluginId: "com.example.notes",
    });

    mcpClientFactory.created[0]!.setToolResult("create_note", { id: "n1" });

    const result = await commandBus.execute({
      kind: "call-tool",
      pluginId: "com.example.notes",
      requestId: "00000000-0000-1000-8000-000000000010",
      toolName: "create_note",
      args: { text: "hello" },
    });

    expect(result).toEqual({
      requestId: "00000000-0000-1000-8000-000000000010",
      result: { id: "n1" },
    });
  });
});

describe("ListPluginsHandler via QueryBus", () => {
  it("lists all installed plugins with their runtime state", async () => {
    const { queryBus, pluginRepository } = setupBus();
    pluginRepository.add(makePlugin("com.example.a"));
    pluginRepository.add(makePlugin("com.example.b"));

    const result = await queryBus.execute({ kind: "list-plugins" }) as {
      plugins: readonly { pluginId: string; state: string }[];
    };

    expect(result.plugins).toHaveLength(2);
    const ids = result.plugins.map((p) => p.pluginId).sort();
    expect(ids).toEqual(["com.example.a", "com.example.b"]);
    expect(result.plugins.every((p) => p.state === "idle")).toBe(true);
  });

  it("returns running state for started plugins", async () => {
    const { queryBus, commandBus, pluginRepository } = setupBus();
    pluginRepository.add(makePlugin("com.example.notes"));

    await commandBus.execute({
      kind: "start-plugin",
      pluginId: "com.example.notes",
    });

    const result = await queryBus.execute({ kind: "list-plugins" }) as {
      plugins: readonly { pluginId: string; state: string }[];
    };
    expect(result.plugins[0]!.state).toBe("running");
  });
});
