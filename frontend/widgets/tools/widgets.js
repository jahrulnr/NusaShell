import { renderMarkdownBody } from '../../shared/markdown-body.js?v=7';
import { commandLanguage, highlightCommand } from './command-highlight.js?v=3';
import { adoptWidgetStyles } from '../../shared/widget-styles.js?v=7';

const LABELS = Object.freeze({
  file_read: 'Read file', file_write: 'Write file', file_patch: 'Patch file', file_list: 'List folder',
  file_mkdir: 'Create folder', file_delete: 'Delete path', file_move: 'Move path', file_copy: 'Copy path', file_info: 'Inspect path',
  grep: 'Search text', find_file: 'Find files', file_search: 'Find files',
  web_search: 'Web search', web_fetch: 'Read page', web_answer: 'Web answer',
  show: 'Preview', read_media: 'Read media', generate_media: 'Generate media', generate_image: 'Generate image',
  generate_speech: 'Generate speech', generate_video: 'Generate video', todo: 'Tasks', ask_question: 'Question',
  subagent: 'Subagent', delegate: 'Delegate', automation: 'Automation', automation_schedule: 'Schedule',
  wait_until: 'Wait until', sleep: 'Pause', skill: 'Skills', memory: 'Memory', docs: 'Docs', memory_project: 'Project memory',
  conversation: 'Conversations', mcp_list: 'MCP servers', tool_list: 'MCP tools', tool_schema: 'Tool schema',
  mcp_search: 'Find MCP tool', contract_read: 'Plugin contract', mcp_register: 'Register plugin', mcp_enable: 'Connect plugin',
  mcp_disable: 'Disconnect plugin', mcp_unregister: 'Remove plugin', mcp_install: 'Install plugin', mcp_server_add: 'Add MCP server',
});

function argsOf(event) {
  if (event?.args && typeof event.args === 'object') return event.args;
  try { return JSON.parse(event?.command || '{}'); } catch { return {}; }
}

function text(value) {
  if (value === undefined || value === null) return '';
  return typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value);
}

function filename(path) {
  return String(path || '').split(/[\\/]/).filter(Boolean).at(-1) || String(path || 'Untitled');
}

function mediaPath(event, args) {
  if (args.path || args.file_path) return args.path || args.file_path;
  const attachments = event?.presentation?.result?.attachments;
  return Array.isArray(attachments) ? attachments.find((item) => item?.file_path)?.file_path || '' : '';
}

function mediaKind(args, path) {
  const declared = String(args.op || args.media_type || '').toLowerCase();
  if (['html', 'image', 'audio', 'video', 'pdf'].includes(declared)) return declared;
  const extension = String(path).split(/[?#]/)[0].split('.').at(-1)?.toLowerCase();
  if (['html', 'htm'].includes(extension)) return 'html';
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'avif'].includes(extension)) return 'image';
  if (['mp3', 'wav', 'ogg', 'm4a', 'flac'].includes(extension)) return 'audio';
  if (['mp4', 'webm', 'mov', 'm4v'].includes(extension)) return 'video';
  return extension === 'pdf' ? 'pdf' : 'media';
}

// A media element that 404s or fails to decode must not sit as a broken
// frame: swap it for a readable fallback carrying the file name plus the
// existing open/retry actions. Retries are explicit user gestures only — no
// automatic reload loops — and each fresh element re-arms the same fallback.
function mediaFallback(host, path, kind, reload) {
  const box = make('div', 'media-preview-fallback');
  const mark = make('span', 'media-preview-fallback-mark', '!');
  mark.setAttribute('aria-hidden', 'true');
  const copy = make('div', 'media-preview-fallback-copy');
  copy.append(make('strong', '', filename(path)),
    make('span', '', `The ${kind} preview could not be loaded — the file may be missing or unreadable.`));
  const actions = make('div', 'media-preview-fallback-actions');
  const retry = make('button', 'instrument-action', 'Retry');
  retry.type = 'button';
  retry.addEventListener('click', () => box.replaceWith(reload()));
  const open = make('button', 'instrument-action', 'Preview file');
  open.type = 'button';
  open.addEventListener('click', () => emit(host, 'artifact-open', { path, mediaType: kind }));
  actions.append(retry, open);
  box.append(mark, copy, actions);
  return box;
}

