import { renderMarkdown } from '../../markdown.js';
import { el, fmtTime, registerOverlayDismiss } from '../../ui.js';
import { rpc } from '../../rpc.js';
import { createAskCard } from '../ask-card.js';
import { openDrawer, agentNameForId, firstVisibleRunId } from './subagents.js';
import { renderArtifactCard, parseArtifactOutput } from '../../artifact-render.js';
import { openAudioLightbox, openVideoLightbox, openTextPreviewPopup, attachZoomButtons } from '../../media-zoom.js';
import { renderMermaidDiagrams } from '../../mermaid-render.js';
import { highlightCode } from '../../highlight-render.js';
import { normalizeToolCall, toolContractClass, toolContractRef } from './tool-contracts.js';
import { agentThread, composerInput, toolJobStrip } from './domrefs.js';

// Live-turn performance strategy: every streaming round stays mounted in the
// DOM (nothing is parked, trimmed, or hidden behind a stub — the old
// "N earlier rounds trimmed for performance" workaround traded UX away).
// Overload protection instead comes from targeted enhancement in agent.js —
// mermaid/highlight/zoom run only on the blocks incrementalRender reports
// as new or changed, so per-delta cost is proportional to the delta, never
// to the whole bubble. (CSS content-visibility on .agent-round was tried as
// an extra guard and reverted: its paint containment clipped the reasoning
// mark / tool-terminal chrome drawn at the round edges.)

export const STARTER_PROMPTS = [
  {
    label: 'Research a topic',
    prompt: 'Help me research a topic. I\'ll ask the question, and you gather sources before answering.',
  },
  {
    label: 'Write & edit',
    prompt: 'Help me draft or improve some writing. I\'ll paste the text or describe what I need next.',
  },
  {
    label: 'Tidy my files',
    prompt: 'Look around my workspace and help me organize, summarize, or clean up the files there.',
  },
  {
    label: 'Automate a task',
    prompt: 'I\'ll describe a task I repeat often — help me script it or schedule it as a CI workflow.',
  },
];

export function applyStarterPrompt(prompt) {
  const input = composerInput();
  if (!input || !prompt) return;
  input.value = prompt;
  const EventCtor = input.ownerDocument?.defaultView?.Event || globalThis.Event;
  input.dispatchEvent(new EventCtor('input', { bubbles: true }));
  input.focus();
}

function starterChip(item) {
  return el('button', {
    class: 'agent-starter',
    type: 'button',
    'data-starter-prompt': item.prompt,
    text: item.label,
    onclick: () => applyStarterPrompt(item.prompt),
  });
}

export function bindStarterPrompts() {
  agentThread()?.addEventListener('click', (event) => {
    const localLink = event.target.closest?.('a[data-local-path]');
    if (localLink) {
      event.preventDefault();
      const path = localLink.getAttribute('data-local-path');
      if (path) void openTextPreviewPopup(path);
      return;
    }
    const chip = event.target.closest?.('[data-starter-prompt]');
    if (!chip) return;
    applyStarterPrompt(chip.getAttribute('data-starter-prompt'));
  });
}

export function renderEmptyThread() {
  const thread = agentThread();
  thread.innerHTML = '';
  thread.append(el('div', { class: 'agent-empty' },
    el('div', { class: 'agent-empty-mark', text: '✦' }),
    el('h2', { text: 'Start a conversation' }),
    el('p', { text: 'Ask anything. The agent can use skills, memory, docs and your MCP servers as tools.' }),
    el('div', { class: 'agent-starter-prompts', id: 'agent-starter-prompts' },
      STARTER_PROMPTS.map(starterChip),
    ),
    el('p', { class: 'agent-empty-hint', text: 'Ctrl+K search rooms · Ctrl+N new · Ctrl+Enter send' }),
  ));
  toolJobStrip().hidden = true;
  const todoStrip = document.getElementById('agent-todo-strip');
  if (todoStrip) todoStrip.hidden = true;
}

// renderTodoItem creates a single todo item row with a status glyph, content,
// and a delete button. The onDelete callback is invoked when the button is
// clicked; the button is passed so the caller can disable it during async.
export function renderTodoItem(item, onDelete) {
  const status = item.status || 'pending';
  const glyphClass = status === 'completed' ? 'is-completed' : status === 'in_progress' ? 'is-in-progress' : 'is-pending';
  const glyph = status === 'completed' ? '✓' : status === 'in_progress' ? '◐' : '☐';
  const deleteBtn = el('button', {
    class: 'agent-todo-item-delete',
    type: 'button',
    title: 'Remove task',
    'aria-label': `Remove task: ${item.content || ''}`,
  }, '×');
  if (typeof onDelete === 'function') {
    deleteBtn.addEventListener('click', () => onDelete(item.id, deleteBtn));
  }
  return el('li', { class: `agent-todo-item is-${status}`, role: 'listitem' },
    el('span', { class: `agent-todo-item-glyph ${glyphClass}`, text: glyph, 'aria-hidden': 'true' }),
    el('span', { class: 'agent-todo-item-content', text: item.content || '' }),
    deleteBtn,
  );
}

// reasoningHasVisibleSource reports whether raw reasoning has anything to show
// without parsing markdown — used to decide if the collapsed Thinking row
// should appear at all.
export function reasoningHasVisibleSource(raw) {
  if (typeof raw !== 'string' || !raw) return false;
  return raw.replace(/[\u200B-\u200D\uFEFF\u2060\u2063]/g, '').trim().length > 0;
}

// compactLine keeps collapsed UI labels to one visual line without exposing
// a raw multiline payload. It is intentionally presentation-only: the full
// source remains available in the expanded disclosure or raw inspector.
function compactLine(value, limit = 64) {
  const text = String(value ?? '').replace(/[\u200B-\u200D\uFEFF\u2060\u2063]/g, '').replace(/\s+/g, ' ').trim();
  if (!text) return '';
  if (text.length <= limit) return text;
  return `${text.slice(0, Math.max(1, limit - 1)).trimEnd()}…`;
}

