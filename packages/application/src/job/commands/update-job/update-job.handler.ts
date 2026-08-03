import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { JobStorePort } from "../../ports/job-store.port.js";
import type { Job, JobMode, JobTrigger } from "../../job-model.js";
import { parseSchedule, computeNextRun, ScheduleParseError } from "../../schedule-parser.js";
import type { UpdateJobCommand } from "./update-job.command.js";

export class UpdateJobHandler implements CommandHandler<UpdateJobCommand, Job> {
  constructor(private readonly store: JobStorePort) {}

  async handle(command: UpdateJobCommand): Promise<Job> {
    const job = await this.store.get(command.id);
    if (!job) throw new ApplicationError("JOB_NOT_FOUND", `Job not found: ${command.id}`);

    let trigger: JobTrigger = job.trigger;
    let nextRunAt = job.nextRunAt;
    if (command.trigger !== undefined) {
      trigger = command.trigger;
      nextRunAt = trigger.kind === "schedule" && job.enabled
        ? computeNextRun(trigger.schedule, job.lastRunAt, new Date())
        : null;
    } else if (command.schedule !== undefined) {
      try {
        const schedule = parseSchedule(command.schedule);
        trigger = { kind: "schedule", schedule };
        nextRunAt = job.enabled ? computeNextRun(schedule, job.lastRunAt, new Date()) : null;
      } catch (error) {
        if (error instanceof ScheduleParseError) {
          throw new ApplicationError("JOB_INVALID_SCHEDULE", error.message);
        }
        throw error;
      }
    }

    const mode: JobMode | undefined = command.mode ?? job.mode;
    const repeatTimes = command.repeatTimes !== undefined ? command.repeatTimes : job.repeat.times;
    const enabled = command.enabled ?? job.enabled;

    const updated: Job = {
      ...job,
      ...(command.name !== undefined ? { name: command.name } : {}),
      trigger,
      mode,
      enabled,
      repeat: { times: repeatTimes, completed: job.repeat.completed },
      nextRunAt,
      ...(command.onComplete !== undefined
        ? command.onComplete === null
          ? {}
          : { onComplete: command.onComplete }
        : {}),
    };
    if (command.onComplete === null) {
      delete (updated as { onComplete?: unknown }).onComplete;
    }
    return this.store.update(updated);
  }
}