function mediaPreview(host, path, kind) {
  const source = `/local-file?path=${encodeURIComponent(path)}`;
  const mount = () => {
    let media;
    if (kind === 'image') {
      media = document.createElement('img'); media.src = source; media.alt = filename(path); media.loading = 'lazy';
    } else if (kind === 'audio') {
      media = document.createElement('audio'); media.src = source; media.controls = true; media.preload = 'metadata';
    } else if (kind === 'video') {
      media = document.createElement('video'); media.src = source; media.controls = true; media.preload = 'metadata';
    } else {
      const frame = document.createElement('iframe');
      frame.src = source;
      frame.title = `Preview ${filename(path)}`;
      frame.loading = 'lazy';
      if (kind === 'html') frame.setAttribute('sandbox', 'allow-scripts');
      else frame.setAttribute('sandbox', '');
      return frame;
    }
    media.addEventListener('error', () => media.replaceWith(mediaFallback(host, path, kind, mount)), { once: true });
    return media;
  };
  return mount();
}

function label(event) {
  const args = argsOf(event);
  const operation = String(args.op || '').trim().toLowerCase().replace(/[\s-]+/g, '_');
  if (event?.tool === 'automation') {
    if (operation === 'pause' || operation === 'sleep') return 'Pause';
    if (operation === 'wait' || operation === 'wait_until') return 'Wait until';
  }
  if (event?.tool === 'automation_schedule') return 'Schedule';
  if (event?.tool === 'wait_until') return 'Wait until';
  if (event?.tool === 'sleep') return 'Pause';
  return LABELS[event?.tool] || String(event?.tool || 'Tool').replace(/[_.-]+/g, ' ');
}

export function formatAutomationDuration(value) {
  if (typeof value === 'string') {
    const match = value.trim().match(/^(\d+(?:\.\d+)?)\s*([smh])$/i);
    if (match) return `${match[1]}${match[2].toLowerCase()}`;
  }
  const seconds = Number(value);
  if (!Number.isFinite(seconds) || seconds <= 0) return '';
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds >= 60) {
    const minutes = Math.floor(seconds / 60);
    const remainder = seconds % 60;
    return remainder ? `${minutes}m ${remainder}s` : `${minutes}m`;
  }
  return `${seconds}s`;
}

function automationPrimary(event, args) {
  const operation = String(args.op || '').trim().toLowerCase().replace(/[\s-]+/g, '_');
  const title = label(event);
  if (operation === 'pause' || operation === 'sleep' || event?.tool === 'sleep') {
    const duration = formatAutomationDuration(args.seconds ?? args.duration ?? args.delay ?? args.cooldown);
    return duration ? `${title} · ${duration}` : title;
  }
  return args.op || title;
}

function status(event) {
  const value = String(event?.status || 'running').toLowerCase();
  if (value === 'complete' || value === 'completed' || value === 'ok') return 'done';
  if (value === 'failed' || value === 'error') return 'failed';
  return value;
}

function canStopExec(event) {
  return event?.tool === 'exec' && ['running', 'pending', 'in_progress', 'working', 'queued'].includes(status(event));
}

function detailText(event) {
  const result = event?.presentation?.result;
  if (Array.isArray(result?.items) && result.items.length) return result.items.map((item) => text(item)).join('\n');
  return text(result?.text || event?.output || event?.command || event?.args);
}

function make(tag, className, content = '') {
  const node = document.createElement(tag);
  if (className) node.className = className;
  node.textContent = content;
  return node;
}

function emit(host, name, detail = {}) {
  host.dispatchEvent(new CustomEvent(name, { bubbles: true, composed: true, detail }));
}

