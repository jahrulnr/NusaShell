import type { CommandHandler } from "../../../messaging/command.js";
import type { ConfigureAiRuntimeCommand } from "./configure-ai-runtime.command.js";
import type { AiConfigurationPort } from "../../ports/ai-configuration.port.js";

export class ConfigureAiRuntimeHandler implements CommandHandler<ConfigureAiRuntimeCommand> {
  readonly kind = "configure-ai-runtime" as const;

  constructor(private readonly port: AiConfigurationPort) {}

  handle(command: ConfigureAiRuntimeCommand): void {
    this.port.configureAiRuntime({
      strategy: command.strategy,
      totalAttemptBudget: command.totalAttemptBudget,
      stream: command.stream,
      vision: command.vision,
      userPrompt: command.userPrompt,
      ...(command.maxToolRounds !== undefined ? { maxToolRounds: command.maxToolRounds } : {}),
      ...(command.maxRepeatedToolCalls !== undefined ? { maxRepeatedToolCalls: command.maxRepeatedToolCalls } : {}),
      ...(command.softRecoverAttempts !== undefined ? { softRecoverAttempts: command.softRecoverAttempts } : {}),
      ...(command.maxConcurrentToolCalls !== undefined ? { maxConcurrentToolCalls: command.maxConcurrentToolCalls } : {}),
      ...(command.compactionEnabled !== undefined ? { compactionEnabled: command.compactionEnabled } : {}),
      ...(command.maxInputTokens !== undefined ? { maxInputTokens: command.maxInputTokens } : {}),
      ...(command.reserveTokens !== undefined ? { reserveTokens: command.reserveTokens } : {}),
      ...(command.recentTurns !== undefined ? { recentTurns: command.recentTurns } : {}),
      ...(command.summaryMaxChars !== undefined ? { summaryMaxChars: command.summaryMaxChars } : {}),
    });
  }
}
