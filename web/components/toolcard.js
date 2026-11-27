// Tool call row + terminal-style card. The row is a one-line summary
// ("Ran <command>"); expanding it shows the input (head first) and the
// output (tail first), each clipped with a fade and expandable in place.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import hljs from '/vendor/highlight/highlight.min.mjs';
import { toolSummary, toolRoom, closeStatus, toolEnding, formatDuration, toolClock, subagentView } from '../lib/turn.js';
import { knownRoom } from '../lib/chat.js';
import { useFollow } from '../lib/follow.js';
import { Icon } from './icons.js';
import { cx, Spinner } from './ui.js';
import { ToolMedia } from './media.js';

const CLIP_LINES = 12;
const SHELL_TOOLS = new Set(['exec', 'exec_session', 'shell', 'bash']);
// A search can name many rooms; three keep the row one line at any width, and
// the rest are counted rather than cut mid-name.
const ROOM_NAMES_SHOWN = 3;

const opened = new Map();
function useOpen(id) {
  const [open, set] = useState(opened.get(id) ?? false);
  return [open, (v) => { opened.set(id, v); set(v); }];
}

// Elapsed is the only thing on a tool row that ticks. It owns the one-second
// interval and its own state, so the clock re-renders this label alone — not
// the row's card, not the transcript — and it is unmounted (and its interval
// cleared) the moment the call stops running or the row goes away.
function Elapsed({ since }) {
  const [, tick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, [since]);
  return html`${formatDuration(Date.now() - since)}`;
}

function parseArgs(input) {
  try { return JSON.parse(input || '{}'); } catch { return null; }
}

function CopyIcon({ text }) {
  const [done, setDone] = useState(false);
  return html`<button type="button" title="Copy" aria-label="Copy"
    onClick=${(e) => { e.stopPropagation(); navigator.clipboard?.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 1200); }); }}
    class="absolute top-1.5 right-1.5 z-10 size-7 inline-flex items-center justify-center rounded-md border border-line bg-raised text-faint hover:text-fg opacity-0 group-hover/clip:opacity-100 focus:opacity-100 transition-opacity">
    <${Icon} name=${done ? 'check' : 'copy'} size=${13} />
  </button>`;
}

// Clip shows the first (head) or last (tail) lines of long text with a fade
// toward the hidden part; a chevron expands it. Tail mode follows appended
// text while the reader stays at the bottom.
function Clip({ id, text, mode, lang, live }) {
  const [full, setFull] = useOpen(id + ':full');
  const box = useRef();
  const lines = text.split('\n');
  const long = lines.length > CLIP_LINES;
  const shown = !long || full ? text : mode === 'head' ? lines.slice(0, CLIP_LINES).join('\n') : lines.slice(-CLIP_LINES).join('\n');

  let content = shown;
  if (lang && hljs.getLanguage(lang)) {
    try { content = html`<code dangerouslySetInnerHTML=${{ __html: hljs.highlight(shown, { language: lang, ignoreIllegals: true }).value }}></code>`; } catch { /* plain */ }
  }
  useFollow(box, { threshold: 24, enabled: mode === 'tail' });

  return html`<div class="group/clip relative">
    <${CopyIcon} text=${text} />
    <pre ref=${box}
      class=${cx('m-0 font-mono text-[12.5px] leading-[1.6] whitespace-pre-wrap break-words', full && long && 'max-h-[60vh] overflow-y-auto')}>${content}</pre>
    ${long && !full && html`<div aria-hidden="true" class=${cx('pointer-events-none absolute inset-x-0 h-20 from-raised to-transparent',
      mode === 'head' ? 'bottom-0 bg-linear-to-t' : 'top-0 bg-linear-to-b')}></div>`}
    ${long && html`<div class=${cx('flex justify-center', full ? 'pt-2' : mode === 'head' ? 'absolute inset-x-0 -bottom-1' : 'absolute inset-x-0 -top-1')}>
      <button type="button" onClick=${() => setFull(!full)} aria-expanded=${full}
        title=${full ? 'Collapse' : `Show all ${lines.length} lines`} aria-label=${full ? 'Collapse' : `Show all ${lines.length} lines`}
        class="size-7 inline-flex items-center justify-center rounded-full border border-line bg-hover text-fg hover:brightness-125 shadow-pop">
        <${Icon} name=${(mode === 'head') !== full ? 'chevronDown' : 'chevronUp'} size=${14} />
      </button>
    </div>`}
    ${live && !full && mode === 'tail' && long && html`<span class="sr-only">Showing the latest output</span>`}
  </div>`;
}

