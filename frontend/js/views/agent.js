// Agent workspace: multi-conversation chat with streaming turns.

import { rpc, on, emit } from '../rpc.js';
import { el, fmtTime, toast, confirmDialog, debounce } from '../ui.js';
import { renderMarkdown } from '../markdown.js';
import { estimateContextTokens, formatContextUsage, effectiveContextWindow } from '../agent-ui.js';
import { bindComposer, updateSendAvailability } from './agent/composer.js';
import { bindModelPicker } from './agent/model-picker.js';
import { bindRoomInfo, updateRoomInfo } from './agent/room-info.js';
import {
  attachmentChip,
  reasoningDisclosure,
  renderEmptyThread,
  renderConversation,
  renderMessage,
  renderToolJob,
  renderTodoItem,
  setAgentOfflineState,
  setToolTerminalStatus,
  toolTerminalMeta,
  toolTerminalOutput,
} from './agent/render.js';

const state = {
  conversations: [],
  activeId: null,
  conversation: null,
  messages: [],
  attachments: [],
  settings: {},
  model: localStorage.getItem('nusashell.model') || '',
  effort: 'auto', // reasoning effort: "auto" (omit) or a level from the model's supported_efforts
  runs: new Map(), // run_id -> {messageEl, toolStripEl, toolJobs, conversationId, runId}
  pendingEvents: new Map(), // run_id -> events that won the start race
  get running() { return runForConversation(this.activeId) !== null; },
  pinned: true, // auto-scroll only when the user is at the bottom (per-room, saved/restored)
  steerId: null, // id of the queued steer shown in the strip (per-room, saved/restored)
  steerDraft: '', // text of pending steer (per-room, saved/restored)
  // Chunk-based lazy load: track how many pre-compaction chunks are available
  // (from the backend) and which chunk index to load next (descending from
  // ChunkCount-1 toward 0). loadedChunks prevents duplicate loads.
  chunkCount: 0,
  nextChunkIndex: -1,
  loadedChunks: new Set(),
  loadingChunk: false,
  // Todo checklist: server-authoritative via agent.todo.updated events and
  // agent.todos.get RPC. todoRenderToken guards against stale async renders
  // (e.g. a fetch that resolves after the user switched to another room).
  todos: { items: [], summary: { total: 0, pending: 0, in_progress: 0, completed: 0 } },
  todoRenderToken: 0,
  conversationLoadToken: 0,
};

// Per-room state that survives conversation switches. When the user switches
// away from a conversation, its per-room state is saved here. When they switch
// back, it's restored. This prevents state from one room leaking into another.
const savedRooms = new Map(); // conversationId -> { pinned, steerDraft, attachments, model }

let agentDataLoading = false;

export async function initAgent() {
  bindComposer({
    state,
    createConversation,
    beginTurn,
    refreshConversations,
    renderAttachments,
    updateComposerStatus,
    showSteerQueued,
    clearSteerQueue,
    promoteSteerToTranscript,
    stopActiveRun,
  });
  bindConversations();
  bindModelPicker({
    getModels: () => models,
    getSelectedModel: () => state.model,
    getSelectedEffort: () => state.effort,
    selectModel,
    selectEffort,
    refreshModels,
  });
  bindRoomInfo({ getConversation: () => state.conversation });
  bindStripToggles();
  bindEvents();
  bindScrollPin();
  window.addEventListener('nusashell:preferred-model', (event) => {
    selectModel(event.detail?.model || '');
  });
  bindBackendAvailability();
  if (backendIsOffline()) {
    setAgentOfflineState(true);
    return;
  }
  await loadAgentData();
}

function bindBackendAvailability() {
  window.addEventListener('nusashell:connection-status', (event) => {
    const status = event.detail?.status;
    if (isOfflineStatus(status)) {
      setAgentOfflineState(true);
      return;
    }
    if (status === 'open') {
      setAgentOfflineState(false);
      void loadAgentData();
    }
  });
}

function backendIsOffline() {
  return isOfflineStatus(document.documentElement.dataset.backendStatus);
}

function isOfflineStatus(status) {
  return status === 'offline' || status === 'closed' || status === 'error';
}

async function loadAgentData() {
  if (agentDataLoading) return;
  agentDataLoading = true;
  try {
    await refreshConversations();
    await refreshModels();
    await refreshStatus();
    const active = state.conversations.find((conversation) => conversation.id === state.activeId);
    const first = active ?? state.conversations[0];
    if (first) await openConversation(first.id);
    else updateComposerStatus();
    setAgentOfflineState(false);
  } catch {
    setAgentOfflineState(true);
  } finally {
    agentDataLoading = false;
  }
}

// ---------- conversations ----------

async function refreshConversations() {
  const { conversations } = await rpc('agent.conversations.list');
  state.conversations = conversations;
  renderConversationList();
  const count = conversations.length;
  document.getElementById('conversation-count').textContent = `${count} thread${count === 1 ? '' : 's'}`;
}

function bindConversations() {
  document.getElementById('conversation-search').addEventListener('input', debounce(renderConversationList, 150));
  document.getElementById('new-conversation-btn').addEventListener('click', () => createConversation());
}

async function createConversation(title = '') {
  try {
    saveRoomState(state.activeId);
    const { conversation } = await rpc('agent.conversations.create', title ? { title } : {});
    state.activeId = conversation.id;
    state.conversation = conversation;
    state.messages = [];
    state.pinned = true;
    state.steerId = null;
    state.steerDraft = '';
    state.attachments = [];
    state.todos = { items: [], summary: { total: 0, pending: 0, in_progress: 0, completed: 0 } };
    state.todoRenderToken++;
    await refreshConversations();
    renderEmptyThread();
    renderAttachments();
    renderTodoStrip();
    updateComposerStatus();
    updateSendAvailability(state);
    document.getElementById('composer-input').focus();
  } catch (err) {
    toast(err.message, 'error');
  }
}

