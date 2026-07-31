import type { CommandHandler } from "../../../messaging/command-handler.js";
import { AcpSessionService } from "../../services/acp-session-service.js";
import type { SetAcpConfigOptionCommand, SetAcpConfigOptionResult } from "./set-acp-config-option.command.js";

export class SetAcpConfigOptionHandler implements CommandHandler<SetAcpConfigOptionCommand, SetAcpConfigOptionResult> {
  constructor(private readonly sessionService: AcpSessionService) {}

  async handle(command: SetAcpConfigOptionCommand): Promise<SetAcpConfigOptionResult> {
    return this.sessionService.setConfigOption(command.conversationId, command.configId, command.value);
  }
}
