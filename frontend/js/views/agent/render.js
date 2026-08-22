import { renderMarkdown } from '../../markdown.js';
import { el, fmtTime, registerOverlayDismiss } from '../../ui.js';
import { createAskCard } from '../ask-card.js';
import { openDrawer, agentNameForId } from './subagents.js';
import { renderArtifactCard, parseArtifactOutput } from '../../artifact-render.js';

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
    prompt: 'I\'ll describe a task I repeat often — help me script it or schedule it as an automation.',
  },
];

export function applyStarterPrompt(prompt) {
  const input = document.getElementById('composer-input');
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
  document.getElementById('agent-thread')?.addEventListener('click', (event) => {
    const chip = event.target.closest?.('[data-starter-prompt]');
    if (!chip) return;
    applyStarterPrompt(chip.getAttribute('data-starter-prompt'));
  });
}

export function renderEmptyThread() {
  const thread = document.getElementById('agent-thread');
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
  document.getElementById('tool-job-strip').hidden = true;
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

export function setAgentOfflineState(isOffline) {
  document.getElementById('agent-offline-state').hidden = !isOffline;
  document.getElementById('agent-thread').hidden = isOffline;
  document.getElementById('agent-composer-stack').hidden = isOffline;
}

export function reasoningDisclosure(reasoning) {
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

export function renderMessage(message) {
  if (message.role === 'system' || isCompactionSummary(message)) {
    return renderCompactionMessage(message);
  }
  if (message.role === 'user') {
    const node = el('div', { class: `agent-message user${message.steer ? ' agent-steer' : ''}` });
    const bubble = el('div', { class: 'agent-bubble' });
    bubble.append(el('div', { text: message.content || (message.attachments?.length ? 'Attached files' : '') }));
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
  return typeof message.content === 'string' && message.content.startsWith('Compacted context handover:');
}

// renderCompactionMessage renders a compaction handover message as
// an assistant-style bubble so the summary content is readable, not crammed
// into a tiny dashed pill. A "Compacted context" label distinguishes it from
// a regular assistant response.
function renderCompactionMessage(message) {
  const node = el('div', { class: 'agent-message assistant agent-compaction-marker' });
  const bubble = el('div', { class: 'agent-bubble' });
  bubble.append(el('div', { class: 'agent-compaction-label', text: '⬇ Compacted context' }));
  const textBox = el('div', { class: 'agent-bubble-text' });
  if (message.content) textBox.innerHTML = renderMarkdown(message.content);
  bubble.append(textBox);
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
  const failedMessage = messages.find((message) => message.status === 'error');
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
  if (message.steps?.length) {
    for (const step of message.steps) {
      if (step.type === 'reasoning' && step.content?.trim()) {
        bubble.append(reasoningDisclosure(step.content));
      } else if (step.type === 'text' && step.content) {
        const textBox = el('div', { class: 'agent-bubble-text' });
        textBox.innerHTML = renderMarkdown(step.content);
        bubble.append(textBox);
      } else if (step.type === 'tool_calls' && step.tool_calls?.length) {
        bubble.append(el('div', { class: 'agent-tool-stack' }, step.tool_calls.map(renderToolCallCard)));
      }
    }
  } else {
    if (message.reasoning?.trim()) bubble.append(reasoningDisclosure(message.reasoning));
    const textBox = el('div', { class: 'agent-bubble-text' });
    if (message.content) textBox.innerHTML = renderMarkdown(message.content);
    if (textBox.innerHTML) bubble.append(textBox);
    if (message.tool_calls?.length) bubble.append(el('div', { class: 'agent-tool-stack' }, message.tool_calls.map(renderToolCallCard)));
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
export function renderToolCallCard(toolCall) {
  if (toolCall.name === 'ask_question') {
    let parsedArgs = {};
    try { parsedArgs = JSON.parse(toolCall.args || '{}'); } catch {}
    return createAskCard(toolCall.id, parsedArgs, {
      sealed: true,
      output: toolCall.output || '',
      ok: toolCall.status !== 'fail',
    });
  }
  if (toolCall.name === 'subagent') {
    return renderSubagentCard(toolCall);
  }
  if (toolCall.name === 'generate_image') {
    return renderGenerateImageCard(toolCall);
  }
  const artifact = parseArtifactOutput(toolCall);
  if (artifact) {
    return renderArtifactCard(toolCall, artifact);
  }
  return renderToolJob(toolCall);
}

// renderSubagentCard builds a delegation card for the `subagent` tool.
// Async flow: saat spawn, output = {"runs":[{"id":"...","status":"starting"}]}.
// Saat selesai, output = YAML frontmatter (status, workspace, output_path)
// + markdown body (summary). Card re-render via EventToolCompleted.
function renderSubagentCard(toolCall) {
  let args = {};
  try { args = JSON.parse(toolCall.args || '{}'); } catch {}
  let runs = [];
  let meta = null;       // parsed YAML header
  let outputText = '';   // markdown body (summary)
  try {
    const parsed = JSON.parse(toolCall.output || '{}');
    runs = parsed.runs || [];
  } catch {
    // Not JSON — try YAML frontmatter + markdown body
    const parsed = parseSubagentResult(toolCall.output || '');
    meta = parsed.meta;
    outputText = parsed.body;
  }

  const agentName = agentNameForId(args.agent_id) || 'Subagent';
  const promptPreview = (args.prompt || '').split('\n')[0].slice(0, 120);
  const isRunning = toolCall.status === 'running' || !toolCall.output;
  const isFailed = toolCall.status === 'fail' || meta?.status === 'failed';
  const isCancelled = meta?.status === 'cancelled';
  const status = isRunning ? 'running' : (isFailed ? 'error' : 'success');

  const card = el('div', { class: `agent-subagent-card is-${status}`, role: 'button', tabindex: '0', 'aria-label': `Open ${agentName} transcript` });
  card._toolArgs = toolCall.args;

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
    const runId = runRow?.dataset.runId || runs[0]?.id;
    if (runId) openDrawer(runId);
  });
  card.addEventListener('keydown', (event) => {
    if (event.key !== 'Enter' && event.key !== ' ') return;
    event.preventDefault();
    card.click();
  });

  return card;
}

// parseSubagentResult splits a YAML frontmatter + markdown body tool
// result into { meta, body }. Returns { meta: null, body: raw } if the
// input is not in the expected format.
function parseSubagentResult(raw) {
  if (!raw.startsWith('---\n')) return { meta: null, body: raw };
  const end = raw.indexOf('\n---\n', 4);
  if (end < 0) return { meta: null, body: raw };
  const header = raw.slice(4, end);
  const body = raw.slice(end + 5);
  const meta = {};
  for (const line of header.split('\n')) {
    const idx = line.indexOf(':');
    if (idx < 0) continue;
    const key = line.slice(0, idx).trim();
    let val = line.slice(idx + 1).trim();
    // strip surrounding quotes
    if (val.startsWith('"') && val.endsWith('"')) {
      val = val.slice(1, -1).replace(/\\"/g, '"').replace(/\\\\/g, '\\');
    }
    meta[key] = val;
  }
  return { meta, body };
}

function parseToolArgs(args) {
  if (!args) return {};
  if (typeof args === 'object' && !Array.isArray(args)) return args;
  if (typeof args === 'string') {
    try { return JSON.parse(args); } catch { return {}; }
  }
  return {};
}

function imageSrc(attachment) {
  if (!attachment) return '';
  if (attachment.data_url) return attachment.data_url;
  if (attachment.file_path) return '/local-file?path=' + encodeURIComponent(attachment.file_path);
  return '';
}

function formatImageCost(value) {
  const n = Number(value);
  if (!Number.isFinite(n) || n <= 0) return '';
  if (n < 0.01) return `$${n.toFixed(4)}`;
  return `$${n.toFixed(2)}`;
}

export function renderGenerateImageCard(toolCall) {
  const args = parseToolArgs(toolCall.args);
  const parsed = parseSubagentResult(toolCall.output || '');
  const meta = parsed.meta || {};
  const attachments = [...(toolCall.output_attachments || toolCall.attachments || [])]
    .filter((item) => !item.type || item.type === 'image');
  const isFailed = toolCall.status === 'fail' || meta.status === 'failed';
  const isRunning = !isFailed && (toolCall.status === 'running' || !toolCall.output);
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
  const card = el('details', { class: 'agent-tool-terminal' });
  const name = toolCall.name || 'tool';
  const isMcp = name.startsWith('mcp__');
  const isMcpCall = name === 'mcp_call';
  const mcpRef = isMcpCall ? parseMcpCallRef(toolCall.args) : null;
  const displayName = isMcp
    ? name.replace(/^mcp__/, '').replace(/__/g, ' · ')
    : isMcpCall && mcpRef
      ? mcpRef.replace(':', ' · ')
      : name;
  const summary = el('summary', {},
    el('span', { class: 'agent-tool-terminal-prompt', text: '›_' }),
    el('span', { class: 'agent-tool-terminal-title', text: displayName }),
    (isMcp || isMcpCall) ? el('span', { class: 'agent-tool-terminal-badge', text: 'MCP' }) : null,
    el('span', { class: 'agent-tool-terminal-meta', text: toolTerminalMeta(toolCall) }),
    el('span', { class: 'agent-tool-elapsed', text: toolCall.elapsed ? formatElapsed(toolCall.elapsed) : '' }),
    el('span', { class: 'agent-tool-terminal-chevron', text: '⌄' }),
  );
  const body = el('div', { class: 'agent-tool-terminal-body' },
    toolTerminalPanel('tool', 'agent-tool-terminal-input', formatToolTerminalInput(toolCall.name, toolCall.args)),
    toolTerminalPanel('Output', 'agent-tool-terminal-output', toolTerminalOutput(toolCall)),
  );
  card._toolArgs = toolCall.args;
  card._toolName = name;
  card.append(summary, body);
  setToolTerminalStatus(card, toolCall.status || 'running');
  return card;
}

// formatElapsed formats a duration in seconds (e.g. 3 → "3s", 75 → "1m 15s").
export function formatElapsed(seconds) {
  const total = Math.max(0, Math.floor(Number(seconds) || 0));
  if (total < 60) return `${total}s`;
  return `${Math.floor(total / 60)}m ${total % 60}s`;
}

// formatTokens renders a token count with a human unit suffix
// (e.g. 10680 → "10.68k", 27085560 → "27.09M", 137 → "137").
export function formatTokens(value) {
  const n = Number(value) || 0;
  if (n >= 1e9) return `${trimUnit((n / 1e9).toFixed(2))}B`;
  if (n >= 1e6) return `${trimUnit((n / 1e6).toFixed(2))}M`;
  if (n >= 1e3) return `${trimUnit((n / 1e3).toFixed(2))}k`;
  return String(n);
}

function trimUnit(value) {
  return value.replace(/\.?0+$/, '');
}

export function toolTerminalMeta(toolCall) {
  const status = toolCall.status || 'running';
  if (status === 'running') return 'Running';
  if (toolCall.name === 'mcp_call') {
    const inner = summarizeMcpCallArgs(toolCall.args);
    if (inner) return inner;
  }
  return summarizeToolArgs(toolCall.args) || (status === 'fail' ? 'Failed' : 'Completed');
}

export function toolTerminalOutput(toolCall) {
  if (toolCall.output !== undefined && toolCall.output !== null && toolCall.output !== '') return truncate(String(toolCall.output), 12000);
  return toolCall.status === 'running' ? '…' : toolCall.status === 'fail' ? 'Tool failed.' : 'ok';
}

export function setToolTerminalStatus(card, status) {
  const normalized = status || 'running';
  card.classList.toggle('is-running', normalized === 'running');
  card.classList.toggle('is-success', normalized === 'ok');
  card.classList.toggle('is-error', normalized === 'fail');
  card.dataset.status = normalized;
}

export function attachmentChip(attachment, onRemove) {
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

function toolTerminalPanel(label, codeClass, text) {
  return el('div', { class: 'agent-tool-terminal-panel' },
    el('div', { class: 'agent-tool-terminal-panel-label', text: label }),
    el('pre', { class: codeClass, text }),
  );
}

function parseMcpCallRef(args) {
  const parsed = parseToolArgs(args);
  return parsed.ref || null;
}

function summarizeMcpCallArgs(args) {
  const parsed = parseToolArgs(args);
  const inner = parsed.arguments_json;
  if (!inner) return '';
  return summarizeToolArgs(parseToolArgs(inner));
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
  if (tool === 'mcp_call') {
    const parsed = parseToolArgs(args);
    const ref = parsed.ref || '?';
    const inner = parseToolArgs(parsed.arguments_json);
    if (Object.keys(inner).length) {
      return `mcp_call(${ref}) ${truncate(JSON.stringify(inner, null, 2), 8000)}`;
    }
    return `mcp_call(${ref}) {}`;
  }
  const input = args && typeof args === 'object' ? truncate(JSON.stringify(args, null, 2), 8000) : '';
  return input ? `${tool}(${input})` : `${tool}()`;
}

function truncate(value, limit) {
  return value.length > limit ? `${value.slice(0, limit)}\n… (truncated)` : value;
}

function attachmentIcon(attachment) {
  if (attachment.type === 'image') return 'IMG';
  if (attachment.type === 'file') return 'PDF';
  if (attachment.type === 'folder') return 'DIR';
  return 'TXT';
}

function renderMessageAttachments(attachments) {
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
