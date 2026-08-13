// Agent workspace: multi-conversation chat with streaming turns.

import { rpc, on, emit } from '../rpc.js';
import { el, fmtTime, toast, confirmDialog, debounce } from '../ui.js';
import { renderMarkdown } from '../markdown.js';
import { estimateContextTokens, formatContextUsage, inspectAttachmentContent, toDataURL } from '../agent-ui.js';

const state = {
  conversations: [],
  activeId: null,
  conversation: null,
  messages: [],
  attachments: [],
  settings: {},
  model: localStorage.getItem('nusashell.model') || '',
  runs: new Map(), // run_id -> {messageEl, toolStripEl, toolJobs}
  pendingEvents: new Map(), // run_id -> events that won the start race
  running: false,
};

const icons = {
  trash: ['M4 7h16M9 7V5h6v2M6.5 7l1 13h9l1-13M10 11v5M14 11v5'],
  send: ['M4 12l16-8-6 16-2.5-6.5L4 12Z'],
  stop: [],
};

export async function initAgent() {
  bindComposer();
  bindConversations();
  bindModelPicker();
  bindEvents();
  await refreshConversations();
  await refreshModels();
  await refreshStatus();
  const first = state.conversations[0];
  if (first) await openConversation(first.id);
  else updateComposerStatus();
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
  document.getElementById('new-conversation-btn').addEventListener('click', createConversation);
}