export function ToolRow({ room, item, cwd }) {
  const [open, setOpen] = useOpen(item.id);
  const running = item.status === 'requested' || item.status === 'streaming';
  // A room tool names the room from its own result; everything else is the
  // summary of its arguments (lib/turn.js). The result is read without the
  // runner's status line when the output carries one — a row's own room prop is
  // `room`, and this is what the call named.
  const answered = closeStatus(item.output);
  const subject = toolRoom(item.name, item.input, answered ? answered.body : item.output || '', knownRoom);
  const { verb, detail } = subject || toolSummary(item.name, item.input);
  const shell = SHELL_TOOLS.has(item.name);
  const args = parseArgs(item.input);
  // How this call ended, in one value (lib/turn.js): the row's word and mark,
  // its footer, and the group's count all read this, so they cannot disagree.
  const ending = toolEnding(item);
  const closed = closeStatus(item.output) || { body: item.output || '', close: null };
  const body = closed.body;
  // A running call counts up (Elapsed re-reads it every second); a finished
  // one shows the duration the runner measured, once.
  const clock = toolClock(item);
  const row = useRef();
  const cardId = `${item.id}:card`;
  const tone = ending.tone === 'bad' ? 'text-bad' : ending.tone === 'warn' ? 'text-warn' : ending.tone === 'muted' ? 'text-muted' : '';

  let inputText = item.input || '';
  let inputLang = 'json';
  let inputLabel = 'Input';
  if (shell && args?.command) {
    inputText = args.command + (args.stdin ? `\n# stdin:\n${args.stdin}` : '');
    inputLang = 'bash';
    inputLabel = cwd ? html`Command in <span class="font-mono">${cwd}</span>` : 'Command';
  } else if (args) {
    inputText = JSON.stringify(args, null, 2);
  }

  // The footer says the same ending in a sentence; the exit code itself lives
  // on the row (decided), so it is not repeated here.
  const footer = (() => {
    switch (ending.kind) {
      case 'running':
        return html`<${Spinner} size=${12} class="text-muted" /><span class="text-muted">Running</span>`;
      case 'exit':
      case 'failed':
        return html`<${Icon} name="xCircle" size=${14} class="text-bad" /><span class="text-bad">Failed</span>`;
      case 'signal':
        return html`<${Icon} name="xCircle" size=${14} class="text-bad" /><span class="text-bad">Stopped by signal: <span class="font-mono">${ending.signal}</span></span>`;
      case 'timeout':
        return html`<${Icon} name="alert" size=${14} class="text-warn" /><span class="text-warn">Timed out at the <span class="font-mono">${ending.cap}</span> cap</span>`;
      case 'stopped':
        if (ending.by === 'agent') return html`<${Icon} name="stop" size=${13} class="text-muted" /><span class="text-muted">Stopped by the agent</span>`;
        if (ending.by === 'server') return html`<${Icon} name="refresh" size=${13} class="text-muted" /><span class="text-muted">NusaShell restarted</span>`;
        return html`<${Icon} name="stop" size=${13} class="text-muted" /><span class="text-muted">Stopped by you</span>`;
      case 'canceled':
        return html`<${Icon} name="stop" size=${13} class="text-muted" /><span class="text-muted">Canceled — the client went away</span>`;
      default:
        return null;
    }
  })();

  return html`<div class="tool-row">
    <button type="button" ref=${row} onClick=${() => setOpen(!open)} aria-expanded=${open} aria-controls=${cardId}
      class="group w-full flex items-center gap-2 py-1 text-left text-[13px] text-muted hover:text-fg min-w-0">
      ${ending.mark !== 'none' && html`<span class=${cx('orb', ending.mark)} aria-hidden="true"></span>`}
      <span class="min-w-0 flex items-baseline gap-1 overflow-hidden">
        <span class=${cx('shrink-0', running && 'shimmer')}>${verb}</span>
        ${!subject && html`<span class="min-w-0 truncate text-muted">${detail}</span>`}
        ${subject?.detail && html`<span class="min-w-0 truncate text-muted">${subject.detail}</span>`}
        ${subject && subject.names.slice(0, ROOM_NAMES_SHOWN).map((n) => html`<span class="min-w-0 flex items-baseline gap-1">
          ${n.label && html`<span class="shrink-0 whitespace-nowrap text-accent">@${n.label}</span>`}
          ${n.title && html`<span class="min-w-0 truncate">“${n.title}”</span>`}
          ${n.short && html`<span class="shrink-0 font-mono text-muted">${n.short}</span>`}
        </span>`)}
        ${subject && subject.names.length > ROOM_NAMES_SHOWN && html`<span class="shrink-0 text-faint">+${subject.names.length - ROOM_NAMES_SHOWN} more</span>`}
      </span>
      <span class="tool-tail ml-auto shrink-0 flex items-center gap-1.5">
        ${ending.word && html`<span class=${cx('text-[12.5px] font-medium', tone)}> ${ending.word}</span>`}
        ${clock && html`<span class="text-muted tabular-nums">${running && item.at
          ? html` · <${Elapsed} since=${item.at} />`
          : ` · ${clock}`}</span>`}
      </span>
      <${Icon} name=${open ? 'chevronDown' : 'chevronRight'} size=${14} class="shrink-0 text-faint" />
    </button>
    ${open && html`<div id=${cardId} class="mt-1 mb-2 rounded-xl border border-line bg-raised overflow-hidden"
      onKeyDown=${(e) => { if (e.key === 'Escape') { e.stopPropagation(); setOpen(false); row.current?.focus(); } }}>
      <section class="px-3 pt-2 pb-3">
        <div class="text-xs text-faint mb-1.5 truncate">${inputLabel}${!shell && html` · <span class="font-mono">${item.name}</span>`}</div>
        <${Clip} id=${item.id + ':in'} text=${inputText || '(no arguments)'} mode="head" lang=${inputLang} />
      </section>
      <section class="px-3 pt-2 pb-3 border-t border-line">
        <div class="text-xs text-faint mb-1.5">Output</div>
        ${body
          ? html`<${Clip} id=${item.id + ':out'} text=${body} mode="tail" live=${running} />`
          : html`<div class="text-[12.5px] text-faint">${running ? 'Waiting for output…' : item.status === 'pending' ? 'No result recorded.' : '(no output)'}</div>`}
      </section>
      ${item.attachments?.length > 0 && html`<section class="px-3 pt-2 pb-3 border-t border-line">
        <div class="text-xs text-faint mb-1.5">${item.attachments.length === 1 ? 'Shown to the model' : `Shown to the model · ${item.attachments.length}`}</div>
        <${ToolMedia} room=${room} list=${item.attachments} />
      </section>`}
      ${footer && html`<footer class="card-foot flex items-center gap-2 px-3 py-2 border-t border-line text-xs">${footer}</footer>`}
    </div>`}
  </div>`;
}