function renderConversationList() {
  const list = document.getElementById('conversation-list');
  const query = document.getElementById('conversation-search').value.toLowerCase();
  const filtered = state.conversations.filter((c) => (c.title || '').toLowerCase().includes(query));
  list.innerHTML = '';
  if (!filtered.length) {
    list.append(el('div', { class: 'agent-conversation-empty', text: query ? 'No conversations with this title.' : 'No conversations yet.' }));
    return;
  }
  for (const c of filtered) {
    const item = el('div', { class: `agent-conversation-item${c.id === state.activeId ? ' is-active' : ''}`, role: 'listitem' },
      el('button', {
        class: 'agent-conversation-open',
        title: c.title,
      },
        el('span', { class: 'agent-conversation-title', text: c.title || 'Untitled' }),
        el('span', { class: 'agent-conversation-time', text: `${c.message_count ?? 0} msgs · ${fmtTime(c.updated_at)}` }),
      ),
      el('button', { class: 'agent-conversation-delete', title: 'Delete conversation', 'aria-label': 'Delete conversation' }, '✕'),
    );
    item.querySelector('.agent-conversation-open').addEventListener('click', () => openConversation(c.id));
    item.querySelector('.agent-conversation-delete').addEventListener('click', async (e) => {
      e.stopPropagation();
      const ok = await confirmDialog('Delete conversation', `"${c.title || 'Untitled'}" and all of its messages will be removed.`, 'Delete');
      if (!ok) return;
      try {
        await rpc('agent.conversations.delete', { id: c.id });
        savedRooms.delete(c.id);
        if (state.activeId === c.id) {
          state.activeId = null;
          state.conversation = null;
          state.messages = [];
          state.attachments = [];
          state.steerId = null;
          state.steerDraft = '';
          state.todos = { items: [], summary: { total: 0, pending: 0, in_progress: 0, completed: 0 } };
          state.todoRenderToken++;
          clearSteerQueue();
          renderEmptyThread();
          renderAttachments();
          renderTodoStrip();
          updateComposerStatus();
          updateSendAvailability(state);
        }
        await refreshConversations();
        toast('Conversation deleted', 'success');
      } catch (err) {
        toast(err.message, 'error');
      }
    });
    list.append(item);
  }
}

async function openConversation(id) {
  // Save per-room state for the current conversation before switching.
  saveRoomState(state.activeId);
  const token = ++state.conversationLoadToken;
  state.activeId = id;
  // the backend returns messages as a sibling of conversation
  const { conversation, messages } = await rpc('agent.conversations.get', { id });
  if (token !== state.conversationLoadToken) return;
  state.conversation = conversation;
  state.messages = messages ?? [];
  // Reset chunk lazy-load state for the new conversation.
  state.chunkCount = conversation?.chunk_count ?? 0;
  state.nextChunkIndex = state.chunkCount - 1;
  state.loadedChunks = new Set();
  state.loadingChunk = false;
  // Restore per-room state (pinned, steerDraft, attachments, model).
  // If no saved state exists, defaults are applied.
  const hasSaved = loadRoomState(id);
  if (!hasSaved) {
    const requestedModel = conversation.model || localStorage.getItem('nusashell.model') || '';
    state.model = models.length && requestedModel && !models.some((model) => `${model.provider_id}:${model.id}` === requestedModel) && !models.some((model) => model.id === requestedModel) ? '' : requestedModel;
    state.effort = conversation.effort || 'auto';
  }
  renderConversationList();
  renderThread(state.messages);
  // If the active conversation has very few bubbles (e.g. after compaction
  // the active slice is just a marker + a handful of retained messages),
  // proactively load the newest archived chunk so the user sees history
  // immediately without having to scroll up.
  maybeLoadOlderChunk();
  // If there's an active run for this conversation (e.g. user switched away
  // and came back, or page was refreshed while a turn was running), re-attach
  // the streaming UI to the rendered thread so live deltas continue updating
  // the visible DOM. After a page refresh, state.runs is empty, so query the
  // backend for the active run first.
  await reattachActiveRunFromBackend();
  if (token !== state.conversationLoadToken) return;
  reattachActiveRun();
  // Restore the steer queue strip if a steer is still pending for this room.
  // The strip is composer chrome, not part of the thread — it is re-shown
  // from saved per-room state rather than re-rendered from messages.
  if (state.steerDraft && state.running) {
    showSteerQueued(state.steerDraft, state.steerId);
  } else {
    clearSteerQueue();
  }
  // Re-render attachment chips for the restored attachments.
  renderAttachments();
  // Fetch the todo checklist for this conversation. The render token ensures
  // that if the user switches rooms while the fetch is in-flight, the stale
  // response is discarded.
  fetchTodos();
  updateModelTrigger();
  updateComposerStatus();
  updateSendAvailability(state);
  updateRoomInfo(conversation);
}

// reattachActiveRunFromBackend queries the backend for the active run of the
// current conversation. After a page refresh, state.runs is empty even though
// a turn may still be running server-side. If a run is found, it is registered
// into state.runs so that reattachActiveRun can wire the DOM and send() routes
// to steering instead of start.
async function reattachActiveRunFromBackend() {
  const conversationId = state.activeId;
  if (!conversationId) return;
  if (runForConversation(conversationId)) return; // already attached
  if (state.conversation?.status !== 'running') return;
  let active;
  try {
    active = await rpc('agent.turns.active', { id: conversationId });
  } catch {
    return; // backend may not support the method yet; fail silently
  }
  if (state.activeId !== conversationId) return;
  if (!active?.active || !active.run_id) return;
  // Register a minimal run entry. reattachActiveRun will populate the DOM
  // references by replacing the last assistant message node.
  state.runs.set(active.run_id, {
    msgNode: null, bubble: null, strip: null, textBox: null, reasoningEl: null,
    toolJobs: new Map(), raw: '', rawReasoning: '',
    round: 1, conversationId: conversationId, runId: active.run_id,
    messageId: active.message_id,
  });
  flushPendingEvents(active.run_id);
  document.getElementById('stop-btn').hidden = false;
  updateSendAvailability(state);
}

