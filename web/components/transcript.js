// Transcript: persisted history and the live turn rendered through one
// item model (lib/turn.js). Prose renders as Markdown; reasoning and tool
// calls fold into collapsible "work" groups between prose.
import { html, useEffect, useLayoutEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { renderInto, renderUserInto, disposeTree, handleCopyClick } from '../lib/markdown.js';
import { historyItems, liveHistory, liveSegments, joinText, group, foldGhosts, placeSubagents, applyLiveEnd, toolEnding, userLayout, relayName, splitReplyQuote, turnEnds, turnSeconds } from '../lib/turn.js';
import { queuedFor, watchQueued } from '../lib/queued.js';
import { ToolRow, SubagentCard, DelegatedRow } from './toolcard.js';
import { Attachments, ToolMedia } from './media.js';
import { ElicitationCard } from './elicitation.js';
import { referencesFromStored } from '../lib/references.js';
import { editMessage, isBusy, retry, selection, loadEarlier, loadHistory, answerApproval, annotationLabel, navigate, waitingPrompt, roomLine, liveStatus, honestyCard, workingBlind } from '../lib/chat.js';
import { emit, state } from '../lib/store.js';
import { openPreview } from './preview.js';
import { fileURL } from '../lib/api.js';
import { Icon } from './icons.js';
import { cx, Button, ErrorBlock, Marker } from './ui.js';

// Expansion state survives re-renders and remounts, keyed by item id.
const opened = new Map();
function useOpen(id, initial = false) {
  const [open, set] = useState(opened.has(id) ? opened.get(id) : initial);
  return [open, (v) => { opened.set(id, v); set(v); }];
}

// openLinkedFile previews a file a reply links, resolved in the thread's
// folder. A modified click keeps the browser's own handling.
function openLinkedFile(ev) {
  const a = ev.target.closest?.('a[data-file]');
  if (!a || ev.button !== 0 || ev.ctrlKey || ev.metaKey || ev.shiftKey || ev.altKey) return false;
  ev.preventDefault();
  const path = a.dataset.file;
  const line = Number(/:(\d+)(?::\d+)?$/.exec(path)?.[1] || 0);
  const id = state.route.view === 'chat' ? state.route.id : '';
  const name = path.replace(/:\d+(?::\d+)?$/, '').split('/').pop();
  openPreview(id ? fileURL(id, path) : a.href, name, line);
  return true;
}

// openThreadLink follows a link to another thread (/#/c/…, as a side
// chat's draft writes it) inside the app instead of a new tab.
function openThreadLink(ev) {
  const a = ev.target.closest?.('a[href^="/#/c/"]');
  if (!a || ev.button !== 0 || ev.ctrlKey || ev.metaKey || ev.shiftKey || ev.altKey) return false;
  ev.preventDefault();
  navigate(a.getAttribute('href').slice(1));
  return true;
}
// openEmbed opens a picture, video, or sound a message shows inline in
// the preview overlay, where video and audio play.
function openEmbed(ev) {
  const b = ev.target.closest?.('[data-media-url]');
  if (!b) return false;
  openPreview(b.dataset.mediaUrl, b.dataset.mediaName);
  return true;
}
const onMarkdownClick = (e) => { if (!openEmbed(e) && !openThreadLink(e) && !openLinkedFile(e)) handleCopyClick(e); };

// quote marks a reply's prose as text the selection bar may quote. room
// is the thread whose workspace the message's local paths resolve in.
export function Markdown({ text, room, class: cls = '', quote = false }) {
  const ref = useRef();
  const conversationId = room?.id || '';
  useLayoutEffect(() => { renderInto(ref.current, text, { conversationId }); }, [text, conversationId]);
  useEffect(() => () => ref.current && disposeTree(ref.current), []);
  return html`<div ref=${ref} class=${cx('md', cls)} data-quote=${quote ? '' : undefined} onClick=${onMarkdownClick}></div>`;
}

// UserMarkdown renders owner-typed text: the usermark profile keeps
// single newlines and typed footnote/mermaid syntax literal while the
// reference selections highlight through the parse. Raw text stays the
// source of truth — copy, edit, and send never read this DOM.
function UserMarkdown({ text, refs, room }) {
  const ref = useRef();
  const conversationId = room?.id || '';
  const key = (refs || []).map((r) => r.start + ',' + r.end + ',' + r.kind + ',' + r.id).join(';');
  useLayoutEffect(() => { renderUserInto(ref.current, text, refs, { conversationId }); }, [text, key, conversationId]);
  useEffect(() => () => ref.current && disposeTree(ref.current), []);
  return html`<div ref=${ref} class="md" data-quote="" onClick=${onMarkdownClick}></div>`;
}

// RelayHead shows who sent a prompt that came from another thread, drawn
// from the provenance the server stored — the body it heads is only what
// the sender wrote — and opens that thread.
function RelayHead({ relay }) {
  const name = relayName(relay);
  const inner = html`
    <span class="flex items-center justify-center size-8 rounded-xl bg-accent/15 text-accent"><${Icon} name="bot" size=${16} /></span>
    <span class="min-w-0 flex-1 leading-tight">
      <span class="block text-[10.5px] font-semibold uppercase tracking-[0.08em] text-accent">Relayed</span>
      <span class="flex items-baseline gap-1.5 min-w-0 text-[13px] mt-0.5">
        <span class="font-medium text-fg">${name}</span>
        ${relay.label && relay.title && html`<span class="text-muted truncate">${relay.title}</span>`}
      </span>
    </span>
    ${relay.id && html`<${Icon} name="link" size=${14} class="text-faint group-hover/relay:text-accent transition-colors" />`}`;
  const cls = 'group/relay flex items-center gap-2.5 px-3 py-2 bg-accent/[0.06] border-b border-accent/20';
  // data-relay-head marks the block's own head: what the message carried is
  // drawn under it, and the layout check reads the DOM order from here.
  return relay.id
    ? html`<a data-relay-head href=${'#/c/' + encodeURIComponent(relay.id)} title="Open the thread that sent this" class=${cx(cls, 'hover:bg-accent/10 transition-colors')}>${inner}</a>`
    : html`<div data-relay-head class=${cls}>${inner}</div>`;
}

function CopyButton({ text, label = 'Copy' }) {
  const [done, setDone] = useState(false);
  return html`<button type="button" title=${label} aria-label=${label}
    onClick=${() => navigator.clipboard?.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 1200); })}
    class="inline-flex items-center justify-center size-7 rounded-md text-faint hover:text-fg hover:bg-hover">
    <${Icon} name=${done ? 'check' : 'copy'} size=${14} />
  </button>`;
}

// Delivery is what a reply took to a chat: the files sent with it and
// the chat's receipt — one tick delivered, two ticks read.
const RECEIPT = { delivered: ['check', 'Delivered', 'text-faint'], read: ['checks', 'Read', 'text-accent'] };
function Delivery({ room, item }) {
  const mark = RECEIPT[item.receipt];
  return html`<div class="flex flex-col items-start gap-1 -mt-2">
    ${item.files.length > 0 && html`<span class="text-[11px] text-faint">Sent to the chat</span>`}
    <${Attachments} room=${room} list=${item.files} start />
    ${mark && html`<span class="inline-flex items-center gap-1 text-[11px] text-faint" title=${mark[1] + ' in the chat'}>
      <${Icon} name=${mark[0]} size=${14} class=${mark[2]} />${mark[1]}
    </span>`}
  </div>`;
}

// Annotations lists the live-page marks a message carried: the badge
// number on the screenshot, what was marked, and the owner's comment.
function Annotations({ list }) {
  if (!list?.length) return null;
  return html`<div class="flex flex-col items-end gap-1 max-w-[85%]">
    ${list.map((a, i) => html`<div key=${i} class="inline-flex items-start gap-2 max-w-full px-2.5 py-1.5 rounded-lg border border-line bg-panel text-[13px]">
      <${Marker} n=${a.marker} class="mt-px" />
      <span class="min-w-0 flex flex-col leading-snug">
        <span class="truncate font-mono text-[12px] text-muted" title=${a.descriptor?.selector || a.descriptor?.pageUrl || ''}>${a.descriptor ? annotationLabel(a.descriptor) : a.label}</span>
        ${a.comment && html`<span class="break-words">${a.comment}</span>`}
      </span>
    </div>`)}
  </div>`;
}

function UserMessage({ room, msg, text, refs, files, annotations, recording, pending, steer, relayed }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(text);
  // Where this message's uploads belong, and whether it is the owner's to
  // rewrite: the server marks a prompt from another thread (userType 'relay'),
  // and lib/turn.js says what that means for the layout.
  const layout = userLayout(relayed);
  const own = layout.extras === 'own';
  // A steered input is not a prompt the owner can rewrite either: it was
  // folded into the turn that already consumed it, and the bubble drawn from
  // the journal never offers an edit.
  const canEdit = msg && !steer && own && !isBusy(room);
  const agentRoom = selection(room).runtime === 'agent';
  if (editing) {
    const save = () => { if (editMessage(room, msg.id, draft)) setEditing(false); };
    return html`<div class="flex justify-end">
      <div class="w-full max-w-[85%] rounded-2xl border border-line bg-raised p-2">
        <textarea class="w-full min-h-20 resize-y bg-transparent outline-none px-2 py-1.5 text-[14px]" value=${draft} autofocus
          onInput=${(e) => setDraft(e.target.value)}
          onKeyDown=${(e) => { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) save(); if (e.key === 'Escape') setEditing(false); }}></textarea>
        <div class="flex items-center justify-between gap-2 px-1">
          <span class="text-xs text-faint">${agentRoom
            ? 'Later messages are replaced and the agent starts a new session with a recap. File changes it made after this message stay.'
            : 'Regenerates from here — later messages are replaced.'}</span>
          <div class="flex gap-2">
            <${Button} kind="ghost" onClick=${() => setEditing(false)}>Cancel</${Button}>
            <${Button} kind="primary" onClick=${save} disabled=${!draft.trim()}>Send</${Button}>
          </div>
        </div>
      </div>
    </div>`;
  }
  // A prompt from another thread leads with who sent it — the provenance
  // the server stored, not anything read back out of the text — and a chat
  // reply with what it answers; show the head as a card or a quote and the
  // rest as the message, its selections moved to match. A steer is owner
  // input too — the same text the composer sends mid-turn — so it renders
  // through this same bubble, marked as steered.
  const relay = layout.relayed ? msg?.relay || null : null;
  const reply = relay ? null : splitReplyQuote(text);
  const cut = reply?.head || 0;
  const shown = text.slice(cut);
  const stored = referencesFromStored(text, refs || msg?.references);
  const shownRefs = cut ? stored.filter((r) => r.start >= cut).map((r) => ({ ...r, start: r.start - cut, end: r.end - cut })) : stored;
  const rec = recording || msg?.recording;
  // What the message carried, and the note that it was recorded: the owner's
  // own prompt shows them above its bubble; a prompt from another thread shows
  // them inside that block, under its head, where they read as its own.
  const attList = files ? files.filter((f) => !f.pickId) : msg?.attachments;
  const annList = annotations || (files ? files.filter((f) => f.pickId) : msg?.annotations);
  const hasExtras = !!(attList?.length || annList?.length || rec);
  const extras = html`<${Attachments} room=${room} list=${attList} start=${!own} />
    <${Annotations} list=${annList} />
    ${rec && html`<span class="inline-flex items-center gap-1.5 h-7 pl-2 pr-2.5 rounded-lg border border-line bg-panel text-[11px] text-muted" title=${'Browser recording ' + rec.id}>
      <${Icon} name="video" size=${12} /> Browser recording · ${rec.status || 'unknown'}
    </span>`}`;
  return html`<div class="group flex flex-col items-end gap-1">
    ${steer && html`<span class="text-[11px] text-faint">steered</span>`}
    ${own && extras}
    ${text && relay && html`<div class=${cx('max-w-[85%] min-w-0 sm:min-w-72 rounded-2xl bg-raised border border-accent/25 overflow-hidden break-words', pending && 'opacity-70')}>
      <${RelayHead} relay=${relay} />
      ${!own && hasExtras && html`<div class="px-3.5 pt-2 pb-1 flex flex-col items-start gap-1">${extras}</div>`}
      ${shown && html`<div class="px-3.5 py-2.5"><${Markdown} text=${shown} room=${room} /></div>`}
    </div>`}
    ${text && !relay && html`<div class=${cx('max-w-[85%] rounded-2xl bg-raised border border-line px-3.5 py-2 break-words', steer && 'border-dashed', pending && 'opacity-70')}>
      ${reply && html`<div class=${cx('border-l-2 border-accent/60 pl-2.5 text-[13px] leading-snug', shown && 'mb-2')}>
        <div class="text-[11px] text-faint">${reply.sender ? reply.sender + ' · ' : ''}In reply to</div>
        <div class="text-muted line-clamp-3 whitespace-pre-wrap" title=${reply.quote}>${reply.quote}</div>
      </div>`}
      ${shown && html`<${UserMarkdown} text=${shown} refs=${shownRefs} room=${room} />`}
    </div>`}
    <div class="flex gap-0.5 opacity-0 group-hover:opacity-100 focus-within:opacity-100 transition-opacity">
      <${CopyButton} text=${text} label="Copy message" />
      ${canEdit && html`<button type="button" title="Edit and regenerate" aria-label="Edit and regenerate" onClick=${() => { setDraft(text); setEditing(true); }}
        class="inline-flex items-center justify-center size-7 rounded-md text-faint hover:text-fg hover:bg-hover"><${Icon} name="pencil" size=${14} /></button>`}
    </div>
  </div>`;
}

function Thoughts({ room, item, live }) {
  const [open, setOpen] = useOpen(item.id);
  return html`<div>
    <button type="button" onClick=${() => setOpen(!open)} aria-expanded=${open}
      class="flex items-center gap-1.5 py-1 text-[13px] text-muted hover:text-fg">
      <${Icon} name=${open ? 'chevronDown' : 'chevronRight'} size=${14} />
      <span class=${live ? 'shimmer' : ''}>${live ? 'Thinking' : 'Thoughts'}</span>
    </button>
    ${open && html`<div class="ml-[7px] pl-4 border-l border-line text-muted text-[13px] max-h-96 overflow-y-auto">
      <${Markdown} text=${item.text} room=${room} />
    </div>`}
  </div>`;
}

function Activity({ room, item, live, cwd }) {
  const [open, setOpen] = useOpen(item.id);
  const tools = item.items.filter((x) => x.kind === 'tool');
  if (!tools.length) return item.items.map((x) => html`<${Thoughts} key=${x.id} room=${room} item=${x} live=${live} />`);
  // The same value the rows read decides the count: a failure is an exit, a
  // signal, or a failed call; a timeout is its own number. A stop and a cancel
  // are deliberate and are not counted against the group.
  const endings = tools.map(toolEnding);
  const failed = endings.filter((e) => e.kind === 'exit' || e.kind === 'signal' || e.kind === 'failed').length;
  const timedOut = endings.filter((e) => e.kind === 'timeout').length;
  const label = live ? `Working · ${tools.length} ${tools.length === 1 ? 'step' : 'steps'}` : `Used ${tools.length} ${tools.length === 1 ? 'tool' : 'tools'}`;
  const tail = !open && live ? item.items.slice(-1) : [];
  // What the tools showed the model stays in view while the run is folded.
  const media = open ? [] : tools.flatMap((t) => t.attachments || []);
  return html`<div>
    <button type="button" onClick=${() => setOpen(!open)} aria-expanded=${open}
      class="flex items-center gap-1.5 py-1 text-[13px] text-muted hover:text-fg">
      <${Icon} name=${open ? 'chevronDown' : 'chevronRight'} size=${14} />
      <span class=${live ? 'shimmer' : ''}>${label}</span>
      ${failed > 0
        ? html`<span class="text-bad text-xs"> · ${failed} failed</span>`
        : timedOut > 0 && html`<span class="text-warn text-xs"> · ${timedOut} timed out</span>`}
    </button>
    ${media.length > 0 && html`<div class="ml-[21px] mb-1"><${ToolMedia} room=${room} list=${media} size="strip" /></div>`}
    ${(open || tail.length > 0) && html`<div class="ml-[7px] pl-4 border-l border-line flex flex-col">
      ${(open ? item.items : tail).map((x) => x.kind === 'tool'
        ? html`<${ToolRow} key=${x.id} room=${room} item=${x} cwd=${x.cwd || cwd} />`
        : html`<${Thoughts} key=${x.id} room=${room} item=${x} live=${live && x === item.items[item.items.length - 1]} />`)}
    </div>`}
  </div>`;
}

// ApprovalCard is a permission question from the running agent. Allow
// answers lead; the card keeps the decision once the turn records it.
function ApprovalCard({ room, seg }) {
  const r = seg.request;
  const [showInput, setShowInput] = useOpen(seg.id + ':input');
  const chosen = (r.options || []).find((o) => o.id === seg.optionId);
  const allows = (r.options || []).filter((o) => o.choice.startsWith('allow'));
  const rejects = (r.options || []).filter((o) => !o.choice.startsWith('allow'));
  const pending = seg.status === 'pending';
  return html`<div role=${pending ? 'alert' : null}
    class=${cx('rounded-xl border px-4 py-3 text-[13px]', pending ? 'border-warn/40 bg-warn/10' : 'border-line bg-panel')}>
    <div class="flex items-start gap-2.5">
      <${Icon} name="shield" class=${pending ? 'text-warn mt-0.5' : 'text-faint mt-0.5'} />
      <div class="flex-1 min-w-0">
        <div class="font-medium break-words">${pending ? 'The agent asks to run' : 'Permission'}: ${r.title || r.action}</div>
        <div class="text-faint text-xs mt-0.5">${r.action}${r.input ? html` · <button type="button" class="underline" onClick=${() => setShowInput(!showInput)}>${showInput ? 'hide' : 'show'} input</button>` : ''}</div>
        ${showInput && r.input && html`<pre class="rawtext mt-2 max-h-48 overflow-auto">${r.input}</pre>`}
        ${!pending && html`<div class="text-muted mt-1">
          ${seg.status === 'answered' ? html`Answered: <span class="text-fg">${chosen?.name || seg.optionId}</span>` : 'Withdrawn — the turn ended before an answer.'}
        </div>`}
      </div>
    </div>
    ${pending && html`<div class="flex flex-wrap justify-end gap-2 mt-2.5">
      ${rejects.map((o) => html`<${Button} key=${o.id} kind="ghost" disabled=${!!seg.answering} busy=${seg.answering === o.id}
        onClick=${() => answerApproval(room, seg, o.id)}>${o.name}</${Button}>`)}
      ${allows.map((o) => html`<${Button} key=${o.id} kind=${o.choice === 'allow_once' ? 'primary' : 'default'} disabled=${!!seg.answering} busy=${seg.answering === o.id}
        onClick=${() => answerApproval(room, seg, o.id)}>${o.name}</${Button}>`)}
    </div>`}
  </div>`;
}

// PlanCard shows an agent's plan: each entry with its status.
const PLAN_MARK = { completed: ['check', 'text-ok'], in_progress: ['play', 'text-accent'], pending: ['clock', 'text-faint'] };
function PlanCard({ entries }) {
  const list = Array.isArray(entries) ? entries : [];
  if (!list.length) return null;
  const done = list.filter((e) => e.status === 'completed').length;
  return html`<div class="rounded-xl border border-line bg-panel px-4 py-3 text-[13px]">
    <div class="flex items-center gap-2 text-xs text-muted mb-2"><span class="font-medium text-fg">Plan</span><span>${done} of ${list.length} done</span></div>
    <ol class="flex flex-col gap-1.5">
      ${list.map((e, i) => {
        const [icon, tone] = PLAN_MARK[e.status] || PLAN_MARK.pending;
        return html`<li key=${i} class="flex items-start gap-2">
          <${Icon} name=${icon} size=${14} class=${cx('mt-0.5 shrink-0', tone)} />
          <span class=${cx('min-w-0 break-words', e.status === 'completed' && 'text-muted line-through')}>${e.content || e.title}</span>
          ${e.priority === 'high' && html`<span class="ml-auto shrink-0 text-[11px] text-warn">high</span>`}
        </li>`;
      })}
    </ol>
  </div>`;
}

function Status({ room }) {
  const [, tick] = useState(0);
  useEffect(() => { const t = setInterval(() => tick((n) => n + 1), 1000); return () => clearInterval(t); }, []);
  const view = liveStatus(room);
  if (!view) return null;
  if (!view.provider) {
    return html`<div class="flex items-center gap-2 text-[13px] text-muted py-1" role="status">
      <span class="orb live"></span>
      <span class="shimmer">${view.text}</span>
      ${view.note && html`<span class="text-faint text-xs">${view.note}</span>`}
      ${view.counter > 0 && html`<span class="text-faint text-xs">${view.counter}s</span>`}
      ${view.slow && html`<span class="text-faint text-xs">Stop ends this turn.</span>`}
    </div>`;
  }
  const live = room.live;
  let text = 'Working';
  if (room.pendingOpen) text = 'Sending';
  else if (room.settling) text = 'Finishing';
  else if (live?.canceling) text = 'Stopping';
  else if (live?.phase?.phase === 'retry_wait') {
    const left = Math.max(0, Math.ceil(((live.phase.at + (live.phase.retryAfterMs || 0)) - Date.now()) / 1000));
    text = `Provider busy — retrying in ${left}s`;
  } else if (live?.phase?.phase === 'compacting') text = 'Compacting history to fit the context window';
  else if (live?.phase?.phase === 'reasoning') text = 'Thinking';
  const secs = live ? turnSeconds(live) : 0;
  return html`<div class="flex items-center gap-2 text-[13px] text-muted py-1" role="status">
    <span class="orb live" style=${live?.phase?.phase === 'retry_wait' ? 'background:var(--bad)' : ''}></span>
    <span class="shimmer">${text}…</span>
    ${secs === null ? html`<span class="text-faint text-xs">—</span>` : secs > 2 && html`<span class="text-faint text-xs">${secs >= 60 ? Math.floor(secs / 60) + 'm ' : ''}${secs % 60}s</span>`}
  </div>`;
}

function HonestyCard({ room }) {
  const [, tick] = useState(0);
  useEffect(() => { const t = setInterval(() => tick((n) => n + 1), 1000); return () => clearInterval(t); }, []);
  const card = honestyCard(room);
  if (!card) return null;
  const dismiss = () => { room.hideHonesty = true; room.failed = null; emit(); };
  return html`<div role="alert" class="rounded-xl border border-bad/30 bg-bad/10 px-4 py-3 text-[13px]">
    <div class="font-medium break-words">${card.title}</div>
    ${card.body && html`<p class="text-muted mt-1 break-words">${card.body}</p>`}
    ${card.message && html`<p class="mt-1 break-words">${card.message}</p>`}
    ${card.retryOff && html`<p class="text-muted mt-1">${card.retryOff}</p>`}
    ${card.queue > 0 && html`<p class="text-faint mt-1">${card.queue} queued messages stay queued. None is reported sent.</p>`}
    <div class="flex justify-end gap-2 mt-2">
      <${Button} kind="ghost" onClick=${dismiss}>Dismiss</${Button}>
      ${card.retry && html`<${Button} onClick=${() => retry(room)}><${Icon} name="refresh" size=${14} /> Retry</${Button}>`}
    </div>
  </div>`;
}

function FailedCard({ room }) {
  const f = room.failed;
  // What a retry would send again: the prompt still waiting for an answer.
  const prompt = waitingPrompt(room);
  // What the provider said is the explanation; the status and machine code
  // are the facts underneath it, never a substitute for it.
  return html`<div role="alert" class="rounded-xl border border-bad/30 bg-bad/10 px-4 py-3 text-[13px]">
    <div class="flex items-start gap-2.5">
      <${Icon} name="alert" class="text-bad mt-0.5" />
      <div class="flex-1 min-w-0">
        <div class="font-medium">Turn failed</div>
        <div class="text-muted mt-0.5 break-words">${f.message || 'The frame carried no message — only the status below.'}</div>
        ${(f.status || f.code) && html`<div class="text-faint text-[11px] mt-1 font-mono">${[f.status, f.code].filter(Boolean).join(' · ')}</div>`}
        ${room.queue.length > 0 && html`<div class="text-faint mt-1">${room.queue.length} queued ${room.queue.length === 1 ? 'message is' : 'messages are'} on hold.</div>`}
      </div>
    </div>
    <div class="flex justify-end gap-2 mt-2">
      <${Button} kind="ghost" onClick=${() => { room.failed = null; emit(); }}>Dismiss</${Button}>
      ${prompt && html`<${Button} onClick=${() => retry(room)}><${Icon} name="refresh" size=${14} /> Retry</${Button}>`}
    </div>
  </div>`;
}

// RoomLine is the one status line above the composer: what the transcript
// cannot show, said once — blind work (a turn is active but names none to
// watch, and this tab is not streaming it) and the worker's queue depth as a
// second clause of the same sentence, never a second row. The work clause
// leads, so at narrow widths truncation eats the queue tail and the icon and
// the work clause survive; the icon follows the leading clause (orb idle,
// the same token the sidebar and home rows use, else the queue icon). It is
// not a live region: a count that moves while the owner reads must not
// interrupt a screen reader — and the why lives in the title, not the text.
// It subscribes to the count itself (lib/queued.js) so a new number re-renders
// this line and nothing else.
export function RoomLine({ room }) {
  const [waiting, setWaiting] = useState(() => queuedFor(room?.id));
  useEffect(() => {
    setWaiting(queuedFor(room?.id));
    return watchQueued((id, count) => { if (id === room?.id) setWaiting(count); });
  }, [room?.id]);
  const line = roomLine(room, waiting);
  if (!line) return null;
  return html`<div class="flex items-center gap-1.5 px-1 py-1 text-[12px] text-muted whitespace-nowrap" title=${line.tip || undefined}>
    ${line.blind
      ? html`<span class="orb idle"></span>`
      : html`<${Icon} name="queue" size=${13} class="text-muted shrink-0" />`}
    <span class="truncate">${line.text}</span>
  </div>`;
}

// Stick to the bottom while the reader is there; keep the reading anchor
// when earlier messages are prepended.
export function Transcript({ room, scroller }) {
  const r = room;
  let hist = liveHistory(historyItems(r.messages), r.live);
  const lastTool = hist.findLast((x) => x.kind === 'tool');
  const cwd = r.sel.workspace || r.info?.workspace || lastTool?.cwd || '';
  // A steer already in history is drawn once, from the record — and where its
  // live segment sits, so the text that streamed before it stays above it. The
  // row is then not drawn again in its history place.
  const liveItems = liveSegments(r.live, hist);
  const moved = new Set(liveItems.filter((x) => x.kind === 'user').map((x) => x.id));
  // Neighbouring text is one block: a part boundary is where the store seals a
  // step, not where the answer breaks. Joining before group() also keeps the
  // copy button's text identical to what was written.
  // Empty failed rows from before members were recognized become one quiet
  // count per turn. Folding before group() keeps that line out of the cards.
  const liveEnd = (() => {
    const term = r.live?.terminal;
    if (!term) return {};
    return term.type === 'turn.completed'
      ? { ended: 'finished' }
      : { ended: 'stopped', ownerStop: !!r.live.canceling };
  })();
  const items = group(joinText(foldGhosts(placeSubagents(applyLiveEnd(hist, r.live), { blind: workingBlind(r) }))));
  const liveGroups = group(joinText(foldGhosts(placeSubagents(liveItems, liveEnd))));
  // A finished turn ends with a button that copies what it said, as the
  // Markdown it was written in; a running turn gets one once it ends.
  const histEnds = turnEnds(items);
  const liveEnds = r.live?.terminal ? turnEnds(liveGroups) : new Map();
  const withCopy = (ends) => (it, i, live) => {
    const node = renderItem(it, live);
    const md = ends.get(i);
    return md === undefined ? node : html`${node}<div key=${it.id + ':copy'} class="flex -mt-2 -ml-1.5"><${CopyButton} text=${md} label="Copy response" /></div>`;
  };

  // Older pages load as the reader nears the top. The distance from the
  // bottom is tracked on every scroll and re-applied after a prepend, so
  // the line being read stays put.
  const fromBottom = useRef(0);
  const sentinel = useRef();
  const first = r.messages[0]?.id;
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const track = () => { fromBottom.current = el.scrollHeight - el.scrollTop; };
    track();
    el.addEventListener('scroll', track, { passive: true });
    return () => el.removeEventListener('scroll', track);
  }, []);
  const prevFirst = useRef(first);
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && prevFirst.current && first !== prevFirst.current) el.scrollTop = el.scrollHeight - fromBottom.current;
    prevFirst.current = first;
  }, [first]);
  const canLoad = r.hist.more && !r.hist.earlierError && r.hist.status === 'ready';
  useEffect(() => {
    const el = sentinel.current;
    if (!el || !canLoad) return;
    // Re-observing after each page fires again if the sentinel is still in
    // view, so a short page keeps filling until the screen is covered.
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) loadEarlier(r);
    }, { root: scroller.current, rootMargin: '600px 0px 0px 0px' });
    io.observe(el);
    return () => io.disconnect();
  }, [canLoad, first, r.hist.loadingEarlier]);

  if (r.hist.status === 'loading') {
    return html`<div class="flex flex-col gap-5 pt-6" aria-busy="true" aria-label="Loading messages">
      <div class="skeleton h-9 w-1/2 self-end rounded-2xl"></div>
      <div class="flex flex-col gap-2"><div class="skeleton h-4 w-4/5"></div><div class="skeleton h-4 w-3/5"></div><div class="skeleton h-4 w-2/3"></div></div>
      <div class="skeleton h-9 w-1/3 self-end rounded-2xl"></div>
      <div class="flex flex-col gap-2"><div class="skeleton h-4 w-3/4"></div><div class="skeleton h-4 w-1/2"></div></div>
    </div>`;
  }
  if (r.hist.status === 'error') {
    return html`<${ErrorBlock} class="mt-6" title="Could not load this thread" message=${r.hist.error} onRetry=${() => loadHistory(r)} />`;
  }

  const renderItem = (it, live) => {
    switch (it.kind) {
      // The record of a steer carries the inputId of the command that wrote
      // it, so the row that wins over the journal replay is drawn as what it
      // is — steered, dashed, no edit — with its annotations and uploads.
      case 'user': return html`<${UserMessage} key=${it.id} room=${r} msg=${it.msg} text=${it.msg.content} steer=${!!it.steerInput} relayed=${!!it.relayed} />`;
      // A steered input is owner text sent mid-turn — what the composer
      // wrote, including the head a relay from another thread adds — so it
      // renders as the same bubble a sent message does, marked steered.
      case 'steer': return html`<${UserMessage} key=${it.id} room=${r} text=${it.text} files=${it.attachments} annotations=${it.annotations} steer />`;
      case 'note': return html`<details key=${it.id} class="text-[13px] text-muted">
        <summary class="cursor-pointer py-1">Context note</summary>
        <pre class="rawtext mt-1">${it.msg.content}</pre>
      </details>`;
      // A compaction checkpoint marks where earlier history was sealed: a
      // thin rule naming it, not a bubble — the summary it kept stays one
      // click behind the line. The flanking rules give the label all the
      // room that is left, so at any width the word stays whole.
      case 'compaction': return html`<details key=${it.id} class="text-[13px] text-muted">
        <summary class="list-none cursor-pointer py-1" title="History compacted — read the summary it kept">
          <span role="separator" aria-label="History compacted" class="flex items-center gap-3">
            <span class="flex-1 border-t border-line"></span>
            <span class="shrink-0">Compacted</span>
            <span class="flex-1 border-t border-line"></span>
          </span>
        </summary>
        <pre class="rawtext mt-1">${it.msg.content}</pre>
      </details>`;
      case 'text': return html`<${Markdown} key=${it.id} text=${it.text} room=${r} quote />`;
      case 'activity': return html`<${Activity} key=${it.id} room=${r} item=${it} live=${live} cwd=${cwd} />`;
      case 'subagent': return html`<${SubagentCard} key=${it.id} item=${it} />`;
      case 'delegated': return html`<${DelegatedRow} key=${it.id} item=${it} />`;
      case 'interrupted': return html`<div key=${it.id} class="text-xs text-faint">— interrupted</div>`;
      case 'ghosts': return html`<p key=${it.id} class="text-xs text-faint">${it.text}</p>`;
      case 'resync': return html`<div key=${it.id} class="text-xs text-faint">${it.text}</div>`;
      case 'approval': return html`<${ApprovalCard} key=${it.id} room=${r} seg=${it} />`;
      case 'elicitation': return html`<${ElicitationCard} key=${it.id} room=${r} seg=${it} />`;
      case 'plan': return html`<${PlanCard} key=${it.id} entries=${it.entries} />`;
      case 'media': return html`<${Attachments} key=${it.id} room=${r} list=${it.attachments} start />`;
      case 'delivery': return html`<${Delivery} key=${it.id} room=${r} item=${it} />`;
      default: return null;
    }
  };

  const empty = !hist.length && !r.live && !r.pendingOpen;
  return html`<div class="flex flex-col gap-4 py-6">
    ${r.hist.more && html`<div ref=${sentinel} aria-busy=${r.hist.loadingEarlier ? 'true' : 'false'} aria-label="Earlier messages">
      ${r.hist.earlierError
        ? html`<div role="alert" class="flex items-center justify-center gap-3 py-3 text-[13px] text-muted">
            <span>Could not load earlier messages.</span>
            <${Button} kind="ghost" onClick=${() => loadEarlier(r)}>Retry</${Button}>
          </div>`
        : html`<div class="flex flex-col gap-4">
            <div class="flex flex-col gap-2"><div class="skeleton h-4 w-3/4"></div><div class="skeleton h-4 w-1/2"></div></div>
            <div class="skeleton h-9 w-2/5 self-end rounded-2xl"></div>
            <div class="flex flex-col gap-2"><div class="skeleton h-4 w-4/5"></div><div class="skeleton h-4 w-3/5"></div></div>
          </div>`}
    </div>`}
    ${empty && r.infoStatus === 'ready' && html`<p class="text-center text-faint text-[13px] py-10">This thread has no messages yet.</p>`}
    ${items.map((it, i) => (moved.has(it.id) ? null : withCopy(histEnds)(it, i, false)))}
    ${!!(r.pendingOpen?.text || r.pendingOpen?.files?.length || r.pendingOpen?.recording) && r.pendingOpen.kind === 'send' && html`<${UserMessage} room=${r} text=${r.pendingOpen.text} refs=${r.pendingOpen.references} files=${r.pendingOpen.files} recording=${r.pendingOpen.recording} pending />`}
    ${!!(r.live?.userText || r.live?.userFiles?.length || r.live?.userRecording) && html`<${UserMessage} room=${r} text=${r.live.userText} refs=${r.live.userRefs} files=${r.live.userFiles} recording=${r.live.userRecording} pending=${!r.live.terminal} />`}
    ${liveGroups.map((it, i) => withCopy(liveEnds)(it, i, !r.live.terminal && i === liveGroups.length - 1))}
    ${(r.live && !r.live.terminal) || r.pendingOpen || r.settling ? html`<${Status} room=${r} />` : null}
    ${typeof r.agentTime === 'number' && !r.live && r.info?.runtime === 'agent' && html`<p class="text-xs text-faint">Agent time ${r.agentTime}s</p>`}
    ${honestyCard(r) ? html`<${HonestyCard} room=${r} />` : r.failed && !r.failed.agentClass && !isBusy(r) && (waitingPrompt(r) || !r.messages.length) && html`<${FailedCard} room=${r} />`}
  </div>`;
}
