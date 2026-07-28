import type { PluginRuntimeState } from "@nusashell/domain";

export interface PluginListItem {
  readonly pluginId: string;
  readonly name: string;
  readonly version: string;
  readonly icon: string;
  readonly installPath: string;
  readonly state: PluginRuntimeState;
  readonly enabled: boolean;
}

export interface ListPluginsResult {
  readonly plugins: readonly PluginListItem[];
}
