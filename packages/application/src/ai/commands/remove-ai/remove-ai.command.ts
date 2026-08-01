import type { Command } from "../../../messaging/command.js";

export interface RemoveAiCommand extends Command {
  readonly kind: "remove-ai";
  readonly providerId: string;
}
