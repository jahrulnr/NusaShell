import type { AgentMessage } from "../ports/agent-provider.port.js";

/**
 * Codex-aligned compaction memento builders.
 *
 * Source of truth: `codex-rs/core/src/compact.rs` +
 * `codex-rs/prompts/templates/compact/summary_prefix.md`.
 *
 * Invariants mirrored from Codex:
 * 1. The replacement history is **retained real user messages + one summary
 *    user message** (`SUMMARY_PREFIX` + body). Tools/assistant steps are not
 *    the durable keep-set; the summarizer reads full history only during the
 *    compact turn.
 * 2. `collectUserMessages` skips prior summary-shaped messages so we do not
 *    stack summaries.
 * 3. `buildCompactedHistory` reverse-fills user messages up to
 *    `COMPACT_USER_MESSAGE_MAX_TOKENS` (~20k), truncating the boundary
 *    message if needed.
 * 4. Empty summary body becomes `"(no summary available)"` so the next model
 *    still sees structure.
 */

/**
 * Verbatim prefix from Codex `summary_prefix.md`. Stored on the summary
 * user message so `isSummaryMessage` can detect prior compactions and skip
 * them when collecting retained user messages.
 */
export const SUMMARY_PREFIX =
  "Another language model started to solve this problem and produced a summary of its thinking process. You also have access to the state of the tools that were used by that language model. Use this to build on the work that has already been done and avoid duplicating work. Here is the summary produced by the other language model, use the information in this summary to assist with your own analysis:";

/** Legacy NusaShell prefix — accepted by `isSummaryMessage` for one release. */
const LEGACY_SUMMARY_PREFIX = "Conversation summary:";

/** Codex-aligned user pack budget (tokens, chars/4 approximation). */
export const COMPACT_USER_MESSAGE_MAX_TOKENS = 20_000;

/** Minimum body length for a provider summary to pass the quality gate. */
export const MIN_SUMMARY_CHARS = 80;

/**
 * Detect a summary-shaped user or system message.
 *
 * Accepts both the Codex `SUMMARY_PREFIX` and the legacy
 * `Conversation summary:` system marker so existing checkpoints migrate.
 */
export function isSummaryMessage(content: string): boolean {
  if (content.startsWith(SUMMARY_PREFIX)) return true;
  if (content.startsWith(LEGACY_SUMMARY_PREFIX)) return true;
  return false;
}

/**
 * Extract a plain-text user message body from an `AgentMessage`, handling
 * both `string` and `AgentContentPart[]` content shapes. Returns `undefined`
 * for non-user messages or image/file-only user messages.
 */
export function userMessageText(message: AgentMessage): string | undefined {
  if (message.role !== "user") return undefined;
  if (typeof message.content === "string") return message.content;
  const textParts = message.content
    .filter((part) => part.type === "text")
    .map((part) => part.text);
  return textParts.length > 0 ? textParts.join("\n") : undefined;
}

/**
 * Collect durable user message texts from a history, skipping prior summary
 * markers. Mirrors Codex `collect_user_messages`.
 */
export function collectUserMessages(messages: readonly AgentMessage[]): string[] {
  const collected: string[] = [];
  for (const message of messages) {
    if (message.role !== "user") continue;
    const text = userMessageText(message);
    if (text === undefined) continue;
    if (isSummaryMessage(text)) continue;
    collected.push(text);
  }
  return collected;
}

/**
 * Approximate token count using the same chars/4 heuristic as
 * `estimateMessageTokens` so budgets stay consistent.
 */
export function approxTokenCount(text: string): number {
  return Math.ceil(text.length / 4);
}

/**
 * Build the Codex-aligned replacement history: packed user messages (newest
 * first, up to `maxTokens`) followed by one summary user message.
 *
 * Mirrors `build_compacted_history_with_limit`. The boundary user message is
 * truncated when its tokens exceed the remaining budget. User messages older
 * than the budget are dropped.
 */
export function buildCompactedHistory(
  userMessages: readonly string[],
  summaryText: string,
  maxTokens: number = COMPACT_USER_MESSAGE_MAX_TOKENS,
): AgentMessage[] {
  const selected: string[] = [];
  if (maxTokens > 0) {
    let remaining = maxTokens;
    // Iterate newest-first (Codex `user_messages.iter().rev()`), push in
    // newest-first order, then reverse back to chronological for output.
    for (let i = userMessages.length - 1; i >= 0; i -= 1) {
      const message = userMessages[i];
      if (!message) continue;
      if (remaining <= 0) break;
      const tokens = approxTokenCount(message);
      if (tokens <= remaining) {
        selected.push(message);
        remaining -= tokens;
      } else {
        // Truncate the boundary message to the remaining char budget.
        const remainingChars = remaining * 4;
        selected.push(truncateText(message, remainingChars));
        break;
      }
    }
    selected.reverse();
  }

  const history: AgentMessage[] = selected.map((text) => ({
    role: "user" as const,
    content: text,
  }));

  const body = summaryText.trim().length > 0 ? summaryText : "(no summary available)";
  history.push({ role: "user", content: body });
  return history;
}

/**
 * Split live messages into leading system injects + rest.
 *
 * Mid-turn compact must preserve the leading stretch of `role:"system"`
 * messages (re-applied by `injectPrompts` at turn boundaries) and only
 * recompact the rest. Mirrors Codex `insert_initial_context_before_last_real_user_or_summary`
 * spirit, simplified for NusaShell's inject model.
 */
export function splitLeadingSystemInjects(messages: readonly AgentMessage[]): {
  leadingSystem: AgentMessage[];
  rest: AgentMessage[];
} {
  const leadingSystem: AgentMessage[] = [];
  let i = 0;
  for (; i < messages.length; i += 1) {
    const message = messages[i];
    if (message && message.role === "system") {
      leadingSystem.push(message);
    } else {
      break;
    }
  }
  return { leadingSystem, rest: messages.slice(i) };
}

function truncateText(text: string, maxChars: number): string {
  if (text.length <= maxChars) return text;
  return `${text.slice(0, Math.max(0, maxChars))}…`;
}
