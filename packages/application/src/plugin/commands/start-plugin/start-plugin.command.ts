import type { Command } from "../../../messaging/command.js";

export interface StartPluginCommand extends Command {
  readonly kind: "start-plugin";
  readonly pluginId: string;
}
