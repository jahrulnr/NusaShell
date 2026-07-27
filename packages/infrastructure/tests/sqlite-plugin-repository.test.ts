import { describe, expect, it, beforeEach, afterEach } from "vitest";
import { mkdtemp, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { SqliteDatabase, SqlitePluginRepository } from "../src/persistence/sqlite/index.js";
import { PluginId } from "@nusashell/domain";
import {
  Plugin,
  PluginManifest,
  PluginVersion,
  type PluginManifestInput,
} from "@nusashell/domain";

function makePlugin(
  id: string = "com.example.notes",
  overrides: Partial<PluginManifestInput> = {},
  enabled: boolean = true,
): Plugin {
  const raw: PluginManifestInput = {
    id,
    name: "Notes",
    version: "1.0.0",
    icon: "note",
    ui: { entry: "index.html" },
    mcp: { transport: "stdio", command: "node", env: {} },
    ...overrides,
  };
  const manifestResult = PluginManifest.create(raw);
  if (!manifestResult.ok) throw new Error(`Invalid manifest: ${manifestResult.error.message}`);
  const idResult = PluginId.create(id);
  if (!idResult.ok) throw new Error(`Invalid id: ${id}`);
  const versionResult = PluginVersion.create(raw.version);
  if (!versionResult.ok) throw new Error(`Invalid version`);
  return Plugin.create({
    id: idResult.value,
    version: versionResult.value,
    manifest: manifestResult.value,
    enabled,
    installPath: `/plugins/${id}`,
    installedAt: new Date("2026-01-01T00:00:00Z"),
  });
}

describe("SqlitePluginRepository", () => {
  let tempDir: string;
  let db: SqliteDatabase;
  let repo: SqlitePluginRepository;

  beforeEach(async () => {
    tempDir = await mkdtemp(join(tmpdir(), "nusashell-sqlite-"));
    db = new SqliteDatabase(join(tempDir, "test.db"));
    repo = new SqlitePluginRepository(db);
  });

  afterEach(async () => {
    db.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  it("save + findById roundtrip", async () => {
    const plugin = makePlugin("com.example.notes");
    await repo.save(plugin);

    const idResult = PluginId.create("com.example.notes");
    if (!idResult.ok) throw new Error("bad id");

    const found = await repo.findById(idResult.value);
    expect(found).not.toBeNull();
    expect(found!.manifest.name).toBe("Notes");
    expect(found!.manifest.version.toString()).toBe("1.0.0");
    expect(found!.enabled).toBe(true);
    expect(found!.installPath).toBe("/plugins/com.example.notes");
  });

  it("list returns all saved plugins", async () => {
    await repo.save(makePlugin("com.example.a"));
    await repo.save(makePlugin("com.example.b"));
    await repo.save(makePlugin("com.example.c"));

    const list = await repo.list();
    expect(list).toHaveLength(3);
  });

  it("remove deletes a plugin", async () => {
    const plugin = makePlugin("com.example.notes");
    await repo.save(plugin);

    const idResult = PluginId.create("com.example.notes");
    if (!idResult.ok) throw new Error("bad id");

    await repo.remove(idResult.value);
    const found = await repo.findById(idResult.value);
    expect(found).toBeNull();
  });

  it("save overwrites existing (UPSERT)", async () => {
    const plugin = makePlugin("com.example.notes", { name: "Original" });
    await repo.save(plugin);

    const updated = makePlugin("com.example.notes", { name: "Updated" });
    await repo.save(updated);

    const idResult = PluginId.create("com.example.notes");
    if (!idResult.ok) throw new Error("bad id");

    const found = await repo.findById(idResult.value);
    expect(found).not.toBeNull();
    expect(found!.manifest.name).toBe("Updated");
  });

  it("findById returns null for unknown id", async () => {
    const idResult = PluginId.create("com.unknown.plugin");
    if (!idResult.ok) throw new Error("bad id");

    const found = await repo.findById(idResult.value);
    expect(found).toBeNull();
  });

  it("preserves manifest with optional fields", async () => {
    const plugin = makePlugin("com.example.advanced", {
      ui: {
        entry: "ui/index.html",
        window: { mode: "panel", defaultSize: { width: 480, height: 560 }, resizable: true },
      },
      mcp: {
        transport: "stdio",
        command: "node",
        args: ["server.js"],
        env: { FOO: "bar" },
        autostart: true,
        keepAliveOnClose: false,
      },
      dependencies: { shell: ">=0.1.0" },
    });
    await repo.save(plugin);

    const idResult = PluginId.create("com.example.advanced");
    if (!idResult.ok) throw new Error("bad id");

    const found = await repo.findById(idResult.value);
    expect(found).not.toBeNull();
    expect(found!.manifest.ui.window?.mode).toBe("panel");
    expect(found!.manifest.ui.window?.defaultSize?.width).toBe(480);
    expect(found!.manifest.mcp.args).toEqual(["server.js"]);
    expect(found!.manifest.mcp.env).toEqual({ FOO: "bar" });
    expect(found!.manifest.mcp.autostart).toBe(true);
    expect(found!.manifest.dependencies.shell).toBe(">=0.1.0");
  });

  it("persists disabled state", async () => {
    const plugin = makePlugin("com.example.notes", {}, false);
    await repo.save(plugin);

    const idResult = PluginId.create("com.example.notes");
    if (!idResult.ok) throw new Error("bad id");

    const found = await repo.findById(idResult.value);
    expect(found).not.toBeNull();
    expect(found!.enabled).toBe(false);
  });
});
