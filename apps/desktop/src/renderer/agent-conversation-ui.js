import markdownit from "markdown-it";
import DOMPurify from "isomorphic-dompurify";

const assistantMarkdown = markdownit({ html: true, linkify: true, breaks: true });
// Reasoning often mentions paths like plugins.md; linkify treats .md as a TLD
// and paints them as blue links. Keep breaks, skip auto-link.
const reasoningMarkdown = markdownit({ html: true, linkify: false, breaks: true });
// Job output is structured markdown (headings, lists, paragraphs) — no breaks
// so single \n between list items doesn't become <br> and add extra spacing.
const jobOutputMarkdown = markdownit({ html: true, linkify: true, breaks: false });

const ASSISTANT_MARKDOWN_TAGS = [
  "h1", "h2", "h3", "h4", "h5", "h6",
  "p", "br", "hr", "blockquote", "pre", "code",
  "ul", "ol", "li", "dl", "dt", "dd",
  "table", "thead", "tbody", "tr", "th", "td",
  "a", "strong", "em", "del", "s", "mark", "sub", "sup", "u",
  "span", "div", "img",
  "details", "summary",
  "kbd", "abbr", "cite", "var", "samp",
  "b", "i",
];

export function renderAssistantMarkdown(content) {
  return DOMPurify.sanitize(wrapMarkdownTables(assistantMarkdown.render(String(content ?? ""))), {
    ALLOWED_TAGS: ASSISTANT_MARKDOWN_TAGS,
    ALLOWED_ATTR: ["href", "title", "alt", "src", "class", "id", "target", "rel", "colspan", "rowspan"],
  });
}

export function renderJobOutputMarkdown(content) {
  const raw = wrapMarkdownTables(jobOutputMarkdown.render(String(content ?? "")));
  // Strip inter-block whitespace so copy-paste doesn't produce double newlines
  // (browsers ignore this whitespace for rendering, but it creates extra text
  // nodes that add blank lines when copied).
  const compact = raw.replace(/>\s+</g, "><").trim();
  return DOMPurify.sanitize(compact, {
    ALLOWED_TAGS: ASSISTANT_MARKDOWN_TAGS,
    ALLOWED_ATTR: ["href", "title", "alt", "src", "class", "id", "target", "rel", "colspan", "rowspan"],
  });
}

export function renderReasoningMarkdown(content) {
  return DOMPurify.sanitize(wrapMarkdownTables(reasoningMarkdown.render(String(content ?? ""))), {
    ALLOWED_TAGS: ASSISTANT_MARKDOWN_TAGS.filter((tag) => tag !== "img"),
    ALLOWED_ATTR: ["href", "title", "class", "id", "target", "rel", "colspan", "rowspan"],
  });
}

/** Give wide markdown tables a local scroll viewport without scrolling prose. */
function wrapMarkdownTables(markup) {
  return markup.replace(/<table>[\s\S]*?<\/table>/g, (table) => (
    `<div class="agent-markdown-table-scroll">${table}</div>`
  ));
}

export function formatMessageTimestamp(timestamp, locale, timeZone) {
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat(locale, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    ...(timeZone ? { timeZone } : {}),
  }).format(date);
}

export function describeToolActivity(toolCalls) {
  const calls = Array.isArray(toolCalls) ? toolCalls : [];
  const failed = calls.filter((call) => !call.ok).length;
  return {
    label: `${calls.length} tool call${calls.length === 1 ? "" : "s"}`,
    succeeded: calls.length - failed,
    failed,
  };
}

/**
 * Return the durable room-level diagnostics shown beside the conversation.
 * Tool calls in a message are the canonical parent-room count; persisted
 * subagent streams are added because their nested calls are also work done in
 * this room and are not duplicated in the parent message payload.
 */
export function getConversationRoomMetadata(conversation) {
  const messages = Array.isArray(conversation?.messages) ? conversation.messages : [];
  const messageToolCalls = messages.reduce((total, message) => {
    if (Array.isArray(message?.toolCalls)) return total + message.toolCalls.length;
    const stepCalls = Array.isArray(message?.steps)
      ? message.steps.reduce((count, step) => count + (step?.type === "tool_calls" && Array.isArray(step.calls) ? step.calls.length : 0), 0)
      : 0;
    return total + stepCalls;
  }, 0);
  const subagentToolCalls = Array.isArray(conversation?.subagentRuns)
    ? conversation.subagentRuns.reduce((total, run) => total + (Array.isArray(run?.steps)
      ? run.steps.reduce((count, step) => count + (step?.type === "tool_calls" && Array.isArray(step.calls) ? step.calls.length : 0), 0)
      : 0), 0)
    : 0;
  const checkpoint = conversation?.checkpoint;
  const compactionCount = Number.isInteger(checkpoint?.compactionCount) && checkpoint.compactionCount >= 0
    ? checkpoint.compactionCount
    : checkpoint?.summary ? 1 : 0;
  return {
    conversationId: typeof conversation?.id === "string" ? conversation.id : "",
    compactionCount,
    toolCallCount: messageToolCalls + subagentToolCalls,
  };
}

