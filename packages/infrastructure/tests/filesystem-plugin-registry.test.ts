import { describe, expect, it, beforeEach, afterEach } from "vitest";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { FilesystemPluginRegistry } from "../src/persistence/filesystem/filesystem-plugin.registry.js";
import { PluginId } from "@nusashell/domain";

const VALID_MANIFEST = {
  id: "nusashell.notes",
  name: "Notes Plugin",
  version: "1.0.0",
  icon: "notes",
  ui: { entry: "index.html" },
  mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"], env: {} },
};

describe("FilesystemPluginRegistry", () => {
  let tempDir: string;

  beforeEach(async () => {
    tempDir = await mkdtemp(join(tmpdir(), "nusashell-test-"));
  });

  afterEach(async () => {
    await rm(tempDir, { recursive: true, force: true });
  });

  it("returns empty list when no plugins exist", async () => {
    const registry = new FilesystemPluginRegistry(tempDir);
    const list = await registry.list();
    expect(list).toHaveLength(0);
  });

  it("loads a plugin from a directory with valid manifest.json", async () => {
    const pluginDir = join(tempDir, "nusashell.notes");
    await mkdir(pluginDir, { recursive: true });
    await writeFile(
      join(pluginDir, "manifest.json"),
      JSON.stringify(VALID_MANIFEST),
    );

    const registry = new FilesystemPluginRegistry(tempDir);
    const list = await registry.list();
    expect(list).toHaveLength(1);
    expect(list[0]!.manifest.name).toBe("Notes Plugin");
  });

  it("finds a plugin by id", async () => {
    const pluginDir = join(tempDir, "nusashell.notes");
    await mkdir(pluginDir, { recursive: true });
    await writeFile(
      join(pluginDir, "manifest.json"),
      JSON.stringify(VALID_MANIFEST),
    );

    const registry = new FilesystemPluginRegistry(tempDir);
    const idResult = PluginId.create("nusashell.notes");
    if (!idResult.ok) throw new Error("bad id");

    const plugin = await registry.findById(idResult.value);
    expect(plugin).not.toBeNull();
    expect(plugin!.manifest.name).toBe("Notes Plugin");
  });

  it("returns null for unknown plugin id", async () => {
    const registry = new FilesystemPluginRegistry(tempDir);
    const idResult = PluginId.create("com.unknown.plugin");
    if (!idResult.ok) throw new Error("bad id");

    const plugin = await registry.findById(idResult.value);
    expect(plugin).toBeNull();
  });

  it("skips directories without manifest.json", async () => {
    const validDir = join(tempDir, "example.valid");
    await mkdir(validDir, { recursive: true });
    await writeFile(
      join(validDir, "manifest.json"),
      JSON.stringify(VALID_MANIFEST),
    );

    const noManifestDir = join(tempDir, "example.broken");
    await mkdir(noManifestDir, { recursive: true });

    const registry = new FilesystemPluginRegistry(tempDir);
    const list = await registry.list();
    expect(list).toHaveLength(1);
    expect(list[0]!.manifest.name).toBe("Notes Plugin");
  });

  it("skips directories with invalid manifest", async () => {
    const validDir = join(tempDir, "example.valid");
    await mkdir(validDir, { recursive: true });
    await writeFile(
      join(validDir, "manifest.json"),
      JSON.stringify(VALID_MANIFEST),
    );

    const invalidDir = join(tempDir, "example.invalid");
    await mkdir(invalidDir, { recursive: true });
    await writeFile(
      join(invalidDir, "manifest.json"),
      JSON.stringify({ id: "", name: "Broken" }),
    );

    const registry = new FilesystemPluginRegistry(tempDir);
    const list = await registry.list();
    expect(list).toHaveLength(1);
  });

  it("loads multiple plugins", async () => {
    for (const id of ["example.a", "example.b", "example.c"]) {
      const dir = join(tempDir, id);
      await mkdir(dir, { recursive: true });
      await writeFile(
        join(dir, "manifest.json"),
        JSON.stringify({ ...VALID_MANIFEST, id }),
      );
    }

    const registry = new FilesystemPluginRegistry(tempDir);
    const list = await registry.list();
    expect(list).toHaveLength(3);
  });
});
