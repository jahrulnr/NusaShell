import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { JobStorePort } from "../../ports/job-store.port.js";
import type { Job } from "../../job-model.js";
import { scheduleOf } from "../../job-model.js";
import { computeNextRun } from "../../schedule-parser.js";
import type { SetJobEnabledCommand } from "./set-job-enabled.command.js";

export class SetJobEnabledHandler implements CommandHandler<SetJobEnabledCommand, Job> {
  constructor(private readonly store: JobStorePort) {}

  async handle(command: SetJobEnabledCommand): Promise<Job> {
    const job = await this.store.get(command.id);
    if (!job) throw new ApplicationError("JOB_NOT_FOUND", `Job not found: ${command.id}`);
    const schedule = scheduleOf(job.trigger);
    // When re-enabling a recurring job whose nextRunAt is in the past, recompute it.
    let nextRunAt = job.nextRunAt;
    if (schedule && command.enabled && schedule.kind !== "once" && (!nextRunAt || new Date(nextRunAt).getTime() <= Date.now())) {
      nextRunAt = computeNextRun(schedule, job.lastRunAt, new Date());
    }
    // When re-enabling a one-shot whose runAt is still in the future, restore nextRunAt.
    if (schedule && command.enabled && schedule.kind === "once" && !nextRunAt) {
      nextRunAt = schedule.runAt;
    }
    const updated: Job = { ...job, enabled: command.enabled, ...(nextRunAt !== job.nextRunAt ? { nextRunAt } : {}) };
    return this.store.update(updated);
  }
}
