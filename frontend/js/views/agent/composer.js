import { rpc } from '../../rpc.js';
import { el, toast } from '../../ui.js';
import { inspectAttachmentContent, toDataURL } from '../../agent-ui.js';
import { agentForm, composerInput, composerStack, sendButton } from './domrefs.js';

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
  input.addEventListener('paste', (event) => {
    handlePaste(event, input, autosize);
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

  // Drag & drop on the whole conversation area (not just the composer form).
  // Shows a drag overlay while dragging and handles both files and folders.
  // Folders are detected via webkitGetAsEntry() and attached as path-only
  // references (type: "folder") — the agent can use file tools to explore
  // the directory. File.path is only available in desktop shells (Electron);
  // in pure web mode folders fall back to a workspace-picker prompt.
  const dropZone = document.getElementById('agent-conversation');
  if (dropZone) {
    let dragCounter = 0;
    let overlay = null;

    const showOverlay = () => {
      if (overlay) return;
      overlay = el('div', { class: 'agent-drop-overlay' }, [
        el('div', { class: 'agent-drop-overlay-inner' }, [
          el('div', { class: 'agent-drop-overlay-icon', text: '⤓' }),
          el('div', { class: 'agent-drop-overlay-text', text: 'Drop files or folders to attach' }),
        ]),
      ]);
      dropZone.appendChild(overlay);
    };
    const hideOverlay = () => {
      if (overlay) { overlay.remove(); overlay = null; }
    };

    dropZone.addEventListener('dragenter', (event) => {
      if (!event.dataTransfer?.types?.includes('Files')) return;
      event.preventDefault();
      dragCounter++;
      showOverlay();
    });
    dropZone.addEventListener('dragover', (event) => {
      if (!event.dataTransfer?.types?.includes('Files')) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = 'copy';
    });
    dropZone.addEventListener('dragleave', (event) => {
      event.preventDefault();
      dragCounter--;
      if (dragCounter <= 0) { dragCounter = 0; hideOverlay(); }
    });
    dropZone.addEventListener('drop', async (event) => {
      event.preventDefault();
      dragCounter = 0;
      hideOverlay();
      await handleDrop(event.dataTransfer);
    });
  }
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
    sending = true;
    try {
      if (state.running || state.conversation?.status === 'running') {
        try {
          await sendSteer(text);
          return;
        } catch (error) {
          // Stale running status — the turn died without the frontend seeing
          // a terminal event (missed turn.done/error over a WS reconnect, a
          // stop that raced the stream). The steer was rejected because there
          // is no active turn, so the message must NOT vanish: fall through
          // and start a new turn with it.
          if (!/no active turn/i.test(error?.message || '')) throw error;
          toast('Agent already stopped — sending as a new message.', 'info');
        }
      }
      await startNewTurn(text);
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

  async function startNewTurn(text) {
    if (!state.activeId) await createConversation(text.slice(0, 48));
    const attachments = [...state.attachments];
    state.localTurnPending = true;
    try {
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
    } finally {
      state.localTurnPending = false;
    }
  }

  async function sendSteer(text) {
    if (!state.activeId) return;
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
  }

  async function handlePaste(event, inputEl, autosize) {
    const items = event.clipboardData?.items;
    if (!items) return;

    // Check for image items first — images always become attachments.
    const imageItems = [];
    for (const item of items) {
      if (item.type.startsWith('image/')) {
        imageItems.push(item);
      }
    }

    // Handle image paste → image attachment.
    if (imageItems.length > 0) {
      event.preventDefault();
      for (const item of imageItems) {
        const file = item.getAsFile();
        if (file) await addAttachments([file]);
      }
      return;
    }

    // Check text length. DataTransferItem.getAsString is async (callback),
    // so we read it synchronously via clipboardData.getData which is available
    // during the paste event.
    const textContent = event.clipboardData.getData('text/plain') || '';
    if (textContent.length > 1024) {
      event.preventDefault();
      if (state.attachments.length >= 4) {
        toast('A turn can include up to 4 attachments.', 'error');
        return;
      }
      const name = `pasted-${Date.now()}.txt`;
      state.attachments.push({
        type: 'text',
        name,
        media_type: 'text/plain',
        content: textContent,
      });
      renderAttachments();
      updateSendAvailability(state);
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

  // handleDrop processes a DataTransfer from a drop event. It distinguishes
  // folders from files using webkitGetAsEntry(). Folders are attached as
  // path-only references (type: "folder") when File.path is available
  // (desktop shell). Files are processed as normal byte attachments.
  async function handleDrop(dataTransfer) {
    if (!dataTransfer) return;
    const items = dataTransfer.items;
    const files = [];

    // First pass: separate folder entries from file entries using the
    // entries API (webkitGetAsEntry). This lets us detect directories
    // which dataTransfer.files does not expose.
    const folderEntries = [];
    const fileItems = [];
    if (items && items.length > 0) {
      const entryPromises = [];
      for (const item of items) {
        if (item.kind !== 'file') continue;
        const entry = item.webkitGetAsEntry?.();
        if (entry && entry.isDirectory) {
          folderEntries.push(entry);
        } else {
          fileItems.push(item);
        }
      }
      // Collect File objects for non-folder items.
      for (const item of fileItems) {
        const file = item.getAsFile();
        if (file) files.push(file);
      }
    } else {
      // Fallback: no items API, use files directly.
      for (const file of dataTransfer.files) files.push(file);
    }

    // Process folders: attach as path-only references.
    for (const entry of folderEntries) {
      if (state.attachments.length >= 4) {
        toast('A turn can include up to 4 attachments.', 'error');
        break;
      }
      await addFolderAttachment(entry);
    }

    // Process files: normal byte attachments.
    if (files.length > 0) await addAttachments(files);
  }

  // addFolderAttachment converts a FileSystemDirectoryEntry into a folder
  // attachment. In desktop shells (Electron), File.path exposes the absolute
  // filesystem path. In pure web mode, File.path is undefined — we still
  // attach the folder name but without a path, and the backend will reject
  // it with a clear validation error.
  async function addFolderAttachment(entry) {
    // Try to get the underlying File object — Electron exposes .path on it.
    const file = await new Promise((resolve) => entry.file(resolve, () => resolve(null)));
    const name = entry.name || file?.name || 'Folder';
    const filePath = file?.path || '';

    if (!filePath) {
      toast(`Cannot attach folder "${name}" — the browser does not expose filesystem paths. Use the workspace picker instead.`, 'error', 6000);
      return;
    }

    state.attachments.push({
      type: 'folder',
      name,
      media_type: 'inode/directory',
      file_path: filePath,
    });
    renderAttachments();
    updateSendAvailability(state);
  }

  async function chooseWorkspace() {
    if (!state.activeId) await createConversation();
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
  const input = composerInput();
  const send = sendButton();
  const hasContent = input.value.trim() || state.attachments.length;
  // Show steer mode when a turn is running — check both the in-memory run map
  // and the persisted conversation status (after refresh, state.runs may be
  // empty until reattachActiveRunFromBackend completes).
  const running = state.running || state.conversation?.status === 'running';
  const form = agentForm();
  const stack = composerStack();
  form?.classList.toggle('is-steer', running);
  stack?.classList.toggle('is-steer', running);
  send.classList.toggle('is-steer', running);
  if (running) {
    send.disabled = !hasContent;
    send.setAttribute('aria-label', 'Steer');
    send.title = hasContent ? 'Steer · Ctrl+Enter (⌘↩ on Mac)' : 'Type a message to steer the agent';
  } else {
    send.disabled = !hasContent;
    send.setAttribute('aria-label', 'Send');
    send.title = 'Send · Ctrl+Enter (⌘↩ on Mac)';
  }
}
