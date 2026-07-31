import type { Command } from "../../../messaging/command.js";

export interface RemoveJobCommand extends Command {
  readonly kind: "remove-job";
  readonly id: string;
}
