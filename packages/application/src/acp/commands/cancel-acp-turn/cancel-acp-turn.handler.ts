import type { CommandHandler } from "../../../messaging/command-handler.js";
import { AcpSessionService } from "../../services/acp-session-service.js";
import type { CancelAcpTurnCommand } from "./cancel-acp-turn.command.js";

export class CancelAcpTurnHandler implements CommandHandler<CancelAcpTurnCommand, void> {
  constructor(private readonly sessionService: AcpSessionService) {}

  async handle(command: CancelAcpTurnCommand): Promise<void> {
    await this.sessionService.cancelTurn(command.traceId, command.conversationId);
  }
}
