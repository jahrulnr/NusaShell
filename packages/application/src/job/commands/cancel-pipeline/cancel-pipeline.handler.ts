import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PipelineScheduler } from "../../services/pipeline-scheduler.js";
import type { CancelPipelineCommand, CancelPipelineResult } from "./cancel-pipeline.command.js";

export class CancelPipelineHandler implements CommandHandler<CancelPipelineCommand, CancelPipelineResult> {
  constructor(private readonly scheduler: PipelineScheduler) {}

  async handle(command: CancelPipelineCommand): Promise<CancelPipelineResult> {
    return this.scheduler.cancel(command.id);
  }
}
