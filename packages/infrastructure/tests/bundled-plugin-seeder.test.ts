import { describe, expect, it, beforeEach, afterEach } from "vitest";
import { mkdtemp, mkdir, rm, writeFile, readFile, readdir, stat, access } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { BundledPluginSeeder } from "../src/plugins/bundled-plugin-seeder.js";

const STATE_FILE = ".bundled-seed.json";

async function makePluginDir(root: string, id: string, version: string, extraFiles: string[] = []): Promise<string> {
  const dir = join(root, id);
  await mkdir(dir, { recursive: true });
  await mkdir(join(dir, "mcp"), { recursive: true });
  await writeFile(
    join(dir, "manifest.json"),
    JSON.stringify({
      id,
      name: id,
      version,
      icon: "📦",
      mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"], env: {} },
    }),
    "utf8",
  );
  await writeFile(join(dir, "mcp", "server.js"), `// ${id} ${version}`, "utf8");
  for (const f of extraFiles) {
    await mkdir(join(dir, f.split("/").slice(0, -1).join("/")), { recursive: true });
    await writeFile(join(dir, f), `// ${f} ${version}`, "utf8");
  }
  return dir;
}

async function readManifestVersion(dir: string): Promise<string> {
  const raw = await readFile(join(dir, "manifest.json"), "utf8");
  return JSON.parse(raw).version as string;
}

