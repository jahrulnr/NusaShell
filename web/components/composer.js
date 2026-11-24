// Composer: per-room draft, Enter to send. While a send turn runs, Enter
// steers that exact turn and Ctrl/⌘+Enter queues for after it; all three
// carry the same draft — text, selections, and attachments. Anything
// else that keeps the room busy queues too. Stop is always its own
// control.
import { html, useEffect, useLayoutEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore, emit, toast } from '../lib/store.js';
import { submit, cancel, isBusy, recallSteer, removeQueued, restoreQueued, resumeQueue, attachFiles, removeFile, navigate, quotedDraft, removeQuote } from '../lib/chat.js';
import { api, errorText } from '../lib/api.js';
import { Icon } from './icons.js';
import { cx, Popover, Kbd, Spinner, Marker } from './ui.js';
import { ModelPicker, WorkspacePicker, ContextMeter } from './pickers.js';
import { AgentOptions } from './agentbar.js';
import { useReferenceChoices, ReferenceMenu } from './references.js';
import { referenceTrigger, reconcileReferences, liveReferences, referenceLabel } from '../lib/references.js';
import { useCommands, parseCommand, isNativeCommand, commandBusyLabel, firstEnabled, stepRow } from '../lib/commands.js';

const isMac = /Mac|iPhone|iPad/.test(navigator.platform);
const MOD = isMac ? '⌘' : 'Ctrl';

const fmtSize = (n) => (n >= 1 << 20 ? (n / (1 << 20)).toFixed(1) + ' MB' : n >= 1024 ? Math.round(n / 1024) + ' KB' : n + ' B');

// QuoteChip is one passage the draft answers: the quote in a line, whether
// a reply rides with it, and — opened — the whole quote and its reply to
// edit before sending.
function QuoteChip({ room, q }) {
  const [open, setOpen] = useState(false);
  const [, bump] = useState(0);
  return html`<div class=${cx('rounded-lg border border-line bg-panel text-[13px] min-w-0', open && 'pb-2')}>
    <div class="flex items-center gap-2 h-8 pl-2 pr-1 min-w-0">
      <button type="button" onClick=${() => setOpen(!open)} aria-expanded=${open} title=${open ? 'Fold the quote' : 'Show the quote and its reply'}
        class="flex-1 min-w-0 flex items-center gap-2 h-full text-left outline-none focus-visible:underline">
        <${Icon} name="reply" size=${14} class="text-faint shrink-0" />
        <span class="truncate text-muted">“${q.text.replace(/\s+/g, ' ')}”</span>
      </button>
      ${q.from && q.from.id !== room.sideOf?.id && html`<span class="shrink-0 text-[11px] text-faint truncate max-w-32" title=${'From ' + q.from.title}>from ${q.from.title}</span>`}
      ${q.reply && html`<span class="shrink-0 h-5 px-1.5 inline-flex items-center rounded-md bg-hover text-[11px] text-muted">With reply</span>`}
      <button type="button" aria-label="Remove this quote" title="Remove this quote" onClick=${() => removeQuote(room, q.key)}
        class="size-6 shrink-0 inline-flex items-center justify-center rounded text-faint hover:text-fg hover:bg-hover"><${Icon} name="x" size=${12} /></button>
    </div>
    ${open && html`<div class="px-2.5 flex flex-col gap-2">
      <blockquote class="quote-chip-text border-l-2 border-line pl-2.5 text-muted whitespace-pre-wrap max-h-40 overflow-y-auto">${q.text}</blockquote>
      <textarea rows=${1} value=${q.reply} placeholder="Reply to this passage…" aria-label="Reply to the quote"
        onInput=${(e) => { q.reply = e.target.value; e.target.style.height = 'auto'; e.target.style.height = e.target.scrollHeight + 'px'; bump((n) => n + 1); }}
        onBlur=${() => { q.reply = q.reply.trim(); emit(); }}
        class="w-full resize-none rounded-md border border-line bg-raised px-2 py-1.5 text-[13px] outline-none focus:border-accent/60 max-h-32"></textarea>
    </div>`}
  </div>`;
}