async function createConversation() {
  if (state.running) return;
  try {
    const { conversation } = await rpc('agent.conversations.create', {});
    state.activeId = conversation.id;
    state.conversation = conversation;
    state.messages = [];
    await refreshConversations();
    renderEmptyThread();
    updateComposerStatus();
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
        if (state.activeId === c.id) {
          state.activeId = null;
          state.conversation = null;
          state.messages = [];
          renderEmptyThread();
          updateComposerStatus();
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

function renderEmptyThread() {
  const thread = document.getElementById('agent-thread');
  thread.innerHTML = '';
  thread.append(el('div', { class: 'agent-empty' },
    el('div', { class: 'agent-empty-mark', text: '✦' }),
    el('h2', { text: 'Start a conversation' }),
    el('p', { text: 'Ask anything. The agent can use skills, memory, docs and your MCP servers as tools.' }),
  ));
  document.getElementById('tool-job-strip').hidden = true;
}

async function openConversation(id) {
  state.activeId = id;
  // the backend returns messages as a sibling of conversation
  const { conversation, messages } = await rpc('agent.conversations.get', { id });
  state.conversation = conversation;
  state.messages = messages ?? [];
  renderConversationList();
  renderThread(state.messages);
  const requestedModel = conversation.model || localStorage.getItem('nusashell.model') || '';
  state.model = models.length && requestedModel && !models.some((model) => model.id === requestedModel) ? '' : requestedModel;
  updateModelTrigger();
  updateComposerStatus();
}

function renderThread(messages) {
  const thread = document.getElementById('agent-thread');
  thread.innerHTML = '';
  if (!messages.length) {
    renderEmptyThread();
    return;
  }
  for (const msg of messages) thread.append(renderMessage(msg));
  thread.scrollTop = thread.scrollHeight;
}

function reasoningDisclosure(reasoning) {
  const content = el('div', { class: 'agent-reasoning-content' });
  content.innerHTML = renderMarkdown(reasoning);
  const details = el('details', { class: 'agent-reasoning' },
    el('summary', {},
      el('span', { class: 'agent-reasoning-mark', text: '⌁' }),
      el('span', { class: 'agent-reasoning-title', text: 'Thinking' }),
      el('span', { class: 'agent-reasoning-hint', text: 'Show reasoning' }),
      el('span', { class: 'agent-reasoning-chevron', text: '⌄' }),
    ),
    content,
  );
  details.addEventListener('toggle', () => {
    const hint = details.querySelector('.agent-reasoning-hint');
    if (hint) hint.textContent = details.open ? 'Hide reasoning' : 'Show reasoning';
  });
  return details;
}

function renderMessage(msg) {
  if (msg.role === 'system') {
    return el('div', { class: 'agent-compaction-marker', text: msg.content });
  }
  const node = el('div', {
    class: `agent-message ${msg.role === 'user' ? 'user' : 'assistant'}${msg.status === 'error' ? ' agent-message-error' : ''}`,
  });
  if (msg.role === 'user') {
    const bubble = el('div', { class: 'agent-bubble' });
    bubble.append(el('div', { text: msg.content || (msg.attachments?.length ? 'Attached files' : '') }));
    if (msg.attachments?.length) bubble.append(renderMessageAttachments(msg.attachments));
    node.append(bubble);
    node.append(el('div', { class: 'agent-message-meta', text: fmtTime(msg.created_at) }));
    return node;
  }
  // assistant: prefer steps (temporal order) when available; fall back to
  // flat fields for older persisted messages
  const bubble = el('div', { class: 'agent-bubble' });
  if (msg.steps?.length) {
    for (const step of msg.steps) {
      if (step.type === 'reasoning' && step.content?.trim()) {
        bubble.append(reasoningDisclosure(step.content));
      } else if (step.type === 'text' && step.content) {
        const textBox = el('div', { class: 'agent-bubble-text' });
        textBox.innerHTML = renderMarkdown(step.content);
        bubble.append(textBox);
      } else if (step.type === 'tool_calls' && step.tool_calls?.length) {
        bubble.append(el('div', { class: 'agent-tool-stack' }, step.tool_calls.map(renderToolJob)));
      }
    }
  } else {
    if (msg.reasoning?.trim()) bubble.append(reasoningDisclosure(msg.reasoning));
    const textBox = el('div', { class: 'agent-bubble-text' });
    if (msg.content) textBox.innerHTML = renderMarkdown(msg.content);
    if (textBox.innerHTML) bubble.append(textBox);
    if (msg.tool_calls?.length) {
      bubble.append(el('div', { class: 'agent-tool-stack' }, msg.tool_calls.map(renderToolJob)));
    }
  }
  if (bubble.children.length) node.append(bubble);
  const meta = el('div', { class: 'agent-turn-meta' });
  if (msg.model) meta.append(el('span', { class: 'agent-turn-tag', text: msg.model }));
  if (msg.usage) {
    meta.append(el('span', { class: 'agent-turn-tag', text: `↑${msg.usage.input_tokens ?? 0} ↓${msg.usage.output_tokens ?? 0}` }));
    if (msg.usage.cache_read) meta.append(el('span', { class: 'agent-turn-tag', text: `cache ${msg.usage.cache_read}` }));
  }
  meta.append(el('span', { class: 'agent-message-meta', text: fmtTime(msg.created_at) }));
  node.append(meta);
  if (msg.status === 'error' && msg.error) {
    node.append(el('div', { class: 'agent-message-meta', text: msg.error }));
  }
  return node;
}

function renderToolJob(tc) {
  const card = el('details', { class: 'agent-tool-terminal' });
  const summary = el('summary', {},
    el('span', { class: 'agent-tool-terminal-prompt', text: '›_' }),
    el('span', { class: 'agent-tool-terminal-title', text: tc.name || 'tool' }),
    el('span', { class: 'agent-tool-terminal-meta', text: toolTerminalMeta(tc) }),
    el('span', { class: 'agent-tool-terminal-chevron', text: '⌄' }),
  );
  const body = el('div', { class: 'agent-tool-terminal-body' },
    toolTerminalPanel('tool', 'agent-tool-terminal-input', formatToolTerminalInput(tc.name, tc.args)),
    toolTerminalPanel('Output', 'agent-tool-terminal-output', toolTerminalOutput(tc)),
  );
  card._toolArgs = tc.args;
  card.append(summary, body);
  setToolTerminalStatus(card, tc.status || 'running');
  return card;
}

function toolTerminalPanel(label, codeClass, text) {
  return el('div', { class: 'agent-tool-terminal-panel' },
    el('div', { class: 'agent-tool-terminal-panel-label', text: label }),
    el('pre', { class: codeClass, text }),
  );
}

function summarizeToolArgs(args) {
  if (!args || typeof args !== 'object' || Array.isArray(args)) return '';
  const entries = Object.entries(args);
  if (!entries.length) return '';
  if (entries.length === 1) {
    const value = JSON.stringify(entries[0][1]);
    return value.length > 42 ? `${value.slice(0, 42)}…` : value;
  }
  return `${entries.length} args`;
}

function formatToolTerminalInput(name, args) {
  const tool = String(name || 'tool');
  const input = args && typeof args === 'object' ? truncate(JSON.stringify(args, null, 2), 8000) : '';
  return input ? `${tool}(${input})` : `${tool}()`;
}

function toolTerminalMeta(tc) {
  const status = tc.status || 'running';
  return status === 'running' ? 'Running' : summarizeToolArgs(tc.args) || (status === 'fail' ? 'Failed' : 'Completed');
}

function toolTerminalOutput(tc) {
  if (tc.output !== undefined && tc.output !== null && tc.output !== '') return truncate(String(tc.output), 12000);
  return tc.status === 'running' ? '…' : tc.status === 'fail' ? 'Tool failed.' : 'ok';
}

function setToolTerminalStatus(card, status) {
  const normalized = status || 'running';
  card.classList.toggle('is-running', normalized === 'running');
  card.classList.toggle('is-success', normalized === 'ok');
  card.classList.toggle('is-error', normalized === 'fail');
  card.dataset.status = normalized;
}

function truncate(s, n) {
  return s.length > n ? s.slice(0, n) + '\n… (truncated)' : s;
}

function attachmentIcon(attachment) {
  if (attachment.type === 'image') return 'IMG';
  if (attachment.type === 'file') return 'PDF';
  return 'TXT';
}

function attachmentChip(attachment, onRemove) {
  const chip = el('span', { class: 'agent-attachment' },
    el('span', { class: 'agent-attachment-name', text: `${attachmentIcon(attachment)} · ${attachment.name || 'Attachment'}` }),
  );
  if (onRemove) {
    const remove = el('button', { class: 'agent-attachment-remove', type: 'button', title: `Remove ${attachment.name || 'attachment'}`, 'aria-label': `Remove ${attachment.name || 'attachment'}` }, '×');
    remove.addEventListener('click', onRemove);
    chip.append(remove);
  }
  return chip;
}

function renderMessageAttachments(attachments) {
  const gallery = el('div', {
    class: 'agent-message-attachments',
    'aria-label': `${attachments.length} attachment${attachments.length === 1 ? '' : 's'}`,
  });
  for (const attachment of attachments) {
    if (attachment.type === 'image') {
      const image = el('img', { src: attachment.data_url, alt: attachment.name, loading: 'lazy' });
      gallery.append(el('figure', { class: 'agent-message-attachment agent-message-image' },
        image,
        el('figcaption', { text: attachment.name }),
      ));
      continue;
    }
    gallery.append(el('div', { class: 'agent-message-attachment agent-message-file' },
      el('span', { class: 'agent-message-file-kind', text: attachment.type === 'file' ? 'PDF' : 'TXT' }),
      el('span', { class: 'agent-message-file-name', text: attachment.name }),
    ));
  }
  return gallery;
}

function renderAttachments() {
  const container = document.getElementById('agent-attachments');
  container.innerHTML = '';
  for (const [index, attachment] of state.attachments.entries()) {
    container.append(attachmentChip(attachment, () => {
      state.attachments.splice(index, 1);
      renderAttachments();
      updateSendAvailability();
    }));
  }
}

// ---------- composer / turns ----------

function bindComposer() {
  const form = document.getElementById('agent-form');
  const input = document.getElementById('composer-input');
  const stopBtn = document.getElementById('stop-btn');
  const attachBtn = document.getElementById('agent-attach-btn');
  const fileInput = document.getElementById('agent-file-input');
  const workspaceBtn = document.getElementById('agent-workspace-btn');

  const autosize = () => {
    input.style.height = 'auto';
    input.style.height = Math.min(input.scrollHeight, 180) + 'px';
  };
  input.addEventListener('input', () => {
    autosize();
    updateSendAvailability();
  });
  input.addEventListener('keydown', (e) => {
    if (e.isComposing || e.keyCode === 229) return;
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      send();
    }
  });
  form.addEventListener('submit', (e) => {
    e.preventDefault();
    send();
  });
  attachBtn.addEventListener('click', () => fileInput.click());
  fileInput.addEventListener('change', async () => {
    await addAttachments(fileInput.files);
    fileInput.value = '';
  });
  form.addEventListener('dragover', (e) => e.preventDefault());
  form.addEventListener('drop', async (e) => {
    e.preventDefault();
    await addAttachments(e.dataTransfer?.files);
  });
  workspaceBtn.addEventListener('click', chooseWorkspace);
  stopBtn.addEventListener('click', async () => {
    for (const runId of state.runs.keys()) {
      try { await rpc('agent.turns.stop', { run_id: runId }); } catch { /* ignore */ }
    }
    stopBtn.hidden = true;
  });

  async function send() {
    const text = input.value.trim();
    if ((!text && !state.attachments.length) || state.running) return;
    if (!state.model) {
      toast('Choose a model first (Models with a provider must be imported in Providers).', 'error');
      return;
    }
    try {
      if (!state.activeId) {
        const { conversation } = await rpc('agent.conversations.create', { title: text.slice(0, 48) });
        state.activeId = conversation.id;
        state.conversation = conversation;
        state.messages = [];
        await refreshConversations();
      }
      const attachments = [...state.attachments];
      const { run_id } = await rpc('agent.turns.start', {
        conversation_id: state.activeId,
        text,
        model: state.model,
        attachments,
      });
      input.value = '';
      autosize();
      state.attachments = [];
      renderAttachments();
      beginTurn(run_id, text, attachments);
    } catch (err) {
      toast(err.message, 'error');
    }
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
      ...(detected.type === 'text'
        ? { content: detected.content }
        : { data_url: toDataURL(bytes, detected.mediaType) }),
    });
  }
  renderAttachments();
  updateSendAvailability();
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
  } catch (err) {
    toast(err.message, 'error');
  }
}

