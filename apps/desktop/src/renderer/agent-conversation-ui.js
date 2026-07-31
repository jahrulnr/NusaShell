import markdownit from "markdown-it";
import DOMPurify from "isomorphic-dompurify";

const assistantMarkdown = markdownit({ html: true, linkify: true, breaks: true });
// Reasoning often mentions paths like plugins.md; linkify treats .md as a TLD
// and paints them as blue links. Keep breaks, skip auto-link.
const reasoningMarkdown = markdownit({ html: true, linkify: false, breaks: true });

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
  return DOMPurify.sanitize(assistantMarkdown.render(String(content ?? "")), {
    ALLOWED_TAGS: ASSISTANT_MARKDOWN_TAGS,
    ALLOWED_ATTR: ["href", "title", "alt", "src", "class", "id", "target", "rel", "colspan", "rowspan"],
  });
}

export function renderReasoningMarkdown(content) {
  return DOMPurify.sanitize(reasoningMarkdown.render(String(content ?? "")), {
    ALLOWED_TAGS: ASSISTANT_MARKDOWN_TAGS.filter((tag) => tag !== "img"),
    ALLOWED_ATTR: ["href", "title", "class", "id", "target", "rel", "colspan", "rowspan"],
  });
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

export function toConversationToolCall(call) {
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
  const output = call?.output !== undefined
    ? clampToolText(call.output, TOOL_OUTPUT_MAX_CHARS)
    : call?.error
      ? clampToolText(call.error, TOOL_OUTPUT_MAX_CHARS)
      : call?.result !== undefined
        ? formatToolOutput(call.result)
        : undefined;
  return {
    id: call.id,
    name: call.name,
    ok: call.ok !== false,
    ...(call.error ? { error: clampToolText(call.error, 4_000) } : {}),
    ...(safeArgs ? { args: safeArgs } : {}),
    ...(output ? { output } : {}),
  };
}

export function sanitizeAssistantSteps(steps) {
  if (!Array.isArray(steps)) return undefined;
  return steps.map((step) => {
    if (step?.type === "tool_calls" && Array.isArray(step.calls)) {
      return { type: "tool_calls", calls: step.calls.map(toConversationToolCall), ...(step.model ? { model: step.model } : {}), ...(step.providerId ? { providerId: step.providerId } : {}) };
    }
    if ((step?.type === "reasoning" || step?.type === "text") && typeof step.content === "string") {
      return { ...step, content: clampToolText(step.content, 1_000_000) };
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
 */
export function buildAgentContext(conversation) {
  const checkpoint = conversation?.checkpoint;
  const messages = Array.isArray(conversation?.messages) ? conversation.messages : [];
  if (!checkpoint?.summary) return messages.map(toProviderMessage);

  return [
    { role: "system", content: `Conversation summary:\n${checkpoint.summary}` },
    ...messages.slice(checkpoint.compactedMessageCount).map(toProviderMessage),
  ];
}

/**
 * Runner checkpoints are relative to the context sent for this turn. Persist
 * them as an absolute offset into the full conversation.
 */
export function mergeCompactionCheckpoint(previous, next, messageCount) {
  if (!next?.summary) return previous;
  const previousOffset = previous?.compactedMessageCount ?? 0;
  const summaryMessageCount = previous?.summary ? 1 : 0;
  return {
    summary: next.summary,
    compactedMessageCount: Math.min(
      messageCount,
      previousOffset + Math.max(0, next.compactedMessageCount - summaryMessageCount),
    ),
    via: next.via,
  };
}

export function searchConversations(conversations, query) {
  const normalized = String(query ?? "").trim().toLocaleLowerCase();
  if (!normalized) return conversations;
  return conversations.filter((conversation) => conversation.title.toLocaleLowerCase().includes(normalized));
}

function toProviderMessage(message) {
  if (message.role !== "user" || !message.attachments?.length) {
    return { role: message.role, content: message.content };
  }
  return {
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
  };
}
