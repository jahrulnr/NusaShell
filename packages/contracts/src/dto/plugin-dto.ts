export type PluginStateDto =
  | "idle"
  | "starting"
  | "running"
  | "stopping"
  | "crashed";

export interface PluginDto {
  readonly pluginId: string;
  readonly name: string;
  readonly version: string;
  readonly icon: string;
  readonly installPath: string;
  readonly state: PluginStateDto;
  readonly enabled: boolean;
  readonly autostart: boolean;
}

export interface PluginStateResultDto {
  readonly pluginId: string;
  readonly state: PluginStateDto;
}

export type PluginGetResultDto = PluginDto;
