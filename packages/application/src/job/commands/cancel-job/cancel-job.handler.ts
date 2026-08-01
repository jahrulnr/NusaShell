import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { JobScheduler } from "../../services/job-scheduler.js";
import type { CancelJobCommand, CancelJobResult } from "./cancel-job.command.js";

export class CancelJobHandler implements CommandHandler<CancelJobCommand, CancelJobResult> {
  constructor(private readonly scheduler: JobScheduler) {}

  async handle(command: CancelJobCommand): Promise<CancelJobResult> {
    const result = await this.scheduler.cancel(command.id);
    if (!result.ok && result.error?.includes("not running")) {
      throw new ApplicationError("JOB_NOT_RUNNING", result.error);
    }
    return result;
  }
}
