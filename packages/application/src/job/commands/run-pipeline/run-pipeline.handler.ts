import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PipelineScheduler } from "../../services/pipeline-scheduler.js";
import type { RunPipelineCommand, RunPipelineResult } from "./run-pipeline.command.js";

export class RunPipelineHandler implements CommandHandler<RunPipelineCommand, RunPipelineResult> {
  constructor(private readonly scheduler: PipelineScheduler) {}

  async handle(command: RunPipelineCommand): Promise<RunPipelineResult> {
    const result = await this.scheduler.runPipeline(command.id);
    return result;
  }
}
