import type { CommandHandler } from "../../../messaging/command-handler.js";
import { AcpSessionService } from "../../services/acp-session-service.js";
import type { RunAcpTurnCommand } from "./run-acp-turn.command.js";

export class RunAcpTurnHandler implements CommandHandler<RunAcpTurnCommand, void> {
  constructor(private readonly sessionService: AcpSessionService) {}

  async handle(command: RunAcpTurnCommand): Promise<void> {
    await this.sessionService.startTurn(
      command.traceId,
      command.conversationId,
      command.workspace,
      command.provider,
      command.prompt,
    );
  }
}
