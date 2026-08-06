import type { Command } from "../../../messaging/command.js";

export interface CancelPipelineCommand extends Command {
  readonly kind: "cancel-pipeline";
  /** Prefer runId; pipelineId also accepted for convenience. */
  readonly id: string;
}

export interface CancelPipelineResult {
  readonly ok: boolean;
  readonly runId?: string;
  readonly traceId?: string;
  readonly status?: string;
  readonly error?: string;
  readonly errorCode?: string;
}
