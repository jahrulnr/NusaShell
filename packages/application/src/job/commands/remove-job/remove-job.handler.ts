import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { JobStorePort } from "../../ports/job-store.port.js";
import type { RemoveJobCommand } from "./remove-job.command.js";

export interface RemoveJobResult {
  readonly id: string;
  readonly removed: boolean;
}

export class RemoveJobHandler implements CommandHandler<RemoveJobCommand, RemoveJobResult> {
  constructor(private readonly store: JobStorePort) {}

  async handle(command: RemoveJobCommand): Promise<RemoveJobResult> {
    const existing = await this.store.get(command.id);
    if (!existing) throw new ApplicationError("JOB_NOT_FOUND", `Job not found: ${command.id}`);
    await this.store.remove(command.id);
    return { id: command.id, removed: true };
  }
}