// SubagentCard is the parent run_subagent call. Expand and collapse are its
// only controls. The status is the keys, and the parent output stays output.
export function SubagentCard({ item }) {
  const view = subagentView(item);
  const [open, setOpen] = useOpen(item.id);
  const panelId = `${item.id}:sub`;
  return html`<details class="subagent-card rounded-xl border border-line bg-raised my-1" open=${open}
    onToggle=${(e) => setOpen(e.currentTarget.open)}>
    <summary class="cursor-pointer px-3 py-2">
      <span class="flex items-start gap-2 min-w-0">
        <${Icon} name=${open ? 'chevronDown' : 'chevronRight'} size=${14} class="shrink-0 text-faint mt-0.5" aria-hidden="true" />
        <span class="min-w-0 flex-1">
          ${view.title && html`<span class="block font-medium text-[13px] break-words">${view.title}</span>`}
          ${view.task && html`<span class=${cx('block text-[13px] text-muted', open ? 'break-words whitespace-pre-wrap' : 'truncate')}>${view.task}</span>`}
          ${view.badges.length > 0 && html`<span class="mt-1 flex flex-wrap gap-1">
            ${view.badges.map((b) => html`<span key=${b} class="inline-flex items-center rounded-md border border-line px-1.5 text-[11px] text-muted">${b}</span>`)}
          </span>`}
          <span class="mt-1 block text-[13px]">${view.status}</span>
          ${view.summary && html`<span class="mt-1 block text-[13px] text-muted break-words">${view.summary}</span>`}
          ${view.note && html`<span class="mt-1 block text-[13px] text-muted break-words">${view.note}</span>`}
        </span>
      </span>
    </summary>
    <div id=${panelId} class="border-t border-line px-3 py-2">
      ${view.members.length > 0 && html`<ul class="m-0 p-0 list-none flex flex-col gap-1">
        ${view.members.map((parts, i) => html`<li key=${i} class="truncate text-[13px] text-muted">${parts.join(' · ')}</li>`)}
      </ul>`}
      ${view.output && html`<section class=${view.members.length ? 'mt-2 pt-2 border-t border-line' : ''}>
        <div class="text-xs text-faint mb-1.5">Output</div>
        <pre class="m-0 font-mono text-[12.5px] leading-[1.6] whitespace-pre-wrap break-words max-h-[40vh] overflow-y-auto">${view.output}</pre>
      </section>`}
    </div>
  </div>`;
}

// DelegatedRow is a member whose card never appeared. It stays one flat line.
export function DelegatedRow({ item }) {
  return html`<div class="flex items-baseline gap-2 py-1 text-[13px] text-muted min-w-0">
    <span class="shrink-0">Delegated</span>
    ${item.name && html`<span class="min-w-0 truncate">${item.name}</span>`}
    ${item.title && html`<span class="min-w-0 truncate">${item.title}</span>`}
  </div>`;
}
