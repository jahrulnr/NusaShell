import type { Command } from "../../../messaging/command.js";
import type { JobMode } from "../../job-model.js";

export interface UpdateJobCommand extends Command {
  readonly kind: "update-job";
  readonly id: string;
  readonly name?: string;
  readonly schedule?: string;
  readonly mode?: JobMode;
  readonly repeatTimes?: number | null;
  readonly enabled?: boolean;
}
