import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PipelineScheduler } from "../../services/pipeline-scheduler.js";
import type { RunPipelineCommand, RunPipelineResult } from "./run-pipeline.command.js";

/**
 * `pipeline.run` — fires a pipeline immediately and returns without waiting for
 * the run to finish. The run executes in the background; progress is published
 * via `pipeline.started` / `pipeline.step_updated` / `pipeline.completed` /
 * `pipeline.failed` events on the event bus. This keeps the IPC request
 * bounded (a long multi-step pipeline must not block the renderer/WS request).
 */
export class RunPipelineHandler implements CommandHandler<RunPipelineCommand, RunPipelineResult> {
  constructor(private readonly scheduler: PipelineScheduler) {}

  async handle(command: RunPipelineCommand): Promise<RunPipelineResult> {
    return this.scheduler.launch(command.id);
  }
}