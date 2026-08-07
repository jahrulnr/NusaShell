import { describe, expect, it, beforeEach, afterEach } from "vitest";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { PluginSyncService } from "../src/plugins/plugin-sync-service.js";
import { SqlitePluginRepository, SqliteDatabase } from "../src/persistence/sqlite/index.js";
import { Plugin, PluginId } from "@nusashell/domain";

const VALID_MANIFEST = {
  id: "nusashell.notes",
  name: "Notes Plugin",
  version: "1.0.0",
  icon: "notes",
  ui: { entry: "index.html" },
  mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"], env: {} },
};

async function makePluginDir(root: string, id: string, manifest: Record<string, unknown> = VALID_MANIFEST): Promise<string> {
  const dir = join(root, id.replace(/\./g, "-"));
  await mkdir(dir, { recursive: true });
  await mkdir(join(dir, "mcp"), { recursive: true });
  await writeFile(join(dir, "manifest.json"), JSON.stringify(manifest), "utf8");
  await writeFile(join(dir, "index.html"), "<html></html>", "utf8");
  await writeFile(join(dir, "mcp", "server.js"), "// stub", "utf8");
  return dir;
}

function notesId(): PluginId {
  const result = PluginId.create("nusashell.notes");
  if (!result.ok) throw new Error("expected valid plugin id");
  return result.value;
}

describe("PluginSyncService enabled-state preservation", () => {
  let tempDir: string;
  let pluginRoot: string;
  let db: SqliteDatabase;
  let repo: SqlitePluginRepository;

  beforeEach(async () => {
    tempDir = await mkdtemp(join(tmpdir(), "nusashell-sync-"));
    pluginRoot = join(tempDir, "plugins");
    await mkdir(pluginRoot, { recursive: true });
    db = new SqliteDatabase(join(tempDir, "test.db"));
    repo = new SqlitePluginRepository(db);
  });

  afterEach(async () => {
    db.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  it("preserves disabled state across re-syncs", async () => {
    await makePluginDir(pluginRoot, "nusashell.notes");
    const sync = new PluginSyncService(pluginRoot, repo);
    await sync.sync();

    // Plugin starts enabled.
    const afterFirstSync = await repo.findById(notesId());
    expect(afterFirstSync?.enabled).toBe(true);

    // User disables the plugin.
    const disabled = Plugin.create({
      id: afterFirstSync!.id,
      version: afterFirstSync!.version,
      manifest: afterFirstSync!.manifest,
      enabled: false,
      installPath: afterFirstSync!.installPath,
      installedAt: afterFirstSync!.installedAt,
    });
    await repo.save(disabled);
    const afterDisable = await repo.findById(notesId());
    expect(afterDisable?.enabled).toBe(false);

    // Re-sync (e.g. app restart) should NOT re-enable the plugin.
    await sync.sync();
    const afterReSync = await repo.findById(notesId());
    expect(afterReSync?.enabled).toBe(false);
  });

  it("enables a new plugin on first sync", async () => {
    await makePluginDir(pluginRoot, "nusashell.notes");
    const sync = new PluginSyncService(pluginRoot, repo);
    await sync.sync();
    const plugin = await repo.findById(notesId());
    expect(plugin?.enabled).toBe(true);
  });

  it("removes stale plugins no longer on filesystem", async () => {
    await makePluginDir(pluginRoot, "nusashell.notes");
    const sync = new PluginSyncService(pluginRoot, repo);
    await sync.sync();
    expect((await repo.list()).length).toBe(1);

    await rm(join(pluginRoot, "nusashell-notes"), { recursive: true, force: true });
    await sync.sync();
    expect((await repo.list()).length).toBe(0);
  });
});
