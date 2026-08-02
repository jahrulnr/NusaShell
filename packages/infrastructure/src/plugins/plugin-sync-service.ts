import { readFile } from "node:fs/promises";
import type { PluginRepositoryPort } from "@nusashell/application";
import {
  Plugin,
  PluginId,
  PluginManifest,
  PluginVersion,
  type PluginManifestInput,
} from "@nusashell/domain";
import { ManifestSchema } from "@nusashell/contracts";
import type { Logger } from "pino";
import { scanPluginDirectories, resolveManifestPath } from "./plugin-directory-layout.js";
import { assertDeclaredFilesExist } from "./plugin-path-checks.js";

export class PluginSyncService {
  private readonly pluginRoots: readonly string[];

  constructor(
    pluginRoots: string | readonly string[],
    private readonly repository: PluginRepositoryPort,
    private readonly logger?: Logger,
  ) {
    this.pluginRoots = Array.isArray(pluginRoots) ? pluginRoots : [pluginRoots];
  }

  async sync(): Promise<void> {
    const foundIds = new Set<string>();

    for (const pluginRoot of this.pluginRoots) {
      const dirs = await scanPluginDirectories(pluginRoot);
      for (const dir of dirs) {
        const plugin = await this.loadPluginFromDir(dir.path);
        if (plugin) {
          const idStr = PluginId.toString(plugin.id);
          foundIds.add(idStr);
          await this.repository.save(plugin);
          this.logger?.debug({ pluginId: idStr, path: dir.path }, "Synced plugin to repository");
        }
      }
    }

    // Remove stale entries that no longer exist on filesystem
    const existing = await this.repository.list();
    for (const plugin of existing) {
      const idStr = PluginId.toString(plugin.id);
      if (!foundIds.has(idStr)) {
        await this.repository.remove(plugin.id);
        this.logger?.debug({ pluginId: idStr }, "Removed stale plugin from repository");
      }
    }
  }

  private async loadPluginFromDir(dirPath: string): Promise<Plugin | null> {
    const manifestPath = resolveManifestPath(dirPath);
    let raw: string;
    try {
      raw = await readFile(manifestPath, "utf-8");
    } catch (err) {
      this.logger?.warn({ err, manifestPath }, "Failed to read manifest file during sync");
      return null;
    }

    let parsedJson: unknown;
    try {
      parsedJson = JSON.parse(raw);
    } catch (err) {
      this.logger?.warn({ err, manifestPath }, "Failed to parse manifest JSON during sync");
      return null;
    }

    const schemaResult = ManifestSchema.safeParse(parsedJson);
    if (!schemaResult.success) {
      this.logger?.warn({ manifestPath, errors: schemaResult.error.issues }, "Invalid manifest schema during sync");
      return null;
    }

    const parsed = schemaResult.data;

    // Do not resurrect broken UI/icon packages from a cold filesystem: a
    // declared ui.entry or local icon that is missing on disk is skipped so
    // the launcher never opens a blank window for it.
    try {
      await assertDeclaredFilesExist(dirPath, parsed);
    } catch (err) {
      this.logger?.warn({ err, manifestPath }, "Plugin skipped during sync: declared file missing or outside plugin dir");
      return null;
    }
    const manifestInput = {
      id: parsed.id,
      name: parsed.name,
      version: parsed.version,
      icon: parsed.icon,
      ui: parsed.ui,
      mcp: parsed.mcp,
      ...(parsed.dependencies ? { dependencies: parsed.dependencies } : {}),
    } as PluginManifestInput;

    const manifestResult = PluginManifest.create(manifestInput);
    if (!manifestResult.ok) return null;

    const idResult = PluginId.create(parsed.id);
    if (!idResult.ok) return null;

    const versionResult = PluginVersion.create(parsed.version);
    if (!versionResult.ok) return null;

    return Plugin.create({
      id: idResult.value,
      version: versionResult.value,
      manifest: manifestResult.value,
      enabled: true,
      installPath: dirPath,
      installedAt: new Date(),
    });
  }
}
