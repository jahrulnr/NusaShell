import markdownit from "markdown-it";

const assistantMarkdown = markdownit({ html: false });

export function renderAssistantMarkdown(content) {
  return assistantMarkdown.render(String(content ?? ""));
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
      ...message.attachments.map((attachment) => attachment.type === "image"
        ? { type: "image", dataUrl: attachment.dataUrl, name: attachment.name }
        : {
            type: "file",
            dataUrl: attachment.dataUrl,
            mediaType: attachment.mediaType,
            name: attachment.name,
          }),
    ],
  };
}