function Tray({ room }) {
  const r = room;
  if (!r.steers.length && !r.queue.length) return null;
  // A file-only input still reads as something: the names it carried.
  const inputLabel = (input) => input.text || (input.files || []).map((f) => f.name).join(', ');
  const stateLabel = { sending: 'Sending', pending: 'Waiting for next step', unknown: 'Delivery unknown — check the thread' };
  return html`<div class="mx-3 mb-0 rounded-t-xl border border-b-0 border-line bg-panel/80 px-3 py-2 flex flex-col gap-1.5 max-h-48 overflow-y-auto text-[13px]">
    ${r.steers.map((s) => html`<div key=${s.inputId} class="flex items-center gap-2 min-w-0">
      <${Icon} name="steer" size=${14} class="text-accent" />
      <span class="flex-1 truncate">${inputLabel(s)}</span>
      <span class=${cx('text-xs shrink-0', s.state === 'unknown' ? 'text-warn' : 'text-faint')}>${stateLabel[s.state]}</span>
      ${s.state === 'pending' && html`<button type="button" onClick=${() => recallSteer(r, s.inputId)} class="text-xs text-muted hover:text-fg px-1.5 h-6 rounded hover:bg-hover">Recall</button>`}
      ${s.state === 'unknown' && html`<button type="button" onClick=${() => { r.steers = r.steers.filter((x) => x !== s); emit(); }} class="text-xs text-muted hover:text-fg px-1.5 h-6 rounded hover:bg-hover">Dismiss</button>`}
    </div>`)}
    ${r.queue.length > 0 && html`<div class="flex items-center justify-between text-xs text-faint">
      <span>${r.queue.length} queued — ${r.held ? 'on hold' : 'sends when the current turn ends'}</span>
      ${r.held && !isBusy(r) && html`<button type="button" onClick=${() => resumeQueue(r)} class="text-accent hover:underline">Send next</button>`}
    </div>`}
    ${r.queue.map((q) => html`<div key=${q.id} class="flex items-center gap-2 min-w-0">
      <${Icon} name="queue" size=${14} class="text-faint" />
      <span class="flex-1 truncate text-muted">${inputLabel(q)}</span>
      <button type="button" title="Move back to the draft" onClick=${() => restoreQueued(r, q.id)} class="text-xs text-muted hover:text-fg px-1.5 h-6 rounded hover:bg-hover">Edit</button>
      <button type="button" aria-label="Remove from queue" onClick=${() => removeQueued(r, q.id)} class="text-faint hover:text-fg size-6 inline-flex items-center justify-center rounded hover:bg-hover"><${Icon} name="x" size=${13} /></button>
    </div>`)}
  </div>`;
}

// canDictate: the browser can record the microphone for voice input.
export const canDictate = () => !!(navigator.mediaDevices?.getUserMedia && window.MediaRecorder);

