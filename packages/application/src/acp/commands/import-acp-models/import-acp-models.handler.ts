import type { CommandHandler } from "../../../messaging/command-handler.js";
import { AcpSessionService } from "../../services/acp-session-service.js";
import type { AcpProviderDescriptor } from "../../ports/acp-client.port.js";
import type { ImportAcpModelsCommand, ImportAcpModelsResult } from "./import-acp-models.command.js";

/**
 * Probe a fresh ACP session for a provider and return its live config options
 * (models, modes, etc.) so the desktop store can persist a model list and
 * config-option snapshot. Does NOT persist anything in the domain layer —
 * persistence is the caller's responsibility (mirrors `probe-acp-provider`).
 */
export class ImportAcpModelsHandler implements CommandHandler<ImportAcpModelsCommand, ImportAcpModelsResult> {
  constructor(private readonly sessionService: AcpSessionService) {}

  async handle(command: ImportAcpModelsCommand): Promise<ImportAcpModelsResult> {
    const provider: AcpProviderDescriptor = {
      providerId: command.provider.providerId,
      command: command.provider.command,
      args: command.provider.args,
      ...(command.provider.authMethodId ? { authMethodId: command.provider.authMethodId } : {}),
      ...(command.provider.env ? { env: command.provider.env } : {}),
      ...(command.provider.preferredConfig ? { preferredConfig: command.provider.preferredConfig } : {}),
    };
    try {
      const configOptions = await this.sessionService.importModels(provider);
      return { ok: true, configOptions };
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : String(error) };
    }
  }
}
