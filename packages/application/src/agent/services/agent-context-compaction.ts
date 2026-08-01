import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { AgentProvider, AgentMessage, AgentCompactionCheckpoint, AgentContextOptions, RunAgentTurnInput } from "./agent-turn-types.js";
import { clampText, estimateMessageTokens, formatMessagesForSummary, positiveInteger } from "./agent-turn-utils.js";

/**
 * Context compaction module — summarizes old conversation messages when the
 * estimated token count exceeds the provider's context window budget.
 *
 * Uses the provider for summarization when possible, falls back to an
 * extractive (truncated excerpt) checkpoint if the provider call fails.
 */
export class ContextCompactor {
  constructor(
    private readonly provider: AgentProvider,
    private readonly context: AgentContextOptions | undefined,
    private readonly compactPrompt: string | undefined,
    private readonly logger?: LoggerPort,
  ) {}

  async compact(
    input: RunAgentTurnInput,
    traceId: string,
  ): Promise<{ messages: readonly AgentMessage[]; checkpoint?: AgentCompactionCheckpoint }> {
    const options = this.context;
    if (!options?.compactionEnabled) return { messages: input.messages };
    const estimatedInputTokens = estimateMessageTokens(input.messages);
    const modelWindow = positiveInteger(input.modelCapabilities?.contextWindow)
      ? input.modelCapabilities.contextWindow
      : options.maxInputTokens;
    const maxInputTokens = Math.min(options.maxInputTokens, modelWindow);
    const wantedReserve = Math.max(options.reserveTokens, input.modelCapabilities?.maxOutput ?? 0);
    const reserveTokens = Math.min(Math.max(0, maxInputTokens - 1000), wantedReserve);
    const threshold = Math.max(1000, maxInputTokens - reserveTokens);
    if (estimatedInputTokens <= threshold) return { messages: input.messages };

    const userIndexes = input.messages.flatMap((message, index) => message.role === "user" ? [index] : []);
    const recentTurns = Math.max(1, options.recentTurns);
    if (userIndexes.length <= recentTurns) return { messages: input.messages };
    const keepFrom = userIndexes[userIndexes.length - recentTurns] ?? 0;
    const oldMessages = input.messages.slice(0, keepFrom);
    const recentMessages = input.messages.slice(keepFrom);
    if (oldMessages.length === 0) return { messages: input.messages };

    this.logger?.info("Agent context compaction triggered traceId=%s estimatedTokens=%d threshold=%d oldMessages=%d", traceId, estimatedInputTokens, threshold, oldMessages.length);

    const excerpt = clampText(formatMessagesForSummary(oldMessages), options.summaryMaxChars);
    let summary = excerpt;
    let via: AgentCompactionCheckpoint["via"] = "extractive";
    try {
      const response = await this.provider.complete({
        traceId,
        round: 0,
        messages: [
          {
            role: "system",
            content: this.compactPrompt ?? "Create a concise context checkpoint for another AI. Preserve goals, decisions, constraints, important tool results, and unfinished work. Reply with the checkpoint only.",
          },
          { role: "user", content: excerpt },
        ],
        tools: [],
        ...(input.model ? { model: input.model } : {}),
        ...(input.effort ? { effort: input.effort } : {}),
        ...(input.modelCapabilities ? { modelCapabilities: input.modelCapabilities } : {}),
        ...(input.signal ? { signal: input.signal } : {}),
      });
      if (response.text?.trim()) {
        summary = clampText(response.text, options.summaryMaxChars);
        via = "provider";
      }
    } catch {
      this.logger?.warn("Agent context compaction used extractive fallback traceId=%s", traceId);
    }

    const checkpoint: AgentCompactionCheckpoint = {
      summary,
      compactedMessageCount: oldMessages.length,
      estimatedInputTokens,
      via,
    };
    return {
      messages: [{ role: "system", content: `Conversation summary:\n${summary}` }, ...recentMessages],
      checkpoint,
    };
  }
}