const MAX_DICTATION = 5 * 60_000;
const fmtClock = (ms) => { const s = Math.floor(ms / 1000); return Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0'); };

// useDictation records the microphone and turns the recording into text on
// the speech-to-text model Settings → Models assigns. phase is idle,
// starting, recording, or transcribing; onText takes the words heard.
export function useDictation(onText, key) {
  const [phase, setPhase] = useState('idle');
  const [since, setSince] = useState(0);
  const [, tick] = useState(0);
  const rec = useRef(null);
  const abort = useRef(null);
  const cb = useRef(onText);
  cb.current = onText;
  useEffect(() => {
    if (phase !== 'recording') return;
    const t = setInterval(() => tick((n) => n + 1), 500);
    return () => clearInterval(t);
  }, [phase]);
  // Leaving the room drops a recording in progress, and its transcript.
  useEffect(() => () => { rec.current?.discard(); abort.current?.abort(); }, [key]);

  const start = async () => {
    if (phase !== 'idle') return;
    setPhase('starting');
    try {
      const doc = await api.settings();
      const role = doc.models?.transcription;
      if (!role?.provider || !role?.model) {
        toast('Voice input needs a speech-to-text model.', 'info', 8000, { label: 'Choose one', run: () => navigate('#/settings/models') });
        setPhase('idle');
        return;
      }
    } catch (err) {
      toast('Could not check voice input: ' + errorText(err), 'error');
      setPhase('idle');
      return;
    }
    let stream;
    try { stream = await navigator.mediaDevices.getUserMedia({ audio: true }); }
    catch (err) {
      toast(err?.name === 'NotAllowedError' ? 'Microphone access was refused. Allow it in the browser to dictate.' : 'Could not open the microphone: ' + (err?.message || err), 'error');
      setPhase('idle');
      return;
    }
    const chunks = [];
    const mr = new MediaRecorder(stream);
    let keep = true;
    const limit = setTimeout(() => mr.state === 'recording' && mr.stop(), MAX_DICTATION);
    mr.ondataavailable = (e) => { if (e.data.size) chunks.push(e.data); };
    mr.onstop = async () => {
      clearTimeout(limit);
      stream.getTracks().forEach((t) => t.stop());
      rec.current = null;
      const audio = new Blob(chunks, { type: mr.mimeType || chunks[0]?.type || 'audio/webm' });
      if (!keep || !audio.size) { setPhase('idle'); return; }
      setPhase('transcribing');
      const ctl = new AbortController();
      abort.current = ctl;
      try {
        const { text } = await api.transcribe(audio, ctl.signal);
        if (text?.trim()) cb.current(text.trim());
        else toast('No speech was heard.', 'info');
      } catch (err) {
        if (!ctl.signal.aborted) toast('Could not transcribe: ' + errorText(err), 'error');
      }
      abort.current = null;
      setPhase('idle');
    };
    rec.current = { stop: () => mr.state === 'recording' && mr.stop(), discard: () => { keep = false; if (mr.state === 'recording') mr.stop(); } };
    mr.start();
    setSince(Date.now());
    setPhase('recording');
  };
  return { phase, elapsed: phase === 'recording' ? Date.now() - since : 0, start, stop: () => rec.current?.stop(), discard: () => rec.current?.discard() };
}

export function Composer({ room, hero = false }) {
  useStore();
  const r = room;
  const ta = useRef();
  const [, bump] = useState(0);
  const [menu, setMenu] = useState(false);
  const live = r.live && !r.live.terminal;
  const canSteer = live && r.live.kind === 'send';
  const steerTurnId = canSteer ? r.live.turnId : '';
  const busy = isBusy(r);
  const hasText = !!r.draft.trim();
  const fileInput = useRef();
  const [dragging, setDragging] = useState(false);
  // On a phone the row is the field and its three buttons; the pickers live in
  // a tray that folds out above them, the way a chat app stacks its extras,
  // instead of being squeezed into one line.
  const [options, setOptions] = useState(false);
  const uploading = r.files.some((f) => f.status === 'uploading');
  const readyFiles = r.files.filter((f) => f.status === 'ready');
  // A native command runs on the server, so the composer waits while it does;
  // the draft itself is the input, so a command needs no attachments.
  const typed = parseCommand(r.draft);
  // A quote rides above the draft, so a draft carrying one is a message, not
  // a command line; only the draft's own first line can be a command.
  const native = !r.quotes.length && typed && isNativeCommand(r, typed.name) ? typed.name : '';
  const canSend = !r.command && (hasText || readyFiles.length > 0 || r.quotes.length > 0 || r.recording) && !uploading;
  // "@" and "/" open the reference picker at the caret; Tab or Enter
  // picks, Escape closes it until the caret leaves that word.
  const cmds = useCommands(r);
  const [caret, setCaret] = useState(null);
  const [dismissed, setDismissed] = useState('');
  const edit = useRef(null);
  const trigger = caret == null ? null : referenceTrigger(r.draft, caret);
  const triggerKey = trigger ? trigger.start + ':' + trigger.prefix : '';
  const open = trigger && dismissed !== triggerKey ? trigger : null;
  const choices = useReferenceChoices(r, open, cmds);
  const matches = choices.items;
  const [active, setActive] = useState(0);
  useEffect(() => setActive(firstEnabled(matches)), [matches.length, triggerKey]);
  useEffect(() => { if (dismissed && dismissed !== triggerKey) setDismissed(''); }, [triggerKey]);
  const refs = liveReferences(r.draft, r.refs);
  const trackCaret = (e) => setCaret(e.target.selectionStart === e.target.selectionEnd ? e.target.selectionStart : null);
  const pick = (c) => {
    const t = open;
    if (!t) return;
    // A row the server switched off says why and stays a row: it is never
    // picked, and never falls back to ordinary text.
    if (c.enabled === false) { toast(c.reason || `/${c.name} is not available in this thread.`, 'info'); return; }
    const label = referenceLabel(c, t.prefix);
    const old = r.draft;
    const space = old.slice(t.end).startsWith(' ') ? '' : ' ';
    const next = old.slice(0, t.start) + label + space + old.slice(t.end);
    const kept = reconcileReferences(old, next, refs, t.start, t.end);
    // A native command is plain text, not a selection: the server decides at
    // submit time from the draft's first line, so it carries no reference.
    // An agent command is a reference the turn sends, as before.
    r.refs = c.kind === 'command' && c.native
      ? kept
      : [...kept, { kind: c.kind, id: c.id, label, start: t.start, end: t.start + label.length }].sort((a, b) => a.start - b.start);
    r.draft = next;
    const at = t.start + label.length + space.length;
    setDismissed(t.start + ':' + t.prefix);
    bump((n) => n + 1);
    requestAnimationFrame(() => { ta.current?.focus(); ta.current?.setSelectionRange(at, at); setCaret(at); });
  };
  const unselect = (ref) => { r.refs = refs.filter((x) => x !== ref); bump((n) => n + 1); };

  // Grow with the draft up to a cap; the textarea scrolls past it.
  useLayoutEffect(() => {
    const el = ta.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, Math.round(window.innerHeight * 0.4)) + 'px';
  }, [r.draft, r.key]);
  useEffect(() => { if (window.matchMedia('(pointer:fine)').matches) ta.current?.focus(); }, [r.key]);
  // A draft changed from outside — a quote from the transcript — asks for
  // the caret at r.caret; r.dictate asks for voice input after it.
  useLayoutEffect(() => {
    if (r.caret == null) return;
    const at = Math.min(r.caret, r.draft.length);
    r.caret = null;
    const el = ta.current;
    if (!el) return;
    el.focus();
    el.setSelectionRange(at, at);
    el.scrollTop = el.scrollHeight;
  }, [r.caret, r.key]);
  const voice = useDictation((text) => {
    const d = r.draft;
    r.draft = !d || /\s$/.test(d) ? d + text : d + ' ' + text;
    r.caret = r.draft.length;
    bump((n) => n + 1);
    emit();
  }, r.key);
  useEffect(() => {
    if (!r.dictate) return;
    r.dictate = false;
    if (canDictate()) voice.start();
  }, [r.dictate, r.key]);
  useEffect(() => { if (!canSteer) setMenu(false); }, [canSteer]);

  // The draft delivers as steer (into the running turn), queue, or send —
  // one payload, its quotes written ahead of its text; the caller picks
  // the timing. Only a successful submit clears it.
  const deliver = (turnId = '') => {
    const out = quotedDraft(r.quotes, r.draft, refs);
    if (submit(r, out.text, out.refs, r.files, turnId)) {
      r.draft = '';
      r.refs = [];
      r.files = [];
      r.quotes = [];
      r.recording = null;
      bump((n) => n + 1);
      emit();
    }
  };
  const onDrop = (e) => {
    if (!e.dataTransfer?.files?.length) return;
    e.preventDefault();
    setDragging(false);
    attachFiles(r, [...e.dataTransfer.files]);
  };
  const onPaste = (e) => {
    const files = [...(e.clipboardData?.files || [])];
    if (!files.length) return;
    e.preventDefault();
    attachFiles(r, files);
  };
  // Enter steers when a send turn runs — attachments included — and
  // queues otherwise.
  const primary = () => deliver(steerTurnId);

  const onKey = (e) => {
    if (open && e.key === 'Escape' && !e.isComposing) {
      e.preventDefault();
      setDismissed(triggerKey);
      return;
    }
    const option = open && matches.length ? matches[Math.min(active, matches.length - 1)] : null;
    // A native command spelled out on the draft's first line runs on Enter:
    // completing its name again would only add a space, and a command is an
    // action, not a selection.
    if (native && e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      setDismissed(triggerKey);
      primary();
      return;
    }
    // With the menu open, the arrows walk the rows (skipping the ones that
    // cannot run) and Tab or Enter takes the selected one.
    if (matches.length && !e.isComposing) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        setActive((a) => stepRow(matches, a, e.key === 'ArrowDown' ? 1 : -1));
        return;
      }
      if ((e.key === 'Tab' || e.key === 'Enter') && !e.shiftKey) {
        e.preventDefault();
        pick(option);
        return;
      }
    }
    if (e.key === 'Escape' && voice.phase === 'recording') {
      e.preventDefault();
      voice.discard();
      return;
    }
    if (e.key !== 'Enter' || e.isComposing || e.keyCode === 229) return;
    if (e.shiftKey) return;
    e.preventDefault();
    if ((e.ctrlKey || e.metaKey) && canSteer) deliver();
    else primary();
  };

  const placeholder = canSteer ? 'Steer the running turn…' : busy ? 'Queue a follow-up…' : hero ? 'What should we work on?' : 'Reply…';
  const primaryLabel = r.command ? `Running /${r.command}…` : native ? `Run /${native}` : canSteer ? 'Steer now' : busy ? 'Add to queue' : 'Send';
  const primaryTitle = r.command ? primaryLabel : primaryLabel + ' (Enter)';

  return html`<div class=${cx('w-full', hero ? '' : 'pb-[max(12px,env(safe-area-inset-bottom))]')}>
    <${Tray} room=${r} />
    ${open && html`<${ReferenceMenu} prefix=${open.prefix} items=${matches} message=${choices.message} loading=${choices.loading} active=${active} onPick=${pick} />`}
    <div onDragOver=${(e) => { if ([...(e.dataTransfer?.types || [])].includes('Files')) { e.preventDefault(); setDragging(true); } }}
      onDragLeave=${(e) => { if (!e.currentTarget.contains(e.relatedTarget)) setDragging(false); }}
      onDrop=${onDrop}
      class=${cx('relative rounded-2xl border border-line bg-raised shadow-sm focus-within:border-[color-mix(in_srgb,var(--accent)_45%,var(--line))] transition-colors', (r.steers.length || r.queue.length) && 'rounded-t-xl', dragging && 'border-accent')}>
      ${dragging && html`<div class="absolute inset-0 z-10 rounded-[inherit] bg-[color-mix(in_srgb,var(--accent)_10%,transparent)] border-2 border-dashed border-accent flex items-center justify-center text-sm text-accent pointer-events-none">Drop files to attach</div>`}
      ${r.quotes.length > 0 && html`<div class="flex flex-col gap-1.5 px-3 pt-2.5">
        ${r.quotes.map((q) => html`<${QuoteChip} key=${q.key} room=${r} q=${q} />`)}
      </div>`}
      ${(r.files.length > 0 || r.recording) && html`<div class="flex flex-wrap gap-1.5 px-3 pt-2.5">
        ${r.recording && html`<span title=${'Browser recording ' + r.recording.id}
          class="inline-flex items-center gap-1.5 h-9 max-w-60 pl-1 pr-1 rounded-lg border border-line bg-panel text-xs">
          <span class="size-7 inline-flex items-center justify-center text-muted"><${Icon} name="video" size=${14} /></span>
          <span class="min-w-0 flex flex-col leading-tight">
            <span class="truncate">${r.recording.label || 'Browser recording'}</span>
            <span class="truncate text-faint">${r.recording.status || 'recording'}</span>
          </span>
          <button type="button" aria-label="Remove recording" onClick=${() => { r.recording = null; bump((n) => n + 1); emit(); }}
            class="size-6 shrink-0 inline-flex items-center justify-center rounded text-faint hover:text-fg hover:bg-hover"><${Icon} name="x" size=${12} /></button>
        </span>`}
        ${r.files.map((f) => html`<span key=${f.key} title=${f.error || (f.pickId ? [f.label, f.comment].filter(Boolean).join(' — ') : f.name)}
          class=${cx('inline-flex items-center gap-1.5 h-9 max-w-60 pl-1 pr-1 rounded-lg border bg-panel text-xs', f.status === 'error' ? 'border-bad/60' : 'border-line')}>
          ${f.pickId ? html`<span class="size-7 inline-flex items-center justify-center"><${Marker} n=${f.marker} /></span>`
            : f.preview ? html`<img src=${f.preview} alt="" class="size-7 rounded object-cover" />` : html`<span class="size-7 inline-flex items-center justify-center text-muted"><${Icon} name="file" size=${14} /></span>`}
          <span class="min-w-0 flex flex-col leading-tight">
            <span class="truncate">${f.pickId ? f.label : f.name}</span>
            <span class=${cx('truncate', f.status === 'error' ? 'text-bad' : 'text-faint')}>${f.pickId ? (f.comment || (f.screenshot ? 'With screenshot' : 'No screenshot')) : f.status === 'uploading' ? 'Uploading…' : f.status === 'error' ? f.error : fmtSize(f.size)}</span>
          </span>
          ${f.status === 'uploading' && html`<${Spinner} size=${11} />`}
          <button type="button" aria-label=${'Remove ' + f.name} onClick=${() => removeFile(r, f.key)}
            class="size-6 shrink-0 inline-flex items-center justify-center rounded text-faint hover:text-fg hover:bg-hover"><${Icon} name="x" size=${12} /></button>
        </span>`)}
      </div>`}
      ${refs.length > 0 && html`<div class="flex flex-wrap gap-1 px-3 pt-2">
        ${refs.map((x) => html`<span key=${x.start + x.label} title=${x.id} class="inline-flex items-center gap-1 h-6 max-w-full pl-2 pr-1 rounded-md border border-line bg-panel text-xs">
          <span class="font-mono truncate">${x.label}</span>
          <button type="button" aria-label=${'Unselect ' + x.label} title="Keep the text, drop the selection" onClick=${() => unselect(x)}
            class="size-4 inline-flex items-center justify-center rounded text-faint hover:text-fg"><${Icon} name="x" size=${11} /></button>
        </span>`)}
      </div>`}
      ${r.command && html`<div class="flex items-center gap-1.5 px-3 pt-2 text-xs text-muted" role="status">
        <${Spinner} size=${12} /> ${commandBusyLabel(r.command)}…
      </div>`}
      <textarea ref=${ta} rows=${hero ? 3 : 1} value=${r.draft} placeholder=${placeholder} aria-label="Message"
        role="combobox" aria-autocomplete="list" aria-expanded=${!!(open && (matches.length || choices.message))}
        aria-controls="reference-menu" aria-activedescendant=${open && matches.length ? 'ref-opt-' + Math.min(active, matches.length - 1) : undefined}
        onBeforeInput=${(e) => { edit.current = { start: e.target.selectionStart, end: e.target.selectionEnd, type: e.inputType || '' }; }}
        onInput=${(e) => {
          const before = edit.current || {};
          edit.current = null;
          r.refs = reconcileReferences(r.draft, e.target.value, refs, before.start, before.end, before.type);
          r.draft = e.target.value;
          trackCaret(e);
          bump((n) => n + 1);
        }}
        onKeyDown=${onKey} onPaste=${onPaste}
        onKeyUp=${trackCaret} onClick=${trackCaret} onFocus=${trackCaret} onBlur=${() => setCaret(null)}
        class="composer-input"></textarea>
      ${options && html`<div class="sm:hidden flex flex-wrap items-center gap-1.5 px-2 pt-2">
        <${WorkspacePicker} room=${r} />
        <${AgentOptions} room=${r} />
        <${ContextMeter} room=${r} />
      </div>`}
      <div class="flex items-center gap-1 px-2 pb-2 pt-1">
        <input ref=${fileInput} type="file" multiple class="hidden" onChange=${(e) => { attachFiles(r, [...e.target.files]); e.target.value = ''; }} />
        <button type="button" onClick=${() => fileInput.current?.click()} title="Attach files" aria-label="Attach files"
          class="size-8 shrink-0 inline-flex items-center justify-center rounded-lg text-muted hover:text-fg hover:bg-hover"><${Icon} name="paperclip" size=${16} /></button>
        <${ModelPicker} room=${r} />
        <div class="hidden sm:flex items-center gap-1 min-w-0"><${WorkspacePicker} room=${r} /></div>
        <div class="flex-1"></div>
        <div class="hidden sm:flex items-center gap-1 min-w-0">
          <${AgentOptions} room=${r} />
          <${ContextMeter} room=${r} />
        </div>
        <button type="button" onClick=${() => setOptions(!options)} aria-expanded=${options} aria-label="Composer options"
          title="Model, workspace, context" class="sm:hidden size-8 shrink-0 inline-flex items-center justify-center rounded-lg hover:bg-hover ${options ? 'text-fg bg-hover' : 'text-muted'}">
          <${Icon} name="sliders" size=${16} />
        </button>
        ${canDictate() && (voice.phase === 'recording'
          ? html`<span class="flex items-center gap-1 h-8 pl-2 pr-0.5 rounded-full bg-bad/12 text-bad">
              <span class="size-2 rounded-full bg-bad animate-pulse" aria-hidden="true"></span>
              <span class="text-xs tabular-nums w-8" aria-live="off">${fmtClock(voice.elapsed)}</span>
              <button type="button" onClick=${voice.discard} title="Discard the recording (Esc)" aria-label="Discard the recording"
                class="size-7 inline-flex items-center justify-center rounded-full hover:bg-bad/15"><${Icon} name="x" size=${13} /></button>
              <button type="button" onClick=${voice.stop} title="Stop and transcribe" aria-label="Stop and transcribe"
                class="size-7 inline-flex items-center justify-center rounded-full bg-bad text-white hover:brightness-110"><${Icon} name="check" size=${14} stroke=${2.5} /></button>
            </span>`
          : html`<button type="button" onClick=${voice.start} disabled=${voice.phase !== 'idle'}
              title=${voice.phase === 'transcribing' ? 'Transcribing…' : 'Dictate'} aria-label=${voice.phase === 'transcribing' ? 'Transcribing' : 'Dictate'}
              class="size-8 shrink-0 inline-flex items-center justify-center rounded-lg text-muted hover:text-fg hover:bg-hover disabled:hover:bg-transparent">
              ${voice.phase === 'idle' ? html`<${Icon} name="mic" size=${16} />` : html`<${Spinner} size=${14} />`}
            </button>`)}
        ${live && html`<button type="button" onClick=${() => cancel(r)} disabled=${r.live.canceling} title="Stop this turn" aria-label="Stop this turn"
          class="size-8 shrink-0 inline-flex items-center justify-center rounded-full border border-line text-fg hover:bg-hover disabled:opacity-50">
          ${r.live.canceling ? html`<${Spinner} size=${13} />` : html`<${Icon} name="stop" size=${12} stroke=${3} />`}
        </button>`}
        <div class="relative flex items-center">
          <button type="button" onClick=${primary} disabled=${!canSend} title=${primaryTitle} aria-label=${primaryLabel}
            class=${cx('h-8 shrink-0 inline-flex items-center justify-center transition',
              canSteer ? 'w-8 rounded-l-full pl-1' : 'size-8 rounded-full',
              canSend ? 'bg-fg text-bg hover:opacity-90' : 'bg-panel text-faint border border-line')}>
            ${r.command ? html`<${Spinner} size=${14} />` : html`<${Icon} name=${busy && !canSteer ? 'queue' : 'arrowUp'} size=${16} stroke=${2.25} />`}
          </button>
          ${canSteer && html`<button type="button" onClick=${() => setMenu(!menu)} aria-label="More send options" aria-expanded=${menu}
            class=${cx('h-8 w-6 shrink-0 inline-flex items-center justify-center rounded-r-full -ml-px',
              canSend ? 'bg-fg text-bg border-l border-bg/20 hover:opacity-90' : 'bg-panel text-faint border border-line')}>
            <${Icon} name="chevronDown" size=${13} stroke=${2.25} />
          </button>`}
          <${Popover} open=${canSteer && menu} onClose=${() => setMenu(false)} placement="top-end" label="Send options" class="sm:w-60 py-1">
            <button type="button" onClick=${() => { setMenu(false); deliver(steerTurnId); }} disabled=${!canSend}
              class="w-full flex items-center justify-between gap-3 px-3 h-9 hover:bg-hover disabled:opacity-50">
              <span>Steer now</span><${Kbd}>↵</${Kbd}>
            </button>
            <button type="button" onClick=${() => { setMenu(false); deliver(); }} disabled=${!canSend}
              class="w-full flex items-center justify-between gap-3 px-3 h-9 hover:bg-hover disabled:opacity-50">
              <span>Add to queue</span><span class="flex gap-1"><${Kbd}>${MOD}</${Kbd}><${Kbd}>↵</${Kbd}></span>
            </button>
          </${Popover}>
        </div>
      </div>
    </div>
  </div>`;
}
