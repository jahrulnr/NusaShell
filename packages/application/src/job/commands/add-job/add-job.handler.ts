import { randomUUID } from "node:crypto";
import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { JobStorePort } from "../../ports/job-store.port.js";
import type { Job, JobTrigger } from "../../job-model.js";
import { parseSchedule, computeNextRun, ScheduleParseError } from "../../schedule-parser.js";
import type { AddJobCommand } from "./add-job.command.js";

export class AddJobHandler implements CommandHandler<AddJobCommand, Job> {
  constructor(private readonly store: JobStorePort) {}

  async handle(command: AddJobCommand): Promise<Job> {
    const trigger = resolveTrigger(command);
    const now = new Date();
    const nextRunAt = trigger.kind === "schedule"
      ? computeNextRun(trigger.schedule, null, now)
      : null;
    const job: Job = {
      id: randomUUID(),
      name: command.name,
      trigger,
      mode: command.mode,
      enabled: true,
      repeat: { times: command.repeatTimes ?? null, completed: 0 },
      nextRunAt,
      lastRunAt: null,
      lastStatus: null,
      lastError: null,
      createdAt: now.toISOString(),
      ...(command.onComplete ? { onComplete: command.onComplete } : {}),
    };
    return this.store.create(job);
  }
}

function resolveTrigger(command: AddJobCommand): JobTrigger {
  if (command.trigger) {
    if (command.trigger.kind === "event") {
      if (!command.trigger.pattern) {
        throw new ApplicationError("JOB_INVALID_TRIGGER", "event trigger requires a non-empty pattern");
      }
      return command.trigger;
    }
    return command.trigger;
  }
  if (command.schedule !== undefined) {
    try {
      const schedule = parseSchedule(command.schedule);
      return { kind: "schedule", schedule };
    } catch (error) {
      if (error instanceof ScheduleParseError) {
        throw new ApplicationError("JOB_INVALID_SCHEDULE", error.message);
      }
      throw error;
    }
  }
  throw new ApplicationError("JOB_INVALID_TRIGGER", "either `trigger` or `schedule` is required");
}
