import type { Command } from "../../../messaging/command.js";
import type { AskAnswerVia } from "../../services/ask-question-service.js";

export interface AnswerAskQuestionCommand extends Command {
  readonly kind: "answer-ask-question";
  readonly traceId: string;
  readonly callId: string;
  readonly via: AskAnswerVia;
  readonly optionIds?: readonly string[];
  readonly text?: string;
}
