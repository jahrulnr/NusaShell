import type { Command } from "../../../messaging/command.js";

export interface SetJobEnabledCommand extends Command {
  readonly kind: "set-job-enabled";
  readonly id: string;
  readonly enabled: boolean;
}
