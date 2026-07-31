import { randomUUID } from "node:crypto";
import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { JobStorePort } from "../../ports/job-store.port.js";
import type { Job } from "../../job-model.js";
import { parseSchedule, computeNextRun, ScheduleParseError } from "../../schedule-parser.js";
import type { AddJobCommand } from "./add-job.command.js";

export class AddJobHandler implements CommandHandler<AddJobCommand, Job> {
  constructor(private readonly store: JobStorePort) {}

  async handle(command: AddJobCommand): Promise<Job> {
    let schedule;
    try {
      schedule = parseSchedule(command.schedule);
    } catch (error) {
      if (error instanceof ScheduleParseError) {
        throw new ApplicationError("JOB_INVALID_SCHEDULE", error.message);
      }
      throw error;
    }
    const now = new Date();
    const nextRunAt = computeNextRun(schedule, null, now);
    const job: Job = {
      id: randomUUID(),
      name: command.name,
      schedule,
      mode: command.mode,
      enabled: true,
      repeat: { times: command.repeatTimes ?? null, completed: 0 },
      nextRunAt,
      lastRunAt: null,
      lastStatus: null,
      lastError: null,
      createdAt: now.toISOString(),
    };
    return this.store.create(job);
  }
}
