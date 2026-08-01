import { describe, expect, it } from "vitest";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import {
  assertDeclaredFilesExist,
  isLocalIconPath,
  resolveInsidePluginDir,
  stripFileScheme,
} from "../src/plugins/plugin-path-checks.js";

describe("isLocalIconPath", () => {
  it("returns false for emoji/text icons", () => {
    expect(isLocalIconPath("📝")).toBe(false);
    expect(isLocalIconPath("N")).toBe(false);
    expect(isLocalIconPath("Notes")).toBe(false);
  });

  it("returns false for HTTP(S) URLs", () => {
    expect(isLocalIconPath("https://example.com/icon.png")).toBe(false);
    expect(isLocalIconPath("http://example.com/icon.png")).toBe(false);
  });

  it("returns false for absolute file:// URLs", () => {
    expect(isLocalIconPath("file:///abs/path/icon.png")).toBe(false);
  });

  it("returns true for relative file:// paths", () => {
    expect(isLocalIconPath("file://icon.png")).toBe(true);
    expect(isLocalIconPath("file://assets/icon.png")).toBe(true);
  });

  it("returns true for ./relative and bare filename paths", () => {
    expect(isLocalIconPath("./icon.png")).toBe(true);
    expect(isLocalIconPath("icon.png")).toBe(true);
    expect(isLocalIconPath("assets/icon.png")).toBe(true);
  });
});

describe("stripFileScheme", () => {
  it("strips a leading file:// scheme", () => {
    expect(stripFileScheme("file://icon.png")).toBe("icon.png");
    expect(stripFileScheme("file://assets/icon.png")).toBe("assets/icon.png");
    expect(stripFileScheme("./icon.png")).toBe("./icon.png");
  });
});

describe("resolveInsidePluginDir", () => {
  it("resolves a relative path inside the plugin dir", () => {
    const dir = "/plugins/nusashell.notes";
    expect(resolveInsidePluginDir(dir, "ui/index.html", "ui.entry")).toBe(
      "/plugins/nusashell.notes/ui/index.html",
    );
  });

  it("throws for an absolute path", () => {
    expect(() => resolveInsidePluginDir("/plugins/x", "/etc/passwd", "ui.entry")).toThrow(
      /points outside its install directory/,
    );
  });

  it("throws for a path that escapes via ..", () => {
    expect(() => resolveInsidePluginDir("/plugins/x", "../escape", "ui.entry")).toThrow(
      /points outside its install directory/,
    );
  });
});

describe("assertDeclaredFilesExist", () => {
  let dir: string;

  async function setup() {
    dir = await mkdtemp(join(tmpdir(), "nusashell-pathcheck-"));
    await mkdir(join(dir, "ui"), { recursive: true });
    await writeFile(join(dir, "ui", "index.html"), "<html></html>");
    await writeFile(join(dir, "icon.png"), Buffer.from([0x89, 0x50, 0x4e, 0x47]));
  }

  it("passes when ui.entry and local icon exist", async () => {
    await setup();
    try {
      await expect(assertDeclaredFilesExist(dir, {
        ui: { entry: "ui/index.html" },
        icon: "file://icon.png",
      })).resolves.toBeUndefined();
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it("passes for a headless plugin (no ui) with an emoji icon", async () => {
    await setup();
    try {
      await expect(assertDeclaredFilesExist(dir, { icon: "🧩" })).resolves.toBeUndefined();
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it("throws when ui.entry is declared but missing", async () => {
    await setup();
    try {
      await expect(assertDeclaredFilesExist(dir, {
        ui: { entry: "ui/missing.html" },
        icon: "🧩",
      })).rejects.toThrow(/ui\.entry not found/);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it("throws when a local icon file is missing", async () => {
    await setup();
    try {
      await expect(assertDeclaredFilesExist(dir, {
        ui: { entry: "ui/index.html" },
        icon: "file://missing.png",
      })).rejects.toThrow(/icon not found/);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });
});
