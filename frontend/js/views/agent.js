// Agent workspace: multi-conversation chat with streaming turns.

import { rpc, on, emit } from '../rpc.js';
import { el, fmtTime, toast, confirmDialog, debounce } from '../ui.js';
import { renderMarkdown } from '../markdown.js';
import { incrementalRender } from '../incremental-render.js';
import { estimateContextTokens, formatContextUsage, effectiveContextWindow, previousWindowStart, conversationTail } from '../agent-ui.js';
import { bindComposer, updateSendAvailability } from './agent/composer.js';
import { bindModelPicker } from './agent/model-picker.js';
import { bindRoomInfo, updateRoomInfo } from './agent/room-info.js';
import {
  attachmentChip,
  formatTokens,
  mountLiveRound,
  setReasoningSource,
  reasoningHasVisibleSource,
  renderEmptyThread,
  bindStarterPrompts,
  renderConversation,
  renderMessage,
  renderToolJob,
  renderToolCallCard,
  renderSubagentCard,
  renderGenerateImageCard,
  renderTodoItem,
  setToolTerminalStatus,
  toolTerminalMeta,
  toolTerminalOutput,
  appendToolJobDelta,
  appendLiveError,
  bindToolStop,
  isStreamingTool,
  parseShowImageOutput,
  parseShowAudioOutput,
  parseShowVideoOutput,
} from './agent/render.js';
import { bindSubagents, setSubagentConversation } from './agent/subagents.js';
import { agentThread, composerInput, stopButton, attachmentsContainer, workspaceButton, workspaceLabel, providerStatus } from './agent/domrefs.js';
import { renderMermaidDiagrams } from '../mermaid-render.js';
import { highlightCode } from '../highlight-render.js';
import { attachZoomButtons } from '../media-zoom.js';
import { renderArtifactCard, parseArtifactOutput } from '../artifact-render.js';
import { createAskCard, sealAskCard, cancelAskCard } from './ask-card.js';
import { playComplete, playError } from '../sounds.js';

// placeToolCard appends a tool card to the right container: standalone cards
// (ask_question, show, generate_*, artifact, subagent — anything with
// dataset.standalone="true") go directly into the bubble so they render
// without the .agent-tool-stack border-left lane; tool terminals go into
// the strip so the border-left visual cue groups them.
function placeToolCard(bubble, strip, card) {
  if (card.dataset.standalone === 'true') {
    (strip?.parentElement || bubble).append(card);
  } else {
    strip.append(card);
    strip.hidden = false;
  }
}

// swapToolCard replaces an existing card with a new one. When the new card
// is standalone but the old one was a terminal inside the strip, the new
// card moves to the bubble (not stay in the strip with border-left).
function swapToolCard(oldCard, newCard, bubble, strip) {
  if (oldCard) {
    if (newCard.dataset.standalone === 'true' && oldCard.parentElement === strip) {
      oldCard.replaceWith(newCard);
      bubble.append(newCard); // move from strip to bubble
    } else {
      oldCard.replaceWith(newCard);
    }
  } else {
    placeToolCard(bubble, strip, newCard);
  }
}

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
  contextEstimate: 0, // backend context tokens for the active room (server SoT: live estimate during a turn, provider-measured after)
  // Chunk-based lazy load: track how many pre-compaction chunks are available
  // (from the backend) and which chunk index to load next (descending from
  // ChunkCount-1 toward 0). loadedChunks prevents duplicate loads.
  chunkCount: 0,
  nextChunkIndex: -1,
  loadedChunks: new Set(),
  loadingChunk: false,
  // Active-message windowing: only the last INITIAL_WINDOW messages are
  // rendered on open; activeWindowStart is the index of the first rendered
  // active message. Older active messages are prepended in WINDOW_BATCH-sized
  // batches on scroll-up (before falling back to archived chunks).
  activeWindowStart: 0,
  assistKeepStart: 0,
  // While a room is opening we scroll to the bottom; suppress the
  // scroll-to-top "load older" trigger until that settles so the initial
  // scrollTop≈0 is not mistaken for the user scrolling up (which would strand
  // the view mid-conversation and churn the DOM).
  suppressTopLoad: false,
  loadingActiveBatch: false,
  // Todo checklist: server-authoritative via agent.todo.updated events and
  // agent.todos.get RPC. todoRenderToken guards against stale async renders
  // (e.g. a fetch that resolves after the user switched to another room).
  todos: { items: [], summary: { total: 0, pending: 0, in_progress: 0, completed: 0 } },
  todoRenderToken: 0,
  conversationLoadToken: 0,
  // Per-room live run buffers. While the user is looking at another room, the
  // deltas for this room keep arriving over the same WebSocket; they are
  // mirrored here (raw text, reasoning, tool jobs) so switching back renders
  // a stable live stream instead of a blank turn. Text and tool state are
  // capped to keep memory bounded. `lastEventAt` powers the sidebar indicator.
  roomBuffers: new Map(), // conversationId -> RoomStreamBuffer
};

// Snapshot-history windowing: how many trailing assistant rounds a full
// thread re-render keeps visible before the explicit "Load older" affordance
// (agent-layout.test.mjs pins this). Kept generous — 12 covers a whole long
// turn at first paint — because per-delta cost is already bounded by
// targeted enhancement + block-diffed markdown (see render.js's strategy
// comment); this window only bounds the one-time markdown parse + DOM
// insert on open. (Live turns never trim.)
const SNAPSHOT_KEEP_ROUNDS = 12;
const MAX_ROOM_BUFFER_CHARS = 512 * 1024; // per room (raw + rawReasoning)
const MAX_LIVE_ROUND_CHARS = 512 * 1024;
const MAX_LIVE_TOOL_JOBS = 128;

function appendBoundedLiveText(target, field, text) {
  if (!target || !text) return;
  const value = String(text);
  const current = String(target[field] || '');
  if (value.length >= MAX_LIVE_ROUND_CHARS) {
    target[field] = value.slice(-MAX_LIVE_ROUND_CHARS);
    return;
  }
  const next = current + value;
  if (next.length <= MAX_LIVE_ROUND_CHARS) {
    target[field] = next;
    return;
  }
  target[field] = next.slice(-MAX_LIVE_ROUND_CHARS);
}

function trimRoomBuffer(buffer) {
  if (!buffer) return;
  let excess = buffer.raw.length + buffer.rawReasoning.length - MAX_ROOM_BUFFER_CHARS;
  if (excess <= 0) return;
  if (buffer.raw.length >= excess) {
    buffer.raw = buffer.raw.slice(excess);
    return;
  }
  excess -= buffer.raw.length;
  buffer.raw = '';
  buffer.rawReasoning = buffer.rawReasoning.slice(excess);
}

function setLiveToolJob(toolJobs, toolCallID, job) {
  if (!toolJobs || !job) return;
  const previous = toolJobs.get(toolCallID);
  if (previous && previous !== job) {
    if (previous._elapsedTimer) clearInterval(previous._elapsedTimer);
    previous.remove?.();
  }
  toolJobs.set(toolCallID, job);
  while (toolJobs.size > MAX_LIVE_TOOL_JOBS) {
    const oldestID = toolJobs.keys().next().value;
    const oldest = toolJobs.get(oldestID);
    if (oldest?._elapsedTimer) clearInterval(oldest._elapsedTimer);
    oldest?.remove?.();
    toolJobs.delete(oldestID);
  }
}

function resetLiveRoundText(run) {
  if (!run) return;
  run.raw = '';
  run.rawReasoning = '';
}

// Active-message windowing sizes (messages, not turns). INITIAL_WINDOW keeps
// the first paint of a long conversation cheap; WINDOW_BATCH is revealed per
// scroll-to-top before archived chunks load.
const INITIAL_WINDOW = 60;
const WINDOW_BATCH = 30;

// windowedActiveMessages returns the currently-rendered slice: older complete
// turns from activeWindowStart, plus the trailing assistant-run tail from
// assistKeepStart, so a 50-round turn cannot hide the last user bubble.
function windowedActiveMessages() {
  return conversationTail(state.messages, {
    prefixStart: state.activeWindowStart,
    assistKeepStart: state.assistKeepStart,
    keepRounds: SNAPSHOT_KEEP_ROUNDS,
  }).visible;
}

function applyConversationTail() {
  const tail = conversationTail(state.messages, {
    prefixWindow: INITIAL_WINDOW,
    keepRounds: SNAPSHOT_KEEP_ROUNDS,
  });
  state.activeWindowStart = tail.prefixStart;
  state.assistKeepStart = tail.assistKeepStart;
}

function trailingRunStart() {
  return conversationTail(state.messages, {
    prefixStart: state.activeWindowStart,
    assistKeepStart: state.assistKeepStart,
    keepRounds: SNAPSHOT_KEEP_ROUNDS,
  }).runStart;
}

// Per-room state that survives conversation switches. When the user switches
// away from a conversation, its per-room state is saved here. When they switch
// back, it's restored. This prevents state from one room leaking into another.
const savedRooms = new Map(); // conversationId -> { pinned, steerDraft, attachments, model }

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
  bindStarterPrompts();
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
  bindSubagents({ getActiveConversationId: () => state.activeId });
  bindEvents();
  bindScrollPin();
  window.addEventListener('nusashell:preferred-model', (event) => {
    selectModel(event.detail?.model || '');
  });
  bindBackendAvailability();
  // A dead backend is covered by the full-window offline screen
  // (js/offline-screen.js); the agent view only loads data once connected.
  if (backendIsOffline()) return;
  await loadAgentData();
}

function bindBackendAvailability() {
  window.addEventListener('nusashell:connection-status', (event) => {
    if (event.detail?.status === 'open') void loadAgentData();
  });
}

function backendIsOffline() {
  return isOfflineStatus(document.documentElement.dataset.backendStatus);
}

function isOfflineStatus(status) {
  return status === 'offline' || status === 'closed' || status === 'error';
}

let agentDataLoading;
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
  } catch (err) {
    console.error('agent data load failed:', err);
  } finally {
    agentDataLoading = false;
  }
}

// ---------- conversations ----------

function deriveTitle(messages, fallback = 'Untitled') {
  // Use the first non-empty user message as the conversation title.
  for (const message of messages ?? []) {
    if (message?.role === 'user' && message.content?.trim()) {
      const text = String(message.content).trim().replace(/\s+/g, ' ');
      return text.length > 60 ? `${text.slice(0, 60)}…` : text;
    }
  }
  return fallback;
}

async function maybeAutoTitleConversation(conversationId) {
  if (!conversationId) return;
  const conv = state.conversations.find((c) => c.id === conversationId);
  if (!conv || (conv.title && conv.title !== 'Untitled')) return;
  let messages = state.messages;
  if (state.activeId !== conversationId) {
    try {
      const gotten = await rpc('agent.conversations.get', { id: conversationId });
      messages = gotten?.messages ?? [];
    } catch {
      return;
    }
  }
  const title = deriveTitle(messages);
  if (!title || title === 'Untitled') return;
  try {
    await rpc('agent.conversations.rename', { id: conversationId, title });
    const target = state.conversations.find((c) => c.id === conversationId);
    if (target) target.title = title;
    renderConversationList();
  } catch {
    // Title is cosmetic — never fail the turn on it.
  }
}

async function refreshConversations() {
  const { conversations } = await rpc('agent.conversations.list');
  state.conversations = (conversations ?? []).filter((c) => Boolean(c.id));
  renderConversationList();
  const count = conversations.length;
  document.getElementById('conversation-count').textContent = `${count} thread${count === 1 ? '' : 's'}`;
}

function setRoomsOpen(open) {
  const shell = document.getElementById('agent-shell');
  const toggle = document.getElementById('agent-rooms-toggle');
  const backdrop = document.getElementById('agent-rooms-backdrop');
  if (!shell) return;
  shell.classList.toggle('is-rooms-open', open);
  toggle?.setAttribute('aria-expanded', String(open));
  if (backdrop) backdrop.hidden = !open;
}