/**
 * Human-readable turn failure from application/transport errors.
 * Backend often puts the real reason in `details.cause` while `message` is
 * a short code phrase — surface both when cause is not already embedded.
 * Never returns the literal string "[object Object]".
 */
export function formatTurnError(error) {
  if (error == null) return "Unknown error";
  if (typeof error === "string") {
    const trimmed = error.trim();
    return !trimmed || trimmed === "[object Object]" ? "Unknown error" : trimmed;
  }
  // Nested transport envelope: `{ ok:false, error:{ code, message } }` or
  // `{ error: { message } }` that slipped past sendRequest unwrap.
  if (typeof error === "object" && error.error != null && typeof error.error === "object") {
    return formatTurnError(error.error);
  }
  let message = typeof error.message === "string" ? error.message.trim() : "";
  if (!message || message === "[object Object]") {
    if (typeof error.code === "string" && error.code.trim()) {
      message = error.code.trim();
    } else {
      try {
        const encoded = JSON.stringify(error);
        if (encoded && encoded !== "{}" && encoded !== "null") {
          return encoded.length > 400 ? `${encoded.slice(0, 397)}…` : encoded;
        }
      } catch {
        // fall through
      }
      return "Unknown error";
    }
  }
  const cause = typeof error?.details?.cause === "string" ? error.details.cause.trim() : "";
  if (!cause || message.includes(cause)) return message;
  return `${message}: ${cause}`;
}

/**
 * Add the model/provider identity to a user-visible turn failure. The model
 * object is the room-bound snapshot used for the turn, so the diagnostic does
 * not follow a later global model-picker change.
 */
export function formatTurnFailure(error, model = {}) {
  const modelName = model?.label || model?.name || model?.id || model?.key || "Unknown model";
  const providerName = model?.providerName || model?.providerId || error?.details?.providerName || error?.details?.providerId || "Unknown provider";
  return `Turn failed [${modelName} · ${providerName}]: ${formatTurnError(error)}`;
}

/**
 * Classify a turn failure for the Retry button + status copy (#45).
 * Pure function — no DOM. Parses the transport error shape (code, details.cause,
 * message) that the backend sends for provider/run failures and returns a small
 * descriptor the renderer can act on.
 *
 * @param {unknown} error - the renderer-side turn error.
 * @returns {{ category: string, message: string, retryable: boolean, label: string }}
 *   - category: "rate_limited" | "auth" | "client_error" | "server_error" |
 *     "superseded" | "cancelled" | "provider" | "unknown"
 *   - message: user-facing copy
 *   - retryable: whether an automatic/manual retry makes sense
 *   - label: button label for the primary action ("Retry" | "Resume" | "Continue" | "")
 */
