import type { Command } from "../../../messaging/command.js";
import type { JobMode, JobTrigger, OnCompleteEmit } from "../../job-model.js";

export interface AddJobCommand extends Command {
  readonly kind: "add-job";
  readonly name: string;
  /** Schedule string (e.g. "every 30m") for schedule triggers. */
  readonly schedule?: string;
  /** Full trigger object (event or schedule). Takes precedence over `schedule`. */
  readonly trigger?: JobTrigger;
  readonly mode: JobMode;
  readonly repeatTimes?: number;
  /** Phase D: emit an automation event when this job completes successfully. */
  readonly onComplete?: OnCompleteEmit;
}