function beginTurn(runId, userText, attachments = []) {
  state.running = true;
  document.getElementById('composer-input').disabled = true;
  document.getElementById('stop-btn').hidden = false;
  updateSendAvailability();

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
    round: 1,
  });
  flushPendingEvents(runId);
  updateComposerStatus();
  thread.scrollTop = thread.scrollHeight;
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
  run.msgNode.classList.remove('agent-pending');
  state.runs.delete(runId);
  if (!state.runs.size) {
    state.running = false;
    document.getElementById('composer-input').disabled = false;
    document.getElementById('stop-btn').hidden = true;
    updateSendAvailability();
    updateComposerStatus();
  }
}

function bindEvents() {
  on('agent.turn.started', (payload) => {
    const { run_id, message_id, round } = payload;
    const run = getRunOrQueue('agent.turn.started', payload);
    if (!run) return;
    run.messageId = message_id;
    // round 1: clear the placeholder; round 2+: append new step elements
    // after the previous round's tool strip, preserving temporal order
    if (!round || round <= 1) {
      if (run.textBox) run.textBox.textContent = '';
      run.raw = '';
      run.rawReasoning = '';
      run.round = 1;
      return;
    }
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
    run.raw = '';
    run.rawReasoning = '';
    run.round = round;
  });
  on('agent.message.delta', (payload) => {
    const { text } = payload;
    const run = getRunOrQueue('agent.message.delta', payload);
    if (!run || !run.textBox) return;
    // accumulate the RAW markdown, never re-read the rendered DOM: the
    // rendered textContent loses newlines and already-consumed markers, so
    // re-rendering from it would collapse paragraphs and break bold/lists
    run.raw += text;
    run.textBox.innerHTML = renderMarkdown(run.raw);
    const thread = document.getElementById('agent-thread');
    thread.scrollTop = thread.scrollHeight;
  });
  on('agent.reasoning.delta', (payload) => {
    const { text } = payload;
    const run = getRunOrQueue('agent.reasoning.delta', payload);
    if (!run) return;
    run.rawReasoning += text;
    if (run.reasoningEl) {
      run.reasoningEl.hidden = false;
      const content = run.reasoningEl.querySelector('.agent-reasoning-content');
      if (content) content.innerHTML = renderMarkdown(run.rawReasoning);
      const thread = document.getElementById('agent-thread');
      thread.scrollTop = thread.scrollHeight;
    }
  });
  on('agent.tool.started', (payload) => {
    const { tool_call_id, name, args } = payload;
    const run = getRunOrQueue('agent.tool.started', payload);
    if (!run) return;
    run.strip.hidden = false;
    const job = renderToolJob({ name, args: args ?? {}, status: 'running' });
    run.toolJobs.set(tool_call_id, job);
    run.strip.append(job);
  });
  on('agent.tool.completed', (payload) => {
    const { tool_call_id, status, output } = payload;
    const run = getRunOrQueue('agent.tool.completed', payload);
    if (!run) return;
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
    const { run_id, message_id, model, usage, error } = payload;
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
    refreshActiveConversation();
    refreshConversations();
  });
  on('agent.turn.error', (payload) => {
    const { run_id, message } = payload;
    const run = getRunOrQueue('agent.turn.error', payload);
    if (run) {
      const bubble = run.msgNode.querySelector('.agent-bubble');
      if (bubble) bubble.textContent = message || 'Turn failed';
      run.msgNode.classList.add('agent-message-error');
    }
    endTurn(run_id);
    toast(message || 'Turn failed', 'error');
  });
  on('agent.compacted', ({ conversation_id, summary }) => {
    if (conversation_id !== state.activeId) return;
    const thread = document.getElementById('agent-thread');
    thread.append(el('div', { class: 'agent-compaction-marker', text: `Compacted · ${summary ? 'context summarized' : 'history trimmed'}` }));
    thread.scrollTop = thread.scrollHeight;
    refreshActiveConversation();
  });
}

