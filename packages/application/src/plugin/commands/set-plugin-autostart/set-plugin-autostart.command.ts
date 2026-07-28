import type { Command } from "../../../messaging/command.js";
export interface SetPluginAutostartCommand extends Command { readonly kind: "set-plugin-autostart"; readonly pluginId: string; readonly autostart: boolean; }