function shell(host, kind, event) {
  const root = host.shadowRoot || host.attachShadow({ mode: 'open' });
  let article = root.querySelector('.tool-instrument');
  if (!article) {
    adoptWidgetStyles(root, 'tool');
    article = make('article', 'tool-instrument');
    const header = make('header', 'instrument-head');
    header.append(make('span', 'instrument-kind'), make('strong', 'instrument-title'));
    const state = make('span', 'instrument-status');
    state.setAttribute('role', 'status');
    const stop = make('button', 'tool-stop', '■ Stop');
    stop.type = 'button';
    stop.title = 'Stop this exec';
    stop.setAttribute('aria-label', 'Stop running exec');
    stop.hidden = true;
    stop.addEventListener('click', (click) => {
      click.preventDefault();
      click.stopPropagation();
      if (stop.disabled) return;
      stop.disabled = true;
      stop.dataset.stopPending = 'true';
      stop.textContent = 'Stopping…';
      emit(host, 'tool-stop-request', {
        runId: host._event?.runId || '',
        toolCallId: host._event?.toolCallId || '',
      });
    });
    header.append(state, make('span', 'instrument-duration'), stop);
    article.append(header, make('div', 'instrument-body'));
    root.append(article);
  }
  article.className = `tool-instrument tool-${kind}`;
  article.dataset.status = status(event);
  const actionLabel = label(event);
  const operationWidget = kind === 'automation';
  article.querySelector('.instrument-kind').textContent = operationWidget ? actionLabel : kind;
  article.querySelector('.instrument-title').textContent = operationWidget ? '' : actionLabel;
  article.querySelector('.instrument-status').textContent = status(event);
  article.querySelector('.instrument-duration').textContent = event.duration || '';
  const stop = article.querySelector('.tool-stop');
  if (stop) {
    const enabled = canStopExec(event) && !stop.dataset.stopAccepted;
    stop.hidden = !enabled;
    stop.disabled = Boolean(stop.dataset.stopPending);
    if (enabled && !stop.dataset.stopPending) stop.textContent = '■ Stop';
    stop.dataset.runId = event.runId || '';
    stop.dataset.toolCallId = event.toolCallId || '';
  }
  return article;
}

function replaceBody(article, ...nodes) {
  const body = article.querySelector('.instrument-body');
  body.replaceChildren(...nodes.filter(Boolean));
  return body;
}

function addDetails(article, event, title = 'Details', { markdownBody = false } = {}) {
  const body = detailText(event);
  const id = `tool-detail-${String(event.id || 'tool').replace(/[^a-zA-Z0-9_-]/g, '-')}`;
  let button = article.querySelector('.instrument-toggle');
  let panel = article.querySelector('.instrument-detail');
  if (!body) {
    button?.remove();
    panel?.remove();
    return null;
  }
  if (!button || !panel) {
    button = make('button', 'instrument-toggle');
    button.type = 'button';
    panel = make('section', 'instrument-detail');
    button.addEventListener('click', () => {
      const expanded = button.getAttribute('aria-expanded') !== 'true';
      button.setAttribute('aria-expanded', String(expanded));
      panel.hidden = !expanded;
      if (expanded && panel._markdownBody && panel._detailSource !== undefined) {
        const source = panel._detailSource;
        panel._markdownBody.replaceChildren();
        void renderMarkdownBody(panel._markdownBody, source);
      }
    });
    article.append(button, panel);
  }
  button.textContent = title;
  button.setAttribute('aria-controls', id);
  if (!button.hasAttribute('aria-expanded')) button.setAttribute('aria-expanded', 'false');
  panel.id = id;
  panel.hidden = button.getAttribute('aria-expanded') !== 'true';
  if (markdownBody) {
    let content = panel.querySelector('.instrument-markdown-detail');
    if (!content) {
      content = make('div', 'instrument-markdown-detail');
      panel.replaceChildren(content);
    }
    panel._markdownBody = content;
    const changed = panel._detailSource !== body;
    panel._detailSource = body;
    if (changed && !panel.hidden) void renderMarkdownBody(content, body);
  } else {
    delete panel._markdownBody;
    panel._detailSource = body;
    let pre = panel.querySelector('pre');
    if (!pre) {
      pre = make('pre');
      panel.replaceChildren(pre);
    }
    pre.textContent = body;
  }
  return panel;
}

