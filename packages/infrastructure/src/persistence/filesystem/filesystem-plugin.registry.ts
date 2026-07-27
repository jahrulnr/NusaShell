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
import { scanPluginDirectories, resolveManifestPath } from "../../plugins/plugin-directory-layout.js";

export class FilesystemPluginRegistry implements PluginRepositoryPort {
  private cache = new Map<string, Plugin>();
  private loaded = false;

  constructor(
    private readonly rootDir: string,
    private readonly logger?: Logger,
  ) {}

  private async ensureLoaded(): Promise<void> {
    if (this.loaded) return;
    this.loaded = true;
    this.cache.clear();

    const dirs = await scanPluginDirectories(this.rootDir);
    for (const dir of dirs) {
      const plugin = await this.loadPluginFromDir(dir.path);
      if (plugin) {
        this.cache.set(PluginId.toString(plugin.id), plugin);
      }
    }
  }

  private async loadPluginFromDir(dirPath: string): Promise<Plugin | null> {
    const manifestPath = resolveManifestPath(dirPath);
    let raw: string;
    try {
      raw = await readFile(manifestPath, "utf-8");
    } catch (err) {
      this.logger?.warn({ err, manifestPath }, "Failed to read manifest file");
      return null;
    }

    let parsedJson: unknown;
    try {
      parsedJson = JSON.parse(raw);
    } catch (err) {
      this.logger?.warn({ err, manifestPath }, "Failed to parse manifest JSON");
      return null;
    }

    const schemaResult = ManifestSchema.safeParse(parsedJson);
    if (!schemaResult.success) {
      return null;
    }

    const parsed = schemaResult.data;

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

  async findById(id: PluginId): Promise<Plugin | null> {
    await this.ensureLoaded();
    return this.cache.get(PluginId.toString(id)) ?? null;
  }

  async list(): Promise<readonly Plugin[]> {
    await this.ensureLoaded();
    return [...this.cache.values()];
  }

  async save(plugin: Plugin): Promise<void> {
    await this.ensureLoaded();
    this.cache.set(PluginId.toString(plugin.id), plugin);
  }

  async remove(id: PluginId): Promise<void> {
    await this.ensureLoaded();
    this.cache.delete(PluginId.toString(id));
  }
}
