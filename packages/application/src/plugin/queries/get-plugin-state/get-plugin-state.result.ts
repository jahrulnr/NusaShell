import type { PluginRuntimeState } from "@nusashell/domain";

export interface GetPluginStateResult {
  readonly pluginId: string;
  readonly state: PluginRuntimeState;
}
