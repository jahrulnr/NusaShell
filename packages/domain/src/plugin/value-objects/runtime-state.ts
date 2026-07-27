export type PluginRuntimeState =
  | "idle"
  | "starting"
  | "running"
  | "background"
  | "stopping"
  | "crashed"
  | "disabled";

export const PLUGIN_RUNTIME_STATES: readonly PluginRuntimeState[] = [
  "idle",
  "starting",
  "running",
  "background",
  "stopping",
  "crashed",
  "disabled",
] as const;
