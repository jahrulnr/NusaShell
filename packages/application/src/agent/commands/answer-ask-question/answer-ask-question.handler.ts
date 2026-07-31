import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { AskQuestionResult, AskQuestionService } from "../../services/ask-question-service.js";
import type { AnswerAskQuestionCommand } from "./answer-ask-question.command.js";

export class AnswerAskQuestionHandler implements CommandHandler<AnswerAskQuestionCommand, AskQuestionResult> {
  constructor(private readonly asks: AskQuestionService) {}

  async handle(command: AnswerAskQuestionCommand): Promise<AskQuestionResult> {
    return this.asks.answer(command.traceId, command.callId, {
      via: command.via,
      ...(command.optionIds ? { optionIds: command.optionIds } : {}),
      ...(command.text !== undefined ? { text: command.text } : {}),
    });
  }
}
