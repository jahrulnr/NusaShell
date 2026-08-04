import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { AgentProvider, AgentMessage, AgentCompactionCheckpoint, AgentContextOptions, RunAgentTurnInput } from "./agent-turn-types.js";
import {
  clampText,
  estimateMessageTokens,
  formatMessagesForSummary,
  resolveContextThreshold,
  tokenLimitReached,
} from "./agent-turn-utils.js";

/**
 * Context compaction module — summarizes old conversation messages when the
 * estimated token count exceeds the provider's context window budget.
 *
 * Uses the provider for summarization when possible, falls back to an
 * extractive (truncated excerpt) checkpoint if the provider call fails.
 *
 * Token-first design (Codex-aligned):
 * - Threshold is 90% of the model window with a 10k free floor.
 * - `recentTurns` is a soft keep preference when carving what to summarize,
 *   never a hard veto that blocks compaction.
 * - When there are too few user turns to carve a prefix, in-list tool shrink
 *   folds old tool results so the live messages array stays under budget.
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
    const threshold = resolveContextThreshold(options, input.modelCapabilities, input.model);
    const estimatedInputTokens = estimateMessageTokens(input.messages);
    if (!tokenLimitReached(estimatedInputTokens, threshold)) return { messages: input.messages };

    // Classic compact: summarize messages before last N user turns.
    // `recentTurns` is a soft keep preference — when there are fewer user
    // turns than recentTurns, still carve a prefix (keep only the last user
    // turn) so fat conversations with few turns are not left unshrunk.
    const userIndexes = input.messages.flatMap((message, index) => message.role === "user" ? [index] : []);
    const recentTurns = Math.max(1, options.recentTurns);
    let keepFrom: number;
    if (userIndexes.length > recentTurns) {
      keepFrom = userIndexes[userIndexes.length - recentTurns] ?? 0;
    } else if (userIndexes.length > 1) {
      // Anti-veto: fewer user turns than recentTurns, but still carve
      // everything before the last user turn so we have something to summarize.
      keepFrom = userIndexes[userIndexes.length - 1] ?? 0;
    } else {
      keepFrom = 0;
    }
    const oldMessages = input.messages.slice(0, keepFrom);
    const recentMessages = input.messages.slice(keepFrom);

    if (oldMessages.length > 0) {
      this.logger?.info("Agent context compaction triggered traceId=%s estimatedTokens=%d threshold=%d oldMessages=%d", traceId, estimatedInputTokens, threshold.soft, oldMessages.length);

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
      const compactedMessages = [{ role: "system", content: `Conversation summary:\n${summary}` } as AgentMessage, ...recentMessages];

      // If still over after classic compact, do in-list tool shrink on the result.
      const stillOver = tokenLimitReached(estimateMessageTokens(compactedMessages), threshold);
      if (stillOver) {
        shrinkToolContents(compactedMessages, threshold, this.logger);
      }
      return { messages: compactedMessages, checkpoint };
    }

    // No old messages to summarize — do in-list tool shrink (ephemeral, no checkpoint).
    this.logger?.info("Agent context in-list shrink triggered traceId=%s estimatedTokens=%d threshold=%d (few user turns)", traceId, estimatedInputTokens, threshold.soft);
    const shrunk = [...input.messages];
    shrinkToolContents(shrunk, threshold, this.logger);
    return { messages: shrunk };
  }

  /**
   * Mid-turn ephemeral shrink: clamp tool result contents in the live messages
   * array so the next `provider.complete` payload stays under budget. Does NOT
   * call the provider for summarization and does NOT produce a durable
   * checkpoint. This maps to Codex's mid-turn roll-over gate.
   */
  shrink(messages: AgentMessage[], modelCapabilities: RunAgentTurnInput["modelCapabilities"], modelId?: string): void {
    const options = this.context;
    if (!options?.compactionEnabled) return;
    const threshold = resolveContextThreshold(options, modelCapabilities, modelId);
    const estimated = estimateMessageTokens(messages);
    if (!tokenLimitReached(estimated, threshold)) return;
    this.logger?.info("Agent mid-turn shrink triggered estimatedTokens=%d threshold=%d", estimated, threshold.soft);
    shrinkToolContents(messages, threshold, this.logger);
  }
}

/**
 * In-list tool shrink: clamp `role:"tool"` message contents from oldest to
 * newest until the estimated token count drops below the soft threshold.
 * Preserves all messages (protocol validity) — only trims content.
 */
function shrinkToolContents(messages: AgentMessage[], threshold: { soft: number }, logger?: LoggerPort): void {
  const toolIndexes: number[] = [];
  for (let i = 0; i < messages.length; i += 1) {
    const message = messages[i];
    if (message && message.role === "tool") toolIndexes.push(i);
  }
  if (toolIndexes.length === 0) return;

  // Per-tool budget: divide the excess across tool messages, oldest first.
  // Each tool message gets clamped to at most `perToolBudget` chars.
  // Start with a conservative budget and reduce until under threshold.
  const targetChars = threshold.soft * 4;
  let totalChars = 0;
  for (const m of messages) {
    if (m && "content" in m) {
      totalChars += typeof m.content === "string" ? m.content.length : JSON.stringify(m.content).length;
    }
  }
  if (totalChars <= targetChars) return;

  const excess = totalChars - targetChars;
  // Clamp oldest tool messages first, up to removing `excess` chars total.
  let remaining = excess;
  for (const idx of toolIndexes) {
    if (remaining <= 0) break;
    const msg = messages[idx];
    if (!msg || msg.role !== "tool" || typeof msg.content !== "string") continue;
    if (msg.content.length <= 200) continue; // skip tiny results
    const maxKeep = Math.max(200, msg.content.length - remaining);
    const clamped = clampText(msg.content, maxKeep);
    remaining -= (msg.content.length - clamped.length);
    messages[idx] = { ...msg, content: clamped };
  }

  // If still over, do a second pass with a harder per-tool cap.
  const stillOver = estimateMessageTokens(messages) > threshold.soft;
  if (stillOver) {
    const perToolBudget = Math.max(200, Math.floor(targetChars / Math.max(1, toolIndexes.length)));
    for (const idx of toolIndexes) {
      const msg = messages[idx];
      if (!msg || msg.role !== "tool" || typeof msg.content !== "string") continue;
      if (msg.content.length <= perToolBudget) continue;
      messages[idx] = { ...msg, content: clampText(msg.content, perToolBudget) };
    }
    // If STILL over after both passes, log so the stuck-over-budget state is
    // observable — there may be no shrinkable tool content left (all <200 chars
    // or non-string), so the next provider.complete will likely overflow.
    const finalEstimate = estimateMessageTokens(messages);
    if (finalEstimate > threshold.soft) {
      logger?.warn("Agent context still over budget after shrink estimatedTokens=%d threshold=%d toolMessages=%d", finalEstimate, threshold.soft, toolIndexes.length);
    }
  }
}