async function readState(root: string): Promise<Record<string, { version: string; removed?: boolean }>> {
  try {
    const raw = await readFile(join(root, STATE_FILE), "utf8");
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

describe("BundledPluginSeeder", () => {
  let tempDir: string;
  let bundledRoot: string;
  let userRoot: string;
  let seeder: BundledPluginSeeder;

  beforeEach(async () => {
    tempDir = await mkdtemp(join(tmpdir(), "nusashell-seed-"));
    bundledRoot = join(tempDir, "bundled");
    userRoot = join(tempDir, "user");
    await mkdir(bundledRoot, { recursive: true });
    await mkdir(userRoot, { recursive: true });
    seeder = new BundledPluginSeeder({ bundledRoot, userRoot });
  });

  afterEach(async () => {
    await rm(tempDir, { recursive: true, force: true });
  });

  it("seeds every bundled plugin into the user root on fresh install", async () => {
    await makePluginDir(bundledRoot, "nusashell.files", "0.1.0");
    await makePluginDir(bundledRoot, "nusashell.notes", "1.0.0");

    const result = await seeder.seed();

    expect(result).toMatchObject({ seeded: ["nusashell.files", "nusashell.notes"], updated: [], skipped: [] });
    const userFiles = await readdir(userRoot);
    expect(userFiles).toContain("nusashell.files");
    expect(userFiles).toContain("nusashell.notes");
    expect(await readManifestVersion(join(userRoot, "nusashell.files"))).toBe("0.1.0");
    // Marker state records the seeded versions.
    const state = await readState(userRoot);
    expect(state["nusashell.files"]?.version).toBe("0.1.0");
    expect(state["nusashell.notes"]?.version).toBe("1.0.0");
  });

  it("copies the full plugin tree (mcp, ui, assets), not just the manifest", async () => {
    await makePluginDir(bundledRoot, "nusashell.notes", "1.0.0", ["ui/index.html", "icon.png"]);
    await seeder.seed();
    const userPlugin = join(userRoot, "nusashell.notes");
    expect((await access(join(userPlugin, "ui", "index.html")).then(() => true).catch(() => false))).toBe(true);
    expect((await access(join(userPlugin, "icon.png")).then(() => true).catch(() => false))).toBe(true);
    expect((await access(join(userPlugin, "mcp", "server.js")).then(() => true).catch(() => false))).toBe(true);
  });

  it("skips plugins already present at the same version (no rewrite)", async () => {
    await makePluginDir(bundledRoot, "nusashell.files", "0.1.0");
    await seeder.seed();
    // User tweaks a file after seed.
    await writeFile(join(userRoot, "nusashell.files", "mcp", "server.js"), "// user edit", "utf8");

    const result = await seeder.seed();
    expect(result.seeded).toEqual([]);
    expect(result.skipped).toContain("nusashell.files");
    // The user edit is preserved — same version => no rewrite.
    expect(await readFile(join(userRoot, "nusashell.files", "mcp", "server.js"), "utf8")).toBe("// user edit");
  });

  it("updates the copy when the bundled version is newer (reconcile on upgrade)", async () => {
    await makePluginDir(bundledRoot, "nusashell.files", "0.1.0");
    await seeder.seed();
    await writeFile(join(userRoot, "nusashell.files", "manifest.json"), JSON.stringify({
      id: "nusashell.files",
      name: "Files",
      version: "0.1.0",
      icon: "📂",
      mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"], env: {}, autostart: true, keepAliveOnClose: true },
    }), "utf8");
    // Bundled ships a newer version.
    await writeFile(join(bundledRoot, "nusashell.files", "manifest.json"), JSON.stringify({
      id: "nusashell.files",
      name: "Files",
      version: "0.2.0",
      icon: "📂",
      mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"], env: {} },
    }), "utf8");
    await writeFile(join(bundledRoot, "nusashell.files", "mcp", "server.js"), "// 0.2.0", "utf8");

    const result = await seeder.seed();
    expect(result.updated).toContain("nusashell.files");
    expect(await readManifestVersion(join(userRoot, "nusashell.files"))).toBe("0.2.0");
    expect(await readFile(join(userRoot, "nusashell.files", "mcp", "server.js"), "utf8")).toBe("// 0.2.0");
    const upgradedManifest = JSON.parse(await readFile(join(userRoot, "nusashell.files", "manifest.json"), "utf8")) as { mcp?: { autostart?: boolean; keepAliveOnClose?: boolean } };
    expect(upgradedManifest.mcp?.autostart).toBe(true);
    expect(upgradedManifest.mcp?.keepAliveOnClose).toBe(true);
    const state = await readState(userRoot);
    expect(state["nusashell.files"]?.version).toBe("0.2.0");
  });

  it("does not downgrade when the installed copy is newer than bundled", async () => {
    await makePluginDir(bundledRoot, "nusashell.files", "0.1.0");
    await seeder.seed();
    // User upgrades their copy beyond the bundled version.
    await writeFile(join(userRoot, "nusashell.files", "manifest.json"), JSON.stringify({
      id: "nusashell.files",
      name: "Files",
      version: "9.9.9",
      icon: "📂",
      mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"], env: {} },
    }), "utf8");

    const result = await seeder.seed();
    expect(result.skipped).toContain("nusashell.files");
    expect(await readManifestVersion(join(userRoot, "nusashell.files"))).toBe("9.9.9");
  });

  it("does not re-seed a bundled plugin the user removed (tombstone)", async () => {
    await makePluginDir(bundledRoot, "nusashell.files", "0.1.0");
    await seeder.seed();
    // User uninstalls the seeded copy.
    await rm(join(userRoot, "nusashell.files"), { recursive: true, force: true });

    const result = await seeder.seed();
    expect(result.seeded).toEqual([]);
    expect(result.skipped).toContain("nusashell.files");
    expect((await stat(join(userRoot, "nusashell.files")).catch(() => null))).toBeNull();
  });

  it("does not touch user plugins that are not bundled", async () => {
    await makePluginDir(bundledRoot, "nusashell.files", "0.1.0");
    await makePluginDir(userRoot, "my.custom", "2.0.0");

    await seeder.seed();
    expect(await readManifestVersion(join(userRoot, "my.custom"))).toBe("2.0.0");
  });

  it("leaves a bundled copy alone when it was removed from the bundle (user keeps it)", async () => {
    await makePluginDir(bundledRoot, "nusashell.old", "1.0.0");
    await seeder.seed();
    // Bundled no longer ships the plugin; the user copy stays.
    await rm(join(bundledRoot, "nusashell.old"), { recursive: true, force: true });

    const result = await seeder.seed();
    expect(await readManifestVersion(join(userRoot, "nusashell.old"))).toBe("1.0.0");
    expect(result.updated).toEqual([]);
  });
});
