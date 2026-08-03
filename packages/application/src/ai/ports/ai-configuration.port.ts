/**
 * Port for configuring AI providers at runtime.
 * Implemented by the composition root (container) and used by command handlers
 * so AI configuration goes through the command bus instead of direct container calls.
 */
export interface AiConfigurationPort {
  configureAi(settings: {
    providerId: string;
    api?: "chat" | "responses" | "messages";
    model?: string;
    baseUrl?: string;
    apiKey?: string;
    timeoutMs?: number;
    maxAttempts?: number;
    omitToolChoice?: boolean;
  }): void;
  configureAiRuntime(settings: {
    strategy: "failover" | "round-robin" | "switch";
    totalAttemptBudget: number;
    stream: boolean;
    vision: "auto" | "on" | "off";
    userPrompt: string;
    maxToolRounds?: number;
    maxRepeatedToolCalls?: number;
    softRecoverAttempts?: number;
    maxConcurrentToolCalls?: number;
    compactionEnabled?: boolean;
    maxInputTokens?: number;
    reserveTokens?: number;
    recentTurns?: number;
    summaryMaxChars?: number;
  }): void;
  removeAi(providerId: string): void;
}
