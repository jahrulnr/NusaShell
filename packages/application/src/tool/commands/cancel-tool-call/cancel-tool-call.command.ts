import type { Command } from "../../../messaging/command.js";

export interface CancelToolCallCommand extends Command {
  readonly kind: "cancel-tool-call";
  readonly pluginId: string;
  readonly requestId: string;
}
