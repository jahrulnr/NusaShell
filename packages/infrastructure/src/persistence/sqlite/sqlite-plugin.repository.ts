import type { PluginRepositoryPort } from "@nusashell/application";
import {
  Plugin,
  PluginId,
  PluginManifest,
  PluginVersion,
  type PluginManifestInput,
} from "@nusashell/domain";
import type { SqliteDatabase } from "./database.js";

interface PluginRow {
  id: string;
  version: string;
  manifest_json: string;
  enabled: number;
  install_path: string;
  installed_at: string;
}

export class SqlitePluginRepository implements PluginRepositoryPort {
  constructor(private readonly database: SqliteDatabase) {}

  async findById(id: PluginId): Promise<Plugin | null> {
    const row = this.database.raw
      .prepare("SELECT * FROM plugins WHERE id = ?")
      .get(PluginId.toString(id)) as PluginRow | undefined;
    if (!row) return null;
    return this.rowToPlugin(row);
  }

  async list(): Promise<readonly Plugin[]> {
    const rows = this.database.raw
      .prepare("SELECT * FROM plugins ORDER BY id")
      .all() as PluginRow[];
    return rows.map((row) => this.rowToPlugin(row));
  }

  async save(plugin: Plugin): Promise<void> {
    const manifestJson = JSON.stringify(this.manifestToInput(plugin.manifest));
    this.database.raw
      .prepare(
        `INSERT OR REPLACE INTO plugins (id, version, manifest_json, enabled, install_path, installed_at)
         VALUES (?, ?, ?, ?, ?, ?)`,
      )
      .run(
        PluginId.toString(plugin.id),
        plugin.version.toString(),
        manifestJson,
        plugin.enabled ? 1 : 0,
        plugin.installPath,
        plugin.installedAt.toISOString(),
      );
  }

  async remove(id: PluginId): Promise<void> {
    this.database.raw
      .prepare("DELETE FROM plugins WHERE id = ?")
      .run(PluginId.toString(id));
  }

  private rowToPlugin(row: PluginRow): Plugin {
    const raw = JSON.parse(row.manifest_json) as PluginManifestInput;
    const manifestResult = PluginManifest.create(raw);
    if (!manifestResult.ok) {
      throw new Error(`Invalid manifest in DB for plugin ${row.id}: ${manifestResult.error.message}`);
    }

    const idResult = PluginId.create(row.id);
    if (!idResult.ok) {
      throw new Error(`Invalid plugin id in DB: ${row.id}`);
    }

    const versionResult = PluginVersion.create(row.version);
    if (!versionResult.ok) {
      throw new Error(`Invalid version in DB for plugin ${row.id}: ${versionResult.error.message}`);
    }

    return Plugin.create({
      id: idResult.value,
      version: versionResult.value,
      manifest: manifestResult.value,
      enabled: row.enabled === 1,
      installPath: row.install_path,
      installedAt: new Date(row.installed_at),
    });
  }

  private manifestToInput(manifest: PluginManifest): PluginManifestInput {
    return {
      id: PluginId.toString(manifest.id),
      name: manifest.name,
      version: manifest.version.toString(),
      icon: manifest.icon,
      ui: manifest.ui,
      mcp: {
        transport: manifest.mcp.transport,
        ...(manifest.mcp.command !== undefined ? { command: manifest.mcp.command } : {}),
        args: manifest.mcp.args,
        ...(manifest.mcp.url !== undefined ? { url: manifest.mcp.url } : {}),
        env: manifest.mcp.env,
        autostart: manifest.mcp.autostart,
        keepAliveOnClose: manifest.mcp.keepAliveOnClose,
      },
      ...(manifest.dependencies.shell !== undefined
        ? { dependencies: { shell: manifest.dependencies.shell } }
        : {}),
    };
  }
}
