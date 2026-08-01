import { describe, expect, it } from "vitest";
import { classifyUpdateChannel } from "../src/main/updater-channel.js";

describe("classifyUpdateChannel", () => {
  it("selects directory updates only for the supported user-space layouts", () => {
    expect(classifyUpdateChannel({
      platform: "linux",
      exePath: "/home/demo/.local/share/nusashell/versions/0.1.0/NusaShell",
      appImage: undefined,
      homeDir: "/home/demo",
    })).toBe("dir-install");
    expect(classifyUpdateChannel({
      platform: "win32",
      exePath: "C:\\Users\\demo\\AppData\\Local\\Programs\\NusaShell\\versions\\0.1.0\\NusaShell.exe",
      appImage: undefined,
      homeDir: "C:\\Users\\demo",
      localAppData: "C:\\Users\\demo\\AppData\\Local",
    })).toBe("dir-install");
    expect(classifyUpdateChannel({
      platform: "darwin",
      exePath: "/Users/demo/Applications/NusaShell.app/Contents/MacOS/NusaShell",
      appImage: undefined,
      homeDir: "/Users/demo",
    })).toBe("dir-install");
  });

  it("keeps AppImage, system package, and development builds on their own paths", () => {
    expect(classifyUpdateChannel({
      platform: "linux",
      exePath: "/tmp/.mount_NusaShell/NusaShell",
      appImage: "/home/demo/Downloads/NusaShell.AppImage",
      homeDir: "/home/demo",
    })).toBe("appimage");
    expect(classifyUpdateChannel({
      platform: "linux",
      exePath: "/usr/bin/nusashell",
      appImage: undefined,
      homeDir: "/home/demo",
    })).toBe("system-package");
    expect(classifyUpdateChannel({
      platform: "linux",
      exePath: "/workspace/NusaShell/node_modules/electron/dist/electron",
      appImage: undefined,
      homeDir: "/home/demo",
    })).toBe("unmanaged");
  });
});
