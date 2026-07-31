import type { Command } from "../../../messaging/command.js";

export interface AnswerAcpPermissionCommand extends Command {
  readonly kind: "answer-acp-permission";
  readonly traceId: string;
  readonly conversationId: string;
  readonly requestId: string;
  readonly optionId: string;
}
