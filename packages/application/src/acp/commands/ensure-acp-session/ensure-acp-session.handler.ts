import type { CommandHandler } from "../../../messaging/command-handler.js";
import { AcpSessionService } from "../../services/acp-session-service.js";
import type { AcpProviderDescriptor } from "../../ports/acp-client.port.js";
import type { EnsureAcpSessionCommand, EnsureAcpSessionResult } from "./ensure-acp-session.command.js";

export class EnsureAcpSessionHandler implements CommandHandler<EnsureAcpSessionCommand, EnsureAcpSessionResult> {
  constructor(private readonly sessionService: AcpSessionService) {}

  async handle(command: EnsureAcpSessionCommand): Promise<EnsureAcpSessionResult> {
    const provider: AcpProviderDescriptor = {
      providerId: command.provider.providerId,
      command: command.provider.command,
      args: command.provider.args,
      ...(command.provider.authMethodId ? { authMethodId: command.provider.authMethodId } : {}),
      ...(command.provider.preferredConfig ? { preferredConfig: command.provider.preferredConfig } : {}),
    };
    return this.sessionService.ensureSession(command.conversationId, command.workspace, provider);
  }
}
