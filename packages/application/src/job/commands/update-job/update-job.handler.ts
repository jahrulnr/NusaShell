import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { JobStorePort } from "../../ports/job-store.port.js";
import type { Job, JobMode } from "../../job-model.js";
import { parseSchedule, computeNextRun, ScheduleParseError } from "../../schedule-parser.js";
import type { UpdateJobCommand } from "./update-job.command.js";

export class UpdateJobHandler implements CommandHandler<UpdateJobCommand, Job> {
  constructor(private readonly store: JobStorePort) {}

  async handle(command: UpdateJobCommand): Promise<Job> {
    const job = await this.store.get(command.id);
    if (!job) throw new ApplicationError("JOB_NOT_FOUND", `Job not found: ${command.id}`);

    let schedule = job.schedule;
    let nextRunAt = job.nextRunAt;
    if (command.schedule !== undefined) {
      try {
        schedule = parseSchedule(command.schedule);
      } catch (error) {
        if (error instanceof ScheduleParseError) {
          throw new ApplicationError("JOB_INVALID_SCHEDULE", error.message);
        }
        throw error;
      }
      nextRunAt = job.enabled ? computeNextRun(schedule, job.lastRunAt, new Date()) : null;
    }

    const mode: JobMode | undefined = command.mode ?? job.mode;
    const repeatTimes = command.repeatTimes !== undefined ? command.repeatTimes : job.repeat.times;
    const enabled = command.enabled ?? job.enabled;

    const updated: Job = {
      ...job,
      ...(command.name !== undefined ? { name: command.name } : {}),
      schedule,
      mode,
      enabled,
      repeat: { times: repeatTimes, completed: job.repeat.completed },
      nextRunAt,
    };
    return this.store.update(updated);
  }
}
