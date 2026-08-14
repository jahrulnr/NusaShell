import { renderMarkdown } from '../../markdown.js';
import { el, fmtTime } from '../../ui.js';

export function renderEmptyThread() {
  const thread = document.getElementById('agent-thread');
  thread.innerHTML = '';
  thread.append(el('div', { class: 'agent-empty' },
    el('div', { class: 'agent-empty-mark', text: '✦' }),
    el('h2', { text: 'Start a conversation' }),
    el('p', { text: 'Ask anything. The agent can use skills, memory, docs and your MCP servers as tools.' }),
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
  if (message.role === 'system') {
    return el('div', { class: 'agent-compaction-marker', text: message.content });
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
  const node = el('div', {
    class: `agent-message assistant${failedMessage ? ' agent-message-error' : ''}`,
  });

  const bubble = el('div', { class: 'agent-bubble' });
  for (const message of messages) appendAssistantSteps(bubble, message);
  if (bubble.children.length) node.append(bubble);

  const meta = el('div', { class: 'agent-turn-meta' });
  const model = [...messages].reverse().find((message) => message.model)?.model;
  if (model) meta.append(el('span', { class: 'agent-turn-tag', text: model }));
  const usage = totalUsage(messages);
  if (usage) {
    meta.append(el('span', { class: 'agent-turn-tag', text: `↑${usage.input_tokens} ↓${usage.output_tokens}` }));
    if (usage.cache_read) meta.append(el('span', { class: 'agent-turn-tag', text: `cache ${usage.cache_read}` }));
  }
  meta.append(el('span', { class: 'agent-message-meta', text: fmtTime(finalMessage.created_at) }));
  node.append(meta);
  if (failedMessage?.error) node.append(el('div', { class: 'agent-message-meta', text: failedMessage.error }));
  if (failedMessage && onRetry) {
    const retryBtn = el('button', { class: 'agent-retry-btn', type: 'button', text: '↻ Retry with model' });
    retryBtn.addEventListener('click', () => onRetry(node));
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
        bubble.append(el('div', { class: 'agent-tool-stack' }, step.tool_calls.map(renderToolJob)));
      }
    }
  } else {
    if (message.reasoning?.trim()) bubble.append(reasoningDisclosure(message.reasoning));
    const textBox = el('div', { class: 'agent-bubble-text' });
    if (message.content) textBox.innerHTML = renderMarkdown(message.content);
    if (textBox.innerHTML) bubble.append(textBox);
    if (message.tool_calls?.length) bubble.append(el('div', { class: 'agent-tool-stack' }, message.tool_calls.map(renderToolJob)));
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

export function renderToolJob(toolCall) {
  const card = el('details', { class: 'agent-tool-terminal' });
  const summary = el('summary', {},
    el('span', { class: 'agent-tool-terminal-prompt', text: '›_' }),
    el('span', { class: 'agent-tool-terminal-title', text: toolCall.name || 'tool' }),
    el('span', { class: 'agent-tool-terminal-meta', text: toolTerminalMeta(toolCall) }),
    el('span', { class: 'agent-tool-terminal-chevron', text: '⌄' }),
  );
  const body = el('div', { class: 'agent-tool-terminal-body' },
    toolTerminalPanel('tool', 'agent-tool-terminal-input', formatToolTerminalInput(toolCall.name, toolCall.args)),
    toolTerminalPanel('Output', 'agent-tool-terminal-output', toolTerminalOutput(toolCall)),
  );
  card._toolArgs = toolCall.args;
  card.append(summary, body);
  setToolTerminalStatus(card, toolCall.status || 'running');
  return card;
}

export function toolTerminalMeta(toolCall) {
  const status = toolCall.status || 'running';
  return status === 'running' ? 'Running' : summarizeToolArgs(toolCall.args) || (status === 'fail' ? 'Failed' : 'Completed');
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

function truncate(value, limit) {
  return value.length > limit ? `${value.slice(0, limit)}\n… (truncated)` : value;
}

function attachmentIcon(attachment) {
  if (attachment.type === 'image') return 'IMG';
  if (attachment.type === 'file') return 'PDF';
  return 'TXT';
}

function renderMessageAttachments(attachments) {
  const gallery = el('div', {
    class: 'agent-message-attachments',
    'aria-label': `${attachments.length} attachment${attachments.length === 1 ? '' : 's'}`,
  });
  for (const attachment of attachments) {
    if (attachment.type === 'image') {
      const image = el('img', { src: attachment.data_url, alt: attachment.name, loading: 'lazy' });
      gallery.append(el('figure', { class: 'agent-message-attachment agent-message-image' }, image, el('figcaption', { text: attachment.name })));
      continue;
    }
    gallery.append(el('div', { class: 'agent-message-attachment agent-message-file' },
      el('span', { class: 'agent-message-file-kind', text: attachment.type === 'file' ? 'PDF' : 'TXT' }),
      el('span', { class: 'agent-message-file-name', text: attachment.name }),
    ));
  }
  return gallery;
}
