import { randomUUID } from "node:crypto";
import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PipelineStorePort } from "../../ports/pipeline-store.port.js";
import type { Pipeline } from "../../pipeline-model.js";
import { validatePipeline } from "../../pipeline-model.js";
import type { AddPipelineCommand } from "./add-pipeline.command.js";

export class AddPipelineHandler implements CommandHandler<AddPipelineCommand, Pipeline> {
  constructor(private readonly store: PipelineStorePort) {}

  async handle(command: AddPipelineCommand): Promise<Pipeline> {
    const error = validatePipeline(command.steps);
    if (error) throw new ApplicationError("PIPELINE_INVALID", error);
    const now = new Date();
    const pipeline: Pipeline = {
      id: randomUUID(),
      name: command.name,
      ...(command.description ? { description: command.description } : {}),
      enabled: true,
      trigger: command.trigger,
      steps: command.steps,
      ...(command.settings ? { settings: command.settings } : {}),
      createdAt: now.toISOString(),
      lastRunAt: null,
      lastStatus: null,
      lastError: null,
    };
    return this.store.create(pipeline);
  }
}
