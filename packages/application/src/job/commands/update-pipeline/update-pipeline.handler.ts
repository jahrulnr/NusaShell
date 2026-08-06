import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PipelineStorePort } from "../../ports/pipeline-store.port.js";
import type { Pipeline } from "../../pipeline-model.js";
import {
  validatePipeline,
  validatePipelineTrigger,
  nextRunAtForPipelineTrigger,
} from "../../pipeline-model.js";
import type { UpdatePipelineCommand } from "./update-pipeline.command.js";

export class UpdatePipelineHandler implements CommandHandler<UpdatePipelineCommand, Pipeline> {
  constructor(private readonly store: PipelineStorePort) {}

  async handle(command: UpdatePipelineCommand): Promise<Pipeline> {
    const pipeline = await this.store.get(command.id);
    if (!pipeline) throw new ApplicationError("PIPELINE_NOT_FOUND", `Pipeline not found: ${command.id}`);

    if (command.steps !== undefined) {
      const error = validatePipeline(command.steps);
      if (error) throw new ApplicationError("PIPELINE_INVALID", error);
    }
    if (command.trigger !== undefined) {
      const triggerError = validatePipelineTrigger(command.trigger);
      if (triggerError) {
        throw new ApplicationError(
          command.trigger.kind === "schedule" ? "JOB_INVALID_SCHEDULE" : "PIPELINE_INVALID",
          triggerError,
        );
      }
    }

    const settings = command.settings === null
      ? undefined
      : command.settings?.timeoutMs !== undefined
        ? { timeoutMs: command.settings.timeoutMs }
        : command.settings !== undefined
          ? pipeline.settings
          : pipeline.settings;

    const trigger = command.trigger !== undefined ? command.trigger : pipeline.trigger;
    const enabled = command.enabled !== undefined ? command.enabled : pipeline.enabled;
    const now = new Date();
    const recomputeNext =
      command.trigger !== undefined ||
      command.enabled !== undefined;

    const updated: Pipeline = {
      ...pipeline,
      ...(command.name !== undefined ? { name: command.name } : {}),
      ...(command.description !== undefined
        ? command.description === null ? {} : { description: command.description }
        : {}),
      trigger,
      ...(command.steps !== undefined ? { steps: command.steps } : {}),
      enabled,
      ...(recomputeNext
        ? {
            nextRunAt: nextRunAtForPipelineTrigger(trigger, pipeline.lastRunAt, now, enabled),
          }
        : {}),
      ...(command.settings !== undefined
        ? command.settings === null
          ? {}
          : settings
            ? { settings }
            : {}
        : {}),
    };
    if (command.description === null) delete (updated as { description?: string }).description;
    if (command.settings === null) delete (updated as { settings?: unknown }).settings;
    return this.store.update(updated);
  }
}
