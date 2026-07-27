import type { PluginRuntimeState } from "@nusashell/domain";

export interface GetPluginResult {
  readonly pluginId: string;
  readonly name: string;
  readonly version: string;
  readonly state: PluginRuntimeState;
  readonly enabled: boolean;
}
