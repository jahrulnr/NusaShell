import type { PluginRuntimeState } from "@nusashell/domain";

export interface StartPluginResult {
  readonly pluginId: string;
  readonly state: PluginRuntimeState;
}
