import { describe, expect, it } from "vitest";
import { join, resolve } from "node:path";
import {
  LINUX_DESKTOP_APP_NAME,
  resolveLinuxDevDesktopPaths,
  resolveWindowIconPath,
} from "../src/main/window-assets.js";

describe("window assets", () => {
  it("resolves the source asset during development", () => {
    expect(resolveWindowIconPath({
      isPackaged: false,
      moduleDir: "/repo/apps/desktop/.vite/build",
      resourcesPath: "/unused",
    })).toBe(resolve("/repo/apps/desktop/.vite/build", "..", "..", "assets", "nusashell.png"));
  });

  it("resolves the copied resource in a packaged app", () => {
    expect(resolveWindowIconPath({
      isPackaged: true,
      moduleDir: "/unused",
      resourcesPath: "/opt/NusaShell/resources",
    })).toBe(join("/opt/NusaShell/resources", "nusashell.png"));
  });

  it("uses one Linux desktop identity for the window, icon, and desktop entry", () => {
    expect(LINUX_DESKTOP_APP_NAME).toBe("nusashell");
    expect(resolveLinuxDevDesktopPaths("/home/dev/.local/share")).toEqual({
      desktopEntry: resolve("/home/dev/.local/share", "applications", `${LINUX_DESKTOP_APP_NAME}.desktop`),
      icon: resolve("/home/dev/.local/share", "icons", "hicolor", "512x512", "apps", `${LINUX_DESKTOP_APP_NAME}.png`),
    });
  });
});
