import type { Command } from "../../../messaging/command.js";

export interface UninstallPluginCommand extends Command {
  readonly kind: "uninstall-plugin";
  readonly pluginId: string;
}
