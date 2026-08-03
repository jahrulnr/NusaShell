import type { Command } from "../../../messaging/command.js";
import type { JobTrigger, PipelineStep, PipelineSettings } from "../../pipeline-model.js";

export interface UpdatePipelineCommand extends Command {
  readonly kind: "update-pipeline";
  readonly id: string;
  readonly name?: string;
  readonly description?: string | null;
  readonly trigger?: JobTrigger;
  readonly steps?: readonly PipelineStep[];
  readonly settings?: PipelineSettings | null;
  readonly enabled?: boolean;
}
