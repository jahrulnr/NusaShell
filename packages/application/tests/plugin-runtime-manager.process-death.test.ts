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

/**
 * Finding 2: external kill of an MCP process left the runtime SoT stuck on
 * "running" because the close watcher was registered AFTER the running
 * transition + plugin.started event, and stdio never set entry.process.
 * These tests pin the fix: the watcher is registered before "running" and a
 * late close flips the state to "crashed".
 */
describe("PluginRuntimeManager process-death SoT (finding 2)", () => {
  it("emitting close after start transitions to crashed and publishes plugin.crashed", async () => {
    const { pluginRepository, manager, mcpClientFactory, eventDispatcher } = setup();
    const plugin = makePlugin("nusashell.notes");
    pluginRepository.add(plugin);

    const eventTypes: string[] = [];
    eventDispatcher.onAny({ handle: (e) => { eventTypes.push(e.type); } });

    await manager.startPlugin(plugin.id);
    expect(await manager.getPluginState(plugin.id)).toBe("running");

    const client = mcpClientFactory.created[0]!;
    client.emitClose();

    // handleProcessExit is async (void) — let it drain.
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(await manager.getPluginState(plugin.id)).toBe("crashed");
    expect(eventTypes).toContain("plugin.crashed");
  });

  it("registers the close watcher BEFORE publishing plugin.started", async () => {
    const { pluginRepository, manager, mcpClientFactory, eventDispatcher } = setup();
    const plugin = makePlugin("nusashell.notes");
    pluginRepository.add(plugin);

    let onCloseRegisteredAtStarted: boolean | null = null;
    eventDispatcher.onAny({
      handle: (event) => {
        if (event.type === "plugin.started") {
          const client = mcpClientFactory.created[0]!;
          onCloseRegisteredAtStarted = client.onCloseRegistered;
        }
      },
    });

    await manager.startPlugin(plugin.id);

    expect(onCloseRegisteredAtStarted).toBe(true);
  });

  it("stop path does not flap to crashed when client closes during intentional stop", async () => {
    const { pluginRepository, manager, mcpClientFactory } = setup();
    const plugin = makePlugin("nusashell.notes");
    pluginRepository.add(plugin);

    await manager.startPlugin(plugin.id);
    const client = mcpClientFactory.created[0]!;

    // FakeMcpClient.close() drops the callback, so a late emitClose is a no-op.
    await manager.stopPlugin(plugin.id);

    client.emitClose();
    await new Promise((resolve) => setTimeout(resolve, 0));

    // Intentional stop lands on idle; the late close must not resurrect it.
    expect(await manager.getPluginState(plugin.id)).toBe("idle");
  });

  it("started event carries the client pid when available", async () => {
    const { pluginRepository, manager, eventDispatcher } = setup();
    const plugin = makePlugin("nusashell.notes");
    pluginRepository.add(plugin);

    let startedPid: number | null = null;
    eventDispatcher.onAny({
      handle: (event) => {
        if (event.type === "plugin.started") {
          startedPid = (event as { pid: number | null }).pid;
        }
      },
    });

    await manager.startPlugin(plugin.id);

    // FakeMcpClient.pid returns 1234 when connected.
    expect(startedPid).toBe(1234);
  });
});
