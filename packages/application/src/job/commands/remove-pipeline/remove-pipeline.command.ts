import type { Command } from "../../../messaging/command.js";

export interface RemovePipelineCommand extends Command {
  readonly kind: "remove-pipeline";
  readonly id: string;
}
