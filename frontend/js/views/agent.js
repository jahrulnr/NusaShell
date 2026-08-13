// Agent workspace: multi-conversation chat with streaming turns.

import { rpc, on, emit } from '../rpc.js';
import { el, fmtTime, toast, confirmDialog, debounce } from '../ui.js';
import { renderMarkdown } from '../markdown.js';
import { estimateContextTokens, formatContextUsage, effectiveContextWindow } from '../agent-ui.js';
import { bindComposer, updateSendAvailability } from './agent/composer.js';
import { bindModelPicker } from './agent/model-picker.js';
import {
  attachmentChip,
  reasoningDisclosure,
  renderEmptyThread,
  renderConversation,
  renderMessage,
  renderToolJob,
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
  steerNode: null, // DOM node for the pending steer bubble (per-room, saved/restored)
  steerDraft: '', // text of pending steer (per-room, saved/restored)
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
    state.steerNode = null;
    state.steerDraft = '';
    state.attachments = [];
    await refreshConversations();
    renderEmptyThread();
    renderAttachments();
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
          state.steerNode = null;
          state.steerDraft = '';
          renderEmptyThread();
          renderAttachments();
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
  state.activeId = id;
  // the backend returns messages as a sibling of conversation
  const { conversation, messages } = await rpc('agent.conversations.get', { id });
  state.conversation = conversation;
  state.messages = messages ?? [];
  // Restore per-room state (pinned, steerDraft, attachments, model).
  // If no saved state exists, defaults are applied.
  const hasSaved = loadRoomState(id);
  if (!hasSaved) {
    const requestedModel = conversation.model || localStorage.getItem('nusashell.model') || '';
    state.model = models.length && requestedModel && !models.some((model) => model.id === requestedModel) ? '' : requestedModel;
    state.effort = conversation.effort || 'auto';
  }
  renderConversationList();
  renderThread(state.messages);
  // Re-wire steer cancel button if there's a pending steer in the rendered thread.
  const steerWaiting = state.messages.find((m) => m.steerWaiting);
  if (steerWaiting) {
    const userMessages = document.getElementById('agent-thread').querySelectorAll('.agent-message.user.agent-steer');
    state.steerNode = userMessages[userMessages.length - 1] ?? null;
    wireSteerCancel(state.steerNode);
  }
  // If there's an active run for this conversation (e.g. user switched away
  // and came back), re-attach the streaming UI to the rendered thread so
  // live deltas continue updating the visible DOM.
  reattachActiveRun();
  // Re-render attachment chips for the restored attachments.
  renderAttachments();
  updateModelTrigger();
  updateComposerStatus();
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

async function retryTurn(failedNode) {
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
  failedNode.remove();
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
// room switches. DOM references (steerNode) are NOT saved — they will be
// stale after renderThread. Only serializable/primitive state is saved.
function saveRoomState(id) {
  if (!id) return;
  savedRooms.set(id, {
    pinned: state.pinned,
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
  state.steerNode = null; // always reset; re-wired after renderThread if needed
  if (saved) {
    state.pinned = saved.pinned;
    state.steerDraft = saved.steerDraft;
    state.attachments = saved.attachments;
    state.model = saved.model;
    state.effort = saved.effort || 'auto';
    return true;
  }
  state.pinned = true;
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

// wireSteerCancel attaches the click handler to a steer bubble's cancel button.
// Used both by showSteerQueued (live) and after renderThread (room switch).
function wireSteerCancel(node) {
  if (!node) return;
  const cancelBtn = node.querySelector('.agent-steer-cancel-inline');
  if (!cancelBtn) return;
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

function endTurn(runId) {
  const run = state.runs.get(runId);
  if (!run) return;
  const convId = run.conversationId;
  run.msgNode.classList.remove('agent-pending');
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
      if (conversation_id === state.activeId && run.textBox) run.textBox.textContent = '';
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
    const strip = el('div', { class: 'agent-tool-stack' });
    strip.hidden = true;
    run.bubble.append(reasoningEl, textBox, strip);
    run.reasoningEl = reasoningEl;
    run.textBox = textBox;
    run.strip = strip;
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
      const content = run.reasoningEl.querySelector('.agent-reasoning-content');
      if (content) content.innerHTML = renderMarkdown(run.rawReasoning);
      scrollToBottom();
    }
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
  });
  on('agent.tool.completed', (payload) => {
    const { tool_call_id, status, output, conversation_id } = payload;
    const run = getRunOrQueue('agent.tool.completed', payload);
    if (!run) return;
    if (conversation_id !== state.activeId) return;
    const job = run.toolJobs.get(tool_call_id);
    if (!job) return;
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
  on('agent.steer.cancelled', ({ conversation_id }) => {
    if (conversation_id !== state.activeId) return;
    clearSteerQueue();
  });
}

function showSteerQueued(text, steerId) {
  const thread = document.getElementById('agent-thread');
  const steerMessage = { role: 'user', content: text, steer: true, steerWaiting: true, created_at: new Date().toISOString() };
  state.messages.push(steerMessage);
  const node = renderMessage(steerMessage);
  state.steerNode = node;
  thread.append(node);
  wireSteerCancel(node);
  scrollToBottom(true);
}

function clearSteerQueue() {
  if (state.steerNode) {
    state.steerNode.remove();
    state.steerNode = null;
  }
  // remove from state.messages
  const idx = state.messages.findIndex((m) => m.steerWaiting);
  if (idx >= 0) state.messages.splice(idx, 1);
  state.steerDraft = '';
}

function promoteSteerToTranscript(text) {
  if (state.steerNode) {
    // Replace the waiting bubble with a normal steer message
    const thread = document.getElementById('agent-thread');
    const steerMessage = { role: 'user', content: text, steer: true, created_at: new Date().toISOString() };
    // update state.messages: replace the waiting entry
    const idx = state.messages.findIndex((m) => m.steerWaiting);
    if (idx >= 0) state.messages[idx] = steerMessage;
    else state.messages.push(steerMessage);
    const newNode = renderMessage(steerMessage);
    state.steerNode.replaceWith(newNode);
    state.steerNode = null;
    scrollToBottom(true);
  } else {
    // Steer was applied but we don't have a pending node (e.g. page refresh)
    const thread = document.getElementById('agent-thread');
    const steerMessage = { role: 'user', content: text, steer: true, created_at: new Date().toISOString() };
    state.messages.push(steerMessage);
    thread.append(renderMessage(steerMessage));
    scrollToBottom(true);
  }
}

async function refreshActiveConversation() {
  if (!state.activeId) return;
  try {
    const { conversation, messages } = await rpc('agent.conversations.get', { id: state.activeId });
    state.conversation = conversation;
    state.messages = messages ?? [];
    renderThread(state.messages);
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
  if (state.model && models.length && !models.some((model) => model.id === state.model)) {
    state.model = '';
    localStorage.removeItem('nusashell.model');
  }
  updateModelTrigger();
}

function updateModelTrigger() {
  const label = document.getElementById('model-trigger-label');
  const chosen = models.find((m) => m.id === state.model);
  const parts = [chosen ? chosen.id : (state.model || 'No model')];
  if (state.effort && state.effort !== 'auto') parts.push(state.effort);
  label.textContent = parts.join(' · ');
  label.title = chosen ? `${chosen.id} · ${chosen.provider_name}${state.effort && state.effort !== 'auto' ? ` · ${state.effort}` : ''}` : '';
  updateComposerStatus();
}

function selectModel(modelID) {
  state.model = modelID;
  localStorage.setItem('nusashell.model', modelID);
  // Clamp effort to the new model's supported efforts; reset to auto if unsupported.
  const chosen = models.find((m) => m.id === modelID);
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
  if (state.running) {
    status.textContent = 'Running…';
    return;
  }
  const chosen = models.find((model) => model.id === state.model);
  if (!chosen) {
    status.textContent = 'Choose a model';
    return;
  }
  const contextWindow = effectiveContextWindow(Number(chosen.context) || 0, Number(state.settings.max_input_tokens) || 0);
  status.textContent = formatContextUsage(estimateContextTokens(state.messages), contextWindow);
}
