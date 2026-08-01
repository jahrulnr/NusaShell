import type { Command } from "../../../messaging/command.js";

export interface ConfigureAiRuntimeCommand extends Command {
  readonly kind: "configure-ai-runtime";
  readonly strategy: "failover" | "round-robin" | "switch";
  readonly totalAttemptBudget: number;
  readonly stream: boolean;
  readonly vision: "auto" | "on" | "off";
  readonly userPrompt: string;
  readonly maxToolRounds?: number;
  readonly maxRepeatedToolCalls?: number;
  readonly softRecoverAttempts?: number;
  readonly maxConcurrentToolCalls?: number;
  readonly compactionEnabled?: boolean;
  readonly maxInputTokens?: number;
  readonly reserveTokens?: number;
  readonly recentTurns?: number;
  readonly summaryMaxChars?: number;
}