// reattachActiveRun checks if there's an active run for the current
// conversation. If so, it replaces the last assistant message node (which
// renderThread created from persisted state) with a fresh streaming
// placeholder and wires the run's DOM references to it. Accumulated raw
// text is re-rendered so the user sees the latest streamed content.
function reattachActiveRun() {
  const run = runForConversation(state.activeId);
  if (!run) return;
  const thread = document.getElementById('agent-thread');
  // Find the last assistant message node in the rendered thread.
  const assistantNodes = thread.querySelectorAll('.agent-message.assistant');
  const lastAssistant = assistantNodes[assistantNodes.length - 1];
  if (!lastAssistant) return;
  // Replace it with a fresh streaming placeholder.
  const bubble = el('div', { class: 'agent-bubble' });
  const msgNode = el('div', { class: 'agent-message assistant agent-pending' }, bubble);
  lastAssistant.replaceWith(msgNode);
  // Re-create streaming elements for the current round.
  const reasoningEl = reasoningDisclosure('');
  reasoningEl.hidden = !run.rawReasoning;
  const textBox = el('div', { class: 'agent-bubble-text' });
  const strip = el('div', { class: 'agent-tool-stack' });
  strip.hidden = true;
  bubble.append(reasoningEl, textBox, strip);
  // Re-render accumulated content.
  if (run.rawReasoning) {
    const content = reasoningEl.querySelector('.agent-reasoning-content');
    if (content) content.innerHTML = renderMarkdown(run.rawReasoning);
  }
  if (run.raw) textBox.innerHTML = renderMarkdown(run.raw);
  // Update the run entry with fresh DOM references.
  run.msgNode = msgNode;
  run.bubble = bubble;
  run.reasoningEl = reasoningEl;
  run.textBox = textBox;
  run.strip = strip;
  clearToolTimers(run);
  run.toolJobs = new Map();
  scrollToBottom(true);
}

function renderThread(messages) {
  const thread = document.getElementById('agent-thread');
  if (!messages.length) {
    renderEmptyThread();
    return;
  }
  thread.replaceChildren(renderConversation(messages, retryTurn));
  scrollToBottom(true);
}

function renderAttachments() {
  const container = document.getElementById('agent-attachments');
  container.innerHTML = '';
  for (const [index, attachment] of state.attachments.entries()) {
    container.append(attachmentChip(attachment, () => {
      state.attachments.splice(index, 1);
      renderAttachments();
      updateSendAvailability(state);
    }));
  }
}

// ---------- todo checklist strip ----------

// fetchTodos loads the todo checklist for the active conversation from the
// backend. A render token is captured so that if the user switches rooms
// while the fetch is in-flight, the stale response is discarded.
async function fetchTodos() {
  if (!state.activeId) {
    state.todos = { items: [], summary: { total: 0, pending: 0, in_progress: 0, completed: 0 } };
    renderTodoStrip();
    return;
  }
  const token = ++state.todoRenderToken;
  try {
    const result = await rpc('agent.todos.get', { conversation_id: state.activeId });
    if (token !== state.todoRenderToken) return; // stale — a newer fetch or room switch won
    state.todos = { items: result.items ?? [], summary: result.summary ?? { total: 0, pending: 0, in_progress: 0, completed: 0 } };
    renderTodoStrip();
  } catch (err) {
    if (token !== state.todoRenderToken) return;
    // Fail-soft: hide the strip rather than crash. The backend may not support
    // todos yet (older version), or the conversation may have been deleted.
    state.todos = { items: [], summary: { total: 0, pending: 0, in_progress: 0, completed: 0 } };
    renderTodoStrip();
  }
}

// bindStripToggles wires the expand/collapse toggle buttons for the todo,
// tool-job, and steer-queue strips. Default is collapsed (list hidden).
// The toggle flips aria-expanded and shows/hides the list element.
function bindStripToggles() {
  const pairs = [
    { toggleId: 'agent-todo-strip-toggle', listId: 'agent-todo-strip-list' },
    { toggleId: 'tool-job-strip-toggle', listId: 'tool-job-list' },
    { toggleId: 'agent-steer-queue-toggle', listId: 'agent-steer-queue-list' },
  ];
  for (const { toggleId, listId } of pairs) {
    const toggle = document.getElementById(toggleId);
    const list = document.getElementById(listId);
    if (!toggle || !list) continue;
    toggle.addEventListener('click', () => {
      const open = toggle.getAttribute('aria-expanded') === 'true';
      toggle.setAttribute('aria-expanded', String(!open));
      list.hidden = open;
    });
  }
}

// renderTodoStrip renders the todo checklist strip from state.todos. It is
// idempotent — safe to call multiple times. The strip is hidden when there
// are no items. Each item gets a status glyph and a delete button. Delete
// buttons are created fresh on each render, so no stale listeners.
// Default state: collapsed (list hidden), expanded only on user click.
function renderTodoStrip() {
  const strip = document.getElementById('agent-todo-strip');
  if (!strip) return;
  const { items, summary } = state.todos;
  if (!items || items.length === 0) {
    strip.hidden = true;
    return;
  }
  strip.hidden = false;
  const countEl = document.getElementById('agent-todo-strip-count');
  const metaEl = document.getElementById('agent-todo-strip-meta');
  const total = items.length;
  if (countEl) countEl.textContent = `${total} Task${total === 1 ? '' : 's'}`;
  if (metaEl) {
    const incomplete = items.filter((i) => i.status !== 'completed').length;
    metaEl.textContent = incomplete === 0 ? 'All done' : `${incomplete} open`;
    metaEl.dataset.done = incomplete === 0 ? 'true' : 'false';
  }
  const list = document.getElementById('agent-todo-strip-list');
  if (!list) return;
  list.replaceChildren(...items.map((item) => renderTodoItem(item, handleTodoDelete)));
}

// handleTodoDelete removes a single todo item by ID. The button is disabled
// during the RPC call to prevent double-clicks. On success, the response
// contains the updated items + summary, so we render directly from it without
// a refetch. On error, the button is re-enabled and a toast is shown.
async function handleTodoDelete(itemId, btn) {
  if (!state.activeId || !itemId) return;
  btn.disabled = true;
  try {
    const result = await rpc('agent.todos.delete', { conversation_id: state.activeId, ids: [itemId] });
    state.todos = { items: result.items ?? [], summary: result.summary ?? { total: 0, pending: 0, in_progress: 0, completed: 0 } };
    renderTodoStrip();
  } catch (err) {
    btn.disabled = false;
    toast(err.message || 'Failed to delete task', 'error');
  }
}

