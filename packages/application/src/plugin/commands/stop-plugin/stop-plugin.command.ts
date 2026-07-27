import type { Command } from "../../../messaging/command.js";

export interface StopPluginCommand extends Command {
  readonly kind: "stop-plugin";
  readonly pluginId: string;
}
