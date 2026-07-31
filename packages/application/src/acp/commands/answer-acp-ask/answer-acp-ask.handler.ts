import type { CommandHandler } from "../../../messaging/command-handler.js";
import { AcpAskBridgeService } from "../../services/acp-ask-bridge-service.js";
import type { AnswerAcpAskCommand } from "./answer-acp-ask.command.js";

export class AnswerAcpAskHandler implements CommandHandler<AnswerAcpAskCommand, void> {
  constructor(private readonly askService: AcpAskBridgeService) {}

  async handle(command: AnswerAcpAskCommand): Promise<void> {
    this.askService.answer(command.conversationId, command.requestId, {
      ...(command.optionIds !== undefined ? { optionIds: command.optionIds } : {}),
      ...(command.text !== undefined ? { text: command.text } : {}),
    });
  }
}