class ToolElement extends HTMLElement {
  set event(value) { this._event = value; if (this.isConnected) this.render(); }
  connectedCallback() { this.render(); }

  setToolStopPending(pending) {
    const button = this.shadowRoot?.querySelector('.tool-stop');
    if (!button) return;
    button.disabled = pending;
    button.dataset.stopPending = String(pending);
    if (pending) button.textContent = 'Stopping…';
    else {
      delete button.dataset.stopPending;
      button.textContent = '■ Stop';
    }
  }
}

class TerminalToolWidget extends ToolElement {
  render() {
    if (!this._event) return;
    const args = argsOf(this._event);
    const article = shell(this, 'terminal', this._event);
    const body = article.querySelector('.instrument-body');
    let line = body.querySelector('.terminal-line');
    let cwd = body.querySelector('.terminal-cwd');
    let output = body.querySelector('.terminal-live-output pre');
    if (!line || !output) {
      line = make('div', 'terminal-line');
      const command = make('button', 'terminal-command');
      command.type = 'button';
      command.addEventListener('click', () => {
        const expanded = command.classList.toggle('is-expanded');
        command.title = expanded ? 'Collapse command' : command.dataset.fullCommand || '';
        command.setAttribute('aria-label', expanded ? 'Collapse full command' : 'Show full command');
      });
      line.append(make('span', 'terminal-prompt', '$'), command);
      cwd = make('span', 'terminal-cwd');
      const viewport = make('section', 'terminal-live-output');
      output = make('pre');
      viewport.append(output);
      replaceBody(article, line, cwd, viewport);
    }
    const command = line.querySelector('.terminal-command');
    const commandText = text(args.command || args.cmd || args.ref || this._event.command);
    const language = commandLanguage(args, commandText);
    const sourceChanged = command.dataset.commandSource !== commandText
      || command.dataset.commandLanguage !== language;
    command.dataset.fullCommand = commandText;
    if (sourceChanged || !command.dataset.commandState) {
      const highlightToken = (this._commandHighlightToken || 0) + 1;
      this._commandHighlightToken = highlightToken;
      command.dataset.commandSource = commandText;
      command.dataset.commandLanguage = language;
      command.dataset.commandState = 'pending';
      command.classList.remove('hljs');
      command.textContent = commandText;
      void highlightCommand(commandText, args).then((result) => {
        if (this._commandHighlightToken !== highlightToken
          || command.dataset.commandSource !== commandText
          || command.dataset.commandLanguage !== language) return;
        if (result.highlighted) {
          command.innerHTML = result.html;
          command.classList.add('hljs');
          command.dataset.commandState = 'highlighted';
        } else {
          command.textContent = commandText;
          command.dataset.commandState = 'plain';
        }
      });
    }
    if (!command.classList.contains('is-expanded')) {
      command.title = commandText;
      command.setAttribute('aria-label', 'Show full command');
    }
    cwd.textContent = args.cwd || '';
    cwd.hidden = !args.cwd;
    const followsTail = output.scrollTop + output.clientHeight >= output.scrollHeight - 12;
    output.textContent = this._event.output || (status(this._event) === 'running' ? 'Waiting for output…' : 'No output.');
    if (followsTail) output.scrollTop = output.scrollHeight;
  }
}

