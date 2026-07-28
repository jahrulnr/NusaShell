import type { Command } from "../../../messaging/command.js";

export interface CancelAgentTurnCommand extends Command {
  readonly kind: "cancel-agent-turn";
  readonly traceId: string;
}