async function refreshActiveConversation() {
  if (!state.activeId) return;
  try {
    const { conversation, messages } = await rpc('agent.conversations.get', { id: state.activeId });
    state.conversation = conversation;
    state.messages = messages ?? [];
    renderThread(state.messages);
    updateComposerStatus();
  } catch {
    // The list refresh will surface a deleted conversation if it raced a turn.
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
  label.textContent = chosen ? chosen.id : (state.model || 'No model');
  label.title = chosen ? `${chosen.id} · ${chosen.provider_name}` : '';
  updateComposerStatus();
}

export function bindModelPicker() {
  const trigger = document.getElementById('model-trigger');
  const menu = document.getElementById('model-menu');
  const closeMenu = () => {
    menu.hidden = true;
    trigger.setAttribute('aria-expanded', 'false');
  };

  const openMenu = () => {
    try {
      renderModelMenu();
      menu.hidden = false;
      const rect = trigger.getBoundingClientRect();
      const menuH = menu.offsetHeight || 320;
      const spaceBelow = window.innerHeight - rect.bottom;
      const spaceAbove = rect.top;
      const menuWidth = menu.offsetWidth || 560;
      menu.style.left = Math.max(8, Math.min(rect.left, window.innerWidth - menuWidth - 8)) + 'px';
      if (spaceBelow < menuH + 6 && spaceAbove > spaceBelow) {
        // the composer sits at the bottom edge: open upward instead of
        // overflowing past the viewport
        menu.style.top = Math.max(8, rect.top - menuH - 6) + 'px';
      } else {
        menu.style.top = rect.bottom + 6 + 'px';
      }
      trigger.setAttribute('aria-expanded', 'true');
      const input = menu.querySelector('input');
      if (input) input.focus();
    } catch (err) {
      console.error('model picker:', err);
    }
  };

  trigger.addEventListener('click', () => {
    if (!menu.hidden) {
      closeMenu();
      return;
    }
    // open immediately with the current list, then refresh in the
    // background so models imported elsewhere appear without a reload
    openMenu();
    refreshModels().then(() => {
      if (!menu.hidden) openMenu();
    }).catch((err) => console.error('model refresh:', err));
  });
  // keep the picker fresh when the Agent view becomes active again
  window.addEventListener('hashchange', () => {
    if (location.hash === '' || location.hash === '#agent') refreshModels();
  });
  document.addEventListener('mousedown', (e) => {
    if (!menu.hidden && !menu.contains(e.target) && e.target !== trigger) closeMenu();
  });
}

function renderModelMenu() {
  const menu = document.getElementById('model-menu');
  menu.innerHTML = '';
  menu.append(el('div', { class: 'agent-model-search' },
    el('input', { type: 'text', placeholder: 'Search models…', autocomplete: 'off' }),
  ));
  const list = el('div', { class: 'agent-model-list' });
  const byProvider = new Map();
  for (const m of models) {
    if (!byProvider.has(m.provider_name)) byProvider.set(m.provider_name, []);
    byProvider.get(m.provider_name).push(m);
  }
  if (!models.length) {
    list.append(el('div', { class: 'agent-model-empty', text: 'No models yet. Configure a provider and import its models first.' }));
  }
  for (const [provider, ms] of byProvider) {
    const section = el('div', { class: 'agent-model-section' },
      el('div', { class: 'agent-model-section-title', text: provider }),
    );
    for (const m of ms) {
      const row = el('div', {
        class: `agent-model-row${m.id === state.model ? ' is-selected' : ''}`,
      },
        el('button', { class: 'agent-model-choice', type: 'button' },
          el('span', { class: 'agent-model-name', text: m.id }),
          el('span', { class: 'agent-model-meta' },
            el('span', { class: 'agent-model-provider', text: m.provider_name }),
          ),
        ),
      );
      row.querySelector('button').addEventListener('click', () => {
        state.model = m.id;
        localStorage.setItem('nusashell.model', m.id);
        updateModelTrigger();
        menu.hidden = true;
        trigger.setAttribute('aria-expanded', 'false');
      });
      section.append(row);
    }
    list.append(section);
  }
  const search = menu.querySelector('input');
  search.addEventListener('input', debounce(() => {
    const q = search.value.toLowerCase();
    for (const section of list.querySelectorAll('.agent-model-section')) {
      const name = section.querySelector('.agent-model-section-title').textContent;
      const rows = [...section.querySelectorAll('.agent-model-row')];
      for (const row of rows) {
        const id = row.querySelector('.agent-model-name').textContent;
        row.hidden = !(id.toLowerCase().includes(q) || name.toLowerCase().includes(q));
      }
    }
  }, 120));
  menu.append(list);
}

export async function openConversationExternal(id) {
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

function updateSendAvailability() {
  const input = document.getElementById('composer-input');
  const send = document.getElementById('send-btn');
  send.disabled = state.running || (!input.value.trim() && !state.attachments.length);
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
  const contextWindow = Number(chosen.context) || Number(state.settings.compaction_threshold) || 0;
  status.textContent = formatContextUsage(estimateContextTokens(state.messages), contextWindow);
}