function bindConversations() {
  document.getElementById('conversation-search').addEventListener('input', debounce(renderConversationList, 150));
  document.getElementById('new-conversation-btn').addEventListener('click', () => createConversation());
  const toggle = document.getElementById('agent-rooms-toggle');
  const backdrop = document.getElementById('agent-rooms-backdrop');
  toggle?.addEventListener('click', () => {
    const shell = document.getElementById('agent-shell');
    setRoomsOpen(!shell?.classList.contains('is-rooms-open'));
  });
  backdrop?.addEventListener('click', () => setRoomsOpen(false));
  // Event delegation for the conversation list: one listener at the
  // container level instead of per-item listeners. Rows are patched in
  // place by renderConversationList, so a click can never race against a
  // listener that was detached by a re-render (click-to-switch-room bug).
  document.getElementById('conversation-list').addEventListener('click', (event) => {
    const item = event.target.closest('.agent-conversation-item');
    if (!item) return;
    const id = item.dataset?.conversationId;
    if (!id) return;
    if (event.target.closest('.agent-conversation-delete')) {
      event.stopPropagation();
      void deleteConversation(id);
      return;
    }
    if (event.target.closest('.agent-conversation-open')) {
      void openConversation(id);
    }
  });
  document.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return;
    if (!document.getElementById('agent-shell')?.classList.contains('is-rooms-open')) return;
    event.preventDefault();
    setRoomsOpen(false);
  });
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
    state.todos = { items: [], summary: { total: 0, pending: 0, in_progress: 0, completed: 0 }, brief: '' };
    state.todoRenderToken++;
    await refreshConversations();
    renderEmptyThread();
    renderAttachments();
    renderTodoStrip();
    updateComposerStatus();
    updateSendAvailability(state);
    composerInput().focus();
    setRoomsOpen(false);
  } catch (err) {
    toast(err.message, 'error');
  }
}

function renderConversationList() {
  const list = document.getElementById('conversation-list');
  const query = document.getElementById('conversation-search').value.toLowerCase();
  const filtered = state.conversations.filter((c) => (c.title || '').toLowerCase().includes(query));
  if (!filtered.length) {
    if (list.childElementCount === 1 && list.firstElementChild?.classList.contains('agent-conversation-empty')) return;
    list.replaceChildren(el('div', { class: 'agent-conversation-empty', text: query ? 'No conversations with this title.' : 'No conversations yet.' }));
    return;
  }
  const existing = new Map();
  for (const node of list.children) {
    const id = node.dataset?.conversationId;
    if (id) existing.set(id, node);
  }
  let prev = null;
  const remove = new Set(existing.keys());
  for (const c of filtered) {
    const node = existing.get(c.id) ?? buildConversationItem(c);
    const item = updateConversationItem(node, c);
    if (item.parentNode !== list) {
      if (prev) prev.after(item);
      else list.prepend(item);
    }
    remove.delete(c.id);
    prev = item;
  }
  for (const id of remove) existing.get(id)?.remove();
}

// buildConversationItem creates a conversation row skeleton. Rows use a flat
// data-conversation-id hook instead of per-item listeners: clicks are handled
// once at the list level (event delegation) so a rapid re-render can never
// race a click against a listener that was just detached.
function buildConversationItem(c) {
  return el('div', {
    class: 'agent-conversation-item' + (c.id === state.activeId ? ' is-active' : ''),
    role: 'listitem',
    'data-conversation-id': c.id,
  },
    el('button', { class: 'agent-conversation-open', title: c.title },
      el('span', { class: 'agent-conversation-title', text: c.title || 'Untitled' }),
      el('span', { class: 'agent-conversation-time', text: String(c.message_count ?? 0) + ' msgs · ' + fmtTime(c.updated_at) }),
    ),
    el('button', { class: 'agent-conversation-delete', title: 'Delete conversation', 'aria-label': 'Delete conversation' }, '✕'),
  );
}

