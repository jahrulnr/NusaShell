import { readFile } from "node:fs/promises";
import type { PluginRepositoryPort } from "@nusashell/application";
import {
  Plugin,
  PluginId,
  PluginManifest,
  PluginVersion,
  type PluginManifestInput,
} from "@nusashell/domain";
import { scanPluginDirectories, resolveManifestPath } from "../../plugins/plugin-directory-layout.js";

interface RawManifest {
  id: string;
  name: string;
  version: string;
  icon: string;
  ui: PluginManifestInput["ui"];
  mcp: PluginManifestInput["mcp"];
  dependencies?: PluginManifestInput["dependencies"];
}

export class FilesystemPluginRegistry implements PluginRepositoryPort {
  private cache = new Map<string, Plugin>();
  private loaded = false;

  constructor(private readonly rootDir: string) {}

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
    } catch {
      return null;
    }

    const parsed = JSON.parse(raw) as RawManifest;

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
