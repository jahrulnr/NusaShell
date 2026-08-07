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
    pluginRepository.add(makePlugin("nusashell.notes"));

    const result = await commandBus.execute({
      kind: "start-plugin",
      pluginId: "nusashell.notes",
    });

    expect(result).toEqual({
      pluginId: "nusashell.notes",
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
    pluginRepository.add(makePlugin("nusashell.notes"));

    await commandBus.execute({
      kind: "start-plugin",
      pluginId: "nusashell.notes",
    });

    const result = await commandBus.execute({
      kind: "stop-plugin",
      pluginId: "nusashell.notes",
    });

    expect(result).toEqual({
      pluginId: "nusashell.notes",
      state: "idle",
    });
  });
});

describe("CallToolHandler via CommandBus", () => {
  it("calls a tool on a running plugin", async () => {
    const { commandBus, pluginRepository, mcpClientFactory } = setupBus();
    pluginRepository.add(makePlugin("nusashell.notes"));

    await commandBus.execute({
      kind: "start-plugin",
      pluginId: "nusashell.notes",
    });

    mcpClientFactory.created[0]!.setToolResult("create_note", { id: "n1" });

    const result = await commandBus.execute({
      kind: "call-tool",
      pluginId: "nusashell.notes",
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
    pluginRepository.add(makePlugin("example.a"));
    pluginRepository.add(makePlugin("example.b"));

    const result = await queryBus.execute({ kind: "list-plugins" }) as {
      plugins: readonly { pluginId: string; state: string }[];
    };

    expect(result.plugins).toHaveLength(2);
    const ids = result.plugins.map((p) => p.pluginId).sort();
    expect(ids).toEqual(["example.a", "example.b"]);
    expect(result.plugins.every((p) => p.state === "idle")).toBe(true);
  });

  it("returns running state for started plugins", async () => {
    const { queryBus, commandBus, pluginRepository } = setupBus();
    pluginRepository.add(makePlugin("nusashell.notes"));

    await commandBus.execute({
      kind: "start-plugin",
      pluginId: "nusashell.notes",
    });

    const result = await queryBus.execute({ kind: "list-plugins" }) as {
      plugins: readonly { pluginId: string; state: string }[];
    };
    expect(result.plugins[0]!.state).toBe("running");
  });

  it("forwards ui and keepAliveOnClose for UI plugins", async () => {
    const { queryBus, pluginRepository } = setupBus();
    pluginRepository.add(makePlugin("nusashell.notes", {
      ui: { entry: "ui/mail.html", window: { mode: "fullscreen" } },
      mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"], keepAliveOnClose: true },
    }));

    const result = await queryBus.execute({ kind: "list-plugins" }) as {
      plugins: readonly {
        pluginId: string;
        ui?: { entry: string };
        keepAliveOnClose: boolean;
      }[];
    };

    expect(result.plugins).toHaveLength(1);
    expect(result.plugins[0]!.ui).toEqual({ entry: "ui/mail.html", window: { mode: "fullscreen" } });
    expect(result.plugins[0]!.keepAliveOnClose).toBe(true);
  });

  it("omits ui for headless plugins and still returns keepAliveOnClose", async () => {
    const { queryBus, pluginRepository } = setupBus();
    // `ui: undefined` is not assignable under exactOptionalPropertyTypes, so
    // the cast keeps the runtime value undefined (i.e. a headless plugin) while
    // satisfying the `Partial<PluginManifestInput>` overrides type.
    const headless = makePlugin("nusashell.indexer", {
      ui: undefined as unknown as { entry: string },
    });

    pluginRepository.add(headless);

    const result = await queryBus.execute({ kind: "list-plugins" }) as {
      plugins: readonly {
        pluginId: string;
        ui?: { entry: string };
        keepAliveOnClose: boolean;
      }[];
    };

    expect(result.plugins).toHaveLength(1);
    expect(result.plugins[0]!.pluginId).toBe("nusashell.indexer");
    expect(result.plugins[0]!.ui).toBeUndefined();
    expect(result.plugins[0]!.keepAliveOnClose).toBe(false);
  });
});
