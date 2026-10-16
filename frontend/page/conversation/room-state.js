// Conversation-room status helpers shared by the sidebar renderer and its
// lifecycle updates. The backend currently emits idle/running, but accepting
// the in-progress aliases keeps the indicator useful while a turn is being
// admitted or resumed.

const ACTIVE_STATUSES = new Set(['running', 'in_progress', 'working', 'queued', 'pending']);

export function selectInitialConversation(conversations) {
  return Array.isArray(conversations) && conversations.length ? conversations[0] : null;
}

export function normalizeConversationStatus(value, fallback = 'idle') {
  const normalized = String(value || '').trim().toLowerCase().replace(/[\s-]+/g, '_');
  return normalized || fallback;
}

export function isConversationActive(conversation) {
  return ACTIVE_STATUSES.has(normalizeConversationStatus(conversation?.status));
}

export function filterConversations(conversations, query) {
  if (!Array.isArray(conversations)) return [];
  const rawNeedle = String(query || '').trim().toLowerCase();
  if (!rawNeedle) return [...conversations];
  const normalizedNeedle = rawNeedle.replace(/[\s-]+/g, '_');
  return conversations.filter((conversation) => {
    const haystack = [conversation?.title, conversation?.id, conversation?.status, conversation?.model]
      .filter((value) => value !== null && value !== undefined)
      .map((value) => String(value).toLowerCase())
      .join(' ');
    return haystack.includes(rawNeedle) || haystack.replace(/[\s-]+/g, '_').includes(normalizedNeedle);
  });
}
