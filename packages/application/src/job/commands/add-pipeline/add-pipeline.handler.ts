import { randomUUID } from "node:crypto";
import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PipelineStorePort } from "../../ports/pipeline-store.port.js";
import type { Pipeline } from "../../pipeline-model.js";
import {
  validatePipeline,
  validatePipelineTrigger,
  nextRunAtForPipelineTrigger,
} from "../../pipeline-model.js";
import type { AddPipelineCommand } from "./add-pipeline.command.js";

export class AddPipelineHandler implements CommandHandler<AddPipelineCommand, Pipeline> {
  constructor(private readonly store: PipelineStorePort) {}

  async handle(command: AddPipelineCommand): Promise<Pipeline> {
    const error = validatePipeline(command.steps);
    if (error) throw new ApplicationError("PIPELINE_INVALID", error);
    const triggerError = validatePipelineTrigger(command.trigger);
    if (triggerError) {
      throw new ApplicationError(
        command.trigger.kind === "schedule" ? "JOB_INVALID_SCHEDULE" : "PIPELINE_INVALID",
        triggerError,
      );
    }
    const now = new Date();
    const settings = command.settings?.timeoutMs !== undefined
      ? { timeoutMs: command.settings.timeoutMs }
      : undefined;
    const pipeline: Pipeline = {
      id: randomUUID(),
      name: command.name,
      ...(command.description ? { description: command.description } : {}),
      enabled: true,
      trigger: command.trigger,
      steps: command.steps,
      ...(settings ? { settings } : {}),
      createdAt: now.toISOString(),
      nextRunAt: nextRunAtForPipelineTrigger(command.trigger, null, now, true),
      lastRunAt: null,
      lastStatus: null,
      lastError: null,
    };
    return this.store.create(pipeline);
  }
}
