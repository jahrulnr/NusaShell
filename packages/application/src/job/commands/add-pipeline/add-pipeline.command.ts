import type { Command } from "../../../messaging/command.js";
import type { JobTrigger, PipelineStep, PipelineSettings } from "../../pipeline-model.js";

export interface AddPipelineCommand extends Command {
  readonly kind: "add-pipeline";
  readonly name: string;
  readonly description?: string;
  readonly trigger: JobTrigger;
  readonly steps: readonly PipelineStep[];
  readonly settings?: PipelineSettings;
}
