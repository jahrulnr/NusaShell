import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { AgentProvider, AgentMessage, AgentCompactionCheckpoint, AgentContextOptions, RunAgentTurnInput } from "./agent-turn-types.js";
import {
  clampText,
  clampToolResultContent,
  estimateMessageTokens,
  formatMessagesForSummary,
  resolveContextThreshold,
  tokenLimitReached,
} from "./agent-turn-utils.js";
import {
  SUMMARY_PREFIX,
  MIN_SUMMARY_CHARS,
  isSummaryMessage,
  collectUserMessages,
  buildCompactedHistory,
  splitLeadingSystemInjects,
} from "./compact-history.js";

/**
 * Context compaction module — Codex-aligned memento replacement.
 *
 * When the estimated token count exceeds the provider's context window budget,
 * the compactor:
 *  1. Calls the provider with the full live history + a compact instruction
 *     user line (`tools: []`, no mid-loop tools) to produce a handoff summary.
 *  2. Takes the provider response text as the summary body; applies a quality
 *     gate (≥ `MIN_SUMMARY_CHARS`); falls back to an extractive excerpt if the
 *     body is empty or too short.
 *  3. Builds the replacement history as **retained real user messages + one
 *     summary user message** (`SUMMARY_PREFIX` + body), mirroring Codex
 *     `build_compacted_history_with_limit`. Tools/assistant steps are not the
 *     durable keep-set; the summarizer reads full history only during the
 *     compact turn.
 *  4. Preserves leading system injects (re-applied by `injectPrompts` at turn
 *     boundaries) at the head of the replacement.
 *  5. If still over budget after packing, drops the oldest retained user
 *     message iteratively (Codex compact-retry spirit).
 *
 * Mid-turn ephemeral `shrink()` stays unchanged: it clamps tool result
 * contents in the live messages array so the next `provider.complete` payload
 * stays under budget. It does NOT produce a durable checkpoint.
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

    this.logger?.info(
      "Agent context compaction triggered traceId=%s estimatedTokens=%d window=%d soft=%d maxInput=%d modelWindow=%s messages=%d",
      traceId,
      estimatedInputTokens,
      threshold.window,
      threshold.soft,
      options.maxInputTokens,
      input.modelCapabilities?.contextWindow ?? "heuristic",
      input.messages.length,
    );

    // 1. Summarize by replaying real history + compact instruction as the
    //    last user message (Codex style: instruction is user text, not only
    //    system). Tools disabled so the model replies with the summary only.
    //    On a resume path the live history may lack injected system prompts;
    //    `input.systemContext` restores them so the summarizer sees the same
    //    session context (Live MCP, skills, memory, todo) as a normal turn.
    const compactInstruction = this.compactPrompt
      ?? "Create a concise context checkpoint for another AI. Preserve goals, decisions, constraints, important tool results, and unfinished work. Reply with the checkpoint only.";
    const systemContext = input.systemContext ?? [];
    const summarizerMessages: AgentMessage[] = [
      ...systemContext,
      ...input.messages,
      { role: "user", content: compactInstruction },
    ];
    let body = "";
    let via: AgentCompactionCheckpoint["via"] = "extractive";
    try {
      const response = await this.provider.complete({
        traceId,
        round: 0,
        messages: summarizerMessages,
        tools: [],
        ...(input.model ? { model: input.model } : {}),
        ...(input.effort ? { effort: input.effort } : {}),
        ...(input.modelCapabilities ? { modelCapabilities: input.modelCapabilities } : {}),
        ...(input.signal ? { signal: input.signal } : {}),
      });
      const text = response.text?.trim() ?? "";
      if (text.length >= MIN_SUMMARY_CHARS) {
        body = clampText(text, options.summaryMaxChars);
        via = "provider";
      }
    } catch {
      this.logger?.warn("Agent context compaction provider call failed; using extractive fallback traceId=%s", traceId);
    }

    // 2. Quality gate: if the provider body is empty or too short, use an
    //    extractive excerpt from the full transcript so the next model still
    //    gets evidence (files/tools/decisions), never a solitary one-line
    //    ghost. Never store empty.
    if (body.length < MIN_SUMMARY_CHARS) {
      const excerpt = clampText(formatMessagesForSummary(input.messages), options.summaryMaxChars);
      body = body.trim().length > 0
        ? `${body.trim()}\n\n${excerpt}`
        : excerpt;
      via = "extractive";
    }

    const summaryText = `${SUMMARY_PREFIX}\n${body}`;

    // 3. Collect durable user messages (skips prior summary markers) and pack
    //    newest-first up to the Codex user budget.
    const retainedUserMessages = collectUserMessages(input.messages);

    // 4. Preserve leading system injects; recompact the rest. When a resume
    //    turn supplied `systemContext`, use it as the replacement head so the
    //    compacted history stays session-aware even though the live messages
    //    skipped re-injection.
    const { leadingSystem: leadingFromLive, rest } = splitLeadingSystemInjects(input.messages);
    const leadingSystem = systemContext.length > 0 ? [...systemContext] : leadingFromLive;
    const compactedFromRest = buildCompactedHistory(
      collectUserMessages(rest),
      summaryText,
    );
    let compactedMessages: AgentMessage[] = [...leadingSystem, ...compactedFromRest];

    // 5. If still over after packing, drop oldest retained user iteratively
    //    (Codex compact-retry spirit). Then re-run shrink on any tool remnants.
    let stillOver = tokenLimitReached(estimateMessageTokens(compactedMessages), threshold);
    let droppedCount = 0;
    while (stillOver && compactedMessages.length > leadingSystem.length + 1) {
      // Drop the first user message after leadingSystem (oldest retained).
      const firstUserAfterInjects = leadingSystem.length;
      const candidate = compactedMessages[firstUserAfterInjects];
      if (candidate && candidate.role === "user" && !isSummaryMessage(String(candidate.content))) {
        compactedMessages.splice(firstUserAfterInjects, 1);
        droppedCount += 1;
        stillOver = tokenLimitReached(estimateMessageTokens(compactedMessages), threshold);
      } else {
        break;
      }
    }
    if (droppedCount > 0) {
      this.logger?.info(
        "Agent context compaction dropped %d oldest retained user messages to fit budget traceId=%s",
        droppedCount,
        traceId,
      );
    }
    if (stillOver) {
      shrinkToolContents(compactedMessages, threshold, this.logger);
    }

    // 6. Checkpoint: `compactedMessageCount` is the absolute store offset
    //    (mapped at seal time on the desktop side). The application layer
    //    reports the count of input messages covered by this compact.
    const checkpoint: AgentCompactionCheckpoint = {
      summary: summaryText,
      compactedMessageCount: input.messages.length,
      estimatedInputTokens,
      via,
      retainedUserMessages: retainedUserMessages,
    };
    return { messages: compactedMessages, checkpoint };
  }

  /**
   * Whether the live message array is at or over the soft context threshold.
   * Used by the turn runner to decide between no-op, tool shrink, or mid-turn
   * memento compact (Codex post-tool roll-over).
   */
  isOverBudget(
    messages: readonly AgentMessage[],
    modelCapabilities: RunAgentTurnInput["modelCapabilities"],
    modelId?: string,
  ): boolean {
    const options = this.context;
    if (!options?.compactionEnabled) return false;
    const threshold = resolveContextThreshold(options, modelCapabilities, modelId);
    return tokenLimitReached(estimateMessageTokens(messages), threshold);
  }

  /**
   * Mid-turn ephemeral shrink: clamp tool result contents in the live messages
   * array so the next `provider.complete` payload stays under budget. Does NOT
   * call the provider for summarization and does NOT produce a durable
   * checkpoint.
   *
   * Prefer full memento `compact()` after tool batches settle when still over
   * budget (Codex MidTurn roll-over). Shrink remains a lighter fallback when
   * the history is only slightly over or memento already dropped tool pairs
   * and residual text still overflows.
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
    const clamped = clampToolResultContent(msg.content, maxKeep, msg.name);
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
      messages[idx] = { ...msg, content: clampToolResultContent(msg.content, perToolBudget, msg.name) };
    }
  }

  // Third pass: replace oldest results with short stubs when a large tool-round
  // count still cannot fit under soft (100×200-char floors still overflow 9k).
  let finalEstimate = estimateMessageTokens(messages);
  if (finalEstimate > threshold.soft) {
    for (const idx of toolIndexes) {
      if (estimateMessageTokens(messages) <= threshold.soft) break;
      const msg = messages[idx];
      if (!msg || msg.role !== "tool" || typeof msg.content !== "string") continue;
      if (msg.content.length <= 80) continue;
      messages[idx] = {
        ...msg,
        content: `[truncated tool result: ${msg.name}]`,
      };
    }
    finalEstimate = estimateMessageTokens(messages);
    if (finalEstimate > threshold.soft) {
      logger?.warn("Agent context still over budget after shrink estimatedTokens=%d threshold=%d toolMessages=%d", finalEstimate, threshold.soft, toolIndexes.length);
    }
  }
}
