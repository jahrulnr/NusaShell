import type { Command } from "../../../messaging/command.js";

export interface CancelJobCommand extends Command {
  readonly kind: "cancel-job";
  readonly id: string;
}

export interface CancelJobResult {
  readonly ok: boolean;
  readonly error?: string;
}
