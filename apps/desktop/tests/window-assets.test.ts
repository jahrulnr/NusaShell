import { describe, expect, it } from "vitest";
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
    })).toBe("/repo/apps/desktop/assets/nusashell.png");
  });

  it("resolves the copied resource in a packaged app", () => {
    expect(resolveWindowIconPath({
      isPackaged: true,
      moduleDir: "/unused",
      resourcesPath: "/opt/NusaShell/resources",
    })).toBe("/opt/NusaShell/resources/nusashell.png");
  });

  it("uses one Linux desktop identity for the window, icon, and desktop entry", () => {
    expect(LINUX_DESKTOP_APP_NAME).toBe("nusashell");
    expect(resolveLinuxDevDesktopPaths("/home/dev/.local/share")).toEqual({
      desktopEntry: "/home/dev/.local/share/applications/nusashell.desktop",
      icon: "/home/dev/.local/share/icons/hicolor/512x512/apps/nusashell.png",
    });
  });
});
