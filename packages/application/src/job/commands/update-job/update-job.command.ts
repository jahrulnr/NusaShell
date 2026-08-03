import type { Command } from "../../../messaging/command.js";
import type { JobMode, JobTrigger, OnCompleteEmit } from "../../job-model.js";

export interface UpdateJobCommand extends Command {
  readonly kind: "update-job";
  readonly id: string;
  readonly name?: string;
  readonly schedule?: string;
  readonly trigger?: JobTrigger;
  readonly mode?: JobMode;
  readonly repeatTimes?: number | null;
  readonly enabled?: boolean;
  /** Phase D: emit an automation event when this job completes successfully. */
  readonly onComplete?: OnCompleteEmit | null;
}
