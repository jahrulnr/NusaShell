import type { Command } from "../../../messaging/command.js";

export interface CancelAcpTurnCommand extends Command {
  readonly kind: "cancel-acp-turn";
  readonly traceId: string;
  readonly conversationId: string;
}
