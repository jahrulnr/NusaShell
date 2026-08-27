import { renderMarkdown } from '../../markdown.js';
import { el, fmtTime, registerOverlayDismiss } from '../../ui.js';
import { rpc } from '../../rpc.js';
import { createAskCard } from '../ask-card.js';
import { openDrawer, agentNameForId, firstVisibleRunId } from './subagents.js';
import { renderArtifactCard, parseArtifactOutput } from '../../artifact-render.js';
import { openAudioLightbox, openVideoLightbox, openTextPreviewPopup } from '../../media-zoom.js';
import { agentThread, composerInput, toolJobStrip } from './domrefs.js';

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
    const node = el('div', { class: `agent-message user${message.steer ? ' agent-steer' : ''}${message.auto_continue ? ' agent-auto-continue' : ''}` });
    const bubble = el('div', { class: 'agent-bubble' });
    bubble.append(el('div', { text: message.content || (message.attachments?.length ? 'Attached files' : '') }));
    if (message.attachments?.length) bubble.append(renderMessageAttachments(message.attachments));
    node.append(bubble);
    const meta = el('div', { class: 'agent-message-meta' });
    if (message.steer) {
      meta.append(el('span', { class: 'agent-message-steer-flag', text: 'Steer message' }));
    }
    if (message.auto_continue) {
      meta.append(el('span', { class: 'agent-message-auto-continue-flag', text: 'Auto-continue' }));
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
        appendToolCards(bubble, step.tool_calls.map(renderToolCallCard));
      }
    }
  } else {
    if (message.reasoning?.trim()) bubble.append(reasoningDisclosure(message.reasoning));
    const textBox = el('div', { class: 'agent-bubble-text' });
    if (message.content) textBox.innerHTML = renderMarkdown(message.content);
    if (textBox.innerHTML) bubble.append(textBox);
    if (message.tool_calls?.length) appendToolCards(bubble, message.tool_calls.map(renderToolCallCard));
  }
}