const HTTP_STATUS_RE = /\bHTTP\s+(\d{3})\b/i;
export function classifyTurnError(error) {
  if (error == null) {
    return { category: "unknown", message: "Unknown error", retryable: false, label: "" };
  }
  const code = typeof error?.code === "string" ? error.code : "";
  const cause = typeof error?.details?.cause === "string" ? error.details.cause : "";
  const rawMessage = typeof error?.message === "string" ? error.message : "";
  const text = `${rawMessage} ${cause}`.trim();
  const explicitStatus = Number(error?.status ?? error?.details?.status ?? 0);

  // Cancelled/interrupted handled by the caller (label already correct).
  if (code === "AGENT_TURN_CANCELLED") {
    return { category: "cancelled", message: "Turn stopped", retryable: false, label: "Continue" };
  }
  if (code === "AGENT_MAX_TOOL_ROUNDS") {
    return { category: "max_rounds", message: "Tool-round limit reached", retryable: true, label: "Resume" };
  }

  // Auth (401/403) — not retryable without fixing credentials.
  if (/\b401\b/.test(text) || /\b403\b/.test(text) || /auth/i.test(text)) {
    return { category: "auth", message: "Authentication failed — check the API key / provider settings", retryable: false, label: "Retry" };
  }
  // Rate limit (429) — back off; retry only after a cooldown.
  if (/\b429\b/.test(text) || /rate\s*limit/i.test(text)) {
    return { category: "rate_limited", message: "Rate limited — wait a moment and try again", retryable: true, label: "Retry" };
  }
  // Superseded — the turn was intentionally replaced by a newer one.
  if (code === "AGENT_TURN_SUPERSEDED" || /supersed/i.test(text)) {
    return { category: "superseded", message: "Turn superseded by a newer turn", retryable: false, label: "" };
  }
  // Server error (5xx) — transient retryable.
  const statusMatch = text.match(HTTP_STATUS_RE);
  const status = explicitStatus || (statusMatch ? Number(statusMatch[1]) : 0);
  if (status >= 500 && status <= 599 || /\b5\d{2}\b/.test(text)) {
    return { category: "server_error", message: "Provider error (5xx) — try again", retryable: true, label: "Retry" };
  }
  // Other 4xx responses are deterministic client/configuration failures. A
  // blind retry only repeats the same request and hides the real action.
  if (status >= 400 && status <= 499 || /\b4\d{2}\b/.test(text)) {
    return { category: "client_error", message: "Provider rejected the request — check the request or provider settings", retryable: false, label: "" };
  }
  if (code === "AGENT_PROVIDER_FAILED") {
    return { category: "provider", message: formatTurnError(error), retryable: true, label: "Retry" };
  }
  return { category: "unknown", message: formatTurnError(error), retryable: false, label: "Retry" };
}

/**
 * Subagent tool/event error payloads may be a string or `{ message }`.
 * Never show `[object Object]` when the backend sent a structured error.
 */
export function formatSubagentError(error) {
  if (!error) return "";
  if (typeof error === "string") return error;
  if (typeof error === "object" && typeof error.message === "string" && error.message) return error.message;
  try {
    return JSON.stringify(error);
  } catch {
    return String(error);
  }
}

const TOOL_ARGS_MAX_CHARS = 8_000;
const TOOL_OUTPUT_MAX_CHARS = 12_000;

export function clampToolText(value, maxChars = TOOL_OUTPUT_MAX_CHARS) {
  const text = String(value ?? "");
  if (text.length <= maxChars) return text;
  // The "\n…" marker must fit inside the budget: AgentConversationStore
  // rejects persisted tool outputs longer than TOOL_OUTPUT_MAX_CHARS, and an
  // over-budget output silently drops the whole assistant message on load.
  return `${text.slice(0, Math.max(0, maxChars - 2))}\n…`;
}

export function formatToolOutput(value, maxChars = TOOL_OUTPUT_MAX_CHARS) {
  if (value === undefined || value === null) return "";
  if (typeof value === "string") return clampToolText(value, maxChars);
  try {
    return clampToolText(JSON.stringify(value, null, 2), maxChars);
  } catch {
    return clampToolText(String(value), maxChars);
  }
}

export function summarizeToolArgs(args) {
  if (!args || typeof args !== "object" || Array.isArray(args)) return "";
  const entries = Object.entries(args);
  if (entries.length === 0) return "";
  if (entries.length === 1) {
    const rendered = formatArgLiteral(entries[0][1]);
    return rendered.length > 42 ? `${rendered.slice(0, 42)}…` : rendered;
  }
  return `${entries.length} args`;
}

export function formatToolTerminalInput(name, args) {
  const tool = String(name || "tool");
  return `${tool}(${formatToolCallArgs(args)})`;
}

export function formatToolCallArgs(args) {
  if (!args || typeof args !== "object" || Array.isArray(args)) return "";
  const entries = Object.entries(args);
  if (entries.length === 0) return "";
  // Single value → docs_search("dokumentasi") rather than docs_search(query=...)
  if (entries.length === 1) return formatArgLiteral(entries[0][1]);
  const named = entries.map(([key, value]) => `${key}=${formatArgLiteral(value)}`);
  const joined = named.join(", ");
  return joined.length <= TOOL_ARGS_MAX_CHARS
    ? joined
    : clampToolText(JSON.stringify(args), TOOL_ARGS_MAX_CHARS);
}

function formatArgLiteral(value) {
  if (typeof value === "string") return JSON.stringify(value);
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (value === null) return "null";
  try {
    return JSON.stringify(value);
  } catch {
    return JSON.stringify(String(value));
  }
}

