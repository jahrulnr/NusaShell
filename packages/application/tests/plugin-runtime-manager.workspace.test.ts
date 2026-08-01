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

const FILES = PluginId.create("nusashell.files").ok ? PluginId.create("nusashell.files").value : (null as never);

describe("PluginRuntimeManager workspace binding", () => {
  it("injects NUSASHELL_WORKSPACE env at spawn when startPlugin is given a workspace", async () => {
    const { manager, pluginRepository, mcpClientFactory } = setup();
    pluginRepository.add(makePlugin("nusashell.files"));
    await manager.startPlugin(FILES, { workspace: "/tmp/proj" });
    expect(mcpClientFactory.stdioCalls[0]!.env.NUSASHELL_WORKSPACE).toBe("/tmp/proj");
  });

  it("syncWorkspace sets roots and notifies when the server is roots-capable", async () => {
    const { manager, pluginRepository, mcpClientFactory } = setup();
    pluginRepository.add(makePlugin("nusashell.files"));
    await manager.startPlugin(FILES);
    const client = mcpClientFactory.created[0]!;
    client.markRootsRequested();

    const result = await manager.syncWorkspace(FILES, "/tmp/proj");
    expect(result.mode).toBe("roots");
    expect(result.respawned).toBe(false);
    expect(client.roots).toEqual([{ uri: "file:///tmp/proj", name: "workspace" }]);
    expect(client.rootsNotifications).toHaveLength(1);

    // Second sync with the same workspace does not re-notify.
    const result2 = await manager.syncWorkspace(FILES, "/tmp/proj");
    expect(result2.mode).toBe("roots");
    expect(client.rootsNotifications).toHaveLength(1);

    // A different workspace notifies again.
    await manager.syncWorkspace(FILES, "/tmp/other");
    expect(client.roots[0]!.uri).toBe("file:///tmp/other");
    expect(client.rootsNotifications).toHaveLength(2);
  });

  it("syncWorkspace on a static (non-roots) server records workspace without respawn", async () => {
    const { manager, pluginRepository, mcpClientFactory } = setup();
    pluginRepository.add(makePlugin("nusashell.files"));
    await manager.startPlugin(FILES);
    const client = mcpClientFactory.created[0]!;
    // Do NOT markRootsRequested → static.
    const result = await manager.syncWorkspace(FILES, "/tmp/proj");
    expect(result.mode).toBe("static");
    expect(result.respawned).toBe(false);
    expect(client.rootsNotifications).toHaveLength(0);
  });

  it("syncWorkspace on an idle plugin records workspace for the next spawn", async () => {
    const { manager, pluginRepository, mcpClientFactory } = setup();
    pluginRepository.add(makePlugin("nusashell.files"));
    const result = await manager.syncWorkspace(FILES, "/tmp/proj");
    expect(result.mode).toBe("idle");
    await manager.startPlugin(FILES);
    expect(mcpClientFactory.stdioCalls[0]!.env.NUSASHELL_WORKSPACE).toBe("/tmp/proj");
  });

  it("startPlugin with different args overrides respawns the running plugin", async () => {
    const { manager, pluginRepository, mcpClientFactory } = setup();
    pluginRepository.add(makePlugin("nusashell.files", { mcp: { transport: "stdio", command: "node", args: ["a.cjs"], env: {} } }));
    await manager.startPlugin(FILES);
    expect(mcpClientFactory.stdioCalls).toHaveLength(1);
    expect(mcpClientFactory.stdioCalls[0]!.args).toEqual(["a.cjs"]);

    // Same args, no respawn.
    await manager.startPlugin(FILES, { args: ["a.cjs"] });
    expect(mcpClientFactory.stdioCalls).toHaveLength(1);

    // Different args → respawn.
    await manager.startPlugin(FILES, { args: ["b.cjs"] });
    expect(mcpClientFactory.stdioCalls).toHaveLength(2);
    expect(mcpClientFactory.stdioCalls[1]!.args).toEqual(["b.cjs"]);
  });

  it("getLaunchSpec returns redacted env keys, args, and roots capability", async () => {
    const { manager, pluginRepository, mcpClientFactory } = setup();
    pluginRepository.add(makePlugin("nusashell.files", {
      mcp: { transport: "stdio", command: "node", args: ["server.cjs"], env: { NUSASHELL_FILES_ROOT: "/home", SECRET_TOKEN: "shh" } },
    }));
    await manager.startPlugin(FILES);
    mcpClientFactory.created[0]!.markRootsRequested();
    const spec = await manager.getLaunchSpec(FILES);
    expect(spec).not.toBeNull();
    expect(spec!.command).toBe("node");
    expect(spec!.args).toEqual(["server.cjs"]);
    expect(spec!.envKeys).toEqual(expect.arrayContaining(["NUSASHELL_FILES_ROOT", "SECRET_TOKEN"]));
    // No env values are exposed.
    expect(JSON.stringify(spec)).not.toContain("shh");
    expect(spec!.rootsCapable).toBe(true);
  });
});