function beginTurn(runId, userText, attachments = []) {
  document.getElementById('stop-btn').hidden = false;
  updateSendAvailability(state);

  const thread = document.getElementById('agent-thread');
  // remove empty state
  const empty = thread.querySelector('.agent-empty');
  if (empty) empty.remove();
  // optimistic user message
  const userMessage = { role: 'user', content: userText, attachments, created_at: new Date().toISOString() };
  state.messages.push(userMessage);
  thread.append(renderMessage(userMessage));
  // assistant placeholder: bubble holds a steps container that will be
  // populated with reasoning/text/tool-strip elements per round, in order
  const bubble = el('div', { class: 'agent-bubble' });
  const msgNode = el('div', { class: 'agent-message assistant agent-pending' }, bubble);
  thread.append(msgNode);
  // first round elements
  const reasoningEl = reasoningDisclosure('');
  reasoningEl.hidden = true;
  const textBox = el('div', { class: 'agent-bubble-text' });
  textBox.append(el('span', { class: 'agent-thinking-dots' },
    el('span'), el('span'), el('span')));
  const strip = el('div', { class: 'agent-tool-stack' });
  strip.hidden = true;
  bubble.append(reasoningEl, textBox, strip);
  state.runs.set(runId, {
    msgNode, bubble, strip, textBox, reasoningEl,
    toolJobs: new Map(), raw: '', rawReasoning: '',
    round: 1, conversationId: state.activeId, runId,
  });
  flushPendingEvents(runId);
  updateComposerStatus();
  scrollToBottom(true);
}

async function retryTurn(failedNode, failedMessageId) {
  if (state.running) return;
  if (!state.activeId) return;
  const model = state.model;
  if (!model) {
    toast('Choose a model first (Models with a provider must be imported in Providers).', 'error');
    return;
  }
  let runId;
  try {
    const res = await rpc('agent.turns.retry', { conversation_id: state.activeId, model, effort: state.effort && state.effort !== 'auto' ? state.effort : undefined });
    runId = res.run_id;
  } catch (err) {
    toast(err.message, 'error');
    return;
  }
  document.getElementById('stop-btn').hidden = false;
  updateSendAvailability(state);

  const thread = document.getElementById('agent-thread');
  // Remove the failed message's error UI (error text + retry button) from
  // the turn node without wiping the entire turn — earlier successful
  // assistant messages in the same turn group must stay visible.
  if (failedMessageId) {
    // Remove the error text and retry button that belong to the failed message.
    failedNode.querySelectorAll('.agent-retry-btn, .agent-error-text').forEach((n) => n.remove());
    // If the turn node has no bubble content left (no prior successful
    // messages), remove the whole node; otherwise keep it and append the
    // new retry bubble as a sibling.
    const bubble = failedNode.querySelector('.agent-bubble');
    if (bubble && bubble.children.length === 0) {
      failedNode.remove();
    } else {
      failedNode.classList.remove('agent-message-error');
    }
  } else {
    // Backward compat: no message ID — remove the whole node.
    failedNode.remove();
  }
  const bubble = el('div', { class: 'agent-bubble' });
  const msgNode = el('div', { class: 'agent-message assistant agent-pending' }, bubble);
  thread.append(msgNode);
  const reasoningEl = reasoningDisclosure('');
  reasoningEl.hidden = true;
  const textBox = el('div', { class: 'agent-bubble-text', text: '…' });
  const strip = el('div', { class: 'agent-tool-stack' });
  strip.hidden = true;
  bubble.append(reasoningEl, textBox, strip);
  state.runs.set(runId, {
    msgNode, bubble, strip, textBox, reasoningEl,
    toolJobs: new Map(), raw: '', rawReasoning: '',
    round: 1, conversationId: state.activeId, runId,
  });
  flushPendingEvents(runId);
  updateComposerStatus();
  scrollToBottom(true);
}

function getRunOrQueue(type, payload) {
  const run = state.runs.get(payload?.run_id);
  if (run || !payload?.run_id) return run;
  const events = state.pendingEvents.get(payload.run_id) || [];
  events.push({ type, payload });
  state.pendingEvents.set(payload.run_id, events.slice(-100));
  setTimeout(() => {
    if (!state.runs.has(payload.run_id)) state.pendingEvents.delete(payload.run_id);
  }, 10000);
  return null;
}

// runForConversation returns the active run for a conversation, if any.
function runForConversation(convId) {
  for (const run of state.runs.values()) {
    if (run.conversationId === convId) return run;
  }
  return null;
}

// saveRoomState persists the per-room state for a conversation so it survives
// room switches. DOM references are NOT saved — they will be stale after
// renderThread. Only serializable/primitive state is saved.
function saveRoomState(id) {
  if (!id) return;
  savedRooms.set(id, {
    pinned: state.pinned,
    steerId: state.steerId,
    steerDraft: state.steerDraft,
    attachments: state.attachments,
    model: state.model,
    effort: state.effort,
  });
}

// loadRoomState restores per-room state for a conversation. Returns true if
// saved state was found and restored. If no saved state exists, model is set
// from the conversation's persisted model or localStorage fallback.
function loadRoomState(id) {
  const saved = savedRooms.get(id);
  state.steerId = null; // always reset; re-shown from saved state if pending
  if (saved) {
    state.pinned = saved.pinned;
    state.steerId = saved.steerId ?? null;
    state.steerDraft = saved.steerDraft;
    state.attachments = saved.attachments;
    state.model = saved.model;
    state.effort = saved.effort || 'auto';
    return true;
  }
  state.pinned = true;
  state.steerId = null;
  state.steerDraft = '';
  state.attachments = [];
  state.effort = 'auto';
  // Model will be set by openConversation from conversation.model
  return false;
}

// clearSavedSteerQueue clears the steer state for a non-active conversation
// (e.g. when its turn ends while the user is viewing another room).
function clearSavedSteerQueue(convId) {
  const saved = savedRooms.get(convId);
  if (saved) {
    saved.steerDraft = '';
  }
}

