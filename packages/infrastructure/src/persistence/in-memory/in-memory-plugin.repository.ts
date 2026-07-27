import type { PluginRepositoryPort } from "@nusashell/application";
import { Plugin, PluginId } from "@nusashell/domain";

export class InMemoryPluginRepository implements PluginRepositoryPort {
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
