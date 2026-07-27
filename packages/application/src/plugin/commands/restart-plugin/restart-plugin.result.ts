import type { PluginRuntimeState } from "@nusashell/domain";

export interface RestartPluginResult {
  readonly pluginId: string;
  readonly state: PluginRuntimeState;
}
