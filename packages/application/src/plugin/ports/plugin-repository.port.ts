import type { Plugin, PluginId } from "@nusashell/domain";

export interface PluginRepositoryPort {
  findById(id: PluginId): Promise<Plugin | null>;
  list(): Promise<readonly Plugin[]>;
  save(plugin: Plugin): Promise<void>;
  remove(id: PluginId): Promise<void>;
}
