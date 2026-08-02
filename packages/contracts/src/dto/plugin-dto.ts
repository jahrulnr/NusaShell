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
  readonly source?: "native-mcp" | "package";
  readonly transport?: string;
  readonly category?: string;
  readonly ui?: {
    readonly entry: string;
    readonly window?: {
      readonly mode?: "panel" | "fullscreen" | "widget";
      readonly defaultSize?: {
        readonly width: number;
        readonly height: number;
      };
      readonly resizable?: boolean;
    };
  };
  readonly keepAliveOnClose: boolean;
}

export interface PluginStateResultDto {
  readonly pluginId: string;
  readonly state: PluginStateDto;
}

export type PluginGetResultDto = PluginDto;