// wireSteerCancelStrip attaches the click handler to the steer queue strip's
// cancel button. Idempotent — safe to call multiple times.
function wireSteerCancelStrip() {
  const cancelBtn = document.getElementById('agent-steer-cancel');
  if (!cancelBtn || cancelBtn.dataset.wired === '1') return;
  cancelBtn.dataset.wired = '1';
  cancelBtn.addEventListener('click', async () => {
    if (!state.activeId) return;
    try {
      await rpc('agent.turns.cancel-steer', { conversation_id: state.activeId });
      clearSteerQueue();
      const input = document.getElementById('composer-input');
      if (input && !input.value.trim()) {
        input.value = state.steerDraft ?? '';
        state.steerDraft = '';
        input.dispatchEvent(new Event('input'));
      }
      updateSendAvailability(state);
    } catch (error) {
      toast(error.message, 'error');
    }
  });
}

// stopActiveRun stops only the run for the active conversation, not all runs.
async function stopActiveRun() {
  const run = runForConversation(state.activeId);
  if (!run || !run.runId) return;
  try { await rpc('agent.turns.stop', { run_id: run.runId }); } catch { /* ignore */ }
}

function flushPendingEvents(runId) {
  const events = state.pendingEvents.get(runId);
  if (!events?.length) return;
  state.pendingEvents.delete(runId);
  for (const event of events) {
    emit(event.type, event.payload);
  }
}

function clearToolTimers(run) {
  if (!run?.toolJobs) return;
  for (const job of run.toolJobs.values()) {
    if (job._elapsedTimer) {
      clearInterval(job._elapsedTimer);
      job._elapsedTimer = null;
    }
  }
}

function endTurn(runId) {
  const run = state.runs.get(runId);
  if (!run) return;
  const convId = run.conversationId;
  run.msgNode.classList.remove('agent-pending');
  // Clean up transient UI elements (thinking dots, retry banner, tool timers).
  run.textBox?.querySelector('.agent-thinking-dots')?.remove();
  run.bubble?.querySelector('.agent-retry-banner')?.remove();
  clearToolTimers(run);
  state.runs.delete(runId);
  // Clear steer for this conversation (turn ended, unapplied steer is cancelled).
  if (convId === state.activeId) {
    clearSteerQueue();
    if (!state.running) {
      document.getElementById('stop-btn').hidden = true;
    }
    updateSendAvailability(state);
    updateComposerStatus();
  } else {
    clearSavedSteerQueue(convId);
  }
}

// ---------- scroll pinning ----------

// Track whether the user is at the bottom of the thread. While pinned,
// streaming deltas auto-scroll to keep the latest content visible. When the
// user scrolls up, pinning is released so they can read history without being
// yanked back down. Scrolling back to the bottom re-pins.
function bindScrollPin() {
  const thread = document.getElementById('agent-thread');
  if (!thread) return;
  thread.addEventListener('scroll', () => {
    state.pinned = isAtBottom(thread);
    // Lazy-load older chunks when the user scrolls to the top.
    if (thread.scrollTop <= 4) maybeLoadOlderChunk();
  }, { passive: true });
}

function isAtBottom(el) {
  if (!el) return true;
  return el.scrollHeight - el.scrollTop - el.clientHeight <= 4;
}

function scrollToBottom(force = false) {
  const thread = document.getElementById('agent-thread');
  if (!thread || (!force && !state.pinned)) return;
  thread.scrollTop = thread.scrollHeight;
}

// ---- chunk-based lazy load ----

// Count visible "bubbles" in the thread: user messages, assistant messages,
// tool jobs, and reasoning blocks. Used to decide whether the active slice
// is too sparse and an older chunk should be proactively loaded.
function countThreadBubbles() {
  const thread = document.getElementById('agent-thread');
  if (!thread) return 0;
  return thread.querySelectorAll(
    '.agent-message.user, .agent-message.assistant, .agent-tool-terminal, .agent-reasoning',
  ).length;
}

// maybeLoadOlderChunk triggers a chunk load when:
// 1. There are chunks available (nextChunkIndex >= 0)
// 2. No chunk is currently loading
// 3. The thread has fewer than MIN_BUBBLES_FOR_PROACTIVE_LOAD bubbles OR the
//    user has scrolled to the top (handled by the scroll listener)
const MIN_BUBBLES_FOR_PROACTIVE_LOAD = 20;

function maybeLoadOlderChunk() {
  if (state.loadingChunk || state.nextChunkIndex < 0) return;
  if (countThreadBubbles() >= MIN_BUBBLES_FOR_PROACTIVE_LOAD) return;
  loadOlderChunk();
}

async function loadOlderChunk() {
  if (state.loadingChunk || state.nextChunkIndex < 0 || !state.activeId) return;
  state.loadingChunk = true;
  const chunkIndex = state.nextChunkIndex;
  const conversationId = state.activeId;
  const token = state.conversationLoadToken;
  const thread = document.getElementById('agent-thread');
  // Save scroll anchor so we can restore position after prepending.
  const prevHeight = thread?.scrollHeight ?? 0;
  const prevScroll = thread?.scrollTop ?? 0;
  try {
    const result = await rpc('agent.conversations.chunk', { id: conversationId, index: chunkIndex });
    if (token !== state.conversationLoadToken || state.activeId !== conversationId) return;
    const chunkMsgs = result?.messages ?? [];
    if (!chunkMsgs.length) {
      // Empty chunk — treat as no more data.
      state.nextChunkIndex = -1;
      return;
    }
    state.loadedChunks.add(chunkIndex);
    state.nextChunkIndex = chunkIndex - 1;
    // Prepend chunk messages to state.messages and to the DOM thread.
    state.messages = [...chunkMsgs, ...state.messages];
    const fragment = renderConversation(chunkMsgs, retryTurn);
    // Insert a "Load older" sentinel above the chunk so the user can manually
    // trigger the next load if the proactive threshold is not met.
    thread?.insertBefore(fragment, thread.firstChild);
    // Restore scroll position so the user doesn't jump.
    if (thread) {
      const newHeight = thread.scrollHeight;
      thread.scrollTop = prevScroll + (newHeight - prevHeight);
    }
  } catch (err) {
    if (token === state.conversationLoadToken && state.activeId === conversationId) {
      // 404 / no more chunks — stop trying.
      state.nextChunkIndex = -1;
    }
  } finally {
    if (token !== state.conversationLoadToken) {
      return;
    }
    state.loadingChunk = false;
    // If the thread is still sparse after loading, keep going until we hit
    // the threshold or run out of chunks.
    if (state.nextChunkIndex >= 0 && countThreadBubbles() < MIN_BUBBLES_FOR_PROACTIVE_LOAD) {
      loadOlderChunk();
    }
  }
}