// appendToolCards splits rendered tool cards into standalone cards (ask,
// show, generate_*, artifact, subagent — anything with its own frame) and
// tool terminals (exec, grep, file_read — plain collapsible rows). Standalone
// cards append directly to the bubble so they render without the
// .agent-tool-stack border-left lane; terminals are grouped inside a stack
// so the border-left visual cue applies only to terminal-style output.
function appendToolCards(bubble, cards) {
  const terminals = [];
  for (const card of cards) {
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
// place them outside the .agent-tool-stack lane — the stack's border-left
// is a visual cue for tool terminals (exec, grep, file_read) only, and
// looks wrong around a media card or ask panel that already has its own
// frame.
export function renderToolCallCard(toolCall) {
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
    return card;
  }
  if (toolCall.name === 'subagent') {
    const card = renderSubagentCard(toolCall);
    card.dataset.standalone = 'true';
    return card;
  }
  if (toolCall.name === 'generate_image') {
    const card = renderGenerateImageCard(toolCall);
    card.dataset.standalone = 'true';
    return card;
  }
  if (toolCall.name === 'generate_speech') {
    const card = renderGenerateSpeechCard(toolCall);
    card.dataset.standalone = 'true';
    return card;
  }
  if (toolCall.name === 'generate_video') {
    const card = renderGenerateVideoCard(toolCall);
    card.dataset.standalone = 'true';
    return card;
  }
  const artifact = parseArtifactOutput(toolCall);
  if (artifact) {
    const card = renderArtifactCard(toolCall, artifact);
    card.dataset.standalone = 'true';
    return card;
  }
  const showImage = parseShowImageOutput(toolCall);
  if (showImage) {
    const card = renderShowImageCard(toolCall, showImage);
    card.dataset.standalone = 'true';
    return card;
  }
  const showAudio = parseShowAudioOutput(toolCall);
  if (showAudio) {
    const card = renderShowAudioCard(toolCall, showAudio);
    card.dataset.standalone = 'true';
    return card;
  }
  const showVideo = parseShowVideoOutput(toolCall);
  if (showVideo) {
    const card = renderShowVideoCard(toolCall, showVideo);
    card.dataset.standalone = 'true';
    return card;
  }
  return renderToolJob(toolCall);
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
  return card;
}
// Async flow: saat spawn, output = YAML frontmatter
// `runs: [{id, status, workspace}]` (status "starting"). Saat selesai,
// output = YAML frontmatter (status, workspace, output_path) + markdown
// body (summary). Card re-render via EventToolCompleted.
export function renderSubagentCard(toolCall) {
  const args = parseToolArgs(toolCall.args);
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
    if (parsed.meta?.runs?.length) runs = parsed.meta.runs;
  }

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
    const runId = runRow?.dataset.runId || runs[0]?.id || meta?.id || firstVisibleRunId();
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

// renderGenerateSpeechCard mirrors renderGenerateImageCard but for
// generate_speech. Surfaces provider/model/voice/duration/cost parsed from
// the YAML output frontmatter plus a click-to-play <audio> plate and a
// Download link, so the speech tool has the same affordances the image
// tool already has. Falls back to /local-file?path= when the inline data
// URL is absent (large audio payloads, replayed history).
function renderGenerateSpeechCard(toolCall) {
  const args = parseToolArgs(toolCall.args);
  const parsed = parseSubagentResult(toolCall.output || '');
  const meta = parsed.meta || {};
  // output_attachments is the wire field; older history may use
  // .attachments. We accept either.
  const attachments = [...(toolCall.output_attachments || toolCall.attachments || [])]
    .filter((item) => !item.type || item.type === 'audio');
  const isFailed = toolCall.status === 'fail' || meta.status === 'failed';
  const isRunning = !isFailed && (toolCall.status === 'running' || !toolCall.output);
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
  return card;
}

// renderGenerateVideoCard mirrors renderGenerateImageCard and
// renderGenerateSpeechCard but for generate_video. Surfaces
// provider/model/duration/resolution/cost metadata parsed from the YAML
// output frontmatter plus a click-to-play <video> plate and a Download
// link.
function renderGenerateVideoCard(toolCall) {
  const args = parseToolArgs(toolCall.args);
  const parsed = parseSubagentResult(toolCall.output || '');
  const meta = parsed.meta || {};
  // output_attachments is the wire field; older history may use
  // .attachments. We accept either.
  const attachments = [...(toolCall.output_attachments || toolCall.attachments || [])]
    .filter((item) => !item.type || item.type === 'video');
  const isFailed = toolCall.status === 'fail' || meta.status === 'failed';
  const isRunning = !isFailed && (toolCall.status === 'running' || !toolCall.output);
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
  const mcpRef = isMcpCall ? (parseToolArgs(toolCall.args).ref || null) : null;
  const displayName = isMcp
    ? name.replace(/^mcp__/, '').replace(/__/g, ' · ')
    : isMcpCall && mcpRef
      ? mcpRef.replace(':', ' · ')
      : name;
  const elapsed = toolCall.elapsed
    ? (() => { const t = Math.max(0, Math.floor(Number(toolCall.elapsed) || 0)); return t < 60 ? `${t}s` : `${Math.floor(t / 60)}m ${t % 60}s`; })()
    : '';
  const streaming = isStreamingTool(name);
  const summary = el('summary', {},
    el('span', { class: 'agent-tool-terminal-prompt', text: '›_' }),
    el('span', { class: 'agent-tool-terminal-title', text: displayName }),
    (isMcp || isMcpCall) ? el('span', { class: 'agent-tool-terminal-badge', text: 'MCP' }) : null,
    el('span', { class: 'agent-tool-terminal-meta', text: toolTerminalMeta(toolCall) }),
    el('span', { class: 'agent-tool-elapsed', text: elapsed }),
    ...(streaming ? [el('button', { class: 'agent-tool-stop', type: 'button', title: 'Stop this tool', 'aria-label': 'Stop running tool', hidden: true }, '■ Stop')] : []),
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
  btn.addEventListener('click', () => {
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
  // The per-call stop button lives on streaming tools only and disappears
  // once the tool settles.
  const stop = card.querySelector('.agent-tool-stop');
  if (stop) stop.hidden = running ? false : true;
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

function toolTerminalPanel(label, codeClass, text) {
  return el('div', { class: 'agent-tool-terminal-panel' },
    el('div', { class: 'agent-tool-terminal-panel-label', text: label }),
    el('pre', { class: codeClass, text }),
  );
}

function summarizeToolArgs(args) {
  if (!args || typeof args !== 'object' || Array.isArray(args)) return '';
  // Dispatcher-family calls carry the verb as `op`; the terminal title and
  // input panel already show it, so summarize the remaining payload only.
  const rest = Object.entries(args).filter(([key]) => key !== 'op');
  if (!rest.length) return '';
  if (rest.length === 1) {
    const value = JSON.stringify(rest[0][1]);
    return value.length > 42 ? `${value.slice(0, 42)}…` : value;
  }
  return `${rest.length} args`;
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
