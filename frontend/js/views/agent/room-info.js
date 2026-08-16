// Room info floating (i) icon — shows conversation diagnostics (tool count,
// compaction count, conversation ID) in a popover. Ported from NusaShell
// Electron agent-conversation-controller.js.

export function bindRoomInfo({ getConversation, copyText }) {
  const trigger = document.getElementById('agent-room-info-trigger');
  const closeBtn = document.getElementById('agent-room-info-close');
  const popover = document.getElementById('agent-room-info-popover');
  if (!trigger || !closeBtn || !popover) return;

  trigger.addEventListener('click', () => toggleRoomInfo());
  closeBtn.addEventListener('click', () => toggleRoomInfo(false));

  // Close on outside click
  document.addEventListener('mousedown', (event) => {
    if (!popover.hidden && !popover.contains(event.target) && event.target !== trigger) {
      toggleRoomInfo(false, { focusTrigger: false });
    }
  });

  // Close on Escape
  popover.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      toggleRoomInfo(false);
    }
  });

  function toggleRoomInfo(force, { focusTrigger = true } = {}) {
    const open = typeof force === 'boolean' ? force : popover.hidden;
    popover.hidden = !open;
    trigger.setAttribute('aria-expanded', String(open));
    if (!open && focusTrigger) trigger.focus({ preventScroll: true });
  }

  // Expose toggle for external close calls
  return { toggleRoomInfo };
}

export function updateRoomInfo(conversation, extraMessages) {
  const info = document.getElementById('agent-room-info');
  const title = document.getElementById('agent-room-info-title');
  const toolCount = document.getElementById('agent-room-tool-count');
  const compactionCount = document.getElementById('agent-room-compaction-count');
  const idEl = document.getElementById('agent-room-id');
  const copyBtn = document.getElementById('agent-room-id-copy');
  if (!info || !title || !toolCount || !compactionCount || !idEl || !copyBtn) return;

  const hasRoom = Boolean(conversation?.id);
  info.hidden = !hasRoom;
  if (!hasRoom) return;

  title.textContent = conversation?.title || 'Conversation details';
  // The active conversation's messages live in state.messages (a sibling of
  // the conversation payload) — prefer them when provided so the popover
  // reflects the real thread instead of a payload without messages.
  const meta = getRoomMetadata(conversation, extraMessages);
  toolCount.textContent = String(meta.toolCallCount);
  compactionCount.textContent = String(meta.compactionCount);
  idEl.textContent = meta.conversationId;

  copyBtn.disabled = false;
  copyBtn.title = 'Copy conversation ID';
  copyBtn.onclick = () => {
    navigator.clipboard?.writeText(meta.conversationId).catch(() => {});
  };
}

function getRoomMetadata(conversation, extraMessages) {
  // Use the caller-provided message list (state.messages) when supplied;
  // otherwise fall back to conversation.messages.
  const messages = Array.isArray(extraMessages)
    ? extraMessages
    : Array.isArray(conversation?.messages) ? conversation.messages : [];
  let toolCallCount = 0;
  for (const message of messages) {
    if (Array.isArray(message?.tool_calls)) {
      // Filter out hydration tool calls (they are hidden from the UI)
      toolCallCount += message.tool_calls.filter(
        (tc) => !tc.id?.startsWith('hydrate-'),
      ).length;
    }
    if (Array.isArray(message?.steps)) {
      for (const step of message.steps) {
        if (step?.type === 'tool_calls' && Array.isArray(step.tool_calls)) {
          toolCallCount += step.tool_calls.filter(
            (tc) => !tc.id?.startsWith('hydrate-'),
          ).length;
        }
      }
    }
  }
  const chunkCount = Number.isInteger(conversation?.chunk_count) ? conversation.chunk_count : 0;
  return {
    conversationId: typeof conversation?.id === 'string' ? conversation.id : '',
    compactionCount: chunkCount,
    toolCallCount,
  };
}
