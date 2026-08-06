import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { JobScheduler } from "../../services/job-scheduler.js";
import type { RunJobNowCommand } from "./run-job-now.command.js";

export interface RunJobNowResult {
  readonly ok: boolean;
  readonly error?: string;
}

/**
 * `job.run` — fires a job immediately WITHOUT waiting for it to finish.
 * The job runs in the background; progress is published via job.* events.
 * This keeps the IPC/WS request bounded (a long agent job must not block the
 * renderer or the request timeout).
 */
export class RunJobNowHandler implements CommandHandler<RunJobNowCommand, RunJobNowResult> {
  constructor(private readonly scheduler: JobScheduler) {}

  async handle(command: RunJobNowCommand): Promise<RunJobNowResult> {
    const result = await this.scheduler.startJobNow(command.id);
    if (!result.ok && result.error?.includes("not found")) {
      throw new ApplicationError("JOB_NOT_FOUND", result.error);
    }
    return result;
  }
}