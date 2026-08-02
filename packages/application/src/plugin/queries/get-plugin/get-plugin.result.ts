import type { PluginRuntimeState } from "@nusashell/domain";

export interface GetPluginResult {
  readonly pluginId: string;
  readonly name: string;
  readonly version: string;
  readonly icon: string;
  readonly installPath: string;
  readonly state: PluginRuntimeState;
  readonly enabled: boolean;
  readonly autostart: boolean;
  readonly source?: "native-mcp" | "package";
  readonly transport?: string;
  readonly category?: string;
  readonly command?: string;
  readonly args?: readonly string[];
  readonly url?: string;
  readonly env?: Readonly<Record<string, string>>;
  readonly headers?: Readonly<Record<string, string>>;
  readonly ui?: {
    readonly entry: string;
    readonly window?: {
      readonly mode?: "panel" | "fullscreen" | "widget";
      readonly defaultSize?: { readonly width: number; readonly height: number };
      readonly resizable?: boolean;
    };
  };
  readonly keepAliveOnClose: boolean;
}
