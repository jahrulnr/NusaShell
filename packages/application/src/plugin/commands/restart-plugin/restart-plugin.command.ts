import type { Command } from "../../../messaging/command.js";

export interface RestartPluginCommand extends Command {
  readonly kind: "restart-plugin";
  readonly pluginId: string;
}