function bindEvents() {
  on('agent.turn.started', (payload) => {
    const { run_id, message_id, round, conversation_id } = payload;
    const run = getRunOrQueue('agent.turn.started', payload);
    if (!run) return;
    run.messageId = message_id;
    // round 1: clear the placeholder; round 2+: append new step elements
    // after the previous round's tool strip, preserving temporal order
    if (!round || round <= 1) {
      run.raw = '';
      run.rawReasoning = '';
      run.round = 1;
      if (conversation_id === state.activeId && run.textBox) {
        run.textBox.textContent = '';
        run.textBox.append(el('span', { class: 'agent-thinking-dots' },
          el('span'), el('span'), el('span')));
      }
      return;
    }
    // Track round even when not active so re-attach knows the current round.
    run.round = round;
    run.raw = '';
    run.rawReasoning = '';
    if (conversation_id !== state.activeId) return;
    // seal previous round: hide its tool strip if empty
    if (run.strip && run.strip.hidden) run.strip.remove();
    // create new reasoning + text + strip for this round
    const reasoningEl = reasoningDisclosure('');
    reasoningEl.hidden = true;
    const textBox = el('div', { class: 'agent-bubble-text' });
    textBox.append(el('span', { class: 'agent-thinking-dots' },
      el('span'), el('span'), el('span')));
    const strip = el('div', { class: 'agent-tool-stack' });
    strip.hidden = true;
    run.bubble.append(reasoningEl, textBox, strip);
    run.reasoningEl = reasoningEl;
    run.textBox = textBox;
    run.strip = strip;
    clearToolTimers(run);
    run.toolJobs = new Map();
  });
  on('agent.message.delta', (payload) => {
    const { text, conversation_id } = payload;
    const run = getRunOrQueue('agent.message.delta', payload);
    if (!run) return;
    // Always accumulate raw text so we can re-render on room switch.
    run.raw += text;
    // Only update DOM if this conversation is the active one.
    if (conversation_id !== state.activeId || !run.textBox) return;
    // Remove thinking dots / retry banner on first delta.
    run.textBox.querySelector('.agent-thinking-dots')?.remove();
    const banner = run.bubble.querySelector('.agent-retry-banner');
    if (banner && run.raw) banner.remove();
    run.textBox.innerHTML = renderMarkdown(run.raw);
    scrollToBottom();
  });
  on('agent.reasoning.delta', (payload) => {
    const { text, conversation_id } = payload;
    const run = getRunOrQueue('agent.reasoning.delta', payload);
    if (!run) return;
    run.rawReasoning += text;
    if (conversation_id !== state.activeId) return;
    if (run.reasoningEl) {
      run.reasoningEl.hidden = false;
      // Remove thinking dots when reasoning starts arriving.
      run.textBox?.querySelector('.agent-thinking-dots')?.remove();
      const content = run.reasoningEl.querySelector('.agent-reasoning-content');
      if (content) content.innerHTML = renderMarkdown(run.rawReasoning);
      scrollToBottom();
    }
  });
  on('agent.provider.retry', (payload) => {
    const { attempt, max_attempts, delay_ms, error, conversation_id } = payload;
    const run = getRunOrQueue('agent.provider.retry', payload);
    if (!run) return;
    if (conversation_id !== state.activeId) return;
    // Remove any previous retry banner, then show a new one at the top of the
    // bubble so the user sees the agent is retrying, not stuck.
    run.bubble.querySelector('.agent-retry-banner')?.remove();
    const delaySec = Math.round((delay_ms || 0) / 100) / 10;
    const shortErr = (error || '').replace(/^provider returned HTTP \d+: /, '').slice(0, 120);
    const banner = el('div', { class: 'agent-retry-banner' });
    banner.append(
      el('span', { class: 'agent-retry-banner-icon' }),
      el('span', { text: `Retrying (${attempt}/${max_attempts})${delaySec > 0 ? ` in ${delaySec}s` : ''} — ${shortErr}` }),
    );
    // Insert at the top of the bubble, before reasoning/text/strip.
    run.bubble.prepend(banner);
    // Also clear thinking dots since we're now in retry state, not initial wait.
    run.textBox?.querySelector('.agent-thinking-dots')?.remove();
    scrollToBottom();
  });
  on('agent.tool.started', (payload) => {
    const { tool_call_id, name, args, conversation_id } = payload;
    const run = getRunOrQueue('agent.tool.started', payload);
    if (!run) return;
    if (conversation_id !== state.activeId) return;
    run.strip.hidden = false;
    const job = renderToolJob({ name, args: args ?? {}, status: 'running' });
    run.toolJobs.set(tool_call_id, job);
    run.strip.append(job);
    // Start an elapsed timer so the user can see how long the tool has been
    // running. The timer is stored on the job element and cleared on complete.
    const startTime = Date.now();
    const elapsedEl = job.querySelector('.agent-tool-elapsed') || el('span', { class: 'agent-tool-elapsed', text: '0s' });
    if (!elapsedEl.parentElement) {
      const head = job.querySelector('.agent-tool-job-card-head') || job;
      head.append(elapsedEl);
    }
    job._elapsedTimer = setInterval(() => {
      const secs = Math.floor((Date.now() - startTime) / 1000);
      elapsedEl.textContent = secs < 60 ? `${secs}s` : `${Math.floor(secs / 60)}m ${secs % 60}s`;
    }, 1000);
  });
  on('agent.tool.completed', (payload) => {
    const { tool_call_id, status, output, conversation_id } = payload;
    const run = getRunOrQueue('agent.tool.completed', payload);
    if (!run) return;
    if (conversation_id !== state.activeId) return;
    const job = run.toolJobs.get(tool_call_id);
    if (!job) return;
    // Clear the elapsed timer — the final duration stays displayed.
    if (job._elapsedTimer) { clearInterval(job._elapsedTimer); job._elapsedTimer = null; }
    const next = { name: job.querySelector('.agent-tool-terminal-title')?.textContent, args: job._toolArgs, status: status || 'ok', output };
    setToolTerminalStatus(job, next.status);
    job.open = false;
    const meta = job.querySelector('.agent-tool-terminal-meta');
    if (meta) meta.textContent = toolTerminalMeta(next);
    const outputEl = job.querySelector('.agent-tool-terminal-output');
    if (outputEl) {
      outputEl.classList.toggle('is-error', next.status === 'fail');
      outputEl.textContent = toolTerminalOutput(next);
    }
  });
  on('agent.turn.done', (payload) => {
    const { run_id, message_id, model, usage, error, conversation_id } = payload;
    const run = getRunOrQueue('agent.turn.done', payload);
    if (run && message_id) {
      // refresh the message node with final metadata
      run.msgNode.querySelectorAll('.agent-turn-meta, .agent-message-meta').forEach((n) => n.remove());
      const meta = el('div', { class: 'agent-turn-meta' });
      if (model) meta.append(el('span', { class: 'agent-turn-tag', text: model }));
      if (usage) {
        meta.append(el('span', { class: 'agent-turn-tag', text: `↑${usage.input_tokens ?? 0} ↓${usage.output_tokens ?? 0}` }));
        if (usage.cache_read) meta.append(el('span', { class: 'agent-turn-tag', text: `cache ${usage.cache_read}` }));
      }
      meta.append(el('span', { class: 'agent-message-meta', text: fmtTime(new Date().toISOString()) }));
      run.msgNode.append(meta);
    }
    endTurn(run_id);
    if (error) toast(error, 'error');
    if (conversation_id === state.activeId) refreshActiveConversation();
    refreshConversations();
  });
  on('agent.turn.error', (payload) => {
    const { run_id, message, conversation_id } = payload;
    const run = getRunOrQueue('agent.turn.error', payload);
    if (run) {
      const bubble = run.msgNode.querySelector('.agent-bubble');
      if (bubble) bubble.textContent = message || 'Turn failed';
      run.msgNode.classList.add('agent-message-error');
      // Add retry button directly to the error node so the user can retry
      // without waiting for refreshActiveConversation to re-render.
      const existingRetry = run.msgNode.querySelector('.agent-retry-btn');
      if (!existingRetry) {
        const retryBtn = el('button', { class: 'agent-retry-btn', type: 'button', text: '↻ Retry with model' });
        retryBtn.addEventListener('click', () => retryTurn(run.msgNode));
        run.msgNode.append(retryBtn);
      }
    }
    endTurn(run_id);
    toast(message || 'Turn failed', 'error');
    if (conversation_id === state.activeId) refreshActiveConversation();
  });
  on('agent.compacted', ({ conversation_id, summary }) => {
    if (conversation_id !== state.activeId) return;
    const thread = document.getElementById('agent-thread');
    thread.append(el('div', { class: 'agent-compaction-marker', text: `Compacted · ${summary ? 'context summarized' : 'history trimmed'}` }));
    scrollToBottom();
    refreshActiveConversation();
  });
  on('agent.steer.queued', ({ conversation_id, steer_id, text }) => {
    if (conversation_id !== state.activeId) return;
    showSteerQueued(text, steer_id);
  });
  on('agent.steer.applied', ({ conversation_id, steer_id, text }) => {
    if (conversation_id !== state.activeId) return;
    promoteSteerToTranscript(text);
  });
  on('agent.steer.cancelled', ({ conversation_id, text }) => {
    if (conversation_id !== state.activeId) return;
    clearSteerQueue();
    // Restore the steer text to the composer so the user can re-send it as a
    // new message. The steer was never applied — it was cancelled because the
    // turn ended without reaching a safe boundary to inject it.
    if (text) {
      const input = document.getElementById('composer-input');
      if (input && !input.value.trim()) {
        input.value = text;
        input.dispatchEvent(new Event('input'));
      }
      toast('Steer was not applied (turn ended before a safe boundary). Text restored to composer.', 'info');
    }
  });
  on('agent.todo.updated', (payload) => {
    const { conversation_id, items, summary } = payload;
    // Only update the DOM if this event is for the active conversation.
    // Events for other conversations are dropped — their todos will be
    // fetched fresh when the user switches to them.
    if (conversation_id !== state.activeId) return;
    state.todos = { items: items ?? [], summary: summary ?? { total: 0, pending: 0, in_progress: 0, completed: 0 } };
    renderTodoStrip();
  });
}

