import markdownit from "markdown-it";

const assistantMarkdown = markdownit({ html: false });

export function renderAssistantMarkdown(content) {
  return assistantMarkdown.render(String(content ?? ""));
}

export function renderReasoningMarkdown(content) {
  return assistantMarkdown.render(String(content ?? ""));
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
