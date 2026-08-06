import { describe, expect, it } from "vitest";
import { PluginRuntimeManager, EventDispatcher } from "../src/index.js";
import {
  FakeClock,
  FakeMcpClientFactory,
  FakePluginRepository,
  FakeProcessAdapter,
  makePlugin,
} from "./fakes.js";

function setup(overrides?: {
  maxRestarts?: number;
  baseDelayMs?: number;
  windowMs?: number;
  enabled?: boolean;
}) {
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
    autoRestart: {
      enabled: overrides?.enabled ?? true,
      baseDelayMs: overrides?.baseDelayMs ?? 0,
      maxRestarts: overrides?.maxRestarts ?? 5,
      windowMs: overrides?.windowMs ?? 100_000,
    },
  });
  return { clock, eventDispatcher, pluginRepository, processAdapter, mcpClientFactory, manager };
}

const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

describe("PluginRuntimeManager auto-restart (#2)", () => {
  it("auto-restarts a crashed plugin back to running (single crash)", async () => {
    const { pluginRepository, manager, mcpClientFactory } = setup({ baseDelayMs: 0 });
    const plugin = makePlugin("nusashell.notes");
    pluginRepository.add(plugin);

    await manager.startPlugin(plugin.id);
    expect(await manager.getPluginState(plugin.id)).toBe("running");

    const firstClient = mcpClientFactory.created[0]!;
    firstClient.emitClose(); // unexpected death → crash → schedule restart

    // Let crash + scheduled restart run.
    await flush();
    await flush();

    // Auto-restart should have brought it back to running with a NEW client.
    expect(await manager.getPluginState(plugin.id)).toBe("running");
    expect(mcpClientFactory.created.length).toBeGreaterThanOrEqual(2);
  });

  it("gives up (stays crashed) after repeated crashes exceed maxRestarts (circuit breaker)", async () => {
    const { pluginRepository, manager, mcpClientFactory } = setup({ baseDelayMs: 0, maxRestarts: 2 });
    const plugin = makePlugin("nusashell.notes");
    pluginRepository.add(plugin);

    await manager.startPlugin(plugin.id);

    // Crash #1 → restart back to running
    mcpClientFactory.created[mcpClientFactory.created.length - 1]!.emitClose();
    await flush();
    await flush();
    expect(await manager.getPluginState(plugin.id)).toBe("running");

    // Crash #2 → restart back to running
    mcpClientFactory.created[mcpClientFactory.created.length - 1]!.emitClose();
    await flush();
    await flush();
    expect(await manager.getPluginState(plugin.id)).toBe("running");

    // Crash #3 → exceeds maxRestarts=2 → circuit open, stay crashed
    mcpClientFactory.created[mcpClientFactory.created.length - 1]!.emitClose();
    await flush();
    await flush();

    expect(await manager.getPluginState(plugin.id)).toBe("crashed");
    const view = await manager.getPlugin(plugin.id);
    expect(view?.restarting).toBe(false);
  });

  it("manual stop cancels a pending auto-restart (does not resurrect)", async () => {
    const { pluginRepository, manager, mcpClientFactory } = setup({ baseDelayMs: 50 });
    const plugin = makePlugin("nusashell.notes");
    pluginRepository.add(plugin);

    await manager.startPlugin(plugin.id);
    const client = mcpClientFactory.created[0]!;

    client.emitClose(); // crash → schedules restart in ~50ms
    await flush();

    // Stop while the restart is pending → must cancel it.
    await manager.stopPlugin(plugin.id);
    expect(await manager.getPluginState(plugin.id)).toBe("idle");

    // Wait past the scheduled delay — must NOT resurrect to running.
    await new Promise((resolve) => setTimeout(resolve, 80));
    expect(await manager.getPluginState(plugin.id)).toBe("idle");
  });
});