function showSteerQueued(text, steerId) {
  // Render the steer as a queue strip above the composer, NOT a bubble in the
  // thread. The steer only becomes a thread bubble once the runner applies it
  // at a safe boundary (promoteSteerToTranscript). Mirrors Electron.
  state.steerDraft = text;
  state.steerId = steerId;
  const root = document.getElementById('agent-steer-queue');
  if (!root) return;
  document.getElementById('agent-steer-queue-text').textContent = text;
  document.getElementById('agent-steer-queue-title').textContent = '1 steer queued';
  document.getElementById('agent-steer-queue-state').textContent = 'Waiting for a safe boundary';
  const cancelBtn = document.getElementById('agent-steer-cancel');
  cancelBtn.hidden = false;
  root.hidden = false;
  wireSteerCancelStrip();
}

function clearSteerQueue() {
  state.steerId = null;
  state.steerDraft = '';
  const root = document.getElementById('agent-steer-queue');
  if (root) root.hidden = true;
  const cancelBtn = document.getElementById('agent-steer-cancel');
  if (cancelBtn) cancelBtn.hidden = true;
}

function promoteSteerToTranscript(text) {
  // The queued steer was applied at a safe boundary — clear the strip and
  // insert the steer as a real user message in the thread, BETWEEN the
  // current assistant bubble (round 1) and the next assistant bubble
  // (round 2+). A new assistant placeholder is created after the steer so
  // the agent.turn.started round 2+ handler appends streaming elements to
  // the new bubble, not the sealed one. Without this, the steer would be
  // appended at the end of the thread (after the streaming assistant) and
  // appear pushed down during live streaming.
  clearSteerQueue();
  const thread = document.getElementById('agent-thread');
  if (!thread) return;
  const steerMessage = { role: 'user', content: text, steer: true, created_at: new Date().toISOString() };
  state.messages.push(steerMessage);
  const steerNode = renderMessage(steerMessage);
  // Find the active run's assistant node. Insert the steer after it, then
  // create a fresh assistant placeholder after the steer for round 2+.
  const run = runForConversation(state.activeId);
  if (run?.msgNode) {
    run.msgNode.after(steerNode);
    // Seal the old bubble: remove empty tool strip, clear pending state.
    run.msgNode.classList.remove('agent-pending');
    if (run.strip && run.strip.hidden) run.strip.remove();
    // Create a new assistant placeholder for the next round.
    const newBubble = el('div', { class: 'agent-bubble' });
    const newMsgNode = el('div', { class: 'agent-message assistant agent-pending' }, newBubble);
    steerNode.after(newMsgNode);
    // Update run references so agent.turn.started round 2+ appends to the
    // new bubble. textBox/reasoningEl/strip will be set by the round 2+
    // handler; clear them here so stale references are not used.
    run.msgNode = newMsgNode;
    run.bubble = newBubble;
    run.textBox = null;
    run.reasoningEl = null;
    run.strip = null;
    clearToolTimers(run);
    run.toolJobs = new Map();
  } else {
    thread.append(steerNode);
  }
  scrollToBottom(true);
}

