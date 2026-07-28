import type { Command } from "../../../messaging/command.js";

export interface InstallPluginCommand extends Command {
  readonly kind: "install-plugin";
  readonly source: "url" | "local";
  readonly path: string;
}
