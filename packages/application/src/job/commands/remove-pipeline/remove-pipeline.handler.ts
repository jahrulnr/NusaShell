import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PipelineStorePort } from "../../ports/pipeline-store.port.js";
import type { RemovePipelineCommand } from "./remove-pipeline.command.js";

export type RemovePipelineResult = { readonly id: string };

export class RemovePipelineHandler implements CommandHandler<RemovePipelineCommand, RemovePipelineResult> {
  constructor(private readonly store: PipelineStorePort) {}

  async handle(command: RemovePipelineCommand): Promise<RemovePipelineResult> {
    await this.store.remove(command.id);
    return { id: command.id };
  }
}