// updateConversationItem patches an existing row in place (same DOM node, no
// destroy+rebuild) and only returns a fresh node when the live dot needs to
// move between the text span and the status span.
function updateConversationItem(item, c) {
  const openBtn = item.querySelector('.agent-conversation-open');
  if (openBtn) openBtn.title = c.title;
  const title = item.querySelector('.agent-conversation-title');
  if (title && title.textContent !== (c.title || 'Untitled')) title.textContent = c.title || 'Untitled';
  const time = item.querySelector('.agent-conversation-time');
  const timeText = String(c.message_count ?? 0) + ' msgs · ' + fmtTime(c.updated_at);
  if (time && time.textContent !== timeText) time.textContent = timeText;
  item.classList.toggle('is-active', c.id === state.activeId);
  const buffer = state.roomBuffers.get(c.id);
  // Only rooms that are not the active one and not terminal get the live
  // dot. The dot signals "this room is still streaming in the background".
  const isLive = Boolean(
    buffer && !buffer.done && c.id !== state.activeId && buffer.lastEventAt > 0,
  );
  item.classList.toggle('is-running', isLive);
  const dot = item.querySelector('.agent-conversation-dot');
  const wantDot = Boolean(isLive);
  if (dot && !wantDot) dot.remove();
  if (!dot && wantDot) {
    const textSpan = item.querySelector('.agent-conversation-time');
    const dotEl = el('span', { class: 'agent-conversation-dot', 'aria-hidden': 'true' });
    if (textSpan) textSpan.append(dotEl);
    else item.append(dotEl);
  }
  return item;
}
// deleteConversation removes a conversation via RPC and resets the active
// room when the deleted conversation was the one being viewed. Extracted from
// the old per-item delete listener so the delegated list handler stays small.
async function deleteConversation(id) {
  const conv = state.conversations.find((c) => c.id === id);
  const ok = await confirmDialog('Delete conversation', '"' + (conv?.title || 'Untitled') + '" and all of its messages will be removed.', 'Delete');
  if (!ok) return;
  try {
    await rpc('agent.conversations.delete', { id });
    savedRooms.delete(id);
    state.roomBuffers.delete(id);
    if (state.activeId === id) {
      state.activeId = null;
      state.conversation = null;
      state.messages = [];
      state.attachments = [];
      state.steerId = null;
      state.steerDraft = '';
      state.contextEstimate = 0;
      state.todos = { items: [], summary: { total: 0, pending: 0, in_progress: 0, completed: 0 }, brief: '' };
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
}

async function openConversation(id) {
  if (!id) {
    console.warn('openConversation called without an id; ignoring');
    return;
  }
  setRoomsOpen(false);
  // Save per-room state for the current conversation before switching.
  saveRoomState(state.activeId);
  const token = ++state.conversationLoadToken;
  state.activeId = id;
  setSubagentConversation(id);
  // the backend returns messages as a sibling of conversation
  const { conversation, messages } = await rpc('agent.conversations.get', { id });
  if (token !== state.conversationLoadToken) return;
  state.conversation = conversation;
  state.messages = messages ?? [];
  // Seal a room buffer the backend says is finished (its terminal event was
  // missed or raced). Must happen before any buffer-driven rendering below.
  sealStaleRoomBuffer(id, conversation?.status);
  // Seed the context badge from the backend for this room (provider-measured
  // fill preferred, else server heuristic). Resetting here prevents another
  // room's number from leaking across a switch.
  state.contextEstimate = Number(conversation?.context_tokens) || Number(conversation?.estimated_tokens) || 0;
  // Only render the most recent INITIAL_WINDOW messages on open; older active
  // messages are revealed on scroll-up. Keeps opening a long conversation fast.
  applyConversationTail();
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
  renderThread(windowedActiveMessages(), true);
  // Archived chunks stay on disk until the user asks for them (Load older
  // or scroll-to-top). Auto-loading after open/compaction re-inflated the
  // just-archived long turn into the DOM and froze the thread.
  // If there's an active run for this conversation (e.g. user switched away
  // and came back, or page was refreshed while a turn was running), re-attach
  // the streaming UI to the rendered thread so live deltas continue updating
  // the visible DOM. After a page refresh, state.runs is empty, so query the
  // backend for the active run first.
  await reattachActiveRunFromBackend();
  if (token !== state.conversationLoadToken) return;
  reattachActiveRun();
  // Merge buffered deltas only when no run was re-attached from the backend:
  // reattachActiveRun already rendered the live buffer (seeded from
  // reattachActiveRunFromBackend), so a second apply here would detach the
  // very nodes the streaming handlers keep updating.
  if (!runForConversation(state.activeId)) {
    applyBufferedRunToDOM(state.activeId);
  }
  // The buffered run (if any) is still a live stream; treat it as the active
  // run again so subsequent deltas keep the DOM in sync. Only re-attach for
  // a LIVE buffer (not the terminal done buffer); a done buffer is already
  // fully rendered and re-registering it would leave a phantom run in
  // state.runs with no future turn.done to clean it up (composer stuck in
  // steer mode).
  const buffer = state.roomBuffers.get(state.activeId);
  if (buffer && !buffer.done && !runForConversation(state.activeId)) {
    state.runs.set(buffer.runId, {
      msgNode: buffer.msgNode,
      bubble: buffer.bubble,
      strip: buffer.strip,
      textBox: buffer.textBox,
      reasoningEl: buffer.reasoningEl,
      toolJobs: buffer.toolJobs,
      raw: buffer.raw,
      rawReasoning: buffer.rawReasoning,
      round: buffer.round,
      conversationId: state.activeId,
      runId: buffer.runId,
      messageId: buffer.messageId,
    });
  }
  const attachedRun = runForConversation(state.activeId);
  if (attachedRun) flushPendingEvents(attachedRun.runId);
  expireCompletedBuffers();
  renderConversationList();
  // Restore the steer queue strip if a steer is still pending for this room.
  // The strip is composer chrome, not part of the thread — it is re-shown
  // from saved per-room state rather than re-rendered from messages.
  if (state.steerDraft && runForConversation(state.activeId)) {
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
  updateRoomInfo(state.conversation, state.messages);
  // Rebuild interactive ask_question cards for asks still pending on this
  // conversation's run (room switch / reload missed the live event; the
  // persisted tool call would otherwise render as a dead sealed card).
  void restorePendingAsks(state.activeId, token);
}

// restorePendingAsks queries the backend for in-flight ask_question calls on the
// active conversation and rebuilds interactive cards, replacing any sealed
// placeholder rendered from the persisted (unanswered) tool call.
async function restorePendingAsks(conversationId, token) {
  const run = runForConversation(conversationId);
  if (!run) return; // no active run → any ask is already resolved
  let asks = [];
  try {
    const res = await rpc('agent.ask.pending', { conversation_id: conversationId });
    asks = res.asks ?? [];
  } catch {
    return;
  }
  if (token !== state.conversationLoadToken || state.activeId !== conversationId) return;
  if (!asks.length) return;
  const cards = [...document.querySelectorAll('#agent-thread .agent-ask-card')];
  for (const a of asks) {
    const runId = a.run_id || run.runId;
    const card = createAskCard(a.tool_call_id, {
      question: a.question,
      options: a.options,
      allow_free_text: a.allow_free_text,
      multi_select: a.multi_select,
    }, {
      runId,
      onSubmit: (err) => toast(err instanceof Error ? err.message : 'Could not send answer', 'error'),
      onStop: () => rpc('agent.turns.stop', { run_id: runId }).catch(() => {}),
    });
    const existing = cards.find((c) => c.dataset.callId === a.tool_call_id);
    if (existing) existing.replaceWith(card);
    else if (run.strip) {
      placeToolCard(run.bubble, run.strip, card);
    }
    setLiveToolJob(run.toolJobs, a.tool_call_id, card);
  }
  scrollToBottom();
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
  // Register a run entry. reattachActiveRun will populate the DOM
  // references by replacing the last assistant message node. When a live
  // room buffer already holds the streamed deltas for this run, seed the
  // entry from it so the re-attached DOM shows the accumulated live tail,
  // not just the deltas that arrive after the switch.
  const liveBuffer = state.roomBuffers.get(conversationId);
  // A capped buffer is still usable: it retains the newest MAX_ROOM_BUFFER_CHARS
  // of the stream, which beats seeding an empty entry (the old behavior made
  // the UI look like it "pulled only the latest delta" after switch-back).
  const hasLive = liveBuffer && !liveBuffer.done && liveBuffer.lastEventAt > 0;
  state.runs.set(active.run_id, {
    msgNode: null, bubble: null, strip: null, textBox: null, reasoningEl: null,
    toolJobs: hasLive ? liveBuffer.toolJobs : new Map(),
    raw: hasLive ? liveBuffer.raw : '',
    rawReasoning: hasLive ? liveBuffer.rawReasoning : '',
    round: hasLive ? (liveBuffer.round || 1) : 1,
    conversationId: conversationId, runId: active.run_id,
    messageId: hasLive ? (liveBuffer.messageId || active.message_id) : active.message_id,
  });
  if (active.queued_steer) {
    state.steerDraft = active.queued_steer;
    state.steerId = active.queued_steer_id ?? null;
  }
  stopButton().hidden = false;
  updateSendAvailability(state);
}

// findMessageNode locates the rendered assistant node that owns messageId.
// renderAssistantTurn stamps dataset.messageIds so a multi-round turn merged
// into one node can still be targeted precisely.
function findMessageNode(thread, messageId) {
  if (!thread || !messageId) return null;
  return thread.querySelector(`.agent-message.assistant[data-message-ids~="${messageId}"]`);
}

function stampRunMessageId(node, messageId) {
  if (!node || !messageId) return;
  const ids = new Set((node.dataset.messageIds || '').split(/\s+/).filter(Boolean));
  ids.add(messageId);
  node.dataset.messageIds = [...ids].join(' ');
}

// roundAlreadyPersisted reports whether the snapshot already contains content
// for messageId (its round finished and was saved server-side). Stream mirrors
// covering a persisted round must NOT be overlaid — renderThread already drew
// it and re-seeding would duplicate the text.
function roundAlreadyPersisted(messageId) {
  if (!messageId) return false;
  const message = state.messages.find((m) => m?.id === messageId);
  if (!message) return false;
  return Boolean(
    (typeof message.content === 'string' && message.content.trim())
    || message.steps?.length
    || message.tool_calls?.length
    || (typeof message.reasoning === 'string' && message.reasoning.trim()),
  );
}

// appendRoundSection appends a fresh streaming section (reasoning + text box
// + tool strip) to an EXISTING assistant node's bubble and returns the refs.
// This replaces the old replaceWith approach: earlier tool rounds rendered
// into the same node stay visible when the user switches back mid-turn.
function appendRoundSection(node, source) {
  let bubble = node.querySelector(':scope > .agent-bubble');
  if (!bubble) {
    bubble = el('div', { class: 'agent-bubble' });
    node.prepend(bubble);
  }
  return { bubble, ...mountLiveRound(bubble, source) };
}

// buildStreamNode creates a brand-new streaming node for a message the
// snapshot does not contain yet and appends it to the end of the thread.
function buildStreamNode(thread, messageId, source) {
  const bubble = el('div', { class: 'agent-bubble' });
  const msgNode = el('div', { class: 'agent-message assistant agent-pending' }, bubble);
  if (messageId) msgNode.dataset.messageIds = messageId;
  const refs = mountLiveRound(bubble, source);
  thread.append(msgNode);
  return { msgNode, bubble, ...refs };
}

// sealStaleRoomBuffer marks a room buffer terminal when the backend says the
// conversation is not running but its buffer was never sealed (missed or
// raced agent.turn.done/error). Without this the buffer stays live forever:
// every switch-back overlays stale stream content over the authoritative
// snapshot and expireCompletedBuffers never prunes it.
function sealStaleRoomBuffer(convId, runningStatus) {
  if (runningStatus === 'running') return;
  const buffer = state.roomBuffers.get(convId);
  if (!buffer || buffer.done || !buffer.runId) return;
  if (state.runs.has(buffer.runId)) return; // genuinely live
  buffer.done = true;
  touchRoomBuffer(buffer);
}

// ensureRunSlot converts the snapshot node that owns run.messageId into a
// live streaming slot, or appends a fresh node when the snapshot does not
// contain that message yet. Existing nodes are ADDED TO, never replaced.
function ensureRunSlot(run) {
  const thread = agentThread();
  if (!thread) return null;
  const existing = findMessageNode(thread, run.messageId);
  if (existing) {
    existing.classList.remove('agent-message-error');
    existing.querySelectorAll('.agent-retry-btn').forEach((n) => n.remove());
    return { msgNode: existing, ...appendRoundSection(existing, run) };
  }
  return buildStreamNode(thread, run.messageId, run);
}

// reattachActiveRun checks if there's an active run for the current
// conversation. If so, it converts the snapshot node that owns the run's
// in-flight message into a live streaming slot (appending the current
// round's section) and wires the run's DOM references to it. Only
// UNpersisted stream content is re-rendered: rounds the snapshot already
// covers stay as rendered, fixing the disappearing-turn bug on switch-back.
function reattachActiveRun() {
  const run = runForConversation(state.activeId);
  if (!run) return;
  const slot = ensureRunSlot(run);
  if (!slot) return;
  const { msgNode, bubble, reasoningEl, textBox, strip } = slot;
  msgNode.classList.add('agent-pending');
  if (roundAlreadyPersisted(run.messageId)) {
    // This round already finished and was saved; the snapshot renders it.
    // Keep the (empty) section attached: every run ref stays valid for the
    // next round or in-flight tool updates. agent.turn.started appends a
    // fresh section cleanly, and turn-end refresh repaints from snapshot.
    resetLiveRoundText(run);
    run.toolJobs = new Map();
    run.msgNode = msgNode;
    run.bubble = bubble;
    run.reasoningEl = null;
    run.textBox = null;
    run.strip = null;
    return;
  }
  // Re-render accumulated content for the current (unpersisted) round.
  setReasoningSource(reasoningEl, run.rawReasoning);
  reasoningEl.hidden = !reasoningHasVisibleSource(run.rawReasoning);
  // If reasoning has content but no text yet, the agent is still thinking —
  // resume the streaming pulse so the user doesn't think it's stuck.
  if (!reasoningEl.hidden && !run.raw?.trim()) reasoningEl.classList.add('is-streaming');
  if (run.raw?.trim()) { textBox.innerHTML = renderMarkdown(run.raw); void renderMermaidDiagrams(textBox); void highlightCode(textBox); attachZoomButtons(textBox); }
  else {
    textBox.append(el('span', { class: 'agent-thinking-dots' },
      el('span'), el('span'), el('span')));
  }
  // Render buffered tool jobs so a switch-back shows the tool cards that
  // ran while this room was hidden. Standalone cards go to the bubble;
  // terminals go to the strip.
  const buffer = state.roomBuffers.get(run.conversationId);
  const toolJobs = buffer?.runId === run.runId && buffer.toolJobs
    ? buffer.toolJobs
    : (run.toolJobs || new Map());
  for (const job of toolJobs.values()) placeToolCard(bubble, strip, job);
  // Update the run entry with fresh DOM references.
  run.msgNode = msgNode;
  run.bubble = bubble;
  run.reasoningEl = reasoningEl;
  run.textBox = textBox;
  run.strip = strip;
  clearToolTimers(run);
  run.toolJobs = toolJobs;
  if (buffer?.runId === run.runId) buffer.toolJobs = toolJobs;
  scrollToBottom(true);
}

// renderThread paints the given (already windowed) messages. force controls the
// deferred scroll: true jumps to the bottom once fully rendered (opening a room
// — idea: auto-scroll when loaded); false respects the pin so a background
// refresh never yanks a user who scrolled up to read history.
function renderThread(messages, force = true) {
  const thread = agentThread();
  if (!thread) return;
  if (!messages.length) {
    renderEmptyThread();
    return;
  }
  thread.replaceChildren(renderConversation(messages, retryTurn));
  // Render any Mermaid diagrams in the freshly painted thread (settle point,
  // not per-delta).
  void renderMermaidDiagrams(thread); void highlightCode(thread); attachZoomButtons(thread);
  // Show the "Load older" button when the window holds back older messages.
  updateOlderSentinel();
  // Defer the scroll until after layout: setting scrollTop synchronously
  // right after replaceChildren observes a stale (small) scrollHeight, so
  // a freshly opened long room would sit at the top until the next paint.
  if (force) {
    // Opening/first paint: force to the bottom robustly across layout frames.
    scrollThreadToBottomHard();
  } else {
    // Background refresh: respect the pin so a user reading history is not yanked.
    requestAnimationFrame(() => scrollToBottom(false));
  }
}

// ---- per-room live run buffers ----

// getOrCreateRoomBuffer returns the live-run buffer for a conversation,
// creating it on first activity. The buffer mirrors the deltas that keep
// arriving on the shared WebSocket while the user is looking at another room.
function getOrCreateRoomBuffer(convId) {
  if (!convId) return null;
  let buffer = state.roomBuffers.get(convId);
  if (!buffer) {
    buffer = {
      runId: null,
      messageId: null,
      round: 1,
      raw: '',
      rawReasoning: '',
      toolJobs: new Map(),
      lastEventAt: 0,
      done: false,
      // DOM refs are filled only when this room becomes active again.
      msgNode: null, bubble: null, strip: null, textBox: null, reasoningEl: null,
    };
    state.roomBuffers.set(convId, buffer);
  }
  return buffer;
}

function touchRoomBuffer(buffer) {
  if (!buffer) return;
  buffer.lastEventAt = Date.now();
}

// resetRoomBufferMirror resets a room's stream mirror at each agent.turn.started
// boundary: the mirror only ever covers the CURRENT round. Persisted earlier
// rounds belong to the snapshot; accumulating them in the mirror duplicated
// text across rounds and hit the size cap prematurely.
function resetRoomBufferMirror(conversationId, run, round) {
  const buffer = state.roomBuffers.get(conversationId);
  if (!buffer || buffer.done) return;
  buffer.raw = '';
  buffer.rawReasoning = '';
  buffer.toolJobs = new Map();
  buffer.round = round || 1;
  buffer.messageId = run?.messageId ?? null;
}

// expireCompletedBuffers prunes room buffers whose runs have finished and are
// longer needed: the persisted snapshot is authoritative after the turn is
// terminal, and a stale buffer would only re-merge old deltas on switch-back.
function expireCompletedBuffers() {
  for (const [convId, buffer] of state.roomBuffers) {
    if (buffer.done && buffer.lastEventAt < Date.now() - 30_000) {
      state.roomBuffers.delete(convId);
    }
  }
}

// applyBufferedRunToDOM merges the buffered LIVE run for the now-active
// conversation into the rendered thread. It replaces the last assistant node
// with a streaming placeholder populated from the accumulated raw text +
// reasoning + tool jobs, so switching back never shows a blank turn.
//
// Terminal (done) buffers are intentionally NOT rendered here: once a turn
// finishes, the persisted snapshot is authoritative and already contains the
// final message, so re-merging a stale done buffer would misrender when a
// newer turn has progressed past it.
function applyBufferedRunToDOM(convId) {
  const buffer = state.roomBuffers.get(convId);
  if (!buffer || buffer.done || buffer.lastEventAt === 0 || !buffer.runId) return;
  const thread = agentThread();
  if (!thread) return;
  // Convert the snapshot node that owns this message into a streaming slot
  // (append-based). A capped buffer still overlays its retained tail — the
  // snapshot cannot render the in-flight round, so showing the newest tail
  // beats a blank bubble.
  const run = state.runs.get(buffer.runId);
  const sourceMessageID = buffer.messageId || run?.messageId || '';
  buffer.messageId = sourceMessageID || buffer.messageId;
  const source = {
    raw: buffer.raw,
    rawReasoning: buffer.rawReasoning,
    toolJobs: buffer.toolJobs,
    messageId: sourceMessageID,
  };
  const slot = ensureRunSlot(source);
  if (!slot) return;
  const { bubble, reasoningEl, textBox, strip } = slot;
  slot.msgNode.classList.add('agent-pending');
  if (roundAlreadyPersisted(source.messageId)) {
    // The turn finished server-side while the user was away; the snapshot
    // already renders this round. Leave the empty section attached (harmless
    // and transient) rather than risking detached refs in live handlers.
    if (run) {
      run.msgNode = slot.msgNode;
      run.bubble = slot.bubble;
      run.reasoningEl = reasoningEl;
      run.textBox = textBox;
      run.strip = strip;
      run.toolJobs = source.toolJobs;
    }
    return;
  }
  if (source.rawReasoning) {
    setReasoningSource(reasoningEl, source.rawReasoning);
    reasoningEl.hidden = !reasoningHasVisibleSource(source.rawReasoning);
    if (!reasoningEl.hidden && !source.raw?.trim()) reasoningEl.classList.add('is-streaming');
  }
  if (source.raw?.trim()) textBox.innerHTML = renderMarkdown(source.raw);
  else textBox.append(el('span', { class: 'agent-thinking-dots' }, el('span'), el('span'), el('span')));
  void renderMermaidDiagrams(textBox); void highlightCode(textBox); attachZoomButtons(textBox);
  for (const job of source.toolJobs.values()) placeToolCard(bubble, strip, job);
  // Write the slot refs back to the buffer: openConversation may re-register
  // a live run FROM this buffer right after, copying these DOM references.
  buffer.msgNode = slot.msgNode;
  buffer.bubble = slot.bubble;
  buffer.reasoningEl = reasoningEl;
  buffer.textBox = textBox;
  buffer.strip = strip;
  if (!run) return; // buffered-only view; live handlers will attach later
  // Refresh the run's DOM refs so the active-run handlers keep updating.
  run.msgNode = slot.msgNode;
  run.bubble = slot.bubble;
  run.reasoningEl = reasoningEl;
  run.textBox = textBox;
  run.strip = strip;
  run.messageId = sourceMessageID || run.messageId;
  run.toolJobs = source.toolJobs;
  if (buffer.runId === run.runId) buffer.toolJobs = source.toolJobs;
  scrollToBottom(true);
}

function renderAttachments() {
  const container = attachmentsContainer();
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
    state.todos = { items: [], summary: { total: 0, pending: 0, in_progress: 0, completed: 0 }, brief: '' };
    renderTodoStrip();
    return;
  }
  const token = ++state.todoRenderToken;
  try {
    const result = await rpc('agent.todos.get', { conversation_id: state.activeId });
    if (token !== state.todoRenderToken) return; // stale — a newer fetch or room switch won
    state.todos = { items: result.items ?? [], summary: result.summary ?? { total: 0, pending: 0, in_progress: 0, completed: 0 }, brief: result.brief ?? '' };
    renderTodoStrip();
  } catch (err) {
    if (token !== state.todoRenderToken) return;
    // Fail-soft: hide the strip rather than crash. The backend may not support
    // todos yet (older version), or the conversation may have been deleted.
    state.todos = { items: [], summary: { total: 0, pending: 0, in_progress: 0, completed: 0 }, brief: '' };
    renderTodoStrip();
  }
}

// bindStripToggles wires the expand/collapse toggle buttons for the todo,
// tool-job, and steer-queue strips. Default is collapsed (list hidden).
// The toggle flips aria-expanded and shows/hides the list element.
function bindStripToggles() {
  const pairs = [
    { toggleId: 'agent-todo-strip-toggle', listId: 'agent-todo-strip-list', extraIds: ['agent-todo-strip-brief'] },
    { toggleId: 'tool-job-strip-toggle', listId: 'tool-job-list' },
    { toggleId: 'agent-steer-queue-toggle', listId: 'agent-steer-queue-list' },
  ];
  for (const { toggleId, listId, extraIds } of pairs) {
    const toggle = document.getElementById(toggleId);
    const list = document.getElementById(listId);
    if (!toggle || !list) continue;
    toggle.addEventListener('click', () => {
      const open = toggle.getAttribute('aria-expanded') === 'true';
      toggle.setAttribute('aria-expanded', String(!open));
      list.hidden = open;
      if (extraIds) {
        for (const id of extraIds) {
          const el = document.getElementById(id);
          if (el) el.hidden = open;
        }
      }
    });
  }
}

// renderTodoStrip renders the todo checklist strip from state.todos. It is
// idempotent — safe to call multiple times. The strip is hidden when there
// are no items and no brief. Each item gets a status glyph and a delete
// button. Delete buttons are created fresh on each render, so no stale
// listeners. Default state: collapsed (list hidden), expanded only on user
// click. The planning brief (if set) is shown as a muted line above the item
// list.
function renderTodoStrip() {
  const strip = document.getElementById('agent-todo-strip');
  if (!strip) return;
  const { items, brief } = state.todos;
  const hasItems = items && items.length > 0;
  const hasGoal = brief && brief.trim();
  if (!hasItems && !hasGoal) {
    strip.hidden = true;
    return;
  }
  strip.hidden = false;
  const countEl = document.getElementById('agent-todo-strip-count');
  const metaEl = document.getElementById('agent-todo-strip-meta');
  const total = items ? items.length : 0;
  if (countEl) countEl.textContent = `${total} Task${total === 1 ? '' : 's'}`;
  if (metaEl) {
    const incomplete = items ? items.filter((i) => i.status !== 'completed').length : 0;
    metaEl.textContent = incomplete === 0 ? 'All done' : `${incomplete} open`;
    metaEl.dataset.done = incomplete === 0 ? 'true' : 'false';
  }
  // Brief: shown as a muted line above the item list. Respects the
  // toggle state so a collapsed strip stays collapsed on re-render.
  const briefEl = document.getElementById('agent-todo-strip-brief');
  const toggleEl = document.getElementById('agent-todo-strip-toggle');
  const stripOpen = toggleEl?.getAttribute('aria-expanded') === 'true';
  if (briefEl) {
    if (hasGoal) {
      briefEl.innerHTML = renderMarkdown(brief.trim());
      void highlightCode(briefEl); attachZoomButtons(briefEl);
      briefEl.title = 'User brief — survives compaction so the agent does not drift';
      briefEl.hidden = !stripOpen;
    } else {
      briefEl.hidden = true;
      briefEl.innerHTML = '';
    }
  }
  const list = document.getElementById('agent-todo-strip-list');
  if (!list) return;
  if (hasItems) {
    list.replaceChildren(...items.map((item) => renderTodoItem(item, handleTodoDelete)));
  } else {
    list.replaceChildren();
  }
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
    state.todos = { items: result.items ?? [], summary: result.summary ?? { total: 0, pending: 0, in_progress: 0, completed: 0 }, brief: result.brief ?? state.todos.brief ?? '' };
    renderTodoStrip();
  } catch (err) {
    btn.disabled = false;
    toast(err.message || 'Failed to delete task', 'error');
  }
}

function beginTurn(runId, userText, attachments = []) {
  stopButton().hidden = false;
  // Reflect the running status immediately: composer steering routes on
  // state.conversation.status (state.running is never set), and without
  // this the next submit races a turns.start into a busy backend instead
  // of entering steer mode directly.
  if (state.conversation) state.conversation.status = 'running';
  updateSendAvailability(state);

  const thread = agentThread();
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
  const { reasoningEl, textBox, strip } = mountLiveRound(bubble, {});
  reasoningEl.hidden = true;
  textBox.append(el('span', { class: 'agent-thinking-dots' },
    el('span'), el('span'), el('span')));
  const previousBuffer = state.roomBuffers.get(state.activeId);
  if (previousBuffer?.done) state.roomBuffers.delete(state.activeId);
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
  if (runForConversation(state.activeId)) return;
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
  stopButton().hidden = false;
  updateSendAvailability(state);

  const thread = agentThread();
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
  const { reasoningEl, textBox, strip } = mountLiveRound(bubble, {});
  reasoningEl.hidden = true;
  textBox.textContent = '…';
  const previousBuffer = state.roomBuffers.get(state.activeId);
  if (previousBuffer?.done) state.roomBuffers.delete(state.activeId);
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
      const draft = state.steerDraft ?? '';
      await rpc('agent.turns.cancel-steer', { conversation_id: state.activeId });
      clearSteerQueue();
      const input = composerInput();
      if (input && !input.value.trim() && draft) {
        input.value = draft;
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
  flushPendingToolDeltas(run);
  for (const job of run.toolJobs.values()) {
    if (job._elapsedTimer) {
      clearInterval(job._elapsedTimer);
      job._elapsedTimer = null;
    }
  }
}

function queueToolDelta(run, toolCallId, text) {
  if (!run || !text) return;
  if (!run.pendingToolDeltas) run.pendingToolDeltas = new Map();
  if (!run.pendingToolDeltas.has(toolCallId) && run.pendingToolDeltas.size >= MAX_LIVE_TOOL_JOBS) {
    run.pendingToolDeltas.delete(run.pendingToolDeltas.keys().next().value);
  }
  run.pendingToolDeltas.set(
    toolCallId,
    ((run.pendingToolDeltas.get(toolCallId) || '') + text).slice(-12000),
  );
  scheduleLiveRender(run);
}

function flushPendingToolDeltas(run) {
  if (!run?.pendingToolDeltas?.size) return false;
  let flushed = false;
  for (const [toolCallId, text] of run.pendingToolDeltas) {
    const job = run.toolJobs?.get(toolCallId);
    if (!job) continue;
    appendToolJobDelta(job, text);
    flushed = true;
  }
  run.pendingToolDeltas.clear();
  return flushed;
}

function scheduleLiveRender(run) {
  if (!run || run.renderScheduled) return;
  run.renderScheduled = true;
  const flush = () => {
    run.renderScheduled = false;
    run.renderFrame = 0;
    renderLiveRun(run);
  };
  if (typeof requestAnimationFrame === 'function') {
    run.renderFrame = requestAnimationFrame(flush);
  } else {
    run.renderFrame = setTimeout(flush, 0);
  }
}

function cancelLiveRender(run) {
  if (!run?.renderScheduled) return;
  if (typeof cancelAnimationFrame === 'function' && typeof run.renderFrame === 'number') {
    cancelAnimationFrame(run.renderFrame);
  } else {
    clearTimeout(run.renderFrame);
  }
  run.renderScheduled = false;
  run.renderFrame = 0;
}

function flushLiveRender(run) {
  if (!run?.renderScheduled) return;
  cancelLiveRender(run);
  renderLiveRun(run);
}

function renderLiveRun(run) {
  if (run.conversationId !== state.activeId) return;
  const textBox = run.textBox;
  const reasoningEl = run.reasoningEl;
  if (!textBox?.isConnected && !reasoningEl?.isConnected && !run.bubble?.isConnected) return;

  const enhanced = [];
  const flushedToolDelta = flushPendingToolDeltas(run);
  if (flushedToolDelta) updateRoomInfo(state.conversation, state.messages);

  if (reasoningEl) {
    setReasoningSource(reasoningEl, run.rawReasoning, { render: false });
    reasoningEl.hidden = !reasoningHasVisibleSource(run.rawReasoning);
    if (reasoningEl.hidden || run.raw?.trim()) {
      reasoningEl.classList.remove('is-streaming');
    } else if (run.rawReasoning) {
      reasoningEl.classList.add('is-streaming');
      textBox?.querySelector('.agent-thinking-dots')?.remove();
    }
    if (reasoningEl.open) {
      const content = reasoningEl.querySelector('.agent-reasoning-content');
      if (content && run.rawReasoning) {
        enhanced.push(...incrementalRender(content, run.rawReasoning));
      }
    }
  }

  if (textBox && run.raw?.trim()) {
    run.reasoningEl?.classList.remove('is-streaming');
    textBox.querySelector('.agent-thinking-dots')?.remove();
    const banner = run.bubble?.querySelector('.agent-retry-banner');
    if (banner) banner.remove();
    enhanced.push(...incrementalRender(textBox, run.raw));
  }
  scheduleLiveEnhancement(run, enhanced);
  scrollToBottom();
}

// scheduleLiveEnhancement runs mermaid/highlight/zoom enhancement ONLY on the
// blocks this delta just created or changed (from incrementalRender) — never
// over the whole bubble. This is what keeps long live turns cheap without
// trimming rounds: enhancement cost stays proportional to the delta, and
// settled blocks (hash-locked highlight/mermaid) are never re-scanned.
function scheduleLiveEnhancement(run, changedNodes = []) {
  if (!run) return;
  const targets = changedNodes.filter((node) => node?.isConnected);
  if (!targets.length) return;
  if (run.enhanceTimer) clearTimeout(run.enhanceTimer);
  run.enhanceTimer = setTimeout(() => {
    run.enhanceTimer = null;
    for (const node of targets) {
      if (!node.isConnected) continue;
      void renderMermaidDiagrams(node);
      void highlightCode(node);
      attachZoomButtons(node);
    }
  }, 120);
}

function replaceGenerateImageJob(job, payload, assign) {
  const elapsed = job?.querySelector('.agent-tool-elapsed')?.textContent || '';
  if (job?._elapsedTimer) {
    clearInterval(job._elapsedTimer);
    job._elapsedTimer = null;
  }
  const card = renderGenerateImageCard({
    name: 'generate_image',
    args: job?._toolArgs ?? {},
    output: payload.output ?? '',
    status: payload.status || 'ok',
    output_attachments: payload.attachments || [],
  });
  if (elapsed) {
    const elapseNode = card.querySelector('.agent-tool-elapsed');
    if (elapseNode) elapseNode.textContent = elapsed;
  }
  if (job) job.replaceWith(card);
  assign(card);
}

function endTurn(runId, keepRun = false) {
  const run = state.runs.get(runId);
  if (!run) {
    if (keepRun) return;
    // Run entry already gone (refresh raced the turn, or a terminal event
    // arrived for an unknown run). Seal any matching room buffer anyway so
    // it cannot stay "live" forever and overlay stale deltas over future
    // snapshots (the disappearing-turn bug).
    for (const [, buffer] of state.roomBuffers) {
      if (buffer.runId === runId && !buffer.done) {
        buffer.done = true;
        touchRoomBuffer(buffer);
      }
    }
    refreshLiveDots();
    return;
  }
  const convId = run.conversationId;
  // Turn is terminal on the wire — mirror it so composer routing stops
  // treating the room as running (see beginTurn).
  const conv = state.conversations.find((c) => c.id === convId);
  if (conv) conv.status = 'idle';
  if (state.activeId === convId && state.conversation) state.conversation.status = 'idle';
  flushLiveRender(run);
  if (run.enhanceTimer) {
    clearTimeout(run.enhanceTimer);
    run.enhanceTimer = null;
  }
  // Detach the DOM for a run that ended while this room was NOT visible.
  // The room's live buffer is kept so switching back renders the final
  // state; the run entry is removed from the shared map (the DOM refs for
  // an invisible room are stale after renderThread anyway).
  if (run.msgNode) run.msgNode.classList.remove('agent-pending');
  // Clean up transient UI elements (thinking dots, retry banner, tool timers,
  // reasoning streaming pulse). Use a bubble-wide query: multi-round turns
  // leave a textBox with dots per round that produced no text, so only
  // removing from run.textBox would leave stale dots above tool stacks.
  run.bubble?.querySelectorAll('.agent-thinking-dots').forEach((n) => n.remove());
  run.bubble?.querySelectorAll('.agent-reasoning.is-streaming').forEach((n) => n.classList.remove('is-streaming'));
  run.bubble?.querySelector('.agent-retry-banner')?.remove();
  clearToolTimers(run);
  if (!keepRun) {
    state.runs.delete(runId);
    // Finalize the room buffer: the turn is terminal now, the persisted
    // snapshot is authoritative on the next switch-back. Keep the buffer
    // around briefly so the sidebar dot can still signal "finished", then
    // let reattach render from the server snapshot.
    const buffer = state.roomBuffers.get(convId);
    if (buffer) {
      buffer.done = true;
      touchRoomBuffer(buffer);
    }
  }
  // Clear steer for this conversation (turn ended, unapplied steer is cancelled).
  if (convId === state.activeId) {
    clearSteerQueue();
    if (!keepRun && !runForConversation(state.activeId)) {
      const stop = stopButton();
      if (stop) stop.hidden = true;
    }
    updateSendAvailability(state);
    updateComposerStatus();
  } else {
    clearSavedSteerQueue(convId);
  }
  refreshLiveDots();
}

// ---------- scroll pinning ----------

// Track whether the user is at the bottom of the thread. While pinned,
// streaming deltas auto-scroll to keep the latest content visible. When the
// user scrolls up, pinning is released so they can read history without being
// yanked back down. Scrolling back to the bottom re-pins.
function bindScrollPin() {
  const thread = agentThread();
  if (!thread) return;
  thread.addEventListener('scroll', () => {
    state.pinned = isAtBottom(thread);
    // Older *active* messages are revealed via the explicit "Load older"
    // button (a deliberate click keeps the scroll anchored — auto-loading
    // during a fast wheel scroll fights the browser's momentum and jumps the
    // view). Archived chunks load one at a time on scroll-to-top once the
    // in-memory window is fully revealed.
    if (!state.suppressTopLoad && thread.scrollTop <= 4 && !hasOlderActiveMessages()) {
      loadOlderChunk();
    }
  }, { passive: true });
}

// hasOlderActiveMessages reports whether older active messages are held back by
// the window and can be revealed with the "Load older" button.
function hasOlderTurnRounds() {
  return state.assistKeepStart > trailingRunStart();
}

function hasOlderActiveMessages() {
  if (state.activeWindowStart > 0) return true;
  return hasOlderTurnRounds() && !runForConversation(state.activeId);
}

function hasOlderHistory() {
  return hasOlderActiveMessages() || state.nextChunkIndex >= 0;
}

// updateOlderSentinel keeps a "Load older messages" button pinned at the top of
// the thread while older active messages remain windowed out or archived
// chunks remain unloaded. Clicking it reveals one batch with a stable
// (anchored) scroll position.
function updateOlderSentinel() {
  const thread = agentThread();
  if (!thread) return;
  let btn = document.getElementById('agent-load-older');
  if (!hasOlderHistory()) {
    btn?.remove();
    return;
  }
  if (!btn) {
    btn = el('button', {
      class: 'agent-load-older',
      id: 'agent-load-older',
      type: 'button',
    });
    btn.addEventListener('click', () => revealOlderHistory());
  }
  btn.textContent = '↑ Load older messages';
  if (thread.firstChild !== btn) thread.insertBefore(btn, thread.firstChild);
}

function revealOlderHistory() {
  if (hasOlderTurnRounds() && !runForConversation(state.activeId)) {
    revealOlderTurnRounds();
    return;
  }
  if (state.activeWindowStart > 0) prependActiveBatch();
  else loadOlderChunk();
}

function revealOlderTurnRounds() {
  const thread = agentThread();
  if (!thread || state.loadingActiveBatch) return;
  const runStart = trailingRunStart();
  if (state.assistKeepStart <= runStart) return;
  state.loadingActiveBatch = true;
  try {
    const userNode = [...thread.querySelectorAll('.agent-message.user, .agent-compaction-marker')].at(-1);
    const userTop = userNode ? userNode.getBoundingClientRect().top : 0;
    state.assistKeepStart = Math.max(runStart, state.assistKeepStart - SNAPSHOT_KEEP_ROUNDS);
    renderThread(windowedActiveMessages(), false);
    if (userNode) {
      const nextUser = [...thread.querySelectorAll('.agent-message.user, .agent-compaction-marker')].at(-1);
      if (nextUser) thread.scrollTop += nextUser.getBoundingClientRect().top - userTop;
    }
  } finally {
    state.loadingActiveBatch = false;
  }
}

// prependActiveBatch renders the previous WINDOW_BATCH of already-loaded active
// messages above the current view, preserving the scroll position. It is driven
// by the explicit "Load older" button (a deliberate click, so there is no
// momentum-scroll fighting the anchor). The active (streaming) run lives at the
// tail, which this never touches, so no run rebinding is needed.
function prependActiveBatch() {
  const thread = agentThread();
  if (!thread || state.activeWindowStart <= 0 || state.loadingActiveBatch) return;
  state.loadingActiveBatch = true;
  try {
    const newStart = previousWindowStart(state.activeWindowStart, WINDOW_BATCH);
    const batch = state.messages.slice(newStart, state.activeWindowStart);
    state.activeWindowStart = newStart;
    if (!batch.length) return;
    // Anchor to the first real message so it stays visually stationary after
    // older messages are inserted above it. Measuring the element's viewport
    // position (rather than scrollHeight deltas) is robust to async layout.
    const anchor = thread.querySelector('.agent-message') || thread.firstElementChild;
    const anchorTop = anchor ? anchor.getBoundingClientRect().top : 0;
    // Insert the batch just above the first real message (below the sentinel
    // button, which updateOlderSentinel keeps pinned at the very top).
    thread.insertBefore(renderConversation(batch, retryTurn), anchor);
    if (anchor) {
      const shift = anchor.getBoundingClientRect().top - anchorTop;
      thread.scrollTop += shift;
    }
    updateOlderSentinel();
  } finally {
    state.loadingActiveBatch = false;
  }
}

function isAtBottom(el) {
  if (!el) return true;
  // Small tolerance so streamed deltas that grow the container do not
  // accidentally unpin a user who is still effectively at the latest line.
  return el.scrollHeight - el.scrollTop - el.clientHeight <= 24;
}

// scrollThreadToBottomHard forces the thread to the bottom robustly after an
// initial render: layout (fonts/markdown) can settle across a couple of frames,
// so a single scrollTop assignment may land short. It also suppresses the
// scroll-to-top loader until it settles so the transient scrollTop≈0 during
// layout is not treated as the user scrolling up.
function scrollThreadToBottomHard() {
  const thread = agentThread();
  if (!thread) return;
  state.suppressTopLoad = true;
  const jump = () => {
    thread.scrollTop = thread.scrollHeight;
    state.pinned = true;
  };
  jump();
  requestAnimationFrame(() => {
    jump();
    requestAnimationFrame(() => {
      jump();
      // Re-enable older-history loading only once we are actually at the bottom.
      state.suppressTopLoad = false;
    });
  });
}

function scrollToBottom(force = false) {
  const thread = agentThread();
  if (!thread) return;
  if (!force && !state.pinned) return;
  thread.scrollTop = thread.scrollHeight;
  // Re-pin after a forced jump so the next delta keeps following.
  state.pinned = true;
}

// ---- chunk-based lazy load ----

// loadOlderChunk fetches one archived pre-compaction chunk and prepends it.
// It does not chain: each Load-older click or scroll-to-top loads one chunk
// so a 1MB archived turn cannot auto-inflate the thread.
async function loadOlderChunk() {
  if (state.loadingChunk || state.nextChunkIndex < 0 || !state.activeId) return;
  state.loadingChunk = true;
  const chunkIndex = state.nextChunkIndex;
  const conversationId = state.activeId;
  const token = state.conversationLoadToken;
  const thread = agentThread();
  // Save the scroll anchor so we can restore position after prepending.
  const prevHeight = thread?.scrollHeight ?? 0;
  const prevScroll = thread?.scrollTop ?? 0;
  // Capture the run that is currently bound to this room (if any) so a
  // chunk-load that resolves after a room switch does not clobber it.
  const prevRun = runForConversation(conversationId);
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
    updateOlderSentinel();
    if (thread) void renderMermaidDiagrams(thread); void highlightCode(thread); attachZoomButtons(thread);
    // Restore the scroll position so the user doesn't jump.
    if (thread) {
      const newHeight = thread.scrollHeight;
      thread.scrollTop = prevScroll + (newHeight - prevHeight);
    }
    // A chunk load replaced the DOM nodes for this room. Rebind the active
    // run's DOM refs to the freshly re-rendered nodes so streaming deltas do
    // not target detached nodes.
    if (prevRun && state.activeId === conversationId && runForConversation(conversationId) === prevRun) {
      // A chunk load replaced this room's DOM nodes. Re-bind the active run
      // by converting the node that owns its in-flight message into a
      // streaming slot (append-based; the old replaceWith-the-last-node
      // approach wiped earlier rounds merged into the turn node).
      const slot = ensureRunSlot(prevRun);
      if (slot) {
        Object.assign(prevRun, slot);
        if (roundAlreadyPersisted(prevRun.messageId)) {
          slot.reasoningEl.closest('.agent-round')?.remove();
          slot.reasoningEl.remove();
          slot.textBox.remove();
          slot.strip.remove();
          prevRun.raw = '';
          prevRun.rawReasoning = '';
          prevRun.toolJobs = new Map();
          prevRun.reasoningEl = null;
          prevRun.textBox = null;
          prevRun.strip = null;
        } else {
          if (prevRun.rawReasoning) {
            setReasoningSource(slot.reasoningEl, prevRun.rawReasoning);
            slot.reasoningEl.hidden = !reasoningHasVisibleSource(prevRun.rawReasoning);
          }
          if (prevRun.raw?.trim()) { slot.textBox.innerHTML = renderMarkdown(prevRun.raw); void renderMermaidDiagrams(slot.textBox); void highlightCode(slot.textBox); attachZoomButtons(slot.textBox); }
          else {
            slot.textBox.append(el('span', { class: 'agent-thinking-dots' },
              el('span'), el('span'), el('span')));
          }
          for (const job of prevRun.toolJobs.values()) placeToolCard(slot.bubble, slot.strip, job);
          clearToolTimers(prevRun);
          prevRun.toolJobs = new Map();
        }
      }
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
    updateOlderSentinel();
  }
}

function bindEvents() {
  on('agent.turn.started', (payload) => {
    const { run_id, message_id, round, conversation_id } = payload;
    let run = state.runs.get(run_id);
    if (!run) {
      // Safety net: the run entry was deleted (endTurn on a turn.done
      // whose auto_continue DTO was missing or false) but the backend
      // reused the run_id for the next turn in the chain. Re-create the
      // entry so streaming events render instead of being silently queued
      // and dropped — which made the agent's response look like it was
      // replaced by the continue.md prompt (empty A2 bubble, no stream).
      if (conversation_id !== state.activeId) {
        getRunOrQueue('agent.turn.started', payload);
        return;
      }
      run = {
        msgNode: null, bubble: null, strip: null, textBox: null, reasoningEl: null,
        toolJobs: new Map(), raw: '', rawReasoning: '',
        round: 1, conversationId: conversation_id, runId: run_id,
        messageId: message_id,
      };
      state.runs.set(run_id, run);
      stopButton().hidden = false;
      updateSendAvailability(state);
    }
    // Auto-continue: the run entry was kept across turns (endTurn with
    // keepRun=true), but the new turn has a different assistant message
    // ID. Set up a fresh streaming slot for the new message instead of
    // reusing the previous turn's textBox — clearing the old textBox
    // would wipe the prior turn's visible content (the "rollback" bug).
    const prevMessageId = run.messageId;
    run.messageId = message_id;
    if (run.msgNode?.isConnected && run.textBox && prevMessageId === message_id) {
      run.round = round || run.round;
      return;
    }
    // round 1: bind the placeholder that beginTurn already created. Do not
    // ensureRunSlot here — that used to append a second Thinking block
    // (and a second assistant node) because the placeholder has no
    // data-message-ids yet. Auto-continue reuses the run with a new
    // message id and must open a fresh node instead of writing over the
    // previous turn.
    if (!round || round <= 1) {
      resetLiveRoundText(run);
      run.round = 1;
      resetRoomBufferMirror(conversation_id, run, 1);
      const reusePlaceholder = conversation_id === state.activeId
        && run.msgNode?.isConnected
        && run.textBox
        && (!prevMessageId || prevMessageId === message_id);
      if (reusePlaceholder) {
        stampRunMessageId(run.msgNode, message_id);
        if (run.textBox) {
          run.textBox.textContent = '';
          run.textBox.append(el('span', { class: 'agent-thinking-dots' },
            el('span'), el('span'), el('span')));
        }
        return;
      }
      if (conversation_id === state.activeId) {
        const slot = ensureRunSlot(run);
        if (slot) {
          run.msgNode = slot.msgNode;
          run.bubble = slot.bubble;
          run.reasoningEl = slot.reasoningEl;
          run.textBox = slot.textBox;
          run.strip = slot.strip;
          stampRunMessageId(run.msgNode, message_id);
          if (run.textBox && !run.raw) {
            run.textBox.textContent = '';
            run.textBox.append(el('span', { class: 'agent-thinking-dots' },
              el('span'), el('span'), el('span')));
          }
        }
      }
      return;
    }
    // Track round even when not active so re-attach knows the current round.
    run.round = round;
    resetLiveRoundText(run);
    resetRoomBufferMirror(conversation_id, run, round);
    if (conversation_id !== state.activeId) return;
    // seal previous round: hide its tool strip if empty
    if (run.strip && run.strip.hidden) run.strip.remove();
    if (run.msgNode?.isConnected) {
      // Same bubble, new round — one section only. Do not also call
      // ensureRunSlot when message_id changes; that appended a second
      // Thinking disclosure on every tool round.
      stampRunMessageId(run.msgNode, message_id);
      const refs = appendRoundSection(run.msgNode, run);
      run.reasoningEl = refs.reasoningEl;
      run.textBox = refs.textBox;
      run.strip = refs.strip;
    } else {
      const slot = ensureRunSlot(run);
      if (!slot) return;
      run.msgNode = slot.msgNode;
      run.bubble = slot.bubble;
      run.reasoningEl = slot.reasoningEl;
      run.textBox = slot.textBox;
      run.strip = slot.strip;
      stampRunMessageId(run.msgNode, message_id);
    }
    clearToolTimers(run);
    run.toolJobs = new Map();
  });
  on('agent.message.delta', (payload) => {
    const { text, conversation_id } = payload;
    const run = getRunOrQueue('agent.message.delta', payload);
    if (!run) return;
    // Always accumulate raw text so we can re-render on room switch.
    appendBoundedLiveText(run, 'raw', text);
    // Mirror into the room buffer so a switch-back that happens at any point
    // (including after this run entry was cleaned up) still renders the full
    // stream. The buffer keeps only a bounded tail if the room is very large.
    if (conversation_id !== state.activeId) {
      const buffer = getOrCreateRoomBuffer(conversation_id);
      buffer.runId = run.runId;
      buffer.messageId = run.messageId;
      buffer.raw += text;
      trimRoomBuffer(buffer);
      touchRoomBuffer(buffer);
      refreshLiveDots();
    }
    // Only update DOM if this conversation is the active one.
    if (conversation_id !== state.activeId || !run.textBox) return;
    scheduleLiveRender(run);
  });
  on('agent.context.estimate', (payload) => {
    // Use the server-side estimate of what is really sent (system + messages
    // + tool definitions) instead of a transcript-only guess.
    const { conversation_id, estimated_tokens } = payload;
    if (conversation_id !== state.activeId) return;
    state.contextEstimate = Number(estimated_tokens) || 0;
    const status = providerStatus();
    if (!status) return;
    if (!Number.isFinite(Number(estimated_tokens)) || Number(estimated_tokens) <= 0) return;
    const chosen = models.find((model) => `${model.provider_id}:${model.id}` === state.model) || models.find((model) => model.id === state.model);
    if (!chosen) return;
    const windowSize = effectiveContextWindow(Number(chosen.context) || 0, Number(state.settings.max_input_tokens) || 0);
    status.textContent = formatContextUsage(Number(estimated_tokens), windowSize);
  });
  on('agent.reasoning.delta', (payload) => {
    const { text, conversation_id } = payload;
    const run = getRunOrQueue('agent.reasoning.delta', payload);
    if (!run) return;
    appendBoundedLiveText(run, 'rawReasoning', text);
    // Mirror into the room buffer for a non-active room so a switch-back
    // restores the full reasoning stream.
    if (conversation_id !== state.activeId) {
      const buffer = getOrCreateRoomBuffer(conversation_id);
      buffer.runId = run.runId;
      buffer.messageId = run.messageId;
      buffer.rawReasoning += text;
      trimRoomBuffer(buffer);
      touchRoomBuffer(buffer);
      refreshLiveDots();
    }
    if (conversation_id !== state.activeId) return;
    scheduleLiveRender(run);
  });
  on('agent.provider.retry', (payload) => {
    const { attempt, max_attempts, delay_ms, error, conversation_id } = payload;
    const run = getRunOrQueue('agent.provider.retry', payload);
    if (!run) return;
    // Clear partial content from the interrupted stream so the retry
    // starts fresh. Without this, new deltas from the retry append to
    // the cut-off text and produce garbled output.
    if (conversation_id !== state.activeId) {
      const buffer = getOrCreateRoomBuffer(conversation_id);
      buffer.runId = run.runId;
      buffer.messageId = run.messageId;
      buffer.raw = '';
      buffer.rawReasoning = '';
      buffer.toolJobs = new Map();
      touchRoomBuffer(buffer);
      refreshLiveDots();
      return;
    }
    const hadRaw = Boolean(run.raw);
    const hadReasoning = Boolean(run.rawReasoning);
    resetLiveRoundText(run);
    if (hadRaw) {
      if (run.textBox) run.textBox.textContent = '';
    }
    if (hadReasoning) {
      setReasoningSource(run.reasoningEl, '');
    }
    if (run.reasoningEl) {
      run.reasoningEl.hidden = true;
    }
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
    // Mirror tool activity into the non-active room buffer so a switch-back
    // restores the tool cards that ran while this room was hidden.
    if (conversation_id !== state.activeId) {
      const buffer = getOrCreateRoomBuffer(conversation_id);
      buffer.runId = run.runId;
      buffer.messageId = run.messageId;
      const job = name === 'generate_image'
        ? renderGenerateImageCard({ name, args: args ?? {}, status: 'running' })
        : renderToolJob({ name, args: args ?? {}, status: 'running' });
      if (isStreamingTool(name)) bindToolStop(job, () => run.runId);
      setLiveToolJob(buffer.toolJobs, tool_call_id, job);
      touchRoomBuffer(buffer);
      refreshLiveDots();
      return;
    }
    // ask_question renders as an ask card (not a tool terminal). The card
    // is created when agent.ask.pending fires with the validated args.
    // Skip the tool terminal here to avoid a double card.
    if (name === 'ask_question') return;
    // The model moved on to a tool call — no longer "thinking". Drop the
    // dots in the current round's textBox so they don't linger above the
    // tool stack when the round produced no text. If the box is empty
    // (tool-only round), remove it entirely so no blank bubble-text slot
    // sits above the tools.
    run.reasoningEl?.classList.remove('is-streaming');
    const tb = run.textBox;
    if (tb) {
      tb.querySelector('.agent-thinking-dots')?.remove();
      if (!tb.textContent.trim() && !tb.querySelector('*')) {
        tb.remove();
        run.textBox = null;
      }
    }
    if (!run.strip) return;
    const job = name === 'generate_image'
      ? renderGenerateImageCard({ name, args: args ?? {}, status: 'running' })
      : renderToolJob({ name, args: args ?? {}, status: 'running' });
    if (isStreamingTool(name)) bindToolStop(job, () => run.runId);
    setLiveToolJob(run.toolJobs, tool_call_id, job);
    placeToolCard(run.bubble, run.strip, job);
    // Appending a tool card grows the thread; follow it if the user is pinned.
    // Without this, a burst of parallel tool.started events would leave the
    // view stranded above the latest activity until some later event scrolled.
    scrollToBottom();
    // Keep room diagnostics live while tools execute.
    updateRoomInfo(state.conversation, state.messages);
    // Start an elapsed timer so the user can see how long the tool has been
    // running. The timer is stored on the job element and cleared on complete.
    const startTime = Date.now();
    const elapsedEl = job.querySelector('.agent-tool-elapsed') || el('span', { class: 'agent-tool-elapsed', text: '0s' });
    if (!elapsedEl.parentElement) {
      const head = job.querySelector('.agent-tool-job-card-head') || job;
      head.append(elapsedEl);
    }
    job._startedAt = startTime;
    job._elapsedTimer = setInterval(() => {
      const secs = Math.floor((Date.now() - startTime) / 1000);
      elapsedEl.textContent = secs < 60 ? `${secs}s` : `${Math.floor(secs / 60)}m ${secs % 60}s`;
    }, 1000);
  });
  on('agent.tool.delta', (payload) => {
    const { tool_call_id, text, conversation_id } = payload;
    const run = getRunOrQueue('agent.tool.delta', payload);
    if (!run || !text) return;
    // Mirror streamed output into the non-active room buffer so a
    // switch-back shows the accumulated tool output, not just the final
    // snapshot.
    if (conversation_id !== state.activeId) {
      const buffer = getOrCreateRoomBuffer(conversation_id);
      const job = buffer.toolJobs.get(tool_call_id);
      if (job) appendToolJobDelta(job, text);
      touchRoomBuffer(buffer);
      return;
    }
    queueToolDelta(run, tool_call_id, text);
  });
  on('agent.tool.completed', (payload) => {
    const { tool_call_id, name, status, output, conversation_id } = payload;
    const run = getRunOrQueue('agent.tool.completed', payload);
    if (!run) {
      if (conversation_id === state.activeId) void refreshActiveConversation();
      else refreshConversations();
      return;
    }
    // Mirror the completed tool output into the non-active room buffer so a
    // switch-back renders the finished tool card.
    if (conversation_id !== state.activeId) {
      const buffer = getOrCreateRoomBuffer(conversation_id);
      buffer.runId = run.runId;
      buffer.messageId = run.messageId;
      let job = buffer.toolJobs.get(tool_call_id);
      if (!job) {
        job = name === 'generate_image'
          ? renderGenerateImageCard({ name, args: {}, status: 'running' })
          : renderToolJob({ name, args: {}, status: 'running' });
        setLiveToolJob(buffer.toolJobs, tool_call_id, job);
      }
      if (job) {
        // show (op=html): swap terminal for artifact card in the buffer
        // too, so a room switch-back shows the card directly.
        if (name === 'show') {
          if (job._elapsedTimer) { clearInterval(job._elapsedTimer); job._elapsedTimer = null; }
          const toolCall = { name, args: job._toolArgs ?? {}, output: output ?? '', status: status || 'ok' };
          const artifact = parseArtifactOutput(toolCall);
          if (artifact) {
            const card = renderArtifactCard(toolCall, artifact);
            card._toolArgs = toolCall.args;
            card.dataset.standalone = 'true';
            swapToolCard(job, card, buffer.bubble, buffer.strip);
            setLiveToolJob(buffer.toolJobs, tool_call_id, card);
          } else {
            // show(op=image|audio|video): swap terminal for inline media
            // card in the buffer too.
            const showImage = parseShowImageOutput(toolCall);
            const showAudio = parseShowAudioOutput(toolCall);
            const showVideo = parseShowVideoOutput(toolCall);
            if (showImage || showAudio || showVideo) {
              const card = renderToolCallCard(toolCall);
              card._toolArgs = toolCall.args;
              swapToolCard(job, card, buffer.bubble, buffer.strip);
              setLiveToolJob(buffer.toolJobs, tool_call_id, card);
            }
          }
        } else if (name === 'subagent') {
          // Re-render the delegation card with the completion output so a
          // room switch-back shows the settled card, not a stuck "running".
          if (job._elapsedTimer) { clearInterval(job._elapsedTimer); job._elapsedTimer = null; }
          const toolCall = { name, args: job._toolArgs ?? {}, output: output ?? '', status: status || 'ok' };
          const card = renderSubagentCard(toolCall);
          card._toolArgs = toolCall.args;
          job.replaceWith(card);
          setLiveToolJob(buffer.toolJobs, tool_call_id, card);
        } else if (name === 'generate_image') {
          replaceGenerateImageJob(job, payload, (card) => setLiveToolJob(buffer.toolJobs, tool_call_id, card));
        } else {
          const next = { name, args: job._toolArgs, status: status || 'ok', output };
          setToolTerminalStatus(job, next.status);
          job.open = false;
          const meta = job.querySelector('.agent-tool-terminal-meta');
          if (meta) meta.textContent = toolTerminalMeta(next);
          const outputEl = job.querySelector('.agent-tool-terminal-output');
          if (outputEl) {
            outputEl.classList.toggle('is-error', next.status === 'fail');
            outputEl.textContent = toolTerminalOutput(next);
          }
        }
      }
      touchRoomBuffer(buffer);
      refreshLiveDots();
      return;
    }
    const flushedToolDelta = flushPendingToolDeltas(run);
    if (flushedToolDelta) updateRoomInfo(state.conversation, state.messages);
    // ask_question: the ask card is already sealed by agent.ask.answered
    // (or agent.ask.cancelled). Skip the tool terminal update — the ask
    // card IS the tool card, there's no terminal to update.
    if (name === 'ask_question') {
      const job = run.toolJobs.get(tool_call_id);
      if (job?._elapsedTimer) { clearInterval(job._elapsedTimer); job._elapsedTimer = null; }
      return;
    }
    // show (op=html): replace the tool terminal with an artifact card once
    // the tool settles. During execution the user sees a normal tool terminal
    // (so they know something is running); after completion the terminal is
    // swapped for the clickable artifact card.
    if (name === 'show') {
      const job = run.toolJobs.get(tool_call_id);
      if (job?._elapsedTimer) { clearInterval(job._elapsedTimer); job._elapsedTimer = null; }
      const toolCall = { name, args: job?._toolArgs ?? {}, output: output ?? '', status: status || 'ok' };
      const artifact = parseArtifactOutput(toolCall);
      if (artifact && job) {
        const card = renderArtifactCard(toolCall, artifact);
        card._toolArgs = toolCall.args;
        card.dataset.standalone = 'true';
        swapToolCard(job, card, run.bubble, run.strip);
        setLiveToolJob(run.toolJobs, tool_call_id, card);
      } else if (job) {
        // show(op=image|audio|video): swap terminal for inline media card.
        // renderToolCallCard dispatches to the correct card via the show
        // parsers (parseShowImageOutput / parseShowAudioOutput /
        // parseShowVideoOutput). Falls through to renderToolJob when the
        // output doesn't match any show variant.
        const showImage = parseShowImageOutput(toolCall);
        const showAudio = parseShowAudioOutput(toolCall);
        const showVideo = parseShowVideoOutput(toolCall);
        if (showImage || showAudio || showVideo) {
          const card = renderToolCallCard(toolCall);
          card._toolArgs = toolCall.args;
          swapToolCard(job, card, run.bubble, run.strip);
          setLiveToolJob(run.toolJobs, tool_call_id, card);
        }
      }
      return;
    }
    // subagent: re-render the delegation card with the completion output.
    // Without this branch the card would keep showing "running" until a
    // full conversation re-render (turn.done) — or forever if no turn
    // follows the async completion.
    if (name === 'subagent') {
      const job = run.toolJobs.get(tool_call_id);
      if (job?._elapsedTimer) { clearInterval(job._elapsedTimer); job._elapsedTimer = null; }
      const toolCall = { name, args: job?._toolArgs ?? {}, output: output ?? '', status: status || 'ok' };
      const card = renderSubagentCard(toolCall);
      card.dataset.standalone = 'true';
      if (job) {
        swapToolCard(job, card, run.bubble, run.strip);
        setLiveToolJob(run.toolJobs, tool_call_id, card);
        card._toolArgs = toolCall.args;
      } else if (run.strip) {
        placeToolCard(run.bubble, run.strip, card);
        setLiveToolJob(run.toolJobs, tool_call_id, card);
      }
      return;
    }
    if (name === 'generate_image') {
      const job = run.toolJobs.get(tool_call_id);
      replaceGenerateImageJob(job, payload, (card) => {
        setLiveToolJob(run.toolJobs, tool_call_id, card);
        if (!job) placeToolCard(run.bubble, run.strip, card);
      });
      return;
    }
    // When the job is missing — typically because a page refresh re-attached
    // the active run but the in-flight tool card never made it into the
    // buffer — synthesize a card from the completion payload so the final
    // output still renders. The output here is the persisted snapshot, so
    // reload-after-complete works the same as the live delta stream.
    const job = run.toolJobs.get(tool_call_id);
    if (!job) {
      const toolCall = { name, args: run.toolArgs?.get?.(tool_call_id) ?? {}, output: output ?? '', status: status || 'ok' };
      const card = name === 'generate_image' ? renderGenerateImageCard(toolCall) : renderToolJob(toolCall);
      if (isStreamingTool(name)) bindToolStop(card, () => run.runId);
      setLiveToolJob(run.toolJobs, tool_call_id, card);
      if (run.strip) {
        placeToolCard(run.bubble, run.strip, card);
      }
      scrollToBottom();
      return;
    }
    // Write the final elapsed duration before clearing the timer so the
    // finished card keeps showing how long the tool ran.
    const elapsedEl = job.querySelector('.agent-tool-elapsed');
    if (elapsedEl && job._startedAt) {
      const secs = Math.floor((Date.now() - job._startedAt) / 1000);
      elapsedEl.textContent = secs < 60 ? `${secs}s` : `${Math.floor(secs / 60)}m ${secs % 60}s`;
    }
    // Clear the elapsed timer — the final duration stays displayed.
    if (job._elapsedTimer) { clearInterval(job._elapsedTimer); job._elapsedTimer = null; }
    const next = { name: job._toolName || name, args: job._toolArgs, status: status || 'ok', output };
    setToolTerminalStatus(job, next.status);
    job.open = false;
    const meta = job.querySelector('.agent-tool-terminal-meta');
    if (meta) meta.textContent = toolTerminalMeta(next);
    // Room diagnostics stay current after the tool settles.
    updateRoomInfo(state.conversation, state.messages);
    const outputEl = job.querySelector('.agent-tool-terminal-output');
    if (outputEl) {
      outputEl.classList.toggle('is-error', next.status === 'fail');
      outputEl.textContent = toolTerminalOutput(next);
    }
  });
  on('agent.turn.done', (payload) => {
    const { run_id, message_id, model, usage, context_tokens, error, conversation_id } = payload;
    // Adopt the authoritative provider-measured context fill for the badge
    // (source of truth), so idle reflects real usage instead of an estimate.
    if (conversation_id === state.activeId && Number(context_tokens) > 0) {
      state.contextEstimate = Number(context_tokens);
    }
    const run = getRunOrQueue('agent.turn.done', payload);
    if (run && message_id) {
      // refresh the message node with final metadata
      run.msgNode.querySelectorAll('.agent-turn-meta, .agent-message-meta').forEach((n) => n.remove());
      const meta = el('div', { class: 'agent-turn-meta' });
      if (model) meta.append(el('span', { class: 'agent-turn-tag', text: model }));
      if (usage) {
        meta.append(el('span', { class: 'agent-turn-tag', text: `↑${formatTokens(usage.input_tokens ?? 0)} ↓${formatTokens(usage.output_tokens ?? 0)}` }));
        if (usage.cache_read) meta.append(el('span', { class: 'agent-turn-tag', text: `cache ${formatTokens(usage.cache_read)}` }));
      }
      meta.append(el('span', { class: 'agent-message-meta', text: fmtTime(new Date().toISOString()) }));
      run.msgNode.append(meta);
      // Render any Mermaid diagrams in the just-finished message (settle point).
      void renderMermaidDiagrams(run.msgNode); void highlightCode(run.msgNode); attachZoomButtons(run.msgNode);
    }
    // Auto-continue: the backend will reuse the same run_id for the next
    // turn in the chain. Keep the run entry so turn.started for the next
    // turn finds it and streaming continues. Skip refreshActiveConversation
    // mid-chain — it would re-render the thread and stale the DOM refs
    // that the next turn's streaming handlers need. The thread is refreshed
    // when the chain ends (turn.done without should_continue).
    const willAutoContinue = Boolean(payload.auto_continue?.should_continue);
    endTurn(run_id, willAutoContinue);
    if (error) {
      toast(error, 'error');
      playError(state.settings?.sound_notifications !== false);
    } else {
      playComplete(state.settings?.sound_notifications !== false);
    }
    if (conversation_id === state.activeId && !willAutoContinue) refreshActiveConversation();
    void maybeAutoTitleConversation(conversation_id);
    refreshConversations();
  });
  on('agent.turn.error', (payload) => {
    const { run_id, message, message_id, conversation_id } = payload;
    playError(state.settings?.sound_notifications !== false);
    const run = getRunOrQueue('agent.turn.error', payload);
    const fallbackNode = !run && conversation_id === state.activeId && message_id
      ? findMessageNode(agentThread(), message_id)
      : null;
    if (run || fallbackNode) {
      // Mirror the terminal error state into the room buffer so a
      // non-active room still shows the failure when switched back.
      if (conversation_id !== state.activeId) {
        const buffer = getOrCreateRoomBuffer(conversation_id);
        buffer.raw += (buffer.raw ? '\n\n' : '') + (message || 'Turn failed');
        trimRoomBuffer(buffer);
        buffer.done = true;
        touchRoomBuffer(buffer);
        refreshLiveDots();
      }
      const node = run?.msgNode || fallbackNode;
      const bubble = node?.querySelector('.agent-bubble');
      appendLiveError(bubble, message);
      node?.classList.add('agent-message-error');
      // Add retry button directly to the error node so the user can retry
      // without waiting for refreshActiveConversation to re-render.
      const existingRetry = node?.querySelector('.agent-retry-btn');
      if (node && !existingRetry) {
        const retryBtn = el('button', { class: 'agent-retry-btn', type: 'button', text: '↻ Retry with model' });
        retryBtn.addEventListener('click', () => retryTurn(node, message_id));
        node.append(retryBtn);
      }
    }
    endTurn(run_id);
    toast(message || 'Turn failed', 'error');
    if (conversation_id === state.activeId) refreshActiveConversation();
    else refreshConversations();
  });
  on('agent.auto_continue', (payload) => {
    const { conversation_id, decision, continue_text } = payload;
    if (conversation_id !== state.activeId) return;
    const status = providerStatus();
    if (status) {
      const { continues_used, max_auto_continues } = decision || {};
      const label = max_auto_continues === 0
        ? `Continuing tasks… (${continues_used})`
        : `Continuing tasks… (${continues_used}/${max_auto_continues})`;
      status.textContent = label;
      status.classList.add('is-running');
    }
    // Insert the synthetic continue user message into the transcript so the
    // user sees what triggered the next turn. Marked auto_continue so the UI
    // can style it differently from real user messages.
    if (continue_text) {
      const continueMessage = { role: 'user', content: continue_text, auto_continue: true, created_at: new Date().toISOString() };
      state.messages.push(continueMessage);
      const node = renderMessage(continueMessage);
      const thread = agentThread();
      if (thread && node) thread.append(node);
      thread?.scrollTo?.({ top: thread.scrollHeight, behavior: 'smooth' });
    }
  });
  on('agent.ask.pending', (payload) => {
    const { conversation_id, run_id, tool_call_id, question, options, allow_free_text, multi_select } = payload;
    if (conversation_id !== state.activeId) return;
    const run = state.runs.get(run_id);
    if (!run) return;
    // The ask card replaces the tool terminal card entirely (matching
    // Electron's createStreamingToolCard). It lives in the tool strip,
    // not nested inside a tool terminal <details>.
    if (!run.strip) return;
    const card = createAskCard(tool_call_id, { question, options, allow_free_text, multi_select }, {
      runId: run_id,
      onSubmit: (err) => toast(err instanceof Error ? err.message : 'Could not send answer', 'error'),
      onStop: () => rpc('agent.turns.stop', { run_id }).catch(() => {}),
    });
    card.dataset.standalone = 'true';
    setLiveToolJob(run.toolJobs, tool_call_id, card);
    placeToolCard(run.bubble, run.strip, card);
    card.focus();
    scrollToBottom();
  });
  on('agent.ask.answered', (payload) => {
    const { run_id, tool_call_id, answer, via } = payload;
    const run = state.runs.get(run_id);
    if (!run) return;
    // The ask card is stored in toolJobs (not in the bubble).
    const card = run.toolJobs.get(tool_call_id);
    if (!card || !card.classList?.contains('agent-ask-card')) return;
    sealAskCard(card, { ok: true, answer, via, optionIds: payload.option_ids || [], text: payload.text || '' });
  });
  on('agent.ask.cancelled', (payload) => {
    const { run_id, tool_call_id, reason } = payload;
    const run = state.runs.get(run_id);
    if (!run) return;
    const card = run.toolJobs.get(tool_call_id);
    if (!card || !card.classList?.contains('agent-ask-card')) return;
    cancelAskCard(card, reason);
  });
  on('agent.compacted', ({ conversation_id, run_id }) => {
    if (conversation_id !== state.activeId) {
      state.roomBuffers.delete(conversation_id);
      refreshLiveDots();
      return;
    }
    const run = runForConversation(conversation_id);
    if (run) {
      if (!run_id || run.runId === run_id) void applyLiveCompaction(conversation_id, run, run_id);
      return;
    }
    state.roomBuffers.delete(conversation_id);
    refreshActiveConversation();
  });
  on('agent.compaction.failed', ({ conversation_id, run_id, error }) => {
    // Compaction failed but the turn continues with the un-compacted
    // conversation. Warn the user so they know context may fill up soon;
    // this is not turn-fatal (emergency compaction will catch overflow).
    const msg = error || 'Context compaction failed';
    toast(`Compaction failed: ${msg}`, 'error');
    const run = runForConversation(conversation_id);
    if (conversation_id === state.activeId && (!run_id || run?.runId === run_id)) refreshActiveConversation();
  });
  on('agent.steer.queued', ({ conversation_id, steer_id, text }) => {
    if (conversation_id !== state.activeId) return;
    showSteerQueued(text, steer_id);
  });
  on('agent.steer.applied', ({ conversation_id, steer_id, text }) => {
    if (conversation_id !== state.activeId) return;
    promoteSteerToTranscript(text);
  });
  on('agent.steer.cancelled', ({ conversation_id, text, reason }) => {
    if (conversation_id !== state.activeId) return;
    clearSteerQueue();
    // User cancel already restored the draft in the cancel-button handler.
    if (reason === 'user') return;
    // Restore the steer text to the composer so the user can re-send it as a
    // new message. The steer was never applied — it was cancelled because the
    // turn ended without reaching a safe boundary to inject it.
    if (text) {
      const input = composerInput();
      if (input && !input.value.trim()) {
        input.value = text;
        input.dispatchEvent(new Event('input'));
      }
      toast('Steer was not applied (turn ended before a safe boundary). Text restored to composer.', 'info');
    }
  });
  on('agent.todo.updated', (payload) => {
    const { conversation_id, items, summary, brief } = payload;
    // Keep the per-room status fresh (used by the sidebar live dot) even for
    // non-active rooms; only the DOM strip touches the active room.
    if (conversation_id !== state.activeId) {
      const buffer = getOrCreateRoomBuffer(conversation_id);
      touchRoomBuffer(buffer);
      refreshLiveDots();
      return;
    }
    state.todos = { items: items ?? [], summary: summary ?? { total: 0, pending: 0, in_progress: 0, completed: 0 }, brief: brief ?? state.todos.brief ?? '' };
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
  const thread = agentThread();
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

async function applyLiveCompaction(conversationId, run, expectedRunId = '') {
  const token = state.conversationLoadToken;
  try {
    const { conversation, messages } = await rpc('agent.conversations.get', { id: conversationId });
    if (token !== state.conversationLoadToken || state.activeId !== conversationId) return;
    if (runForConversation(conversationId) !== run || (expectedRunId && run.runId !== expectedRunId)) return;
    state.conversation = conversation;
    state.messages = messages ?? [];
    state.contextEstimate = Number(conversation?.context_tokens) || Number(conversation?.estimated_tokens) || 0;
    applyConversationTail();
    state.chunkCount = conversation?.chunk_count ?? 0;
    state.nextChunkIndex = state.chunkCount - 1;
    state.loadedChunks = new Set();
    state.loadingChunk = false;

    const thread = agentThread();
    const liveNode = run.msgNode?.isConnected ? run.msgNode : null;
    const renderedMessageIDs = new Set((liveNode?.dataset.messageIds || '').split(/\s+/).filter(Boolean));
    const snapshot = windowedActiveMessages().filter((message) => !renderedMessageIDs.has(message.id));
    if (liveNode && thread) {
      const frag = renderConversation(snapshot, retryTurn);
      while (liveNode.previousSibling) liveNode.previousSibling.remove();
      let after = liveNode.nextSibling;
      while (after) {
        const next = after.nextSibling;
        if (after.classList?.contains('agent-compaction-marker')) after.remove();
        after = next;
      }
      liveNode.before(frag);
      void renderMermaidDiagrams(thread); void highlightCode(thread); attachZoomButtons(thread);
    } else {
      renderThread(windowedActiveMessages(), state.pinned);
      reattachActiveRun();
    }
    updateOlderSentinel();
    updateComposerStatus();
    scrollToBottom();
  } catch (err) {
    console.warn('applyLiveCompaction failed:', err?.message || err);
    if (state.activeId === conversationId && runForConversation(conversationId) === run) {
      void refreshActiveConversation();
    }
  }
}

async function refreshActiveConversation() {
  if (!state.activeId) return;
  const conversationId = state.activeId;
  const token = state.conversationLoadToken;
  try {
    const { conversation, messages } = await rpc('agent.conversations.get', { id: conversationId });
    if (token !== state.conversationLoadToken || state.activeId !== conversationId) return;
    const liveRun = runForConversation(conversationId);
    state.conversation = conversation;
    state.messages = messages ?? [];
    // Re-sync the context badge with the backend source of truth (the turn
    // that just finished persisted the provider-measured context_tokens).
    state.contextEstimate = Number(conversation?.context_tokens) || Number(conversation?.estimated_tokens) || 0;
    // Re-window to the tail on refresh (a turn finished / compaction changed
    // the set). Render respects the pin so a user who scrolled up is not yanked.
    applyConversationTail();
    // Reset chunk state — compaction may have created new chunks.
    state.chunkCount = conversation?.chunk_count ?? 0;
    state.nextChunkIndex = state.chunkCount - 1;
    state.loadedChunks = new Set();
    state.loadingChunk = false;
    // If a live buffer exists for this room (a turn is still streaming across
    // the refresh race), re-merge it so the visible stream stays stable while
    // the server snapshot catches up.
    const buffer = state.roomBuffers.get(conversationId);
  const hasLiveBuffer = buffer && !buffer.done && buffer.lastEventAt > 0;
    const thread = agentThread();
    const liveNode = liveRun?.msgNode?.isConnected ? liveRun.msgNode : null;
    if (liveRun && liveNode && thread && runForConversation(conversationId) === liveRun) {
      const renderedMessageIDs = new Set((liveNode.dataset.messageIds || '').split(/\s+/).filter(Boolean));
      const snapshot = windowedActiveMessages().filter((message) => !renderedMessageIDs.has(message.id));
      const fragment = renderConversation(snapshot, retryTurn);
      while (liveNode.previousSibling) liveNode.previousSibling.remove();
      let after = liveNode.nextSibling;
      while (after) {
        const next = after.nextSibling;
        after.remove();
        after = next;
      }
      liveNode.before(fragment);
      void renderMermaidDiagrams(thread); void highlightCode(thread); attachZoomButtons(thread);
    } else {
      renderThread(windowedActiveMessages(), state.pinned);
      if (liveRun && runForConversation(conversationId) === liveRun) reattachActiveRun();
      else if (hasLiveBuffer) applyBufferedRunToDOM(conversationId);
    }
    updateOlderSentinel();
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
  if (!label) return;
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

// refresh reloads the conversation list without disrupting the active
// conversation thread (which is kept live by WS events). Called when
// the user navigates back to the Agent view.
export async function refresh() {
  if (backendIsOffline()) return;
  await refreshConversations();
  expireCompletedBuffers();
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
  const wsLabel = workspaceLabel();
  const wsBtn = workspaceButton();
  if (wsLabel) wsLabel.textContent = workspace ? workspace.split(/[\\/]/).filter(Boolean).pop() : 'Home';
  wsBtn.title = workspace || 'Home (user home directory)';

  const status = providerStatus();
  const stopBtn = stopButton();
  // Only consider a run that actually belongs to the room the user is
  // looking at; a turn running in another room should not flip this room's
  // header into "running". A live buffer for the active room counts as
  // running too (the run is still streaming over the WS). After a page
  // refresh the run map is empty until reattachActiveRunFromBackend
  // resolves, so fall back to the persisted status for the active room.
  const activeRun = runForConversation(state.activeId);
  const activeBuffered = state.roomBuffers.get(state.activeId);
  const activeRoomLive = Boolean(
    activeRun || (activeBuffered && !activeBuffered.done && activeBuffered.lastEventAt > 0),
  );
  if (activeRoomLive) {
    // Live turn: keep the useful context-usage badge visible (running is
    // already conveyed by the prompt pulse + activity); just pulse it.
    status.classList.add('is-running');
    if (stopBtn) stopBtn.hidden = false;
    const chosen = models.find((model) => `${model.provider_id}:${model.id}` === state.model) || models.find((model) => model.id === state.model);
    if (chosen) {
      const windowSize = effectiveContextWindow(Number(chosen.context) || 0, Number(state.settings.max_input_tokens) || 0);
      // Backend is the source of truth: state.contextEstimate holds the live
      // server estimate (agent.context.estimate) during the turn and the
      // provider-measured fill (agent.turn.done) after. Never sum UI bubbles.
      status.textContent = formatContextUsage(backendContextTokens(), windowSize);
      return;
    }
    // No model selected — fall back to a neutral running label.
    status.textContent = 'Running';
    return;
  }
  if (state.activeId && state.conversation?.id === state.activeId && state.conversation?.status === 'running') {
    status.classList.add('is-running');
    if (stopBtn) stopBtn.hidden = false;
    status.textContent = 'Running';
    return;
  }
  status.classList.remove('is-running');
  if (stopBtn) stopBtn.hidden = true;
  const chosen = models.find((model) => `${model.provider_id}:${model.id}` === state.model) || models.find((model) => model.id === state.model);
  if (!chosen) {
    status.textContent = 'Choose a model';
    return;
  }
  const contextWindow = effectiveContextWindow(Number(chosen.context) || 0, Number(state.settings.max_input_tokens) || 0);
  // Idle: show the backend source of truth (provider-measured context_tokens,
  // falling back to the server heuristic), never a client bubble sum.
  status.textContent = formatContextUsage(backendContextTokens(), contextWindow);
}

// backendContextTokens returns the server's context-fill number for the active
// room. It prefers the live/last value already tracked in state.contextEstimate
// (seeded from the conversation on open and updated by server events), then the
// persisted provider-measured context_tokens, then the server heuristic
// estimate. The frontend never estimates context by summing message bubbles.
function backendContextTokens() {
  const live = Number(state.contextEstimate);
  if (Number.isFinite(live) && live > 0) return live;
  const ctx = Number(state.conversation?.context_tokens);
  if (Number.isFinite(ctx) && ctx > 0) return ctx;
  const est = Number(state.conversation?.estimated_tokens);
  if (Number.isFinite(est) && est > 0) return est;
  return 0;
}

// ---- sidebar live activity helpers ----

// maybeRenderConversationList re-renders the conversation list only when it
// is the visible view (avoids layout churn while typing in the composer).
function maybeRenderConversationList() {
  const view = document.querySelector('.view.agent-view');
  if (!view || view.classList.contains('active')) renderConversationList();
}

// refreshLiveDots re-renders just the sidebar indicators (no thread touch).
// Called when any non-active room buffer changes so the user can see at a
// glance which rooms are still streaming. Renders are coalesced to one per
// animation frame: while several rooms stream deltas simultaneously this
// keeps the list from being rebuilt dozens of times per second (which both
// wastes layout work and could race a click on a row).
let liveDotsRaf = 0;
function refreshLiveDots() {
  if (liveDotsRaf) return;
  liveDotsRaf = requestAnimationFrame(() => {
    liveDotsRaf = 0;
    maybeRenderConversationList();
  });
}

















