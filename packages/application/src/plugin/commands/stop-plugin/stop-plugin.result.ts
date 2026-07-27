import type { PluginRuntimeState } from "@nusashell/domain";

export interface StopPluginResult {
  readonly pluginId: string;
  readonly state: PluginRuntimeState;
}