async function refreshActiveConversation() {
  if (!state.activeId) return;
  const conversationId = state.activeId;
  const token = state.conversationLoadToken;
  try {
    const { conversation, messages } = await rpc('agent.conversations.get', { id: conversationId });
    if (token !== state.conversationLoadToken || state.activeId !== conversationId) return;
    state.conversation = conversation;
    state.messages = messages ?? [];
    // Reset chunk state — compaction may have created new chunks.
    state.chunkCount = conversation?.chunk_count ?? 0;
    state.nextChunkIndex = state.chunkCount - 1;
    state.loadedChunks = new Set();
    state.loadingChunk = false;
    renderThread(state.messages);
    maybeLoadOlderChunk();
    updateComposerStatus();
  } catch (err) {
    // The list refresh will surface a deleted conversation if it raced a turn.
    console.warn('refreshActiveConversation failed:', err?.message || err);
  }
}

// ---------- models ----------

let models = [];

async function refreshModels() {
  try {
    const res = await rpc('ai.models.list');
    models = res.models ?? [];
  } catch {
    models = [];
  }
  if (state.model && models.length && !models.some((model) => `${model.provider_id}:${model.id}` === state.model) && !models.some((model) => model.id === state.model)) {
    state.model = '';
    localStorage.removeItem('nusashell.model');
  }
  updateModelTrigger();
}

function updateModelTrigger() {
  const label = document.getElementById('model-trigger-label');
  const chosen = models.find((m) => `${m.provider_id}:${m.id}` === state.model) || models.find((m) => m.id === state.model);
  const parts = [chosen ? (chosen.display_name || chosen.id) : (state.model || 'No model')];
  if (state.effort && state.effort !== 'auto') parts.push(state.effort);
  label.textContent = parts.join(' · ');
  label.title = chosen ? `${chosen.id} · ${chosen.provider_name}${state.effort && state.effort !== 'auto' ? ` · ${state.effort}` : ''}` : '';
  updateComposerStatus();
}

function selectModel(modelID) {
  state.model = modelID;
  localStorage.setItem('nusashell.model', modelID);
  // Clamp effort to the new model's supported efforts; reset to auto if unsupported.
  // Match by qualified ID (provider_id:model_id) or bare ID for backward compat.
  const chosen = models.find((m) => `${m.provider_id}:${m.id}` === modelID) || models.find((m) => m.id === modelID);
  if (chosen) {
    const supported = chosen.supported_efforts || [];
    if (state.effort !== 'auto' && supported.length && !supported.includes(state.effort)) {
      state.effort = chosen.default_effort && supported.includes(chosen.default_effort) ? chosen.default_effort : 'auto';
    } else if (state.effort !== 'auto' && !supported.length) {
      state.effort = 'auto';
    }
  }
  updateModelTrigger();
}

function selectEffort(effort) {
  state.effort = effort || 'auto';
  updateModelTrigger();
}

export async function openConversationExternal(id) {
  if (backendIsOffline()) return;
  await refreshConversations();
  await openConversation(id);
}

// ---------- status ----------

async function refreshStatus() {
  try {
    const { settings } = await rpc('settings.get');
    state.settings = settings ?? {};
  } catch {
    /* app not ready */
  }
  updateComposerStatus();
}

function updateComposerStatus() {
  const workspace = state.conversation?.workspace || '';
  const workspaceLabel = document.getElementById('agent-workspace-label');
  const workspaceButton = document.getElementById('agent-workspace-btn');
  workspaceLabel.textContent = workspace ? workspace.split(/[\\/]/).filter(Boolean).pop() : 'Home';
  workspaceButton.title = workspace || 'Home (user home directory)';

  const status = document.getElementById('agent-provider-status');
  if (state.running || state.conversation?.status === 'running') {
    status.textContent = 'Running…';
    status.classList.add('is-running');
    document.getElementById('stop-btn').hidden = false;
    return;
  }
  status.classList.remove('is-running');
  const chosen = models.find((model) => `${model.provider_id}:${model.id}` === state.model) || models.find((model) => model.id === state.model);
  if (!chosen) {
    status.textContent = 'Choose a model';
    return;
  }
  const contextWindow = effectiveContextWindow(Number(chosen.context) || 0, Number(state.settings.max_input_tokens) || 0);
  status.textContent = formatContextUsage(estimateContextTokens(state.messages), contextWindow);
}
