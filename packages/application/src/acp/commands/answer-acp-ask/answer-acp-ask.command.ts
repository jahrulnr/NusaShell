import type { Command } from "../../../messaging/command.js";

export interface AnswerAcpAskCommand extends Command {
  readonly kind: "answer-acp-ask";
  readonly traceId: string;
  readonly conversationId: string;
  readonly requestId: string;
  readonly optionIds?: readonly string[];
  readonly text?: string;
}