// reasoningPreview gives the collapsed Thinking row enough context to scan
// without parsing/rendering Markdown. Fenced code is omitted because it is
// rarely useful as a preview and can make the header look like a tool output.
function reasoningPreview(raw) {
  const text = String(raw || '')
    .replace(/[\u200B-\u200D\uFEFF\u2060\u2063]/g, '')
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/^\s{0,3}#{1,6}\s*/gm, '')
    .replace(/^\s*>\s?/gm, '')
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/[\*_~`]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
    // This phrase is emitted as a context-resumption marker in several
    // rounds. Drop it when the same line has useful reasoning after it.
    .replace(/^continue from (?:the )?current context\.?\s*/i, '')
    .trim();
  return compactLine(text);
}

// reasoningHasVisibleContent returns true when a rendered reasoning block
// actually contains something the user can see. It strips zero-width and
// whitespace-only text, and counts visual elements (images, diagrams, tables,
// horizontal rules, etc.) as visible content. A collapsed disclosure with
// stored raw source also counts as visible so the Thinking row can appear
// before markdown is parsed.
export function reasoningHasVisibleContent(content) {
  if (!content) return false;
  const details = content.classList?.contains('agent-reasoning')
    ? content
    : content.closest?.('.agent-reasoning');
  if (details && reasoningHasVisibleSource(details._reasoningRaw)) return true;
  const text = (content.textContent || '').replace(/[\u200B-\u200D\uFEFF\u2060\u2063]/g, '').trim();
  if (text.length > 0) return true;
  return content.querySelector?.('img, svg, video, canvas, table, hr, .mermaid') !== null;
}

function materializeReasoning(details) {
  if (!details?.open) return;
  const raw = details._reasoningRaw || '';
  const content = details.querySelector('.agent-reasoning-content');
  if (!content) return;
  const marker = `${raw.length}:${raw.slice(0, 48)}`;
  if (content.dataset.rendered === marker) return;
  content.innerHTML = raw ? renderMarkdown(raw) : '';
  content.dataset.rendered = marker;
  void renderMermaidDiagrams(content);
  void highlightCode(content);
  attachZoomButtons(content);
}

// setReasoningSource updates the stored raw reasoning on a disclosure without
// parsing markdown. If the user already opened it, the body is refreshed.
export function setReasoningSource(details, raw, options = {}) {
  if (!details) return;
  details._reasoningRaw = typeof raw === 'string' ? raw : '';
  details.hidden = !reasoningHasVisibleSource(details._reasoningRaw);
  if (details.open && options.render !== false) materializeReasoning(details);
}

export function reasoningDisclosure(reasoning, options = {}) {
  const raw = typeof reasoning === 'string' ? reasoning : '';
  const title = typeof options.title === 'string' && options.title.trim() ? options.title : 'Thinking';
  const collapsedHint = typeof options.collapsedHint === 'string' && options.collapsedHint.trim()
    ? options.collapsedHint : 'Show reasoning';
  const openHint = typeof options.openHint === 'string' && options.openHint.trim()
    ? options.openHint : 'Hide reasoning';
  const mark = typeof options.mark === 'string' && options.mark ? options.mark : '⌁';
  const preview = reasoningPreview(raw);
  const content = el('div', { class: 'agent-reasoning-content' });
  const details = el('details', { class: 'agent-reasoning' },
    el('summary', {},
      el('span', { class: 'agent-reasoning-mark', text: mark }),
      el('span', { class: 'agent-reasoning-title', text: title }),
      el('span', { class: 'agent-reasoning-preview', text: preview, title: preview }),
      el('span', { class: 'agent-reasoning-hint', text: collapsedHint }),
      el('span', { class: 'agent-reasoning-chevron', text: '⌄' }),
    ),
    content,
  );
  details._reasoningRaw = raw;
  details.hidden = !reasoningHasVisibleSource(raw);
  details.addEventListener('toggle', () => {
    const hint = details.querySelector('.agent-reasoning-hint');
    if (hint) hint.textContent = details.open ? openHint : collapsedHint;
    if (details.open) materializeReasoning(details);
  });
  return details;
}

const disclosureSelector = 'details.agent-reasoning, details.agent-tool-event';

function disclosureStateKeys(details, root) {
  const kind = details.classList.contains('agent-tool-event')
    ? 'tool'
    : 'thinking';
  const round = details.closest('.agent-round');
  const message = details.closest('.agent-message');
  const firstMessageID = message?.dataset.messageIds?.split(/\s+/).find(Boolean) || '';
  const ownerKey = round?.dataset.messageId
    || firstMessageID
    || message?.dataset.messageId
    || '';
  const owner = round || message || root;
  const siblings = [...owner.querySelectorAll(kind === 'tool'
    ? 'details.agent-tool-event'
    : 'details.agent-reasoning')];
  const ordinal = Math.max(0, siblings.indexOf(details));
  const all = [...root.querySelectorAll(disclosureSelector)];
  const keys = [];
  if (kind === 'tool' && details.dataset.toolCallId) {
    keys.push(`${kind}:${ownerKey || 'conversation'}:${details.dataset.toolCallId}`);
  }
  if (ownerKey) keys.push(`${kind}:${ownerKey}:${ordinal}`);
  keys.push(`${kind}:anonymous:${all.indexOf(details)}`);
  return keys;
}

// captureDisclosureState records only user-controlled disclosure state. The
// content itself remains owned by the conversation snapshot, so a refresh can
// rebuild the transcript without surprising the reader by collapsing panels.
export function captureDisclosureState(root) {
  const states = new Map();
  if (!root?.querySelectorAll) return states;
  for (const details of root.querySelectorAll(disclosureSelector)) {
    for (const key of disclosureStateKeys(details, root)) states.set(key, Boolean(details.open));
  }
  return states;
}

export function restoreDisclosureState(root, states) {
  if (!root?.querySelectorAll || !(states instanceof Map)) return;
  for (const details of root.querySelectorAll(disclosureSelector)) {
    const key = disclosureStateKeys(details, root).find((candidate) => states.has(candidate));
    if (key !== undefined && details.open !== states.get(key)) details.open = states.get(key);
  }
}

// renderCompactionStatus renders the short-lived inline status shown while
// the backend is folding the conversation context. It deliberately lives
// outside the persisted message model: the matching compacted/failed event
// removes it when the operation reaches a terminal state.
export function renderCompactionStatus() {
  return el('div', {
    class: 'agent-compaction-status',
    role: 'status',
    'aria-live': 'polite',
    'aria-atomic': 'true',
  },
  el('span', { class: 'agent-compaction-status-mark', text: '↻', 'aria-hidden': 'true' }),
  el('span', { class: 'agent-compaction-status-text', text: 'Context automatically compacting' }),
  el('span', { class: 'agent-compaction-status-dots', 'aria-hidden': 'true' },
    el('span'), el('span'), el('span'),
  ),
  );
}

export function appendLiveError(bubble, message = 'Turn failed') {
  if (!bubble) return null;
  let errorEl = bubble.querySelector(':scope > .agent-live-error');
  if (!errorEl) {
    errorEl = el('div', { class: 'agent-message-meta agent-error-text agent-live-error' });
    bubble.append(errorEl);
  }
  errorEl.textContent = message || 'Turn failed';
  return errorEl;
}

// mountLiveRound appends one streaming round (reasoning + text + tool strip)
// to the live assistant bubble. Rounds are never parked or trimmed: the whole
// turn history stays mounted so nothing disappears mid-stream (see the
// strategy comment at the top of this file).
export function mountLiveRound(bubble, source = {}) {
  const reasoningEl = reasoningDisclosure(source.rawReasoning || '');
  const textBox = el('div', { class: 'agent-bubble-text' });
  const strip = el('div', { class: 'agent-tool-stack' });
  strip.hidden = true;
  const round = el('div', { class: 'agent-round' });
  if (source.messageId) round.dataset.messageId = source.messageId;
  round.append(reasoningEl, textBox, strip);
  bubble.append(round);
  return { reasoningEl, textBox, strip };
}

export function thinkingDots() {
  return el('span', { class: 'agent-thinking-dots' }, el('span'), el('span'), el('span'));
}

function ensureThinkingDots(textBox) {
  if (!textBox || textBox.querySelector('.agent-thinking-dots')) return;
  textBox.append(thinkingDots());
}

// setThinkingDots controls the one generic wait indicator for a live round.
// Compaction has its own more descriptive status row, so callers can suppress
// these dots while compaction is active and restore them when the provider is
// ready to continue. Keeping the operation idempotent prevents event-order
// races from creating duplicate loading indicators.
export function setThinkingDots(textBox, visible) {
  if (!textBox) return;
  textBox.querySelectorAll('.agent-thinking-dots').forEach((dots) => dots.remove());
  if (visible && !textBox.textContent.trim()) ensureThinkingDots(textBox);
}

function newPendingSlot() {
  const bubble = el('div', { class: 'agent-bubble' });
  const msgNode = el('div', { class: 'agent-message assistant agent-pending' }, bubble);
  const refs = mountLiveRound(bubble, {});
  refs.reasoningEl.hidden = true;
  ensureThinkingDots(refs.textBox);
  return { msgNode, bubble, ...refs };
}

// bindOptimisticTurn paints the user bubble and a single Thinking-dots
// placeholder. If agent.turn.started already mounted a placeholder (WS
// raced ahead of the turns.start HTTP response), the user is inserted
// before that node and the placeholder is reused. Appending a second
// pending node is what produced two "..." rows — one above the user and
// one below.
export function bindOptimisticTurn(thread, userMessage, existingSlot = null) {
  const userNode = renderMessage(userMessage);
  if (existingSlot?.msgNode?.isConnected) {
    existingSlot.msgNode.before(userNode);
    existingSlot.msgNode.classList.add('agent-pending');
    ensureThinkingDots(existingSlot.textBox);
    return existingSlot;
  }
  thread.append(userNode);
  const slot = newPendingSlot();
  thread.append(slot.msgNode);
  return slot;
}

export function sealLiveNodeBeforeSteer(node) {
  if (!node) return;
  node.querySelectorAll('.agent-thinking-dots').forEach((dots) => dots.remove());
  node.querySelectorAll(':scope > .agent-bubble > .agent-round').forEach((round) => {
    const reasoning = round.querySelector(':scope > .agent-reasoning');
    const textBox = round.querySelector(':scope > .agent-bubble-text');
    const strip = round.querySelector(':scope > .agent-tool-stack');
    const hasReasoning = reasoningHasVisibleSource(reasoning?._reasoningRaw);
    const hasText = Boolean(textBox?.textContent?.trim() || textBox?.querySelector('img, svg, video, canvas, table, hr, .mermaid'));
    const hasTools = Boolean(strip?.children.length);
    if (!hasReasoning && !hasText && !hasTools) round.remove();
  });
}

export function insertAfterOrAppend(parent, node, anchor) {
  if (!parent || !node) return;
  if (anchor?.parentElement === parent) {
    anchor.after(node);
    return;
  }
  parent.append(node);
}

export function renderMessage(message) {
  if (message.role === 'system' || isCompactionSummary(message)) {
    return renderCompactionMessage(message);
  }
  if (message.role === 'user') {
    const node = el('div', { class: `agent-message user${message.steer ? ' agent-steer' : ''}` });
    const bubble = el('div', { class: 'agent-bubble' });
    const content = el('div', { class: 'agent-bubble-text' });
    const raw = typeof message.content === 'string' ? message.content : '';
    if (raw) content.innerHTML = renderMarkdown(raw);
    if (!content.innerHTML && raw) content.textContent = raw;
    if (!content.innerHTML && message.attachments?.length) content.textContent = 'Attached files';
    bubble.append(content);
    if (message.attachments?.length) bubble.append(renderMessageAttachments(message.attachments));
    node.append(bubble);
    const meta = el('div', { class: 'agent-message-meta' });
    if (message.steer) {
      meta.append(el('span', { class: 'agent-message-steer-flag', text: 'Steer message' }));
    }
    meta.append(el('span', { text: fmtTime(message.created_at) }));
    node.append(meta);
    return node;
  }

  return renderAssistantTurn([message]);
}

// isCompactionSummary detects a compaction handover message by its content
// prefix. Compaction summaries carry role=user so they appear in the
// provider request's messages array, but the UI renders them as
// assistant-style bubbles — matching the domain.CompactionSummaryPrefix.
function isCompactionSummary(message) {
  return typeof message.content === 'string' && message.content.startsWith('[COMPACTION CHECKPOINT]');
}

// renderCompactionMessage renders a compaction handover inside the same
// expandable disclosure used by Thinking. The summary is intentionally
// collapsed so a long handover does not take over the room on reload.
function renderCompactionMessage(message) {
  const node = el('div', { class: 'agent-message assistant agent-compaction-marker' });
  if (message.id) node.dataset.messageIds = message.id;
  const bubble = el('div', { class: 'agent-bubble' });
  const raw = typeof message.content === 'string' ? message.content : '';
  const handover = raw.startsWith('[COMPACTION CHECKPOINT]')
    ? raw.slice('[COMPACTION CHECKPOINT]'.length).trimStart()
    : raw;
  const details = reasoningDisclosure(handover, {
    title: 'Compacted context',
    collapsedHint: 'Show handover',
    openHint: 'Hide handover',
    mark: '↳',
  });
  details.classList.add('agent-compaction-reasoning');
  bubble.append(details);
  node.append(bubble);
  node.append(el('div', { class: 'agent-turn-meta' },
    el('span', { class: 'agent-message-meta', text: fmtTime(message.created_at) }),
  ));
  return node;
}

export function renderConversation(messages, onRetry) {
  const transcript = document.createDocumentFragment();
  for (let index = 0; index < messages.length;) {
    const message = messages[index];
    if (message.role !== 'assistant') {
      transcript.append(renderMessage(message));
      index++;
      continue;
    }

    // One agent run may persist an assistant message for each tool round.
    // Consecutive assistant messages are still one response to the preceding
    // user message, so they share a single model and usage summary.
    const turn = [];
    while (messages[index]?.role === 'assistant') {
      turn.push(messages[index]);
      index++;
    }
    transcript.append(renderAssistantTurn(turn, onRetry));
  }
  return transcript;
}

function renderAssistantTurn(messages, onRetry) {
  const finalMessage = messages[messages.length - 1];
  // Retry is offered only for a failed message that is STILL the last
  // message of this turn group. A failed message followed by a completed
  // assistant message was already recovered (a successful retry or a later
  // turn — the error status stays in formed history by design). Showing
  // Retry on it made the backend answer NOT_FOUND ("no failed assistant
  // turn to retry") because its lastFailedAssistantIndex scan stops at
  // the first done assistant from the end.
  const failedMessage = finalMessage?.status === 'error' ? finalMessage : null;
  // Stamp the persisted message ids this node was rendered from so runtime
  // code can locate the node that owns a specific in-flight message. Reattach
  // must convert ONLY that node into a streaming slot — replacing the last
  // assistant node wholesale would wipe earlier tool rounds that
  // renderConversation merged into the same node.
  const messageIds = messages.map((message) => message.id).filter(Boolean);
  const node = el('div', {
    class: `agent-message assistant${failedMessage ? ' agent-message-error' : ''}`,
  });
  if (messageIds.length) node.dataset.messageIds = messageIds.join(' ');

  const bubble = el('div', { class: 'agent-bubble' });
  for (const message of messages) appendAssistantSteps(bubble, message);
  if (bubble.children.length) node.append(bubble);

  const meta = el('div', { class: 'agent-turn-meta' });
  const model = [...messages].reverse().find((message) => message.model)?.model;
  if (model) meta.append(el('span', { class: 'agent-turn-tag', text: model }));
  if (finalMessage.context_updated) {
    meta.append(el('span', { class: 'agent-turn-tag agent-context-updated-badge', text: '⟳ Context updated' }));
  }
  const usage = totalUsage(messages);
  if (usage) {
    meta.append(el('span', { class: 'agent-turn-tag', text: `↑${formatTokens(usage.input_tokens)} ↓${formatTokens(usage.output_tokens)}` }));
    if (usage.cache_read) meta.append(el('span', { class: 'agent-turn-tag', text: `cache ${formatTokens(usage.cache_read)}` }));
  }
  meta.append(el('span', { class: 'agent-message-meta', text: fmtTime(finalMessage.created_at) }));
  node.append(meta);
  if (failedMessage?.error) node.append(el('div', { class: 'agent-message-meta agent-error-text', text: failedMessage.error }));
  if (failedMessage && onRetry) {
    const retryBtn = el('button', { class: 'agent-retry-btn', type: 'button', text: '↻ Retry with model' });
    // Pass the failed message ID so retryTurn can remove only the failed
    // message's DOM, not the entire turn node (which may contain successful
    // assistant messages from earlier rounds in the same turn).
    retryBtn.addEventListener('click', () => onRetry(node, failedMessage.id));
    node.append(retryBtn);
  }
  return node;
}

function appendAssistantSteps(bubble, message) {
  const round = el('div', { class: 'agent-round' });
  if (message.id) round.dataset.messageId = message.id;
  if (message.steps?.length) {
    for (const step of message.steps) {
      if (step.type === 'reasoning' && step.content?.trim()) {
        round.append(reasoningDisclosure(step.content));
      } else if (step.type === 'text' && step.content) {
        const textBox = el('div', { class: 'agent-bubble-text' });
        textBox.innerHTML = renderMarkdown(step.content);
        round.append(textBox);
      } else if (step.type === 'tool_calls' && step.tool_calls?.length) {
        appendToolCards(round, step.tool_calls.map(renderToolCallCard));
      }
    }
  } else {
    if (message.reasoning?.trim()) round.append(reasoningDisclosure(message.reasoning));
    const textBox = el('div', { class: 'agent-bubble-text' });
    if (message.content) textBox.innerHTML = renderMarkdown(message.content);
    if (textBox.innerHTML) round.append(textBox);
    if (message.tool_calls?.length) appendToolCards(round, message.tool_calls.map(renderToolCallCard));
  }
  if (round.children.length) bubble.append(round);
}

// appendToolCards splits rendered tool cards into standalone cards (ask,
// show, generate_*, artifact, subagent — anything with its own frame) and
// tool terminals (exec, grep, file_read — plain collapsible rows). Standalone
// cards append directly to the bubble so they render without the
// execution rail; terminals are grouped inside a stack so the rail applies
// only to terminal-style output.
function appendToolCards(bubble, cards) {
  const terminals = [];
  for (const card of cards) {
    if (!card) continue;
    if (card.dataset.standalone === 'true') {
      bubble.append(card);
    } else {
      terminals.push(card);
    }
  }
  if (terminals.length) {
    bubble.append(el('div', { class: 'agent-tool-stack' }, terminals));
  }
}

function totalUsage(messages) {
  let hasUsage = false;
  const total = { input_tokens: 0, output_tokens: 0, cache_read: 0 };
  for (const message of messages) {
    if (!message.usage) continue;
    hasUsage = true;
    total.input_tokens += message.usage.input_tokens ?? 0;
    total.output_tokens += message.usage.output_tokens ?? 0;
    total.cache_read += message.usage.cache_read ?? 0;
  }
  return hasUsage ? total : null;
}

// renderToolCallCard dispatches to the right card type based on the tool
// name. ask_question renders as a sealed ask card (matching Electron's
// toolActivity); subagent renders as a delegation card (clickable → drawer);
// everything else renders as a tool terminal.
//
// Cards with their own border/frame (ask_question, subagent, generate_*,
// show, artifact) are marked dataset.standalone="true" so the caller can
// place them outside the .agent-tool-stack rail — the stack's rail is a
// visual cue for tool terminals (exec, grep, file_read) only, and
// looks wrong around a media card or ask panel that already has its own
// frame.
export function isSubagentAuxiliaryTool(name) {
  return name === 'subagent_wait' || name === 'subagent_result';
}

export function isMediaGenerationTool(name) {
  return name === 'generate_media' || name === 'generate_image' || name === 'generate_speech' || name === 'generate_video';
}

// decorateToolCard is the shared DOM boundary for built-in tools. The
// backend contract owns the stable class identity; data attributes make the
// request/result contract addressable without coupling future styling to a
// renderer's internal layout classes.
export function decorateToolCard(card, toolCall) {
  if (!card) return card;
  const normalized = normalizeToolCall(toolCall);
  const name = normalized.name || 'tool';
  const ref = toolContractRef(name, normalized.presentation);
  card.classList.add('agent-tool-card', ref.css_class);
  card.dataset.tool = name;
  card.dataset.toolContract = ref.id;
  card.dataset.toolContractVersion = String(ref.version);
  card.dataset.toolClass = ref.css_class;
  if (normalized.presentation?.variant) card.dataset.presentationVariant = String(normalized.presentation.variant);
  if (normalized.presentation?.result?.format) card.dataset.resultFormat = String(normalized.presentation.result.format);
  if (card.matches?.('.artifact-card')) {
    card.classList.add('agent-tool-result', `${ref.css_class}-result`);
  }
  const markParts = (selector, part) => {
    card.querySelectorAll(selector).forEach((node) => {
      node.classList.add(`agent-tool-${part}`, `${ref.css_class}-${part}`);
    });
  };
  markParts('.agent-tool-event-path, .agent-ask-question, .agent-subagent-prompt, .agent-genimage-head, .agent-genaudio-head, .agent-genvideo-head, .agent-genimage-prompt, .agent-genaudio-prompt, .agent-genvideo-prompt', 'request');
  markParts('.agent-tool-event-result, .agent-tool-terminal-output, .agent-ask-answer, .agent-subagent-summary, .agent-genimage-plate, .agent-genaudio-plate, .agent-genvideo-plate, .artifact-card', 'result');
  return card;
}

export function renderToolCallCard(toolCall) {
  // `subagent` is the user-facing delegation card. The wait call and the
  // synthetic result call are provider bookkeeping for that same run; showing
  // them as extra terminal rows duplicates the card and adds no affordance.
  toolCall = normalizeToolCall(toolCall);
  if (isSubagentAuxiliaryTool(toolCall.name)) return null;
  const finish = (card) => decorateToolCard(card, toolCall);
  if (toolCall.name === 'ask_question') {
    // args arrives as a parsed object from the wire DTO (json.RawMessage);
    // parseToolArgs tolerates both object and JSON-string shapes.
    const parsedArgs = parseToolArgs(toolCall.args);
    const card = createAskCard(toolCall.id, parsedArgs, {
      sealed: true,
      output: toolCall.output || '',
      ok: toolCall.status !== 'fail',
    });
    card.dataset.standalone = 'true';
    return finish(card);
  }
  if (toolCall.name === 'subagent') {
    const card = renderSubagentCard(toolCall);
    card.dataset.standalone = 'true';
    return finish(card);
  }
  if (isMediaGenerationTool(toolCall.name)) {
    const card = renderMediaGenerationCard(toolCall);
    card.dataset.standalone = 'true';
    return finish(card);
  }
  const artifact = parseArtifactOutput(toolCall);
  if (artifact) {
    const card = renderArtifactCard(toolCall, artifact);
    card.dataset.standalone = 'true';
    return finish(card);
  }
  const showImage = parseShowImageOutput(toolCall);
  if (showImage) {
    const card = renderShowImageCard(toolCall, showImage);
    card.dataset.standalone = 'true';
    return finish(card);
  }
  const showAudio = parseShowAudioOutput(toolCall);
  if (showAudio) {
    const card = renderShowAudioCard(toolCall, showAudio);
    card.dataset.standalone = 'true';
    return finish(card);
  }
  const showVideo = parseShowVideoOutput(toolCall);
  if (showVideo) {
    const card = renderShowVideoCard(toolCall, showVideo);
    card.dataset.standalone = 'true';
    return finish(card);
  }
  return finish(renderToolJob(toolCall));
}

function renderMediaGenerationCard(toolCall) {
  if (toolCall.name !== 'generate_media') {
    if (toolCall.name === 'generate_image') return renderGenerateImageCard(toolCall);
    if (toolCall.name === 'generate_speech') return renderGenerateSpeechCard(toolCall);
    if (toolCall.name === 'generate_video') return renderGenerateVideoCard(toolCall);
    return renderToolJob(toolCall);
  }
  const args = parseToolArgs(toolCall.args);
  const kind = String(args.media_type || '').toLowerCase();
  const normalizedName = kind === 'image' ? 'generate_image' : kind === 'speech' ? 'generate_speech' : kind === 'video' ? 'generate_video' : '';
  if (!normalizedName) return renderToolJob(toolCall);
  return renderMediaGenerationCard({ ...toolCall, name: normalizedName });
}

// parseShowImageOutput extracts a show(op=image) result from a tool call's
// output. Returns { src, path, name } or null when the output is not a
// show image result. The src field may be absent — the backend no longer
// embeds a base64 data URL in the tool output (it bloats the conversation
// JSON and is useless to the LLM). When src is missing, the frontend falls
// back to /local-file?path= using the path field.
export function parseShowImageOutput(toolCall) {
  if (toolCall.name !== 'show') return null;
  if (!toolCall.output) return null;
  try {
    const parsed = JSON.parse(toolCall.output);
    if (parsed && parsed.show && parsed.show.type === 'image' && parsed.show.path) {
      const src = parsed.show.src || ('/local-file?path=' + encodeURIComponent(parsed.show.path));
      return { src, path: parsed.show.path, name: parsed.show.name || '' };
    }
    return null;
  } catch {
    return null;
  }
}

// parseShowAudioOutput mirrors parseShowImageOutput for the audio variant.
// The wire shape is { show: { type:"audio", path, name } }. The src field
// may be absent — the backend no longer embeds a base64 data URL in the
// tool output. When src is missing, the frontend falls back to
// /local-file?path= using the path field. Returns null for any non-audio
// show result so the dispatcher in renderToolCallCard can fall through to
// the default tool terminal.
export function parseShowAudioOutput(toolCall) {
  if (toolCall.name !== 'show') return null;
  if (!toolCall.output) return null;
  try {
    const parsed = JSON.parse(toolCall.output);
    if (parsed && parsed.show && parsed.show.type === 'audio' && parsed.show.path) {
      const src = parsed.show.src || ('/local-file?path=' + encodeURIComponent(parsed.show.path));
      return {
        src,
        path: parsed.show.path,
        name: parsed.show.name || '',
      };
    }
    return null;
  } catch {
    return null;
  }
}

// parseShowVideoOutput mirrors parseShowImageOutput / parseShowAudioOutput
// for the video variant. The wire shape is { show: { type, path, name } }.
// The src field may be absent — the backend no longer embeds a base64 data
// URL in the tool output (a 4.5MB video becomes a 6MB base64 string that
// bloats the conversation JSON and is useless to the LLM). When src is
// missing, the frontend falls back to /local-file?path= using the path
// field. Returns null for any non-video show result so the dispatcher in
// renderToolCallCard can fall through to the default tool terminal.
export function parseShowVideoOutput(toolCall) {
  if (toolCall.name !== 'show') return null;
  if (!toolCall.output) return null;
  try {
    const parsed = JSON.parse(toolCall.output);
    if (parsed && parsed.show && parsed.show.type === 'video' && parsed.show.path) {
      const src = parsed.show.src || ('/local-file?path=' + encodeURIComponent(parsed.show.path));
      return {
        src,
        path: parsed.show.path,
        name: parsed.show.name || '',
      };
    }
    return null;
  } catch {
    return null;
  }
}

// renderShowImageCard builds an inline image card for a show(op=image) result.
// Mirrors the generated-image card layout but simpler (no model/cost chips).
function renderShowImageCard(toolCall, showImage) {
  const card = el('div', { class: 'agent-genimage-card is-done', 'data-tool': 'show' });
  const head = el('div', { class: 'agent-genimage-head' },
    el('span', { class: 'agent-genimage-kicker', text: 'image' }),
    el('span', { class: 'agent-genimage-title', text: showImage.path ? showImage.path.split(/[\\/]/).pop() : 'Image' }),
  );
  card.append(head);
  const plate = el('div', { class: 'agent-genimage-plate' });
  const gallery = el('div', { class: 'agent-genimage-gallery', 'data-count': '1' });
  const label = showImage.path ? showImage.path.split(/[\\/]/).pop() : 'Image';
  const img = el('img', { src: showImage.src, alt: label, loading: 'lazy' });
  img.addEventListener('error', () => img.classList.add('img-load-error'));
  const open = () => openImageLightbox({ src: showImage.src, name: label, caption: showImage.path });
  const btn = el('button', { class: 'agent-genimage-open', type: 'button', 'aria-label': 'View ' + label });
  btn.append(img);
  btn.addEventListener('click', open);
  gallery.append(el('figure', { class: 'agent-genimage-frame' }, btn));
  plate.append(gallery);
  card.append(plate);
  const caption = el('div', { class: 'agent-genimage-caption' });
  if (showImage.path) {
    caption.append(el('a', {
      class: 'agent-genimage-download',
      href: showImage.src,
      download: label,
      text: 'Download',
    }));
  }
  if (caption.children.length) card.append(caption);
  decorateToolCard(card, toolCall);
  return card;
}

// renderShowAudioCard builds an inline audio card for a show(op=audio)
// result. Mirrors renderShowImageCard layout: a header with the path
// basename, a plate with a click-to-play button wrapping a hidden audio
// element (controls surface on click via openAudioLightbox), and a
// Download affordance. The inline data URL is the playback source so the
// user hears the audio without an HTTP round trip when the tool result
// was just delivered.
function renderShowAudioCard(toolCall, showAudio) {
  const card = el('div', { class: 'agent-genaudio-card is-done', 'data-tool': 'show' });
  const title = showAudio.name || (showAudio.path ? showAudio.path.split(/[\\/]/).pop() : 'Audio');
  const head = el('div', { class: 'agent-genaudio-head' },
    el('span', { class: 'agent-genaudio-kicker', text: 'audio' }),
    el('span', { class: 'agent-genaudio-title', text: title }),
  );
  card.append(head);
  const plate = el('div', { class: 'agent-genaudio-plate' });
  const audio = el('audio', { controls: true, preload: 'metadata', src: showAudio.src });
  audio.addEventListener('error', () => audio.classList.add('audio-load-error'));
  const open = () => openAudioLightbox({ src: showAudio.src, name: title, caption: showAudio.path });
  const btn = el('button', { class: 'agent-genaudio-open', type: 'button', 'aria-label': 'Listen to ' + title });
  btn.append(audio);
  btn.addEventListener('click', open);
  plate.append(btn);
  card.append(plate);
  const caption = el('div', { class: 'agent-genaudio-caption' });
  if (showAudio.path) {
    caption.append(el('a', {
      class: 'agent-genaudio-download',
      href: showAudio.src,
      download: title,
      text: 'Download',
    }));
  }
  if (caption.children.length) card.append(caption);
  decorateToolCard(card, toolCall);
  return card;
}

// renderShowVideoCard builds an inline video card for a show(op=video)
// result. Mirrors renderShowImageCard and renderShowAudioCard layout:
// a header with the path basename, a plate with a click-to-play button
// wrapping a hidden <video> element (controls surface on click via
// openVideoLightbox), and a Download affordance. The inline data URL is
// the playback source so the user can play the video without an HTTP
// round trip when the tool result was just delivered.
function renderShowVideoCard(toolCall, showVideo) {
  const card = el('div', { class: 'agent-genvideo-card is-done', 'data-tool': 'show' });
  const title = showVideo.name || (showVideo.path ? showVideo.path.split(/[\\/]/).pop() : 'Video');
  const head = el('div', { class: 'agent-genvideo-head' },
    el('span', { class: 'agent-genvideo-kicker', text: 'video' }),
    el('span', { class: 'agent-genvideo-title', text: title }),
  );
  card.append(head);
  const plate = el('div', { class: 'agent-genvideo-plate' });
  const video = el('video', { controls: true, preload: 'metadata', src: showVideo.src });
  video.addEventListener('error', () => video.classList.add('video-load-error'));
  const open = () => openVideoLightbox({ src: showVideo.src, name: title, caption: showVideo.path });
  const btn = el('button', { class: 'agent-genvideo-open', type: 'button', 'aria-label': 'Play ' + title });
  btn.append(video);
  btn.addEventListener('click', open);
  plate.append(btn);
  card.append(plate);
  const caption = el('div', { class: 'agent-genvideo-caption' });
  if (showVideo.path) {
    caption.append(el('a', {
      class: 'agent-genvideo-download',
      href: showVideo.src,
      download: title,
      text: 'Download',
    }));
  }
  if (caption.children.length) card.append(caption);
  decorateToolCard(card, toolCall);
  return card;
}
// Async flow: saat spawn, output = YAML frontmatter
// `runs: [{id, status, workspace}]` (status "starting"). Saat selesai,
// output = YAML frontmatter (status, workspace, output_path) + markdown
// body (summary). Card re-render via EventToolCompleted.
export function renderSubagentCard(toolCall) {
  toolCall = normalizeToolCall(toolCall);
  const args = parseToolArgs(toolCall.args);
  let runs = [];
  let meta = {};          // parsed YAML header
  let outputText = '';   // markdown body (summary)
  try {
    const parsed = JSON.parse(toolCall.output || '{}');
    runs = parsed.runs || [];
  } catch {
    // Not JSON — try YAML frontmatter + markdown body
    const parsed = parseSubagentResult(toolCall.output || '');
    meta = parsed.meta || {};
    outputText = parsed.body;
    if (parsed.meta?.runs?.length) runs = parsed.meta.runs;
  }
  // The original `subagent` tool call is replaced with a short completion
  // sentence after the run finishes. That sentence still contains the stable
  // acprun_* ID; recover it so a reloaded card can ask the backend for the
  // historical transcript instead of falling back to whichever run is cached.
  const embeddedRunIDs = extractSubagentRunIDs(toolCall.output || '');
  if (!runs.length && embeddedRunIDs.length) runs = embeddedRunIDs.map((id) => ({ id }));

  const agentName = agentNameForId(args.agent_id) || 'Subagent';
  const promptPreview = (args.prompt || '').split('\n')[0].slice(0, 120);
  // Single-run spawn results are flat in the UI: merge the run's fields
  // up so status/workspace/error reflect the actual spawn outcome (a
  // failed spawn would otherwise look like a success — the tool call
  // itself returns ok while the run item carries status: failed).
  if (runs.length === 1) {
    const first = runs[0];
    if (!meta.status && first.status) meta.status = first.status;
    if (!meta.workspace && first.workspace) meta.workspace = first.workspace;
    if (!meta.error && first.error) meta.error = first.error;
    if (!meta.id && first.id) meta.id = first.id;
  }
  const anyRunFailed = runs.some((r) => r.status === 'failed');
  const isRunning = toolCall.status === 'running' || !toolCall.output;
  const isFailed = toolCall.status === 'fail' || meta?.status === 'failed' || anyRunFailed;
  const isCancelled = meta?.status === 'cancelled';
  const status = isRunning ? 'running' : (isCancelled ? 'cancelled' : (isFailed ? 'error' : 'success'));

  const runIDs = [...new Set(runs.map((run) => run.id).filter(Boolean))];
  const card = el('div', { class: `agent-subagent-card is-${status}`, role: 'button', tabindex: '0', 'aria-label': `Open ${agentName} transcript` });
  card.dataset.tool = 'subagent';
  card._toolArgs = toolCall.args;
  if (runIDs.length === 1) card.dataset.runId = runIDs[0];

  // Header: icon + agent name + status
  const header = el('div', { class: 'agent-subagent-header' },
    el('span', { class: 'agent-subagent-icon', text: '◇' }),
    el('span', { class: 'agent-subagent-name', text: agentName }),
    el('span', { class: `agent-subagent-status is-${status}`, text: isRunning ? 'running' : (isCancelled ? 'cancelled' : (isFailed ? 'failed' : 'done')) }),
  );

  // Body: prompt preview + metadata + summary + runs list
  const body = el('div', { class: 'agent-subagent-body' });
  if (promptPreview) {
    body.append(el('p', { class: 'agent-subagent-prompt', text: promptPreview }));
  }
  // Metadata chips (workspace, output_path) saat done
  if (meta) {
    const metaRow = el('div', { class: 'agent-subagent-meta' });
    if (meta.workspace) {
      metaRow.append(el('span', { class: 'agent-subagent-meta-chip', title: meta.workspace, text: '📁 ' + meta.workspace.split('/').pop() }));
    }
    if (meta.output_path) {
      metaRow.append(el('span', { class: 'agent-subagent-meta-chip', title: meta.output_path, text: '📄 transcript' }));
    }
    if (metaRow.children.length) body.append(metaRow);
  }
  // Summary preview saat done
  if (!isRunning && outputText) {
    const summaryPreview = outputText.split('\n')[0].slice(0, 160);
    body.append(el('p', { class: 'agent-subagent-summary', text: summaryPreview }));
  }
  // Spawn error message (single failed run with no summary body)
  if (!isRunning && meta?.error) {
    body.append(el('p', { class: 'agent-subagent-summary is-error', text: meta.error.slice(0, 200) }));
  }
  if (runs.length > 1) {
    body.append(el('div', { class: 'agent-subagent-runs' },
      ...runs.map((r) => el('div', { class: `agent-subagent-run is-${r.status}`, 'data-run-id': r.id },
        el('span', { class: 'agent-subagent-run-status', text: r.status === 'completed' ? '✓' : (r.status === 'failed' ? '✗' : '•') }),
        el('span', { class: 'agent-subagent-run-id', text: r.id.slice(-8) }),
      )),
    ));
  }
  body.append(el('span', { class: 'agent-subagent-cta', text: isRunning ? 'View live transcript →' : 'View transcript →' }));

  card.append(header, body);

  card.addEventListener('click', (event) => {
    const runRow = event.target.closest('[data-run-id]');
    const runId = runRow?.dataset.runId || card.dataset.runId || runs[0]?.id || meta?.id || embeddedRunIDs[0] || firstVisibleRunId();
    if (runId) openDrawer(runId);
  });
  card.addEventListener('keydown', (event) => {
    if (event.key !== 'Enter' && event.key !== ' ') return;
    event.preventDefault();
    card.click();
  });

  decorateToolCard(card, toolCall);
  return card;
}

function extractSubagentRunIDs(raw) {
  const ids = [];
  const seen = new Set();
  for (const match of String(raw).matchAll(/\bacprun_[A-Za-z0-9_-]+/g)) {
    if (seen.has(match[0])) continue;
    seen.add(match[0]);
    ids.push(match[0]);
  }
  return ids;
}

// parseSubagentResult splits a YAML frontmatter + markdown body tool
// result into { meta, body }. Returns { meta: null, body: raw } if the
// input is not in the expected format.
//
// The frontmatter is parsed line-wise, including YAML list items
// (`- key: value` blocks, as produced by the spawn result's `runs:`),
// which are collected into meta.runs as an array of { key: value }
// objects. Indented continuation lines belong to the current list item;
// unindented lines belong to the top-level meta.
function parseSubagentResult(raw) {
  if (!raw.startsWith('---\n')) return { meta: null, body: raw };
  const end = raw.indexOf('\n---\n', 4);
  if (end < 0) return { meta: null, body: raw };
  const header = raw.slice(4, end);
  const body = raw.slice(end + 5);
  const meta = {};
  const runs = [];
  let currentRun = null;
  for (const line of header.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    if (trimmed.startsWith('- ')) {
      currentRun = {};
      runs.push(currentRun);
      assignSubagentKeyValue(currentRun, trimmed.slice(2));
      continue;
    }
    if (currentRun && line.startsWith(' ')) {
      assignSubagentKeyValue(currentRun, trimmed);
      continue;
    }
    currentRun = null;
    assignSubagentKeyValue(meta, line);
  }
  if (runs.length) meta.runs = runs;
  return { meta, body };
}

// assignSubagentKeyValue sets target[key] = value from a "key: value"
// line, stripping surrounding quotes from the value.
function assignSubagentKeyValue(target, text) {
  const idx = text.indexOf(':');
  if (idx < 0) return;
  const key = text.slice(0, idx).trim();
  let val = text.slice(idx + 1).trim();
  // strip surrounding quotes
  if (val.startsWith('"') && val.endsWith('"')) {
    val = val.slice(1, -1).replace(/\\"/g, '"').replace(/\\\\/g, '\\');
  }
  target[key] = val;
}

function parseToolArgs(args) {
  if (!args) return {};
  if (typeof args === 'object' && !Array.isArray(args)) return args;
  if (typeof args === 'string') {
    try { return JSON.parse(args); } catch { return {}; }
  }
  return {};
}

function toolPresentationParts(toolCall) {
  const result = toolCall?.presentation?.result;
  if (result && result.format === 'media') {
    return {
      meta: result.meta && typeof result.meta === 'object' ? result.meta : {},
      body: result.text ? String(result.text) : '',
    };
  }
  return parseSubagentResult(toolCall?.output || '');
}

function imageSrc(attachment) {
  if (!attachment) return '';
  if (attachment.data_url) return attachment.data_url;
  if (attachment.file_path) return '/local-file?path=' + encodeURIComponent(attachment.file_path);
  return '';
}

// renderToolAttachments is the canonical output attachment renderer. Tool
// output uses its own class namespace so a future tool can be styled without
// changing the message attachment layout.
export function renderToolAttachments(attachments, className = 'agent-tool-output-attachments') {
  const items = Array.isArray(attachments) ? attachments.filter((item) => item && typeof item === 'object') : [];
  if (!items.length) return null;
  const root = el('div', {
    class: `${className} agent-tool-attachment-list`,
    role: 'list',
    'aria-label': `${items.length} tool output attachment${items.length === 1 ? '' : 's'}`,
  });
  for (const attachment of items) {
    const type = String(attachment.type || 'file').toLowerCase();
    const name = String(attachment.name || 'Tool output');
    const src = imageSrc(attachment);
    const item = el('div', { class: `agent-tool-attachment-item agent-tool-attachment-${type}`, role: 'listitem' });
    if (type === 'image' && src) {
      const image = el('img', { src, alt: name, loading: 'lazy' });
      image.addEventListener('error', () => image.classList.add('img-load-error'));
      item.append(image, el('span', { class: 'agent-tool-attachment-name', text: name }));
    } else if (type === 'video' && src) {
      const video = el('video', { controls: true, preload: 'metadata', src });
      video.addEventListener('error', () => video.classList.add('video-load-error'));
      item.append(video, el('span', { class: 'agent-tool-attachment-name', text: name }));
    } else if (type === 'audio' && src) {
      const audio = el('audio', { controls: true, preload: 'metadata', src });
      audio.addEventListener('error', () => audio.classList.add('audio-load-error'));
      item.append(audio, el('span', { class: 'agent-tool-attachment-name', text: name }));
    } else {
      const label = src
        ? el('a', { class: 'agent-tool-attachment-link', href: src, download: name, text: name })
        : el('span', { class: 'agent-tool-attachment-name', text: name });
      item.append(
        el('span', { class: 'agent-tool-attachment-kind', text: type.toUpperCase() }),
        label,
        attachment.content ? el('pre', { class: 'agent-tool-attachment-text', text: String(attachment.content) }) : null,
      );
    }
    root.append(item);
  }
  return root;
}

function formatImageCost(value) {
  const n = Number(value);
  if (!Number.isFinite(n) || n <= 0) return '';
  if (n < 0.01) return `$${n.toFixed(4)}`;
  return `$${n.toFixed(2)}`;
}

export function renderGenerateImageCard(toolCall) {
  toolCall = normalizeToolCall(toolCall);
  const args = parseToolArgs(toolCall.args);
  const parsed = toolPresentationParts(toolCall);
  const meta = parsed.meta || {};
  const attachments = [...(toolCall.output_attachments || toolCall.attachments || [])]
    .filter((item) => !item.type || item.type === 'image');
  const isFailed = toolCall.status === 'fail' || meta.status === 'failed';
  const hasPresentationResult = Boolean(toolCall.presentation?.result?.text || meta.file_path || attachments.length);
  const isRunning = !isFailed && (toolCall.status === 'running' || (!toolCall.output && !hasPresentationResult));
  const status = isRunning ? 'running' : (isFailed ? 'error' : 'success');
  const prompt = String(args.prompt || '').trim();
  const promptPreview = prompt.split('\n')[0].slice(0, 140);

  const card = el('div', { class: `agent-genimage-card is-${status}`, 'data-tool': 'generate_image' });
  card._toolArgs = toolCall.args;
  card._toolName = 'generate_image';

  const head = el('div', { class: 'agent-genimage-head' },
    el('span', { class: 'agent-genimage-kicker', text: 'proof' }),
    el('span', { class: 'agent-genimage-title', text: isRunning ? 'Developing' : (isFailed ? 'Did not develop' : 'Print') }),
    el('span', { class: 'agent-tool-elapsed', text: '' }),
  );
  card.append(head);

  const plate = el('div', { class: 'agent-genimage-plate' });
  if (isRunning) {
    plate.append(el('div', { class: 'agent-genimage-grain', 'aria-hidden': 'true' }));
    plate.append(el('p', { class: 'agent-genimage-wait', text: 'Emulsion in the tray' }));
  } else if (isFailed) {
    const errText = (parsed.body || toolCall.output || 'Image generation failed').replace(/^error:\s*/i, '').slice(0, 280);
    plate.append(el('p', { class: 'agent-genimage-error', text: errText }));
  } else {
    const images = [...attachments];
    if (!images.length && meta.file_path) {
      images.push({ type: 'image', name: meta.file_path.split(/[\\/]/).pop(), file_path: meta.file_path, media_type: meta.media_type });
    }
    if (!images.length) {
      plate.append(el('p', { class: 'agent-genimage-wait', text: 'No image bytes in this result' }));
    } else {
      const gallery = el('div', { class: 'agent-genimage-gallery', 'data-count': String(images.length) });
      for (const attachment of images) {
        const src = imageSrc(attachment);
        const label = attachment.name || promptPreview || 'Generated image';
        const img = el('img', { src, alt: label, loading: 'lazy' });
        img.addEventListener('error', () => img.classList.add('img-load-error'));
        const open = () => openImageLightbox({
          src,
          name: attachment.name || 'generated-image.png',
          caption: [meta.model, meta.size, meta.quality, formatImageCost(meta.cost_usd)].filter(Boolean).join(' · '),
        });
        const btn = el('button', {
          class: 'agent-genimage-open',
          type: 'button',
          'aria-label': 'View ' + label,
        });
        btn.append(img);
        btn.addEventListener('click', open);
        gallery.append(el('figure', { class: 'agent-genimage-frame' }, btn));
      }
      plate.append(gallery);
    }
  }
  card.append(plate);

  const caption = el('div', { class: 'agent-genimage-caption' });
  if (promptPreview) {
    caption.append(el('p', { class: 'agent-genimage-prompt', text: promptPreview, title: prompt }));
  }
  const chips = el('div', { class: 'agent-genimage-meta' });
  const model = meta.model || '';
  const size = meta.size && meta.size !== 'auto' ? meta.size : (args.size && args.size !== 'auto' ? args.size : '');
  const quality = meta.quality && meta.quality !== 'auto' ? meta.quality : (args.quality && args.quality !== 'auto' ? args.quality : '');
  const cost = formatImageCost(meta.cost_usd);
  if (model) chips.append(el('span', { class: 'agent-genimage-chip', text: model }));
  if (size) chips.append(el('span', { class: 'agent-genimage-chip', text: size }));
  if (quality) chips.append(el('span', { class: 'agent-genimage-chip', text: quality }));
  if (cost) chips.append(el('span', { class: 'agent-genimage-chip', text: cost }));
  if (chips.children.length) caption.append(chips);
  const first = attachments[0] || (meta.file_path ? { name: meta.file_path.split(/[\\/]/).pop(), file_path: meta.file_path } : null);
  const downloadSrc = imageSrc(first);
  if (downloadSrc && !isRunning && !isFailed) {
    caption.append(el('a', {
      class: 'agent-genimage-download',
      href: downloadSrc,
      download: first?.name || 'generated-image.png',
      text: 'Download',
    }));
  }
  if (caption.children.length) card.append(caption);
  decorateToolCard(card, toolCall);
  return card;
}

// renderGenerateSpeechCard mirrors renderGenerateImageCard but for
// generate_speech. Surfaces provider/model/voice/duration/cost parsed from
// the YAML output frontmatter plus a click-to-play <audio> plate and a
// Download link, so the speech tool has the same affordances the image
// tool already has. Falls back to /local-file?path= when the inline data
// URL is absent (large audio payloads, replayed history).
function renderGenerateSpeechCard(toolCall) {
  toolCall = normalizeToolCall(toolCall);
  const args = parseToolArgs(toolCall.args);
  const parsed = toolPresentationParts(toolCall);
  const meta = parsed.meta || {};
  // output_attachments is the wire field; older history may use
  // .attachments. We accept either.
  const attachments = [...(toolCall.output_attachments || toolCall.attachments || [])]
    .filter((item) => !item.type || item.type === 'audio');
  const isFailed = toolCall.status === 'fail' || meta.status === 'failed';
  const hasPresentationResult = Boolean(toolCall.presentation?.result?.text || meta.file_path || attachments.length);
  const isRunning = !isFailed && (toolCall.status === 'running' || (!toolCall.output && !hasPresentationResult));
  const status = isRunning ? 'running' : (isFailed ? 'error' : 'success');
  const prompt = String(args.text || args.prompt || '').trim();
  const promptPreview = prompt.split('\n')[0].slice(0, 140);

  const card = el('div', { class: `agent-genaudio-card is-${status}`, 'data-tool': 'generate_speech' });
  card._toolArgs = toolCall.args;
  card._toolName = 'generate_speech';

  const head = el('div', { class: 'agent-genaudio-head' },
    el('span', { class: 'agent-genaudio-kicker', text: 'speech' }),
    el('span', { class: 'agent-genaudio-title', text: isRunning ? 'Synthesizing' : (isFailed ? 'Did not synthesize' : 'Take') }),
    el('span', { class: 'agent-tool-elapsed', text: '' }),
  );
  card.append(head);

  const plate = el('div', { class: 'agent-genaudio-plate' });
  if (isRunning) {
    plate.append(el('div', { class: 'agent-genaudio-grain', 'aria-hidden': 'true' }));
    plate.append(el('p', { class: 'agent-genaudio-wait', text: 'Voice warming up' }));
  } else if (isFailed) {
    const errText = (parsed.body || toolCall.output || 'Speech generation failed').replace(/^error:\s*/i, '').slice(0, 280);
    plate.append(el('p', { class: 'agent-genaudio-error', text: errText }));
  } else {
    const audios = [...attachments];
    if (!audios.length && meta.file_path) {
      audios.push({ type: 'audio', name: meta.file_path.split(/[\\/]/).pop(), file_path: meta.file_path, media_type: meta.media_type });
    }
    if (!audios.length) {
      plate.append(el('p', { class: 'agent-genaudio-wait', text: 'No audio bytes in this result' }));
    } else {
      const gallery = el('div', { class: 'agent-genaudio-gallery', 'data-count': String(audios.length) });
      for (const attachment of audios) {
        const src = attachment.data_url || (attachment.file_path ? '/local-file?path=' + encodeURIComponent(attachment.file_path) : '');
        const label = attachment.name || promptPreview || 'Generated speech';
        const audio = el('audio', { controls: true, preload: 'metadata', src });
        audio.addEventListener('error', () => audio.classList.add('audio-load-error'));
        const open = () => openAudioLightbox({
          src,
          name: attachment.name || 'generated-speech.wav',
          caption: [meta.model, meta.voice, meta.provider].filter(Boolean).join(' · '),
        });
        const btn = el('button', { class: 'agent-genaudio-open', type: 'button', 'aria-label': 'Listen to ' + label });
        btn.append(audio);
        btn.addEventListener('click', open);
        gallery.append(el('figure', { class: 'agent-genaudio-frame' }, btn));
      }
      plate.append(gallery);
    }
  }
  card.append(plate);

  const caption = el('div', { class: 'agent-genaudio-caption' });
  if (promptPreview) {
    caption.append(el('p', { class: 'agent-genaudio-prompt', text: promptPreview, title: prompt }));
  }
  const chips = el('div', { class: 'agent-genaudio-meta' });
  const provider = meta.provider || '';
  const model = meta.model || '';
  const voice = meta.voice || '';
  const cost = formatImageCost(meta.cost_usd);
  if (provider) chips.append(el('span', { class: 'agent-genaudio-chip', text: provider }));
  if (model) chips.append(el('span', { class: 'agent-genaudio-chip', text: model }));
  if (voice) chips.append(el('span', { class: 'agent-genaudio-chip', text: 'voice: ' + voice }));
  if (cost) chips.append(el('span', { class: 'agent-genaudio-chip', text: cost }));
  if (chips.children.length) caption.append(chips);
  const first = attachments[0] || (meta.file_path ? { name: meta.file_path.split(/[\\/]/).pop(), file_path: meta.file_path } : null);
  const downloadSrc = first ? (first.data_url || (first.file_path ? '/local-file?path=' + encodeURIComponent(first.file_path) : '')) : '';
  if (downloadSrc && !isRunning && !isFailed) {
    caption.append(el('a', {
      class: 'agent-genaudio-download',
      href: downloadSrc,
      download: first?.name || 'generated-speech.wav',
      text: 'Download',
    }));
  }
  if (caption.children.length) card.append(caption);
  decorateToolCard(card, toolCall);
  return card;
}

// renderGenerateVideoCard mirrors renderGenerateImageCard and
// renderGenerateSpeechCard but for generate_video. Surfaces
// provider/model/duration/resolution/cost metadata parsed from the YAML
// output frontmatter plus a click-to-play <video> plate and a Download
// link.
function renderGenerateVideoCard(toolCall) {
  toolCall = normalizeToolCall(toolCall);
  const args = parseToolArgs(toolCall.args);
  const parsed = toolPresentationParts(toolCall);
  const meta = parsed.meta || {};
  // output_attachments is the wire field; older history may use
  // .attachments. We accept either.
  const attachments = [...(toolCall.output_attachments || toolCall.attachments || [])]
    .filter((item) => !item.type || item.type === 'video');
  const isFailed = toolCall.status === 'fail' || meta.status === 'failed';
  const hasPresentationResult = Boolean(toolCall.presentation?.result?.text || meta.file_path || attachments.length);
  const isRunning = !isFailed && (toolCall.status === 'running' || (!toolCall.output && !hasPresentationResult));
  const status = isRunning ? 'running' : (isFailed ? 'error' : 'success');
  const prompt = String(args.prompt || '').trim();
  const promptPreview = prompt.split('\n')[0].slice(0, 140);

  const card = el('div', { class: `agent-genvideo-card is-${status}`, 'data-tool': 'generate_video' });
  card._toolArgs = toolCall.args;
  card._toolName = 'generate_video';

  const head = el('div', { class: 'agent-genvideo-head' },
    el('span', { class: 'agent-genvideo-kicker', text: 'video' }),
    el('span', { class: 'agent-genvideo-title', text: isRunning ? 'Rendering' : (isFailed ? 'Did not render' : 'Clip') }),
    el('span', { class: 'agent-tool-elapsed', text: '' }),
  );
  card.append(head);

  const plate = el('div', { class: 'agent-genvideo-plate' });
  if (isRunning) {
    plate.append(el('div', { class: 'agent-genvideo-grain', 'aria-hidden': 'true' }));
    plate.append(el('p', { class: 'agent-genvideo-wait', text: 'Frames rendering' }));
  } else if (isFailed) {
    const errText = (parsed.body || toolCall.output || 'Video generation failed').replace(/^error:\s*/i, '').slice(0, 280);
    plate.append(el('p', { class: 'agent-genvideo-error', text: errText }));
  } else {
    const videos = [...attachments];
    if (!videos.length && meta.file_path) {
      videos.push({ type: 'video', name: meta.file_path.split(/[\\/]/).pop(), file_path: meta.file_path, media_type: meta.media_type });
    }
    if (!videos.length) {
      plate.append(el('p', { class: 'agent-genvideo-wait', text: 'No video bytes in this result' }));
    } else {
      const gallery = el('div', { class: 'agent-genvideo-gallery', 'data-count': String(videos.length) });
      for (const attachment of videos) {
        const src = attachment.data_url || (attachment.file_path ? '/local-file?path=' + encodeURIComponent(attachment.file_path) : '');
        const label = attachment.name || promptPreview || 'Generated video';
        const video = el('video', { controls: true, preload: 'metadata', src });
        video.addEventListener('error', () => video.classList.add('video-load-error'));
        const open = () => openVideoLightbox({
          src,
          name: attachment.name || 'generated-video.mp4',
          caption: [meta.model, meta.resolution, meta.provider].filter(Boolean).join(' · '),
        });
        const btn = el('button', { class: 'agent-genvideo-open', type: 'button', 'aria-label': 'Play ' + label });
        btn.append(video);
        btn.addEventListener('click', open);
        gallery.append(el('figure', { class: 'agent-genvideo-frame' }, btn));
      }
      plate.append(gallery);
    }
  }
  card.append(plate);

  const caption = el('div', { class: 'agent-genvideo-caption' });
  if (promptPreview) {
    caption.append(el('p', { class: 'agent-genvideo-prompt', text: promptPreview, title: prompt }));
  }
  const chips = el('div', { class: 'agent-genvideo-meta' });
  const provider = meta.provider || '';
  const model = meta.model || '';
  const duration = meta.duration_seconds ? `${meta.duration_seconds}s` : '';
  const resolution = meta.resolution || '';
  const cost = formatImageCost(meta.cost_usd);
  if (provider) chips.append(el('span', { class: 'agent-genvideo-chip', text: provider }));
  if (model) chips.append(el('span', { class: 'agent-genvideo-chip', text: model }));
  if (duration) chips.append(el('span', { class: 'agent-genvideo-chip', text: duration }));
  if (resolution) chips.append(el('span', { class: 'agent-genvideo-chip', text: resolution }));
  if (cost) chips.append(el('span', { class: 'agent-genvideo-chip', text: cost }));
  if (chips.children.length) caption.append(chips);
  const first = attachments[0] || (meta.file_path ? { name: meta.file_path.split(/[\\/]/).pop(), file_path: meta.file_path } : null);
  const downloadSrc = first ? (first.data_url || (first.file_path ? '/local-file?path=' + encodeURIComponent(first.file_path) : '')) : '';
  if (downloadSrc && !isRunning && !isFailed) {
    caption.append(el('a', {
      class: 'agent-genvideo-download',
      href: downloadSrc,
      download: first?.name || 'generated-video.mp4',
      text: 'Download',
    }));
  }
  if (caption.children.length) card.append(caption);
  decorateToolCard(card, toolCall);
  return card;
}

function openImageLightbox({ src, name, caption }) {
  if (!src) return;
  const overlay = el('div', {
    class: 'agent-image-lightbox',
    role: 'dialog',
    'aria-modal': 'true',
    'aria-label': name || 'Generated image',
  });
  const closeBtn = el('button', { class: 'agent-image-lightbox-close', type: 'button', text: 'Close', 'aria-label': 'Close' });
  const img = el('img', { src, alt: name || 'Generated image' });
  const bar = el('div', { class: 'agent-image-lightbox-bar' });
  if (caption) bar.append(el('span', { class: 'agent-image-lightbox-caption', text: caption }));
  bar.append(el('a', { class: 'agent-image-lightbox-download', href: src, download: name || 'generated-image.png', text: 'Download' }));
  overlay.append(closeBtn, el('div', { class: 'agent-image-lightbox-frame' }, img, bar));
  const close = () => {
    unregister();
    document.removeEventListener('keydown', onKey, true);
    overlay.remove();
  };
  const unregister = registerOverlayDismiss(close);
  const onKey = (event) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      close();
      return;
    }
    if (event.key !== 'Tab') return;
    const focusables = [...overlay.querySelectorAll('button, a[href]')];
    if (!focusables.length) return;
    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };
  overlay.addEventListener('click', (event) => {
    if (event.target === overlay) close();
  });
  closeBtn.addEventListener('click', close);
  document.addEventListener('keydown', onKey, true);
  document.body.append(overlay);
  closeBtn.focus();
}

export function renderToolJob(toolCall) {
  const name = toolCall.name || 'tool';
  // Built-in tools render as compact execution-timeline events. Running
  // calls stay open for live feedback; settled calls start collapsed. exec
  // (live streaming) and MCP calls
  // (raw passthrough) use the same timeline event but with a terminal
  // output panel instead of the folded raw pair.
  if (!isStreamingTool(name) && !name.startsWith('mcp__') && name !== 'mcp_call') {
    return renderToolEvent(toolCall);
  }
  return renderToolEvent(toolCall, {
    terminalOutput: true,
    mcp: name.startsWith('mcp__') || name === 'mcp_call',
  });
}

// ---- built-in tool event card (execution timeline) ----
//
// Built-in tool calls render as compact timeline events instead of raw
// request/output terminals. The whole event is a collapsible <details>:
// its head row carries a status node, the action title,
// and elapsed time (never a duplicated tool-name label), and the body leads
// with the primary argument as a single legible path line. The result is
// visible without any extra toggle — one folded "show request and raw
// output" details stays at the bottom for debugging, mirroring production
// tools (IDE call inspectors, CI logs) that never surface raw payloads by
// default. exec and MCP calls use the same event with a terminal output
// panel (live-streamed for exec, raw passthrough for MCP).

// toolEventPrimary extracts the one argument a human scans first: the path
// for file tools, the query for searches, the URL for fetches. Returns ''
// when nothing reads better than the summary line.
function toolEventPrimary(name, args) {
  const parsed = args && typeof args === 'object' && !Array.isArray(args) ? args : parseToolArgs(args);
  if (!parsed || typeof parsed !== 'object') return '';
  const value = (key) => {
    const raw = parsed[key];
    if (raw === undefined || raw === null) return '';
    const text = typeof raw === 'object' ? JSON.stringify(raw) : String(raw);
    return text.trim();
  };
  if (name === 'grep') {
    const pattern = value('pattern');
    const path = value('path');
    return pattern ? [pattern, path && `in ${path}`].filter(Boolean).join(' ') : '';
  }
  if (name === 'mcp_call') {
    return value('ref');
  }
  const keys = ['command', 'path', 'file_path', 'url', 'query', 'pattern', 'ref', 'id', 'name', 'skill', 'source'];
  for (const key of keys) {
    const text = value(key);
    if (text) return key === 'path' || key === 'file_path' || key === 'command' ? text : `${key}: ${text}`;
  }
  return '';
}

function toolEventRequest(name, args, presentation) {
  const primary = toolEventPrimary(name, args);
  if (primary) return primary;
  const request = String(presentation?.request || '').trim().replace(/\s+/g, ' ');
  if (request && request.length <= 180) return request;
  const parsed = parseToolArgs(args);
  return Object.keys(parsed).length ? (summarizeToolArgs(parsed) || `${name}()`) : `${name}()`;
}

function toolEventRawText(name, presentation, output) {
  const request = presentation?.request !== undefined && presentation?.request !== null
    ? String(presentation.request)
    : '';
  // Raw output wins: the folded panel exists to show the unprocessed payload.
  // result.text is a parsed view and only fills in when raw output is absent
  // (e.g. late presentation patches that never carried the raw body).
  const rawOutput = String(output || '') || String(presentation?.result?.text || '');
  const parts = [];
  if (request) parts.push(request);
  if (rawOutput && rawOutput !== '…') parts.push(rawOutput);
  return truncate(parts.join('\n\n'), 12000) || `${name || 'tool'}()`;
}

// renderToolEventResult paints the result box: summary line, then the
// compact body the presentation format asks for (list rows, meta chips, or a
// capped text preview). Falls back to the status line while running.
// toolCssSlug is the CSS owner for a built-in tool event. Plugin MCP tools
// (mcp__*) share `mcp` so their dressing stays in one section; built-ins
// such as mcp_call keep their own slug.
function toolCssSlug(name) {
  const raw = String(name || 'tool');
  if (raw.startsWith('mcp__')) return 'mcp';
  const slug = raw.toLowerCase().replace(/_/g, '-').replace(/[^a-z0-9-]+/g, '');
  return slug || 'tool';
}

function toolPartClass(name, part) {
  return `agent-tool-event-${part} agent-tool-${toolCssSlug(name)}-${part} ${toolContractClass(name)}-${part}`;
}

function renderToolEventResult(toolCall) {
  const name = toolCall?.name || '';
  const result = toolCall?.presentation?.result;
  if (result?.format === 'list' && Array.isArray(result.items) && result.items.length) {
    return renderToolPresentationList(result.items, toolPartClass(name, 'rows'));
  }
  if (result?.format === 'status') return renderToolPresentationStatus(result, toolPartClass(name, 'status'));
  if (result?.format === 'media') {
    const root = el('div', { class: toolPartClass(name, 'media') });
    const attachments = Array.isArray(result.attachments) ? result.attachments : [];
    if (attachments.length) root.append(renderToolAttachments(attachments, toolPartClass(name, 'attachments')));
    const text = result.text ? String(result.text) : '';
    if (text) root.append(el('pre', { class: toolPartClass(name, 'output'), text }));
    if (!root.children.length && result.summary) root.append(el('p', { class: toolPartClass(name, 'summary'), text: String(result.summary) }));
    return root.children.length ? root : null;
  }
  const text = toolTerminalOutput(toolCall);
  if (text === '…' || text === '… waiting for output') {
    return el('div', { class: toolPartClass(name, 'wait'), text: 'Running…' });
  }
  const pre = el('pre', { class: toolPartClass(name, 'output'), text: text === 'ok' ? '' : text });
  if (result?.language) pre.dataset.language = String(result.language);
  return pre;
}

// toolEventSummary prefers the presentation summary, then a short raw output
// line (a completed grep's "3 matches" reads better than "2 args"), then the
// args summary. Long outputs never leak into the summary row.
function toolEventSummary(toolCall) {
  const result = toolCall?.presentation?.result;
  if (result?.summary) return String(result.summary);
  const status = toolCall.status || 'running';
  if (status === 'running') return 'Running';
  const output = String(toolCall.output ?? '').trim();
  if (output && output !== 'ok' && output.length <= 120 && !output.includes('\n')) return output;
  return toolTerminalMeta(toolCall);
}

// toolEventHeadSummary is the collapsed-state label. A human usually needs
// the command/path/query first; result summaries stay in the expanded body.
// This keeps a long sequence of "Command completed" rows scannable without
// changing the backend presentation contract.
function toolEventHeadSummary(toolCall) {
  const primary = compactLine(toolEventPrimary(toolCall?.name, toolCall?.args));
  return primary || compactLine(toolEventSummary(toolCall));
}

function renderToolEvent(toolCall, { terminalOutput = false, mcp = false } = {}) {
  toolCall = normalizeToolCall(toolCall);
  const name = toolCall.name || 'tool';
  const status = toolCall.status || 'running';
  const presentation = toolCall.presentation || null;
  const streaming = isStreamingTool(name);
  // The whole event is a <details>, open by default: the head row collapses
  // the card to a single line. The body follows Proposal B — a path line, a
  // summary-first result (never a nested toggle), and the raw request/output
  // folded away. exec/MCP replace the result box with a terminal output
  // panel (live-streamed for exec).
  const slug = toolCssSlug(name);
  const contract = toolContractRef(name, presentation);
  const contractClass = toolContractClass(name, presentation);
  const card = el('details', { class: `agent-tool-event agent-tool-${slug}` });
  card.classList.add('agent-tool-card', contractClass);
  card.open = status === 'running';
  card.dataset.tool = name;
  card.dataset.toolContract = contract.id;
  card.dataset.toolContractVersion = String(contract.version);
  card.dataset.toolClass = contract.css_class;
  if (terminalOutput) card.classList.add('is-terminal');
  if (toolCall.id) card.dataset.toolCallId = String(toolCall.id);
  if (presentation?.variant) card.dataset.presentationVariant = String(presentation.variant);
  if (presentation?.result?.format) card.dataset.resultFormat = String(presentation.result.format);
  card._toolArgs = toolCall.args;
  card._toolName = name;
  card._toolPresentation = presentation;
  card._toolOutput = toolCall.output ?? '';

  const elapsed = toolCall.elapsed
    ? (() => { const t = Math.max(0, Math.floor(Number(toolCall.elapsed) || 0)); return t < 60 ? `${t}s` : `${Math.floor(t / 60)}m ${t % 60}s`; })()
    : '';
  const head = el('summary', { class: 'agent-tool-event-head' },
    el('span', { class: 'agent-tool-event-node', text: toolStatusGlyph(status), 'aria-hidden': 'true' }),
    el('span', { class: 'agent-tool-event-head-text' },
      el('span', { class: 'agent-tool-event-title', text: presentation?.action || toolTimelineTitle(name, status) }),
      el('span', { class: 'agent-tool-event-head-summary', text: toolEventHeadSummary(toolCall) }),
      mcp ? el('span', { class: 'agent-tool-event-badge', text: 'MCP' }) : null,
      ...(streaming ? [el('button', { class: 'agent-tool-stop', type: 'button', title: 'Stop this tool', 'aria-label': 'Stop running tool', hidden: true }, '■ Stop')] : []),
      el('span', { class: 'agent-tool-elapsed', text: elapsed }),
    ),
    el('span', { class: 'agent-tool-event-chevron', 'aria-hidden': 'true', text: '⌄' }),
  );

  const body = el('div', { class: 'agent-tool-event-body' });
  const primary = toolEventRequest(name, toolCall.args, presentation);
  if (primary) body.append(el('p', { class: `${toolPartClass(name, 'path')} agent-tool-request ${contractClass}-request`, text: primary }));

  if (terminalOutput) {
    // exec streams deltas into this panel via appendToolJobDelta; MCP shows
    // the raw passthrough, capped with its own scroll.
    body.append(el('pre', { class: `agent-tool-terminal-output agent-tool-${slug}-output ${contractClass}-output ${contractClass}-result agent-tool-result`, text: toolTerminalOutput(toolCall) }));
  } else {
    const result = el('div', { class: `${toolPartClass(name, 'result')} agent-tool-result` },
      el('div', { class: toolPartClass(name, 'summary-text'), text: toolEventSummary(toolCall) }),
    );
    const resultContent = renderToolEventResult(toolCall);
    if (resultContent) result.append(resultContent);
    body.append(result);
    const raw = el('details', { class: `${toolPartClass(name, 'details')} agent-tool-raw` },
      el('summary', { text: 'show request and raw output' }),
      el('pre', { text: toolEventRawText(name, presentation, toolCall.output) }),
    );
    body.append(raw);
  }
  card.append(head, body);
  setToolTerminalStatus(card, toolCall.status || 'running');
  return card;
}

// isStreamingTool reports whether a tool streams live output chunks
// (agent.tool.delta events) and therefore offers a per-call stop button.
export function isStreamingTool(name) {
  return name === 'exec';
}

// bindToolStop wires the per-call stop button (shown while the tool is
// running) to cancel the underlying turn via agent.turns.stop. The run_id
// is resolved lazily from the owning run entry so the button works even
// when the card was rendered before the run was registered.
// Per-call stop button for streaming tools (exec): surfaced programmatically
// inside the tool terminal card, so it has no static id in the HTML source.
export function bindToolStop(card, getRunId) {
  const btn = card?.querySelector('.agent-tool-stop');
  if (!btn) return;
  btn.hidden = false;
  btn.addEventListener('click', (event) => {
    event.preventDefault();
    event.stopPropagation();
    if (btn.disabled) return;
    btn.disabled = true;
    btn.textContent = 'Stopping…';
    const runId = typeof getRunId === 'function' ? getRunId() : getRunId;
    rpc('agent.turns.stop', { run_id: runId }).then(() => {
      setTimeout(() => btn.remove(), 600);
    }).catch(() => {
      btn.disabled = false;
      btn.textContent = '■ Stop';
    });
  });
}

// appendToolJobDelta appends a live output chunk to a running tool
// terminal's output panel. The panel accumulates streamed text until the
// tool completes, at which point the final output replaces it.
export function appendToolJobDelta(card, text) {
  if (!card || !text) return;
  const outputEl = card.querySelector('.agent-tool-terminal-output');
  if (!outputEl) return;
  if (!card._streamStarted) {
    // First live chunk replaces the placeholder ("… waiting for output").
    card._streamStarted = true;
    outputEl.textContent = '';
  }
  const MAX_TOOL_STREAM = 12000;
  if (outputEl.textContent.length + text.length > MAX_TOOL_STREAM) {
    outputEl.textContent = outputEl.textContent.slice(-MAX_TOOL_STREAM) + text;
  } else {
    outputEl.textContent += text;
  }
  outputEl.scrollTop = outputEl.scrollHeight;
}

// applyQueuedToolDeltas writes buffered live chunks onto tool cards that
// already exist. Chunks whose card is not mounted yet stay in `remaining`
// so a later tool.started (or first-delta ensure) can flush them. Dropping
// unflushed entries is how "… waiting for output" never filled.
export function applyQueuedToolDeltas(toolJobs, pending) {
  const remaining = new Map();
  let flushed = false;
  if (!pending?.size) return { flushed, remaining };
  for (const [id, text] of pending) {
    const job = toolJobs?.get(id);
    if (!job || !text) {
      if (id && text) remaining.set(id, text);
      continue;
    }
    appendToolJobDelta(job, text);
    flushed = true;
  }
  return { flushed, remaining };
}

// reasoningShouldStream is the live pulse for the current round's Thinking
// disclosure. It must turn off once the model emits visible text or starts
// a tool — otherwise renderLiveRun re-adds is-streaming on every tool delta
// and completed rounds keep the animation.
export function reasoningShouldStream({
  hidden = false,
  hasText = false,
  thinkingLive = false,
  toolsStarted = false,
} = {}) {
  return Boolean(thinkingLive) && !hidden && !hasText && !toolsStarted;
}

export function setReasoningStreaming(details, live) {
  if (!details) return;
  details.classList.toggle('is-streaming', Boolean(live) && !details.hidden);
}

export function sealReasoningStreaming(root) {
  if (!root) return;
  if (root.classList?.contains('agent-reasoning')) {
    root.classList.remove('is-streaming');
  }
  root.querySelectorAll?.('.agent-reasoning.is-streaming').forEach((n) => n.classList.remove('is-streaming'));
}

// formatTokens renders a token count with a human unit suffix
// (e.g. 10680 → "10.68k", 27085560 → "27.09M", 137 → "137").
export function formatTokens(value) {
  const n = Number(value) || 0;
  const trim = (v) => v.replace(/\.?0+$/, '');
  if (n >= 1e9) return `${trim((n / 1e9).toFixed(2))}B`;
  if (n >= 1e6) return `${trim((n / 1e6).toFixed(2))}M`;
  if (n >= 1e3) return `${trim((n / 1e3).toFixed(2))}k`;
  return String(n);
}

export function toolTerminalMeta(toolCall) {
  const summary = toolCall?.presentation?.result?.summary;
  if (summary) return String(summary);
  const status = toolCall.status || 'running';
  if (status === 'running') return 'Running';
  if (toolCall.name === 'mcp_call') {
    const parsed = parseToolArgs(toolCall.args);
    const inner = parsed.arguments_json;
    if (inner) {
      const summary = summarizeToolArgs(parseToolArgs(inner));
      if (summary) return summary;
    }
  }
  return summarizeToolArgs(toolCall.args) || (status === 'fail' ? 'Failed' : 'Completed');
}

export function toolTerminalOutput(toolCall) {
  const result = toolCall?.presentation?.result;
  if (result) {
    if (result.format === 'list' && Array.isArray(result.items)) {
      return result.items.length ? '' : (result.summary ? String(result.summary) : 'No results');
    }
    if (result.text !== undefined && result.text !== null && result.text !== '') return truncate(String(result.text), 12000);
    if (result.summary) return String(result.summary);
    if (result.format === 'media' && Array.isArray(result.attachments) && result.attachments.length) {
      return `${result.attachments.length} attachment${result.attachments.length === 1 ? '' : 's'}`;
    }
  }
  if (toolCall.output !== undefined && toolCall.output !== null && toolCall.output !== '') return truncate(String(toolCall.output), 12000);
  if (toolCall.status === 'running') {
    // Streaming tools start with the placeholder, which live deltas replace.
    return isStreamingTool(toolCall.name) ? '… waiting for output' : '…';
  }
  return toolCall.status === 'fail' ? 'Tool failed.' : 'ok';
}

export function setToolTerminalStatus(card, status) {
  const normalized = status || 'running';
  const running = normalized === 'running';
  const success = normalized === 'ok';
  // Both 'fail' and 'interrupted' render the same error styling — the
  // message and meta already distinguish them, and the data flow treats
  // them as terminal (no further events) for the strip's purposes.
  const errored = normalized === 'fail' || normalized === 'interrupted';
  card.classList.toggle('is-running', running);
  card.classList.toggle('is-success', success);
  card.classList.toggle('is-error', errored);
  card.dataset.status = normalized;
  const glyph = toolStatusGlyph(normalized);
  const eventNode = card.querySelector('.agent-tool-event-node');
  if (eventNode) eventNode.textContent = glyph;
  const eventTitle = card.querySelector('.agent-tool-event-title');
  if (eventTitle && card._toolName) eventTitle.textContent = card._toolPresentation?.action || toolTimelineTitle(card._toolName, normalized);
  const headSummary = card.querySelector('.agent-tool-event-head-summary');
  if (headSummary && card._toolName) {
    headSummary.textContent = toolEventHeadSummary({
      name: card._toolName,
      args: card._toolArgs,
      status: normalized,
      output: card._toolOutput,
      presentation: card._toolPresentation,
    });
  }
  // The per-call stop button lives on streaming tools only and disappears
  // once the tool settles.
  const stop = card.querySelector('.agent-tool-stop');
  if (stop) stop.hidden = running ? false : true;
}

// setToolTerminalPresentation updates a live card when the backend sends the
// frontend view after the initial SSE delta created the card. Raw tool output
// is deliberately not reconstructed here; only presentation fields are
// painted into the browser.
export function setToolTerminalPresentation(card, presentation) {
  if (!card || !presentation) return;
  const previous = card._toolPresentation || {};
  const merged = {
    ...previous,
    ...presentation,
    // A review ToolResult can be generated without args. Keep the call's
    // request/action while replacing only the result that just completed.
    action: presentation.action || previous.action || '',
    request: presentation.request || previous.request || '',
    result: { ...(previous.result || {}), ...(presentation.result || {}) },
  };
  card._toolPresentation = merged;
  const contract = toolContractRef(card._toolName || card.dataset.tool, merged);
  decorateToolCard(card, {
    name: card._toolName || card.dataset.tool,
    presentation: merged,
  });
  card.classList.add('agent-tool-card', contract.css_class);
  card.dataset.toolContract = contract.id;
  card.dataset.toolContractVersion = String(contract.version);
  card.dataset.toolClass = contract.css_class;
  if (merged.variant) card.dataset.presentationVariant = String(merged.variant);
  if (merged.result?.format) card.dataset.resultFormat = String(merged.result.format);
  const eventTitle = card.querySelector('.agent-tool-event-title');
  if (eventTitle && merged.action) eventTitle.textContent = String(merged.action);
  const headSummary = card.querySelector('.agent-tool-event-head-summary');
  if (headSummary) {
    headSummary.textContent = toolEventHeadSummary({
      name: card._toolName,
      args: card._toolArgs,
      status: card.dataset.status,
      output: card._toolOutput,
      presentation: merged,
    });
  }
  const request = card.querySelector('.agent-tool-request');
  if (request && merged.request) request.textContent = String(merged.request);
  const output = card.querySelector('.agent-tool-terminal-output');
  if (output) {
    // exec/MCP terminal panel: repaint the settled output in place.
    const slug = toolCssSlug(card._toolName || card.dataset.tool);
    const contractClass = toolContractClass(card._toolName || card.dataset.tool, merged);
    output.replaceWith(renderToolPresentationResult(
      merged.result || {},
      `agent-tool-terminal-output agent-tool-${slug}-output ${contractClass}-output ${contractClass}-result agent-tool-result`,
      card._toolOutput,
    ));
    return;
  }
  // Event card: repaint the summary line and the result body in place; the
  // raw fold lives in the card body below the result box.
  const resultBox = card.querySelector('.agent-tool-event-result');
  if (!resultBox) return;
  const summaryText = resultBox.querySelector('.agent-tool-event-summary-text');
  if (summaryText) summaryText.textContent = toolEventSummary({ name: card._toolName, status: card.dataset.status, output: card._toolOutput, presentation: merged });
  for (const child of [...resultBox.children]) {
    if (child.classList.contains('agent-tool-event-summary-text')) continue;
    child.remove();
  }
  const resultContent = renderToolEventResult({ name: card._toolName, status: card.dataset.status, output: card._toolOutput, presentation: merged });
  if (resultContent) resultBox.append(resultContent);
  const rawPre = card.querySelector('.agent-tool-raw > pre');
  if (rawPre) rawPre.textContent = toolEventRawText(card._toolName, merged, card._toolOutput);
}

// setToolTerminalOutput paints a settled result onto a contract-backed event.
// Learning and ACP transcripts use this so they never query card internals
// directly.
export function setToolTerminalOutput(card, output, status, metaText = '') {
  if (!card) return;
  const text = String(output ?? '');
  const normalized = status || card.dataset.status || 'ok';
  if (card.classList.contains('agent-tool-event')) {
    card._toolOutput = text;
    // exec/MCP hybrid: paint the terminal panel directly.
    const termOut = card.querySelector('.agent-tool-terminal-output');
    if (termOut) {
      termOut.textContent = text.length > 12000 ? `${text.slice(0, 12000)}\n… (truncated)` : (text || 'ok');
      termOut.classList.toggle('is-error', status === 'fail');
      setToolTerminalStatus(card, status);
      return;
    }
    const summaryText = card.querySelector('.agent-tool-event-summary-text');
    if (summaryText) {
      summaryText.textContent = metaText || toolEventSummary({ name: card._toolName, status, output: text, presentation: card._toolPresentation });
    }
    const resultBox = card.querySelector('.agent-tool-event-result');
    if (resultBox) {
      for (const child of [...resultBox.children]) {
        if (child.classList.contains('agent-tool-event-summary-text')) continue;
        child.remove();
      }
      const resultContent = renderToolEventResult({ name: card._toolName, status, output: text, presentation: card._toolPresentation });
      if (resultContent) resultBox.append(resultContent);
    }
    const rawPre = card.querySelector('.agent-tool-raw > pre');
    if (rawPre) rawPre.textContent = toolEventRawText(card._toolName, card._toolPresentation, text);
    setToolTerminalStatus(card, status);
    return;
  }
  setToolTerminalStatus(card, normalized);
}

function toolStatusGlyph(status) {
  if (status === 'running') return '…';
  if (status === 'fail' || status === 'interrupted') return '!';
  return '✓';
}

function toolTimelineTitle(name, status) {
  const labels = {
    exec: ['Running command', 'Command completed', 'Command failed'],
    file_list: ['Listing files', 'Files listed', 'File listing failed'],
    file_read: ['Reading file', 'File read', 'File read failed'],
    grep: ['Searching', 'Search completed', 'Search failed'],
    file_search: ['Searching files', 'Search completed', 'Search failed'],
    memory: ['Updating memory', 'Memory updated', 'Memory update failed'],
    skill: ['Loading skill', 'Skill loaded', 'Skill load failed'],
    mcp_call: ['Calling MCP tool', 'MCP call completed', 'MCP call failed'],
  }[name];
  if (labels) {
    if (status === 'running') return labels[0];
    if (status === 'fail' || status === 'interrupted') return labels[2];
    return labels[1];
  }
  const readable = String(name || 'tool')
    .replace(/^mcp__/, '')
    .replace(/__/g, ' ')
    .replace(/[_-]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim() || 'tool';
  const noun = readable.charAt(0).toUpperCase() + readable.slice(1);
  if (status === 'running') return `Running ${readable}`;
  if (status === 'fail' || status === 'interrupted') return `${noun} failed`;
  return `${noun} completed`;
}

export function attachmentChip(attachment, onRemove) {
  const icon = attachment.type === 'image' ? 'IMG'
    : attachment.type === 'audio' ? 'AUD'
    : attachment.type === 'video' ? 'VID'
    : attachment.type === 'file' ? 'PDF'
    : attachment.type === 'folder' ? 'DIR'
    : 'TXT';
  const chip = el('span', { class: 'agent-attachment' },
    el('span', { class: 'agent-attachment-name', text: `${icon} · ${attachment.name || 'Attachment'}` }),
  );
  if (onRemove) {
    const remove = el('button', { class: 'agent-attachment-remove', type: 'button', title: `Remove ${attachment.name || 'attachment'}`, 'aria-label': `Remove ${attachment.name || 'attachment'}` }, '×');
    remove.addEventListener('click', onRemove);
    chip.append(remove);
  }
  return chip;
}

function renderToolPresentationResult(result, className, fallbackText = '') {
  if (result?.format === 'list' && Array.isArray(result.items) && result.items.length) {
    return renderToolPresentationList(result.items, className);
  }
  if (result?.format === 'status') {
    return renderToolPresentationStatus(result, className);
  }
  if (result?.format === 'media') {
    const root = el('div', { class: `${className} agent-tool-media-result` });
    const attachments = renderToolAttachments(result.attachments, `${className}-attachments`);
    if (attachments) root.append(attachments);
    const text = result.text !== undefined && result.text !== null ? String(result.text) : '';
    if (text) root.append(el('pre', { class: `${className}-text`, text }));
    if (!root.children.length && result.summary) root.textContent = String(result.summary);
    return root;
  }
  const text = result?.text !== undefined && result?.text !== null && result.text !== ''
    ? String(result.text)
    : (result?.summary ? String(result.summary) : fallbackText);
  const output = el('pre', { class: className, text });
  if (result?.language) output.dataset.language = String(result.language);
  if (result?.format === 'document') output.classList.add('agent-tool-terminal-document');
  return output;
}

function renderToolPresentationStatus(result, className) {
  const root = el('div', { class: `${className} agent-tool-terminal-status-result` });
  const chips = renderToolPresentationMeta(result?.meta);
  if (chips) root.append(chips);
  if (!root.children.length) root.textContent = 'Completed';
  return root;
}

function renderToolPresentationMeta(meta) {
  if (!meta || typeof meta !== 'object' || Array.isArray(meta)) return null;
  const chips = el('div', { class: 'agent-tool-terminal-meta-chips' });
  const hidden = new Set(['count', 'total', 'status', 'ok', 'summary']);
  for (const [key, value] of Object.entries(meta)) {
    if (hidden.has(key)) continue;
    const display = compactToolMetaValue(key, value);
    if (!display) continue;
    const label = key.replace(/[_-]+/g, ' ');
    chips.append(el('span', { class: 'agent-tool-terminal-meta-chip', title: `${label}: ${display}` },
      el('b', { text: `${label}:` }),
      el('span', { text: display }),
    ));
  }
  return chips.children.length ? chips : null;
}

function compactToolMetaValue(key, value) {
  if (value === null || value === undefined || value === '') return '';
  if (Array.isArray(value)) {
    const scalar = value.filter((item) => item !== null && item !== undefined && typeof item !== 'object');
    if (scalar.length === value.length && scalar.length <= 3) return scalar.map(String).join(', ');
    return `${value.length} item${value.length === 1 ? '' : 's'}`;
  }
  if (typeof value === 'object') return '';
  let text = String(value);
  if (key === 'sha256' && text.length > 12) text = `${text.slice(0, 12)}…`;
  return text.length > 72 ? `${text.slice(0, 69)}…` : text;
}

function renderToolPresentationList(items, className = 'agent-tool-terminal-output') {
  const list = el('div', { class: `${className} agent-tool-terminal-result-list`, role: 'list' });
  const validItems = items.filter((item) => item && typeof item === 'object');
  const previewItems = validItems.slice(0, 6);
  for (const item of previewItems) {
    const title = item.name || item.title || item.id || item.path || item.ref || 'Result';
    const detail = toolPresentationItemDetail(item);
    list.append(el('div', { class: 'agent-tool-terminal-result-item', role: 'listitem' },
      el('strong', { text: String(title) }),
      detail ? el('span', { text: String(detail).slice(0, 240) }) : null,
    ));
  }
  const remaining = validItems.length - previewItems.length;
  if (remaining > 0) {
    list.append(el('div', {
      class: 'agent-tool-result-more',
      role: 'listitem',
      text: `+${remaining} more result${remaining === 1 ? '' : 's'}`,
    }));
  }
  return list;
}

function toolPresentationItemDetail(item) {
  if (item.mode || item.size || item.modified) {
    return [item.mode, item.size, item.modified].filter((value) => value !== undefined && value !== null && value !== '').join(' · ');
  }
  if (item.line !== undefined && item.line !== null) {
    const content = item.content ? ` · ${item.content}` : '';
    return `line ${item.line}${content}`;
  }
  if (item.matches !== undefined && item.matches !== null) return `${item.matches} matches`;
  return item.description || item.snippet || item.content || item.path || item.status || '';
}

function summarizeToolArgs(args) {
  if (!args || typeof args !== 'object' || Array.isArray(args)) return '';
  // Dispatcher-family calls carry the verb as `op`; the meta line already
  // shows it, so summarize the remaining payload only.
  const rest = Object.entries(args).filter(([key]) => key !== 'op');
  if (!rest.length) return '';
  if (rest.length === 1) {
    const value = JSON.stringify(rest[0][1]);
    return value.length > 42 ? `${value.slice(0, 42)}…` : value;
  }
  return `${rest.length} args`;
}

function truncate(value, limit) {
  return value.length > limit ? `${value.slice(0, limit)}\n… (truncated)` : value;
}

export function renderMessageAttachments(attachments) {
  const gallery = el('div', {
    class: 'agent-message-attachments',
    'aria-label': `${attachments.length} attachment${attachments.length === 1 ? '' : 's'}`,
  });
  for (const attachment of attachments) {
    if (attachment.type === 'image') {
      const src = attachment.data_url || (attachment.file_path ? '/local-file?path=' + encodeURIComponent(attachment.file_path) : '');
      const image = el('img', { src, alt: attachment.name, loading: 'lazy' });
      image.addEventListener('error', () => image.classList.add('img-load-error'));
      const fig = el('figure', { class: 'agent-message-attachment agent-message-image' }, image, el('figcaption', { text: attachment.name }));
      gallery.append(fig);
      continue;
    }
    if (attachment.type === 'video') {
      const src = attachment.data_url || (attachment.file_path ? '/local-file?path=' + encodeURIComponent(attachment.file_path) : '');
      const video = el('video', { controls: true, preload: 'metadata', src });
      video.addEventListener('error', () => video.classList.add('video-load-error'));
      const fig = el('figure', { class: 'agent-message-attachment agent-message-video' }, video, el('figcaption', { text: attachment.name }));
      gallery.append(fig);
      continue;
    }
    if (attachment.type === 'audio') {
      // Inline data URL wins so playback works before /local-file is
      // resolvable (e.g. the server has not yet flushed the attachment);
      // otherwise fall back to the local-file proxy path used for every
      // other persisted media kind.
      const src = attachment.data_url || (attachment.file_path ? '/local-file?path=' + encodeURIComponent(attachment.file_path) : '');
      const audio = el('audio', { controls: true, preload: 'metadata', src });
      audio.addEventListener('error', () => audio.classList.add('audio-load-error'));
      const fig = el('figure', { class: 'agent-message-attachment agent-message-audio' }, audio, el('figcaption', { text: attachment.name }));
      gallery.append(fig);
      continue;
    }
    if (attachment.type === 'folder') {
      gallery.append(el('div', { class: 'agent-message-attachment agent-message-file' },
        el('span', { class: 'agent-message-file-kind', text: 'DIR' }),
        el('span', { class: 'agent-message-file-name', text: attachment.name }),
        attachment.file_path ? el('span', { class: 'agent-message-file-path', text: attachment.file_path }) : null,
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
