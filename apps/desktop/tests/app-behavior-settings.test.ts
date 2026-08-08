import { mkdtemp, readFile, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { platform } from "node:os";
import { describe, expect, it } from "vitest";
import {
  AppBehaviorStore,
  DEFAULT_APP_BEHAVIOR,
  normalizeAppBehavior,
  shouldHideOnClose,
  shouldQuitOnAllWindowsClosed,
} from "../src/main/app-behavior-settings.js";

describe("normalizeAppBehavior", () => {
  it("returns defaults when raw is missing or unknown", () => {
    expect(normalizeAppBehavior(undefined)).toEqual(DEFAULT_APP_BEHAVIOR);
    expect(normalizeAppBehavior({})).toEqual(DEFAULT_APP_BEHAVIOR);
    expect(normalizeAppBehavior({ launchAtLogin: "yes", extra: true })).toEqual(DEFAULT_APP_BEHAVIOR);
  });

  it("keeps known boolean keys and drops unknown ones", () => {
    expect(normalizeAppBehavior({
      launchAtLogin: true,
      startHidden: false,
      keepInBackground: false,
      trayTheme: "dark",
    })).toEqual({
      launchAtLogin: true,
      startHidden: false,
      keepInBackground: false,
      canvasEnabled: true,
    });
  });
});

describe("close decision helpers", () => {
  it("hides on close only when keepInBackground and not quitting", () => {
    expect(shouldHideOnClose({ keepInBackground: true, isQuitting: false })).toBe(true);
    expect(shouldHideOnClose({ keepInBackground: true, isQuitting: true })).toBe(false);
    expect(shouldHideOnClose({ keepInBackground: false, isQuitting: false })).toBe(false);
  });

  it("quits on all-windows-closed only when not keepInBackground, never on darwin", () => {
    expect(shouldQuitOnAllWindowsClosed({ keepInBackground: false, platform: "linux" })).toBe(true);
    expect(shouldQuitOnAllWindowsClosed({ keepInBackground: true, platform: "linux" })).toBe(false);
    expect(shouldQuitOnAllWindowsClosed({ keepInBackground: false, platform: "darwin" })).toBe(false);
    expect(shouldQuitOnAllWindowsClosed({ keepInBackground: true, platform: "darwin" })).toBe(false);
  });
});

describe("AppBehaviorStore", () => {
  it("loads defaults when the file is missing", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-app-behavior-"));
    const store = new AppBehaviorStore(join(directory, "app-behavior.json"));
    expect(await store.load()).toEqual(DEFAULT_APP_BEHAVIOR);
    expect(await store.hasPersistedSettings()).toBe(false);
  });

  it("merges patches and persists via tmp+rename", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-app-behavior-"));
    const path = join(directory, "app-behavior.json");
    const store = new AppBehaviorStore(path);

    const result = await store.set({ launchAtLogin: true, startHidden: false });
    expect(result).toEqual({
      launchAtLogin: true,
      startHidden: false,
      keepInBackground: true,
      canvasEnabled: true,
    });

    const onDisk = JSON.parse(await readFile(path, "utf8")) as unknown;
    expect(onDisk).toEqual(result);

    // Unix permission bits (0o600) are not enforced on Windows — the NTFS
    // ACL system doesn't map to POSIX mode bits. Skip on win32.
    if (platform() !== "win32") {
      const mode = (await stat(path)).mode & 0o777;
      expect(mode).toBe(0o600);
    }

    const reloaded = new AppBehaviorStore(path);
    expect(await reloaded.load()).toEqual(result);
    expect(await reloaded.hasPersistedSettings()).toBe(true);
  });

  it("drops unknown keys from a corrupt/partial file on load", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-app-behavior-"));
    const path = join(directory, "app-behavior.json");
    await writeFile(path, JSON.stringify({
      launchAtLogin: true,
      startHidden: "nope",
      trayTheme: "dark",
    }));
    const store = new AppBehaviorStore(path);
    expect(await store.load()).toEqual({
      launchAtLogin: true,
      startHidden: true,
      keepInBackground: true,
      canvasEnabled: true,
    });
  });
});
