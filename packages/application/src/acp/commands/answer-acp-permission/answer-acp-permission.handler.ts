import type { CommandHandler } from "../../../messaging/command-handler.js";
import { AcpPermissionService } from "../../services/acp-permission-service.js";
import type { AnswerAcpPermissionCommand } from "./answer-acp-permission.command.js";

export class AnswerAcpPermissionHandler implements CommandHandler<AnswerAcpPermissionCommand, void> {
  constructor(private readonly permissionService: AcpPermissionService) {}

  async handle(command: AnswerAcpPermissionCommand): Promise<void> {
    this.permissionService.answer(command.conversationId, command.requestId, command.optionId);
  }
}