export function renderToolCodeHtml(content) {
  const text = String(content ?? "");
  const callMatch = text.match(/^([A-Za-z_][\w./:-]*)\(([\s\S]*)\)$/);
  if (callMatch) {
    const [, name, inner] = callMatch;
    return `<span class="tok-cmd">${escapeHtml(name)}</span>(${highlightCallArgs(inner)})`;
  }
  const escaped = escapeHtml(text);
  const lines = escaped.split("\n");
  if (lines.length === 0) return "";
  lines[0] = `<span class="tok-cmd">${lines[0]}</span>`;
  return lines.join("\n")
    .replace(/(&quot;.*?&quot;)(\s*:)/g, '<span class="tok-key">$1</span>$2')
    .replace(/(:\s*)(&quot;.*?&quot;)/g, '$1<span class="tok-str">$2</span>')
    .replace(/(:\s*)(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g, '$1<span class="tok-num">$2</span>')
    .replace(/(:\s*)(true|false|null)\b/g, '$1<span class="tok-lit">$2</span>');
}

function highlightCallArgs(inner) {
  return escapeHtml(inner)
    .replace(/([A-Za-z_][\w]*)(=)/g, '<span class="tok-key">$1</span>$2')
    .replace(/(&quot;.*?&quot;)/g, '<span class="tok-str">$1</span>')
    .replace(/\b(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)\b/g, '<span class="tok-num">$1</span>')
    .replace(/\b(true|false|null)\b/g, '<span class="tok-lit">$1</span>');
}

function escapeHtml(value) {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

export function toConversationToolCall(call, callPosition) {
  const args = call?.args && typeof call.args === "object" && !Array.isArray(call.args)
    ? call.args
    : undefined;
  let safeArgs;
  if (args && Object.keys(args).length > 0) {
    try {
      const encoded = JSON.stringify(args);
      if (encoded.length <= TOOL_ARGS_MAX_CHARS) safeArgs = args;
      else {
        // JSON-escaping inside the {"_truncated":"…"} wrapper makes the final
        // size unpredictable, so shrink iteratively against the real measure.
        let budget = TOOL_ARGS_MAX_CHARS - JSON.stringify({ _truncated: "" }).length;
        for (let attempt = 0; attempt < 3; attempt++) {
          safeArgs = { _truncated: clampToolText(encoded, budget) };
          const overflow = JSON.stringify(safeArgs).length - TOOL_ARGS_MAX_CHARS;
          if (overflow <= 0) break;
          budget -= overflow;
        }
        if (JSON.stringify(safeArgs).length > TOOL_ARGS_MAX_CHARS) safeArgs = undefined;
      }
    } catch {
      safeArgs = undefined;
    }
  }
  // IPC returns the canonical projection under toolResult; keep the UI card
  // byte-for-byte aligned with the provider-facing role:"tool" content.
  const modelOutput = call?.modelOutput ?? call?.toolResult?.modelOutput;
  const output = modelOutput !== undefined
    ? clampToolText(modelOutput, TOOL_OUTPUT_MAX_CHARS)
    : call?.output !== undefined
      ? clampToolText(call.output, TOOL_OUTPUT_MAX_CHARS)
      : call?.error
        ? clampToolText(call.error, TOOL_OUTPUT_MAX_CHARS)
        : call?.result !== undefined
          ? formatToolOutput(call.result)
          : undefined;
  return {
    id: call.id,
    ...(Number.isInteger(call?.callPosition) && call.callPosition > 0
      ? { callPosition: call.callPosition }
      : Number.isInteger(callPosition) && callPosition > 0
        ? { callPosition }
        : {}),
    name: call.name,
    ok: call.ok !== false,
    ...(call.error ? { error: clampToolText(call.error, 4_000) } : {}),
    args: safeArgs ?? {},
    ...(output ? { output } : {}),
    ...(modelOutput ? { modelOutput: clampToolText(modelOutput, TOOL_OUTPUT_MAX_CHARS) } : {}),
    ...(call?.status ? { status: call.status } : {}),
    ...(call?.truncated ? { truncated: call.truncated } : {}),
    ...(call?.structuredContent ? { structuredContent: call.structuredContent } : {}),
  };
}

export function sanitizeAssistantSteps(steps) {
  if (!Array.isArray(steps)) return undefined;
  return steps.map((step, index) => {
    const stepPosition = Number.isInteger(step?.stepPosition) && step.stepPosition > 0
      ? step.stepPosition
      : index + 1;
    if (step?.type === "tool_calls" && Array.isArray(step.calls)) {
      return { type: "tool_calls", stepPosition, calls: step.calls.map((call, callIndex) => toConversationToolCall(call, callIndex + 1)), ...(step.model ? { model: step.model } : {}), ...(step.providerId ? { providerId: step.providerId } : {}) };
    }
    if ((step?.type === "reasoning" || step?.type === "text") && typeof step.content === "string") {
      return { ...step, stepPosition, content: clampToolText(step.content, 1_000_000) };
    }
    return step;
  });
}

export function composerTextareaSize({
  scrollHeight,
  lineHeight,
  paddingTop = 0,
  paddingBottom = 0,
  maxRows = 10,
}) {
  const maxHeight = (lineHeight * maxRows) + paddingTop + paddingBottom;
  return {
    height: Math.min(scrollHeight, maxHeight),
    overflowY: scrollHeight > maxHeight ? "auto" : "hidden",
  };
}

/**
 * Build the provider-visible context without restoring messages already covered
 * by a durable compaction checkpoint.
 *
 * Codex-aligned (memento replacement): when the checkpoint has
 * `retainedUserMessages`, the provider context is:
 *   retained user messages (chronological) + summary user message + residual
 *   store messages (everything after `compactedMessageCount`).
 * The summary is a `role:"user"` message with the `SUMMARY_PREFIX` marker,
 * not a `role:"system"` blurb, so the model treats it as durable context.
 *
 * Legacy fallback: when `retainedUserMessages` is absent (old checkpoints),
 * the pre-Codex shape is used: `system: Conversation summary:\n…` + residual
 * slice. This keeps existing chats working through one release.
 *
 * Assistant messages that carry persisted `toolCalls` are reconstructed into
 * the provider shape the runner expects: one `{role:"assistant", toolCalls}`
 * message followed by one `{role:"tool"}` result per call. Without this the
 * model sees an assistant claim with no tool-use record and no results, so it
 * cannot verify what was actually done and may redo or over-confess work.
 */
export function buildAgentContext(conversation) {
  const checkpoint = conversation?.checkpoint;
  const messages = Array.isArray(conversation?.messages) ? conversation.messages : [];
  if (!checkpoint?.summary) return messages.filter((m) => m.status !== "interrupted").flatMap(toProviderMessages);

  const residual = messages.slice(checkpoint.compactedMessageCount).filter((m) => m.status !== "interrupted").flatMap(toProviderMessages);

  // Codex-aligned memento: retained user messages + summary user message.
  if (Array.isArray(checkpoint.retainedUserMessages) && checkpoint.retainedUserMessages.length >= 0) {
    const retainedUsers = checkpoint.retainedUserMessages.map((text) => ({ role: "user", content: text }));
    return [
      ...retainedUsers,
      { role: "user", content: checkpoint.summary },
      ...residual,
    ];
  }

  // Legacy: system summary + residual slice.
  return [
    { role: "system", content: `Conversation summary:\n${checkpoint.summary}` },
    ...residual,
  ];
}

/**
 * Fixed English steer injected as a one-shot user line when continuing an
 * interrupted pure-text reply. Not persisted as a user row in the store.
 */
export const CONTINUE_STEER = "Your previous reply was interrupted. The incomplete assistant message above is already shown to the user. Continue exactly from where it ends — do not restate or rewrite earlier sections.";

/**
 * True when an interrupted assistant can tool-resume (in-turn tools settled),
 * not merely when `resumeMessages` is a non-empty inject+user snapshot from a
 * pre-tool provider fail.
 */
export function hasToolResumeSnapshot(message) {
  if (!message || typeof message !== "object") return false;
  if (!Array.isArray(message.resumeMessages) || message.resumeMessages.length === 0) return false;
  return message.resumeMessages.some((entry) => {
    if (!entry || typeof entry !== "object") return false;
    if (entry.role === "tool") return true;
    if (entry.role === "assistant" && Array.isArray(entry.toolCalls) && entry.toolCalls.length > 0) return true;
    return false;
  });
}

/**
 * Build provider context for a Continue/Retry on the last interrupted
 * assistant message. Two resume modes:
 *
 * - **tool**: resume graph has settled tools (`hasToolResumeSnapshot`) →
 *   return `resumeMessages` as-is (caller sets `resume: true`).
 * - **text**: non-empty partial `content`, no tool graph →
 *   `buildAgentContext` base + incomplete assistant + `CONTINUE_STEER` user
 *   line. The steer is not persisted as a user row.
 * - **noop**: no body and no tool resume → same as `buildAgentContext`.
 *
 * `buildAgentContext` still filters `interrupted` for normal user sends so
 * dead mid-cuts don't pollute history.
 */
export function buildContinueContext(conversation) {
  const messages = Array.isArray(conversation?.messages) ? conversation.messages : [];
  const last = messages.at(-1);
  if (!last || last.status !== "interrupted") return buildAgentContext(conversation);

  // Tool path: only when tools actually settled — not inject-only snapshots.
  if (hasToolResumeSnapshot(last) && Array.isArray(last.resumeMessages) && last.resumeMessages.length > 0) {
    return [...last.resumeMessages];
  }

  // Text path: non-empty partial body → base + assistant + steer.
  const partialBody = typeof last.content === "string" ? last.content.trim() : "";
  if (!partialBody) return buildAgentContext(conversation);

  const base = buildAgentContext(conversation);
  return [
    ...base,
    { role: "assistant", content: last.content },
    { role: "user", content: CONTINUE_STEER },
  ];
}

/**
 * Runner checkpoints are relative to the context sent for this turn. Persist
 * them as an absolute offset into the full conversation.
 *
 * Codex-aligned: also carries `retainedUserMessages` forward so the desktop
 * can reconstruct the memento replacement on the next turn.
 */
export function mergeCompactionCheckpoint(previous, next, messageCount) {
  if (!next?.summary) return previous;
  const previousOffset = previous?.compactedMessageCount ?? 0;
  const summaryMessageCount = previous?.summary ? 1 : 0;
  const merged = {
    summary: next.summary,
    compactedMessageCount: Math.min(
      messageCount,
      previousOffset + Math.max(0, next.compactedMessageCount - summaryMessageCount),
    ),
    via: next.via,
    compactionCount: (previous?.compactionCount ?? (previous?.summary ? 1 : 0)) + 1,
  };
  if (Array.isArray(next.retainedUserMessages)) {
    merged.retainedUserMessages = next.retainedUserMessages;
  } else if (Array.isArray(previous?.retainedUserMessages)) {
    // Carry forward from previous if the new checkpoint didn't include them.
    merged.retainedUserMessages = previous.retainedUserMessages;
  }
  return merged;
}

/**
 * Filter the conversation list by title only.
 *
 * List payloads are `AgentConversationSummary` (title + meta, no messages or
 * checkpoint text). Full threads are not loaded for sidebar search, so
 * matching message content would require per-keystroke I/O. Keep this
 * title-only and pair with honest empty-state / placeholder copy
 * (`conversationSearchEmptyCopy`).
 */
export function searchConversations(conversations, query) {
  const normalized = String(query ?? "").trim().toLocaleLowerCase();
  if (!normalized) return conversations;
  return conversations.filter((conversation) =>
    String(conversation?.title ?? "").toLocaleLowerCase().includes(normalized),
  );
}

/** Empty-list message for the conversations sidebar (title-scoped search). */
export function conversationSearchEmptyCopy(hasConversations) {
  return hasConversations ? "No conversations with this title." : "No conversations yet.";
}

/**
 * Merge an incoming conversation-message snapshot into the currently-held
 * in-memory messages, never dropping a message that is already visible.
 *
 * Ticket #47: while turn N's assistant reply is still being sealed by main,
 * a user submitting turn N+1 calls store.append(user) and may receive a
 * snapshot that has not yet included turn N's assistant message. Overwriting
 * `this.conversation.messages` with that stale list would make the assistant
 * message "disappear" from the thread until a later room switch re-reads the
 * store (source of truth).
 *
 * Merge strategy (order-preserving):
 *   - Start from `incoming` (the authoritative, store-backed list).
 *   - Re-append any `current` message that is NOT present in `incoming`, as
 *     long as it is not an interrupted slot that an incoming message replaced.
 *   - Identity = `role + createdAt` (fall back to object equality when two
 *     items share a slot). This avoids duplicating shared messages.
 *
 * @returns an array of messages, best-effort chronological.
 */
export function mergeConversationMessages(current, incoming) {
  const list = Array.isArray(incoming) ? incoming : [];
  const currentList = Array.isArray(current) ? current : [];
  const positioned = [...currentList, ...list];
  if (positioned.length > 0 && positioned.every(isPositionedMessage)) {
    const byId = new Map(list.map((message) => [message.id, message]));
    for (const message of currentList) {
      const candidate = byId.get(message.id);
      if (!candidate || message.revision > candidate.revision) byId.set(message.id, message);
    }
    return [...byId.values()].sort((left, right) => {
      const positionOrder = left.position - right.position;
      return positionOrder || left.id.localeCompare(right.id);
    });
  }
  if (currentList.length === 0) return list;
  const incomingKeys = new Set(list.map(messageKey));
  // Preserve seen messages that the incoming snapshot lacks (e.g. a not-yet
  // sealed assistant reply). Skip interrupted slots the incoming replaced.
  const missing = currentList.filter((m) => !incomingKeys.has(messageKey(m)) && !(m?.status === "interrupted"));
  if (missing.length === 0) return list;
  // Stale-tail guard: only append the missing item when it belongs to the same
  // (or earlier) chronological span than the incoming tail — avoids inserting
  // a stale streak after a compaction that legitimately dropped old messages.
  const merged = [...list];
  for (const m of missing) {
    if (!merged.some((x) => Object.is(x, m))) merged.push(m);
  }
  return merged;
}

function isPositionedMessage(message) {
  return Boolean(message)
    && typeof message.id === "string"
    && message.id.length > 0
    && Number.isInteger(message.position)
    && message.position > 0
    && Number.isInteger(message.revision)
    && message.revision >= 0;
}

function messageKey(message) {
  if (!message || typeof message !== "object") return `${String(message)}`;
  const role = message.role ?? "";
  const createdAt = message.createdAt ?? "";
  const traceId = message.traceId ?? "";
  return `${role}|${createdAt}|${traceId}`;
}

/**
 * Mirror of `wrapUntrustedResult` in
 * packages/application/src/agent/services/agent-turn-utils.ts. The renderer is
 * plain JS and the application package is typed, so we do not cross-import;
 * keep the envelope text in sync with the canonical version when it changes.
 *
 * Tool results whose name carries attacker-controllable content (MCP plugin
 * output) are wrapped so the model treats them as DATA, not instructions.
 */
const UNTRUSTED_TOOL_PREFIXES = ["mcp_"];
const UNTRUSTED_DELIMITER_RE = /untrusted_tool_result/gi;

function isUntrustedTool(name) {
  return UNTRUSTED_TOOL_PREFIXES.some((prefix) => String(name ?? "").startsWith(prefix));
}

function neutralizeUntrustedDelimiters(content) {
  return content.replace(UNTRUSTED_DELIMITER_RE, "untrusted-tool-result");
}

function wrapUntrustedToolResult(toolName, content) {
  if (!isUntrustedTool(toolName)) return content;
  // Current turns persist the canonical provider projection, which already
  // contains its XML trust boundary. Rehydrate it as-is rather than nesting.
  if (content.startsWith("<untrusted_tool_result") && content.endsWith("</untrusted_tool_result>")) return content;
  const safe = neutralizeUntrustedDelimiters(content);
  return (
    `<untrusted_tool_result source="${toolName}" format="terminal">\n` +
    `${safe}\n` +
    "</untrusted_tool_result>"
  );
}

function toolResultContent(call) {
  // Prefer the exact mid-turn projection when available (dual-rep).
  if (typeof call.modelOutput === "string" && call.modelOutput.length > 0) {
    return clampToolText(call.modelOutput, TOOL_OUTPUT_MAX_CHARS);
  }
  if (typeof call.output === "string" && call.output.length > 0) {
    return clampToolText(call.output, TOOL_OUTPUT_MAX_CHARS);
  }
  if (typeof call.error === "string" && call.error.length > 0) {
    return clampToolText(`[TOOL ERROR] ${call.error}`, TOOL_OUTPUT_MAX_CHARS);
  }
  return "";
}

function toProviderToolCall(call) {
  if (!call || typeof call !== "object") return undefined;
  const id = typeof call.id === "string" ? call.id : undefined;
  const name = typeof call.name === "string" ? call.name : undefined;
  if (!id || !name) return undefined;
  let args = call.args && typeof call.args === "object" && !Array.isArray(call.args) ? call.args : undefined;
  if (args) {
    try {
      const encoded = JSON.stringify(args);
      args = encoded.length <= TOOL_ARGS_MAX_CHARS ? args : { _truncated: clampToolText(encoded, TOOL_ARGS_MAX_CHARS - 24) };
    } catch {
      args = undefined;
    }
  }
  return { id, name, args: args ?? {} };
}

/**
 * Expand one durable message into the provider-message array the runner expects.
 *
 * For assistant messages with tool calls:
 * - If `message.steps` contains `tool_calls` steps, rebuild round-accurately
 *   from steps: each `tool_calls` step becomes an assistant+toolCalls message
 *   followed by its tool results; `text` steps become standalone assistant
 *   messages; `reasoning` steps are skipped (keeps confabulated reasoning out
 *   of the next prompt). This preserves the live round order the model saw.
 * - Otherwise (flat fallback), emit one assistant with empty content + all
 *   toolCalls, then tool results, then — if `message.content` is nonempty — a
 *   trailing assistant with the final text. This puts the answer AFTER tools
 *   instead of gluing it onto the tool-call message.
 */
function toProviderMessages(message) {
  if (message.role === "assistant" && Array.isArray(message.toolCalls) && message.toolCalls.length > 0) {
    if (Array.isArray(message.steps) && message.steps.some((s) => s?.type === "tool_calls" && Array.isArray(s.calls) && s.calls.length > 0)) {
      return toProviderMessagesFromSteps(message);
    }
    return toProviderMessagesFlat(message);
  }
  if (message.role !== "user" || !message.attachments?.length) {
    return [{ role: message.role, content: message.content }];
  }
  return [{
    role: "user",
    content: [
      ...(message.content ? [{ type: "text", text: message.content }] : []),
      ...message.attachments.map((attachment) => {
        if (attachment.type === "text") {
          return { type: "text", text: `Attached text file: ${attachment.name}\n\n${attachment.content}` };
        }
        return attachment.type === "image"
          ? { type: "image", dataUrl: attachment.dataUrl, name: attachment.name }
          : {
            type: "file",
            dataUrl: attachment.dataUrl,
            mediaType: attachment.mediaType,
            name: attachment.name,
          };
      }),
    ],
  }];
}

/**
 * Rebuild provider messages from round-accurate `steps`.
 * - `reasoning` steps are skipped (not re-injected into provider context).
 * - `tool_calls` steps emit `{role:"assistant", content:"", toolCalls}` + tool results.
 * - `text` steps emit `{role:"assistant", content}` (mid-turn or final).
 * - If no text step was emitted, `message.content` is used as a trailing assistant.
 */
function toProviderMessagesFromSteps(message) {
  const out = [];
  let hasText = false;
  for (const step of message.steps) {
    if (!step || typeof step !== "object") continue;
    if (step.type === "reasoning") continue;
    if (step.type === "text" && typeof step.content === "string") {
      hasText = true;
      out.push({ role: "assistant", content: step.content });
      continue;
    }
    if (step.type === "tool_calls" && Array.isArray(step.calls) && step.calls.length > 0) {
      const calls = step.calls.map(toProviderToolCall).filter(Boolean);
      if (calls.length === 0) continue;
      out.push({ role: "assistant", content: "", toolCalls: calls });
      for (const call of step.calls) {
        const expanded = toProviderToolCall(call);
        if (!expanded) continue;
        out.push({
          role: "tool",
          toolCallId: expanded.id,
          name: expanded.name,
          content: wrapUntrustedToolResult(expanded.name, toolResultContent(call)),
        });
      }
    }
  }
  if (!hasText && typeof message.content === "string" && message.content.trim()) {
    out.push({ role: "assistant", content: message.content });
  }
  return out.length > 0 ? out : [{ role: "assistant", content: message.content ?? "" }];
}

/**
 * Flat fallback when no `steps` are available: emit assistant with empty
 * content + all toolCalls, then tool results, then a trailing assistant with
 * the final text (if nonempty). This puts the answer AFTER tools.
 */
function toProviderMessagesFlat(message) {
  const calls = message.toolCalls.map(toProviderToolCall).filter(Boolean);
  if (calls.length === 0) return [{ role: "assistant", content: message.content ?? "" }];
  const assistantMessage = {
    role: "assistant",
    content: "",
    toolCalls: calls,
  };
  const toolResults = message.toolCalls
    .map((call) => {
      const expanded = toProviderToolCall(call);
      if (!expanded) return undefined;
      return {
        role: "tool",
        toolCallId: expanded.id,
        name: expanded.name,
        content: wrapUntrustedToolResult(expanded.name, toolResultContent(call)),
      };
    })
    .filter(Boolean);
  const finalText = typeof message.content === "string" ? message.content.trim() : "";
  if (finalText) {
    return [assistantMessage, ...toolResults, { role: "assistant", content: message.content }];
  }
  return [assistantMessage, ...toolResults];
}
