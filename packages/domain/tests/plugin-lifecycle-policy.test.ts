import { describe, expect, it } from "vitest";
import {
  Plugin,
  PluginId,
  PluginLifecyclePolicy,
  PluginManifest,
  PluginRuntime,
  PluginVersion,
} from "../src/index.js";

const manifestInput = {
  id: "nusashell.notes",
  name: "Notes",
  version: "1.0.0",
  icon: "notes.png",
  ui: {
    entry: "ui/index.html",
    window: { mode: "panel" as const },
  },
  mcp: {
    transport: "stdio" as const,
    command: "node mcp/server.js",
  },
};

function createPlugin(enabled = true): Plugin {
  const manifest = PluginManifest.create(manifestInput);
  if (!manifest.ok) {
    throw new Error("manifest setup failed");
  }
  const id = PluginId.create("nusashell.notes");
  const version = PluginVersion.create("1.0.0");
  if (!id.ok || !version.ok) {
    throw new Error("value object setup failed");
  }
  return Plugin.create({
    id: id.value,
    version: version.value,
    manifest: manifest.value,
    enabled,
    installPath: "/plugins/notes",
    installedAt: new Date("2026-07-27T00:00:00Z"),
  });
}

describe("PluginLifecyclePolicy", () => {
  it("rejects start when plugin is disabled", () => {
    const plugin = createPlugin(false);
    const idResult = PluginId.create("nusashell.notes");
    expect(idResult.ok).toBe(true);
    if (!idResult.ok) return;
    const runtime = PluginRuntime.createIdle(idResult.value);
    const result = PluginLifecyclePolicy.canStart(plugin, runtime);
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.error.code).toBe("PLUGIN_DISABLED");
    }
  });

  it("allows start from idle when enabled", () => {
    const plugin = createPlugin(true);
    const idResult = PluginId.create("nusashell.notes");
    if (!idResult.ok) throw new Error("id setup failed");
    const runtime = PluginRuntime.createIdle(idResult.value);
    const result = PluginLifecyclePolicy.canStart(plugin, runtime);
    expect(result.ok).toBe(true);
  });

  it("allows stop from running", () => {
    const plugin = createPlugin(true);
    const idResult = PluginId.create("nusashell.notes");
    if (!idResult.ok) throw new Error("id setup failed");
    const runtime = PluginRuntime.create(idResult.value, "running");
    const result = PluginLifecyclePolicy.canStop(plugin, runtime);
    expect(result.ok).toBe(true);
  });

  it("allows stop from starting (cancel hung connect)", () => {
    const plugin = createPlugin(true);
    const idResult = PluginId.create("nusashell.notes");
    if (!idResult.ok) throw new Error("id setup failed");
    const runtime = PluginRuntime.create(idResult.value, "starting");
    const result = PluginLifecyclePolicy.canStop(plugin, runtime);
    expect(result.ok).toBe(true);
  });

  it("allows callTool only when running", () => {
    const plugin = createPlugin(true);
    const idResult = PluginId.create("nusashell.notes");
    if (!idResult.ok) throw new Error("id setup failed");
    const running = PluginRuntime.create(idResult.value, "running");
    const idle = PluginRuntime.create(idResult.value, "idle");
    expect(PluginLifecyclePolicy.canCallTool(plugin, running).ok).toBe(true);
    expect(PluginLifecyclePolicy.canCallTool(plugin, idle).ok).toBe(false);
  });
});

describe("PluginManifest", () => {
  it("requires mcp.command for stdio transport", () => {
    const result = PluginManifest.create({
      ...manifestInput,
      mcp: { transport: "stdio" },
    });
    expect(result.ok).toBe(false);
  });

  it("rejects stdio command 'node' with no args (would eval stdin as JS)", () => {
    const result = PluginManifest.create({
      ...manifestInput,
      mcp: { transport: "stdio", command: "node" },
    });
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.error.message).toMatch(/node.*args/);
    }
  });

  it("rejects stdio command 'node' with only whitespace args", () => {
    const result = PluginManifest.create({
      ...manifestInput,
      mcp: { transport: "stdio", command: "node", args: ["   "] },
    });
    expect(result.ok).toBe(false);
  });

  it("accepts stdio command 'node' with a script path in args", () => {
    const result = PluginManifest.create({
      ...manifestInput,
      mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"] },
    });
    expect(result.ok).toBe(true);
  });

  it("requires mcp.url for sse transport", () => {
    const result = PluginManifest.create({
      ...manifestInput,
      mcp: { transport: "sse" },
    });
    expect(result.ok).toBe(false);
  });

  it("rejects headers on stdio transport", () => {
    const result = PluginManifest.create({
      ...manifestInput,
      mcp: { transport: "stdio", command: "node", headers: { Authorization: "secret" } },
    });
    expect(result.ok).toBe(false);
  });

  it("accepts a headless manifest without ui", () => {
    const { ui: _omit, ...headless } = manifestInput;
    void _omit;
    const result = PluginManifest.create(headless);
    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(result.value.ui).toBeUndefined();
    }
  });

  it("rejects ui with empty entry when ui is present", () => {
    const result = PluginManifest.create({
      ...manifestInput,
      ui: { entry: "" },
    });
    expect(result.ok).toBe(false);
  });
});