class QuestionToolWidget extends ToolElement {
  render() {
    if (!this._event) return;
    const args = argsOf(this._event);
    const article = shell(this, 'question', this._event);
    const question = make('div', 'question-copy');
    const questionSource = args.question || 'Waiting for your answer';
    question.dataset.source = questionSource;
    renderMarkdownBody(question, questionSource);
    const body = replaceBody(article, question);
    const options = make('div', 'question-options');
    options.setAttribute('role', 'list');
    for (const option of Array.isArray(args.options) ? args.options : []) {
      const item = make('span', 'question-option', option.label || option.id);
      item.setAttribute('role', 'listitem');
      options.append(item);
    }
    body.append(options);
    const done = status(this._event) !== 'running';
    const open = make('button', 'instrument-action', done ? 'View answer' : 'Answer');
    open.type = 'button'; open.dataset.primaryAction = '';
    if (done) {
      const panel = addDetails(article, this._event, 'Answer', { markdownBody: true });
      const toggle = article.querySelector('.instrument-toggle');
      if (panel && toggle) {
        // A settled ask is read-only: its recorded answer already lives in
        // the details disclosure, so View answer proxies that control
        // instead of re-entering the pending-ask flow through question-open.
        const syncExpanded = () => open.setAttribute('aria-expanded', toggle.getAttribute('aria-expanded') || 'false');
        open.setAttribute('aria-controls', panel.id);
        open.addEventListener('click', () => { toggle.click(); syncExpanded(); });
        if (!toggle._viewAnswerSync) {
          toggle._viewAnswerSync = true;
          toggle.addEventListener('click', () => {
            article.querySelector('[data-primary-action]')
              ?.setAttribute('aria-expanded', toggle.getAttribute('aria-expanded') || 'false');
          });
        }
        syncExpanded();
        body.append(open);
      }
    } else {
      open.addEventListener('click', () => emit(this, 'question-open', { event: this._event }));
      body.append(open);
    }
  }
}

class FileToolWidget extends ToolElement {
  render() {
    if (!this._event) return;
    const args = argsOf(this._event); const path = args.path || args.destination || args.source || '';
    const article = shell(this, 'file', this._event);
    const body = replaceBody(article, make('code', 'tool-file-path', path || 'Workspace'));
    if (args.source && args.destination) body.append(make('span', 'file-route', `${args.source} → ${args.destination}`));
    const open = make('button', 'instrument-action', 'Preview file'); open.type = 'button'; open.dataset.primaryAction = '';
    open.addEventListener('click', () => emit(this, 'artifact-open', { path }));
    body.append(open); addDetails(article, this._event);
  }
}

class SearchToolWidget extends ToolElement {
  render() {
    if (!this._event) return;
    const args = argsOf(this._event); const article = shell(this, 'search', this._event);
    replaceBody(article,
      make('strong', 'search-query', args.pattern || args.query || 'Search'),
      make('span', 'search-scope', args.path || args.glob_pattern || 'workspace'));
    addDetails(article, this._event, 'Matches');
  }
}

class WebToolWidget extends ToolElement {
  render() {
    if (!this._event) return;
    const args = argsOf(this._event); const article = shell(this, 'web', this._event);
    const body = replaceBody(article, make('strong', 'web-query', args.query || args.question || filename(args.url) || 'Web'));
    if (args.url) { const link = make('a', 'web-link', args.url); link.href = args.url; link.target = '_blank'; link.rel = 'noreferrer'; body.append(link); }
    addDetails(article, this._event, 'Sources');
  }
}

