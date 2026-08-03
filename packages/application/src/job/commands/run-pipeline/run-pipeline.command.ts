import type { Command } from "../../../messaging/command.js";

export interface RunPipelineCommand extends Command {
  readonly kind: "run-pipeline";
  readonly id: string;
}

export interface RunPipelineResult {
  readonly ok: boolean;
  readonly error?: string;
}
