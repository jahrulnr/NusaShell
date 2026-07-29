import { describe, expect, it } from "vitest";
import {
  fitPluginWindowToWorkArea,
  normalizePluginWindowOptions,
  pluginWindowTitle,
  resolvePluginUiPath,
} from "../src/main/plugin-window-options.js";

describe("plugin window options", () => {
  it("uses a plugin's declared entry, size, and lifecycle behavior", () => {
    expect(normalizePluginWindowOptions({
      entry: "ui/mail.html",
      window: {
        mode: "fullscreen",
        defaultSize: { width: 1280, height: 800 },
        resizable: true,
      },
      keepAliveOnClose: true,
    })).toEqual({
      entry: "ui/mail.html",
      width: 1280,
      height: 800,
      resizable: true,
      keepAliveOnClose: true,
    });
  });

  it("clamps unsafe or unusable window dimensions", () => {
    expect(normalizePluginWindowOptions({
      entry: "ui/index.html",
      window: { defaultSize: { width: 20_000, height: 20 } },
      keepAliveOnClose: false,
    })).toMatchObject({ width: 1920, height: 300 });
  });

  it("prevents a UI entry from escaping the plugin install directory", () => {
    expect(() => resolvePluginUiPath("/plugins/mail", "../outside.html"))
      .toThrow(/outside/i);
  });

  it("does not expose a file icon URL in the native window title", () => {
    expect(pluginWindowTitle("Mail", "file:///plugins/mail/icon.png")).toBe("Mail");
    expect(pluginWindowTitle("Notes", "📝")).toBe("📝 Notes");
  });

  it("fits a plugin's requested size inside the current display work area", () => {
    expect(fitPluginWindowToWorkArea(
      { width: 1280, height: 800 },
      { width: 1024, height: 728 },
    )).toEqual({ width: 1024, height: 728 });
  });

  it("keeps a plugin's requested size when the display has enough room", () => {
    expect(fitPluginWindowToWorkArea(
      { width: 1280, height: 800 },
      { width: 1920, height: 1040 },
    )).toEqual({ width: 1280, height: 800 });
  });
});
