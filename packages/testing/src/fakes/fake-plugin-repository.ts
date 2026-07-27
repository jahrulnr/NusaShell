import type { PluginRepositoryPort } from "@nusashell/application";
import { Plugin, PluginId, PluginManifest, PluginVersion, type PluginManifestInput } from "@nusashell/domain";

export class FakePluginRepository implements PluginRepositoryPort {
  private readonly plugins = new Map<string, Plugin>();

  add(plugin: Plugin): void {
    this.plugins.set(PluginId.toString(plugin.id), plugin);
  }

  findById(id: PluginId): Promise<Plugin | null> {
    return Promise.resolve(this.plugins.get(PluginId.toString(id)) ?? null);
  }

  list(): Promise<readonly Plugin[]> {
    return Promise.resolve([...this.plugins.values()]);
  }

  save(plugin: Plugin): Promise<void> {
    this.plugins.set(PluginId.toString(plugin.id), plugin);
    return Promise.resolve();
  }

  remove(id: PluginId): Promise<void> {
    this.plugins.delete(PluginId.toString(id));
    return Promise.resolve();
  }
}

export function makeManifest(
  overrides: Partial<PluginManifestInput> = {},
): PluginManifest {
  const raw: PluginManifestInput = {
    id: "com.example.notes",
    name: "Notes",
    version: "1.0.0",
    icon: "note",
    ui: { entry: "index.html" },
    mcp: {
      transport: "stdio",
      command: "node",
      env: {},
    },
    ...overrides,
  };
  const result = PluginManifest.create(raw);
  if (!result.ok) {
    throw new Error(`Invalid manifest: ${result.error.message}`);
  }
  return result.value;
}

export function makePlugin(
  id: string = "com.example.notes",
  overrides: Partial<PluginManifestInput> = {},
  enabled: boolean = true,
): Plugin {
  const manifest = makeManifest({ id, ...overrides });
  const idResult = PluginId.create(id);
  if (!idResult.ok) {
    throw new Error(`Invalid plugin id: ${id}`);
  }
  const versionResult = PluginVersion.create(manifest.version.toString());
  if (!versionResult.ok) {
    throw new Error(`Invalid version`);
  }
  return Plugin.create({
    id: idResult.value,
    version: versionResult.value,
    manifest,
    enabled,
    installPath: `/plugins/${id}`,
    installedAt: new Date("2026-01-01T00:00:00Z"),
  });
}
