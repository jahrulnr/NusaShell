import type { Command } from "../../../messaging/command.js";

export interface RunJobNowCommand extends Command {
  readonly kind: "run-job-now";
  readonly id: string;
}
