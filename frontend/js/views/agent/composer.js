import { rpc } from '../../rpc.js';
import { toast } from '../../ui.js';
import { inspectAttachmentContent, toDataURL } from '../../agent-ui.js';

export function bindComposer({ state, createConversation, beginTurn, refreshConversations, renderAttachments, updateComposerStatus, showSteerQueued, clearSteerQueue, promoteSteerToTranscript, stopActiveRun }) {
  const form = document.getElementById('agent-form');
  const input = document.getElementById('composer-input');
  const stopButton = document.getElementById('stop-btn');
  const attachButton = document.getElementById('agent-attach-btn');
  const fileInput = document.getElementById('agent-file-input');
  const workspaceButton = document.getElementById('agent-workspace-btn');

              const autosize = () => {
    input.style.height = 'auto';
    input.style.height = `${Math.min(input.scrollHeight, 180)}px`;
    // Show a scrollbar only when the content exceeds the cap.
    input.style.overflowY = input.scrollHeight > 180 ? 'auto' : 'hidden';
  };
  input.addEventListener('input', () => {
    autosize();
    updateSendAvailability(state);
  });
  input.addEventListener('keydown', (event) => {
    if (event.isComposing || event.keyCode === 229) return;
    if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
      event.preventDefault();
      send();
    }
  });
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    send();
  });
  attachButton.addEventListener('click', () => fileInput.click());
  fileInput.addEventListener('change', async () => {
    await addAttachments(fileInput.files);
    fileInput.value = '';
  });
  form.addEventListener('dragover', (event) => event.preventDefault());
  form.addEventListener('drop', async (event) => {
    event.preventDefault();
    await addAttachments(event.dataTransfer?.files);
  });
  workspaceButton.addEventListener('click', chooseWorkspace);
  stopButton.addEventListener('click', async () => {
    await stopActiveRun();
  });

  let sending = false;
  async function send() {
    if (sending) return;
    const text = input.value.trim();
    if (!text && !state.attachments.length) return;
    if (!state.model) {
      toast('Choose a model first (Models with a provider must be imported in Providers).', 'error');
      return;
    }
    // Route to steering when a turn is running. Check both the in-memory run
    // map (state.running) and the persisted conversation status — after a page
    // refresh, state.runs may still be empty even though a turn is active
    // server-side, and openConversation may not have finished re-attaching.
    if (state.running || state.conversation?.status === 'running') {
      await sendSteer(text);
      return;
    }
    sending = true;
    try {
      if (!state.activeId) await createConversation(text.slice(0, 48));
      const attachments = [...state.attachments];
      const { run_id: runID } = await rpc('agent.turns.start', {
        conversation_id: state.activeId,
        text,
        model: state.model,
        effort: state.effort && state.effort !== 'auto' ? state.effort : undefined,
        attachments,
      });
      input.value = '';
      autosize();
      state.attachments = [];
      renderAttachments();
      beginTurn(runID, text, attachments);
    } catch (error) {
      // If the conversation is busy (a turn is already running server-side but
      // the frontend hasn't re-attached yet), fall back to steering instead of
      // showing a hard error.
      if (error.message?.includes('conversation is busy')) {
        await sendSteer(text);
        return;
      }
      toast(error.message, 'error');
    } finally {
      sending = false;
    }
  }

  async function sendSteer(text) {
    if (!state.activeId) return;
    try {
      state.steerDraft = text;
      const attachments = [...state.attachments];
      await rpc('agent.turns.steer', {
        conversation_id: state.activeId,
        text,
        attachments,
      });
      input.value = '';
      autosize();
      state.attachments = [];
      renderAttachments();
      // showSteerQueued is handled by the agent.steer.queued event handler,
      // not here — calling it explicitly would double-render the steer bubble
      // (once from the RPC response, once from the event).
      updateSendAvailability(state);
    } catch (error) {
      toast(error.message, 'error');
    }
  }

  async function addAttachments(fileList) {
    const files = [...(fileList ?? [])];
    if (!files.length) return;
    for (const file of files) {
      if (state.attachments.length >= 4) {
        toast('A turn can include up to 4 attachments.', 'error');
        break;
      }
      if (file.size > 4 * 1024 * 1024) {
        toast(`${file.name} is larger than the 4 MiB limit.`, 'error');
        continue;
      }
      const bytes = new Uint8Array(await file.arrayBuffer());
      const detected = inspectAttachmentContent(bytes);
      if (!detected) {
        toast(`${file.name} is not a supported image, PDF, or UTF-8 text file.`, 'error');
        continue;
      }
      state.attachments.push({
        type: detected.type,
        name: file.name || 'Attachment',
        media_type: detected.mediaType,
        ...(detected.type === 'text' ? { content: detected.content } : { data_url: toDataURL(bytes, detected.mediaType) }),
      });
    }
    renderAttachments();
    updateSendAvailability(state);
  }

  async function chooseWorkspace() {
    if (!state.activeId) {
      toast('Start or select a conversation first.', 'error');
      return;
    }
    try {
      const { conversation } = await rpc('agent.conversations.pick-workspace', { id: state.activeId });
      state.conversation = conversation;
      updateComposerStatus();
      await refreshConversations();
    } catch (error) {
      toast(error.message, 'error');
    }
  }
}

export function updateSendAvailability(state) {
  const input = document.getElementById('composer-input');
  const send = document.getElementById('send-btn');
  const hasContent = input.value.trim() || state.attachments.length;
  // Show steer mode when a turn is running — check both the in-memory run map
  // and the persisted conversation status (after refresh, state.runs may be
  // empty until reattachActiveRunFromBackend completes).
  const running = state.running || state.conversation?.status === 'running';
  if (running) {
    send.disabled = !hasContent;
    send.title = hasContent ? 'Steer · Ctrl+Enter (⌘↩ on Mac)' : 'Type a message to steer the agent';
  } else {
    send.disabled = !hasContent;
    send.title = 'Send · Ctrl+Enter (⌘↩ on Mac)';
  }
}
