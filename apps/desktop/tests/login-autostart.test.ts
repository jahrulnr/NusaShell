import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it, vi } from "vitest";
import {
  buildXdgAutostartDesktop,
  createLoginAutostart,
  LOGIN_AUTOSTART_REQUIRES_PACKAGED,
  resolveAutostartDesktopPath,
} from "../src/main/login-autostart.js";

describe("buildXdgAutostartDesktop", () => {
  it("includes --hidden when requested and quotes spaced paths", () => {
    const withHidden = buildXdgAutostartDesktop({
      exePath: "/opt/NusaShell/NusaShell",
      hidden: true,
    });
    expect(withHidden).toContain("Exec=/opt/NusaShell/NusaShell --hidden");
    expect(withHidden).toContain("X-GNOME-Autostart-enabled=true");

    const spaced = buildXdgAutostartDesktop({
      exePath: "/home/user/My Apps/NusaShell.AppImage",
      hidden: false,
    });
    expect(spaced).toContain('Exec="/home/user/My Apps/NusaShell.AppImage"');
  });
});

describe("createLoginAutostart (Linux)", () => {
  it("writes, reads, and removes the XDG autostart file", async () => {
    const homeDir = await mkdtemp(join(tmpdir(), "nusashell-login-autostart-"));
    const configHome = join(homeDir, "config");
    const login = createLoginAutostart({
      platform: "linux",
      isPackaged: true,
      exePath: "/opt/NusaShell/NusaShell",
      homeDir,
      xdgConfigHome: configHome,
    });

    expect(await login.get()).toBe(false);

    await login.set(true, { hidden: true });
    const path = resolveAutostartDesktopPath({ homeDir, xdgConfigHome: configHome });
    const content = await readFile(path, "utf8");
    expect(content).toContain("Exec=/opt/NusaShell/NusaShell --hidden");
    expect(await login.get()).toBe(true);

    await login.set(true, { hidden: false });
    expect(await readFile(path, "utf8")).toContain("Exec=/opt/NusaShell/NusaShell\n");

    await login.set(false, { hidden: true });
    expect(await login.get()).toBe(false);
  });

  it("rejects set() when not packaged", async () => {
    const homeDir = await mkdtemp(join(tmpdir(), "nusashell-login-autostart-"));
    const login = createLoginAutostart({
      platform: "linux",
      isPackaged: false,
      exePath: "/tmp/electron",
      homeDir,
    });
    await expect(login.set(true, { hidden: true })).rejects.toThrow(LOGIN_AUTOSTART_REQUIRES_PACKAGED);
  });

  it("reconcile rewrites a stale Exec path when launchAtLogin is on", async () => {
    const homeDir = await mkdtemp(join(tmpdir(), "nusashell-login-autostart-"));
    const configHome = join(homeDir, "config");
    const path = resolveAutostartDesktopPath({ homeDir, xdgConfigHome: configHome });
    const { mkdir } = await import("node:fs/promises");
    const { dirname } = await import("node:path");
    await mkdir(dirname(path), { recursive: true });
    await writeFile(path, buildXdgAutostartDesktop({
      exePath: "/old/path/NusaShell.AppImage",
      hidden: true,
    }));

    const login = createLoginAutostart({
      platform: "linux",
      isPackaged: true,
      exePath: "/new/path/NusaShell.AppImage",
      homeDir,
      xdgConfigHome: configHome,
    });
    await login.reconcile({
      launchAtLogin: true,
      startHidden: false,
      keepInBackground: true,
    });

    expect(await readFile(path, "utf8")).toContain("Exec=/new/path/NusaShell.AppImage\n");
  });
});

describe("createLoginAutostart (native branches)", () => {
  it("uses setLoginItemSettings on darwin/win32", async () => {
    const setLoginItemSettings = vi.fn();
    const getLoginItemSettings = vi.fn(() => ({ openAtLogin: true }));
    const mac = createLoginAutostart({
      platform: "darwin",
      isPackaged: true,
      exePath: "/Applications/NusaShell.app/Contents/MacOS/NusaShell",
      homeDir: "/Users/demo",
      setLoginItemSettings,
      getLoginItemSettings,
    });
    await mac.set(true, { hidden: true });
    expect(setLoginItemSettings).toHaveBeenCalledWith({
      openAtLogin: true,
      openAsHidden: true,
    });
    expect(await mac.get()).toBe(true);

    const win = createLoginAutostart({
      platform: "win32",
      isPackaged: true,
      exePath: "C:\\\\Program Files\\\\NusaShell\\\\NusaShell.exe",
      homeDir: "C:\\\\Users\\\\demo",
      setLoginItemSettings,
      getLoginItemSettings,
    });
    await win.set(true, { hidden: true });
    expect(setLoginItemSettings).toHaveBeenLastCalledWith({
      openAtLogin: true,
      path: "C:\\\\Program Files\\\\NusaShell\\\\NusaShell.exe",
      args: ["--hidden"],
    });
  });
});
