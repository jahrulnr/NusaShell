import type { Command } from "../../../messaging/command.js";
import type { JobMode } from "../../job-model.js";

export interface AddJobCommand extends Command {
  readonly kind: "add-job";
  readonly name: string;
  readonly schedule: string;
  readonly mode: JobMode;
  readonly repeatTimes?: number;
}
