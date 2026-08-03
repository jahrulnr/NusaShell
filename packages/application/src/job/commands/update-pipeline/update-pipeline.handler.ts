import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PipelineStorePort } from "../../ports/pipeline-store.port.js";
import type { Pipeline } from "../../pipeline-model.js";
import { validatePipeline } from "../../pipeline-model.js";
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

    const updated: Pipeline = {
      ...pipeline,
      ...(command.name !== undefined ? { name: command.name } : {}),
      ...(command.description !== undefined
        ? command.description === null ? {} : { description: command.description }
        : {}),
      ...(command.trigger !== undefined ? { trigger: command.trigger } : {}),
      ...(command.steps !== undefined ? { steps: command.steps } : {}),
      ...(command.enabled !== undefined ? { enabled: command.enabled } : {}),
      ...(command.settings !== undefined
        ? command.settings === null ? {} : { settings: command.settings }
        : {}),
    };
    if (command.description === null) delete (updated as { description?: string }).description;
    if (command.settings === null) delete (updated as { settings?: unknown }).settings;
    return this.store.update(updated);
  }
}
