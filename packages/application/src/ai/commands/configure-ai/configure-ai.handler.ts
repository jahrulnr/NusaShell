import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { ConfigureAiCommand } from "./configure-ai.command.js";
import type { AiConfigurationPort } from "../../ports/ai-configuration.port.js";

export class ConfigureAiHandler implements CommandHandler<ConfigureAiCommand> {
  readonly kind = "configure-ai" as const;

  constructor(private readonly port: AiConfigurationPort) {}

  async handle(command: ConfigureAiCommand): Promise<void> {
    this.port.configureAi({
      providerId: command.providerId,
      ...(command.api !== undefined ? { api: command.api } : {}),
      ...(command.model !== undefined ? { model: command.model } : {}),
      ...(command.baseUrl !== undefined ? { baseUrl: command.baseUrl } : {}),
      ...(command.apiKey !== undefined ? { apiKey: command.apiKey } : {}),
      ...(command.timeoutMs !== undefined ? { timeoutMs: command.timeoutMs } : {}),
      ...(command.maxAttempts !== undefined ? { maxAttempts: command.maxAttempts } : {}),
    });
  }
}