class MediaToolWidget extends ToolElement {
  render() {
    if (!this._event) return;
    const args = argsOf(this._event); const path = mediaPath(this._event, args); const kind = mediaKind(args, path);
    const article = shell(this, 'media', this._event);
    const plate = make('div', 'media-plate');
    plate.append(make('span', 'media-type', kind), make('strong', 'media-name', filename(path || args.prompt)));
    const body = replaceBody(article, plate);
    if (!path) { addDetails(article, this._event, 'View result'); return; }

    const controls = make('div', 'media-actions');
    const previewId = `media-preview-${String(this._event.id || 'tool').replace(/[^a-zA-Z0-9_-]/g, '-')}`;
    const open = make('button', 'instrument-action', 'Preview'); open.type = 'button'; open.dataset.primaryAction = '';
    open.setAttribute('aria-expanded', 'false'); open.setAttribute('aria-controls', previewId);
    const files = make('button', 'instrument-action media-open-preview', 'Preview file'); files.type = 'button';
    files.addEventListener('click', () => emit(this, 'artifact-open', { path, mediaType: kind }));
    controls.append(open, files); body.append(controls);

    const preview = make('section', 'media-preview'); preview.id = previewId; preview.hidden = true;
    open.addEventListener('click', () => {
      const expanded = open.getAttribute('aria-expanded') !== 'true';
      open.setAttribute('aria-expanded', String(expanded)); preview.hidden = !expanded;
      if (expanded && !preview.firstChild) preview.append(mediaPreview(this, path, kind));
    });
    body.append(preview); addDetails(article, this._event);
  }
}

class TaskToolWidget extends ToolElement {
  render() {
    if (!this._event) return;
    const args = argsOf(this._event); const items = Array.isArray(args.items) ? args.items : [];
    const article = shell(this, 'task', this._event);
    const body = replaceBody(article, make('strong', 'task-count', `${items.length} item${items.length === 1 ? '' : 's'}`), make('span', 'task-mode', args.mode || 'update'));
    const open = make('button', 'instrument-action', 'Open tasks'); open.type = 'button'; open.dataset.primaryAction = '';
    open.addEventListener('click', () => emit(this, 'task-open')); body.append(open); addDetails(article, this._event);
  }
}

class SubagentToolWidget extends ToolElement {
  render() {
    if (!this._event) return;
    const args = argsOf(this._event); const article = shell(this, 'subagent', this._event);
    const agent = args.agent_name || args.agent_id || args.agent || 'ACP';
    const body = replaceBody(article,
      make('strong', 'subagent-title', args.title || 'Delegated task'),
      make('small', 'subagent-agent', `ACP · ${agent}`),
    );
    if (args.prompt) {
      const prompt = make('div', 'subagent-prompt');
      renderMarkdownBody(prompt, args.prompt);
      body.append(prompt);
    }
    const open = make('button', 'instrument-action', 'Open subagents'); open.type = 'button'; open.dataset.primaryAction = '';
    open.addEventListener('click', () => emit(this, 'subagent-open', { id: args.id })); body.append(open); addDetails(article, this._event);
  }
}

class SimpleToolWidget extends ToolElement {
  render() {
    if (!this._event) return;
    const kind = this.localName.replace(/-tool-widget$/, ''); const args = argsOf(this._event);
    const article = shell(this, kind, this._event);
    const primary = kind === 'automation'
      ? automationPrimary(this._event, args)
      : args.op || args.query || args.id || args.ref || args.at || args.name || this._event.presentation?.action || label(this._event);
    replaceBody(article, make('strong', 'simple-primary', text(primary)));
    addDetails(article, this._event);
  }
}

class AutomationToolWidget extends SimpleToolWidget {}
class KnowledgeToolWidget extends SimpleToolWidget {}
class ConnectorToolWidget extends SimpleToolWidget {}
class GenericToolWidget extends SimpleToolWidget {}

const DEFINITIONS = [
  ['terminal-tool-widget', TerminalToolWidget], ['question-tool-widget', QuestionToolWidget], ['file-tool-widget', FileToolWidget],
  ['search-tool-widget', SearchToolWidget], ['web-tool-widget', WebToolWidget], ['media-tool-widget', MediaToolWidget],
  ['task-tool-widget', TaskToolWidget], ['subagent-tool-widget', SubagentToolWidget], ['automation-tool-widget', AutomationToolWidget],
  ['knowledge-tool-widget', KnowledgeToolWidget], ['connector-tool-widget', ConnectorToolWidget], ['generic-tool-widget', GenericToolWidget],
];
for (const [name, constructor] of DEFINITIONS) if (!customElements.get(name)) customElements.define(name, constructor);
