import type { Command } from "../../../messaging/command.js";

export interface KillToolJobCommand extends Command {
  readonly kind: "kill-tool-job";
  readonly handleId: string;
}
