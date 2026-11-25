import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore, state, emit, toast } from '../lib/store.js';
import { room, freshRoom, rename, removeRoom, runCommand, isBusy, openRoom, navigate, annotateRoom } from '../lib/chat.js';
import { useCommands, commandBusyLabel } from '../lib/commands.js';
import { Icon, Logo } from './icons.js';
import { Popover, IconButton, Dialog, Button, ErrorBlock, Spinner } from './ui.js';
import { Transcript, QueuedLine } from './transcript.js';
import { useFollow } from '../lib/follow.js';
import { SetupWizard, needsSetup } from './setup.js';
import { Composer } from './composer.js';
import { LabelChip, LinksButton } from './links.js';
import { useBrowser, BrowserButton } from './browser.js';
import { SidePane, PaneButtons } from './sidepane.js';
import { SelectionBar } from './selectionbar.js';

export function MenuButton() {
  return html`<${IconButton} icon="sidebar" label="Open sidebar" class="md:hidden" onClick=${() => { state.sidebarOpen = true; emit(); }} />`;
}

function Header({ room: r, browser, pane, setPane }) {
  const [menu, setMenu] = useState(false);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [confirm, setConfirm] = useState(false);
  const titleInput = useRef();
  // settle records that this edit already ended: Enter closes the field and
  // its own blur must not save it a second time.
  const settle = useRef('');
  useEffect(() => { if (editing) { titleInput.current?.focus(); titleInput.current?.select(); } }, [editing]);
  const title = r.info?.title || 'Untitled thread';
  const ws = r.info?.workspace;
  // Compact runs on the same catalog the composer's "/" menu reads, so its
  // availability and reason are the server's, not a second rule here.
  const compact = useCommands(r).find((c) => c.name === 'compact');

  // The field holds what is being typed, not the room's record: a refresh
  // that lands mid-edit must not rewrite the text under the cursor, and the
  // name only reaches the room when the owner commits it.
  const editTitle = () => { settle.current = ''; setDraft(r.info?.title || ''); setEditing(true); };
  const closeTitle = (how) => {
    if (settle.current) return;
    settle.current = how;
    setEditing(false);
    const next = draft.trim();
    if (how === 'save' && next && next !== (r.info?.title || '')) rename(r, next);
  };
  return html`<header class="flex items-center gap-1 h-12 px-2 sm:px-3 border-b border-line shrink-0">
    <${MenuButton} />
    <div class="flex-1 min-w-0 flex items-center gap-3">
      ${editing
        ? html`<input ref=${titleInput} value=${draft} aria-label="Thread title" enterkeyhint="done"
            onInput=${(e) => setDraft(e.target.value)}
            onKeyDown=${(e) => { if (e.key === 'Enter') closeTitle('save'); if (e.key === 'Escape') closeTitle('cancel'); }}
            onBlur=${() => closeTitle('save')}
            class="flex-1 min-w-0 h-8 rounded-lg bg-raised border border-accent px-2 outline-none text-base sm:text-[14px]" />`
        : html`<button type="button" onClick=${editTitle} title="Rename" class="min-w-0 truncate text-left font-medium hover:text-fg px-1 rounded">${title}</button>`}
      <${LabelChip} room=${r} />
      ${r.info?.memoryOff && html`<span class="hidden sm:flex items-center gap-1 text-[11px] text-faint shrink-0" title="Memory is off for this thread">
        <${Icon} name="shield" size=${12} /> No memory</span>`}
      ${ws && html`<span class="hidden sm:flex items-center gap-1.5 text-[13px] text-faint min-w-0" title=${ws}>
        <${Icon} name="folder" size=${14} /><span class="truncate max-w-56">${ws.split('/').pop()}</span>
      </span>`}
    </div>
    ${r.command && html`<span class="flex items-center gap-1.5 text-xs text-muted"><${Spinner} size=${12} /> ${commandBusyLabel(r.command)}</span>`}
    ${!editing && html`<${BrowserButton} browser=${browser} />
    <${PaneButtons} pane=${pane} setPane=${setPane} />
    <${LinksButton} room=${r} />`}
    ${!editing && html`<div class="relative">
      <${IconButton} icon="more" label="Thread actions" onClick=${() => setMenu(!menu)} aria-expanded=${menu} />
      <${Popover} open=${menu} onClose=${() => setMenu(false)} placement="bottom-end" label="Thread actions" class="sm:w-56 py-1">
        <button type="button" class="w-full flex items-center gap-2.5 px-3 h-9 hover:bg-hover" onClick=${() => { setMenu(false); editTitle(); }}>
          <${Icon} name="pencil" size=${15} /> Rename
        </button>
        <button type="button" disabled=${compact?.enabled === false || isBusy(r) || !!r.command}
          title=${compact?.enabled === false ? (compact.reason || 'Not available now') : 'Summarize older history to free context'}
          class="w-full flex items-center gap-2.5 px-3 h-9 hover:bg-hover disabled:opacity-40 disabled:hover:bg-transparent"
          onClick=${() => { setMenu(false); runCommand(r, 'compact'); }}>
          <${Icon} name="compress" size=${15} /> Compact history
        </button>
        <button type="button" class="w-full flex items-center gap-2.5 px-3 h-9 hover:bg-hover"
          title=${r.info?.memoryOff ? 'Let this thread read and save memories again' : 'This thread stops reading and saving memories; nothing is deleted'}
          onClick=${async () => { setMenu(false); const off = !r.info?.memoryOff; if (!await annotateRoom(r.id, { memoryOff: off })) return;
            toast(off ? 'Memory is off for this thread.' : 'Memory is back on for this thread.'); }}>
          <${Icon} name="shield" size=${15} /> ${r.info?.memoryOff ? 'Use memory here' : 'Keep memory out of here'}
        </button>
        <a href=${`/api/conversations/${encodeURIComponent(r.id)}/export`} download onClick=${() => setMenu(false)}
          title="Download the whole thread as JSON" class="w-full flex items-center gap-2.5 px-3 h-9 hover:bg-hover">
          <${Icon} name="download" size=${15} /> Export as JSON
        </a>
        <div class="my-1 border-t border-line"></div>
        <button type="button" class="w-full flex items-center gap-2.5 px-3 h-9 hover:bg-hover text-bad" onClick=${() => { setMenu(false); setConfirm(true); }}>
          <${Icon} name="trash" size=${15} /> Delete thread
        </button>
      </${Popover}>
    </div>`}
    <${Dialog} open=${confirm} onClose=${() => setConfirm(false)} title="Delete this thread?"
      footer=${html`<${Button} kind="ghost" autofocus onClick=${() => setConfirm(false)}>Keep</${Button}>
        <${Button} kind="danger" onClick=${() => { setConfirm(false); removeRoom(r); }}>Delete</${Button}>`}>
      <p class="text-muted">“${title}” and its history are removed permanently. Running or queued turns in it stop.</p>
    </${Dialog}>
  </header>`;
}

export function ChatView({ id }) {
  useStore();
  const r = room(id);
  // The pane belongs to the room, so it is kept with the thread's view.
  const pane = r.pane || null;
  const setPane = (p) => { r.pane = p; emit(); };
  const browser = useBrowser(r, pane, setPane);
  const scroller = useRef();
  const follow = useFollow(scroller, { key: id });

  if (r.infoStatus === 'notfound') {
    return html`<div class="flex flex-col h-full">
      <header class="flex items-center h-12 px-2 border-b border-line"><${MenuButton} /></header>
      <div class="flex-1 grid place-items-center p-6 text-center">
        <div>
          <p class="font-medium">Thread not found</p>
          <p class="text-muted text-[13px] mt-1">It may have been deleted in another tab.</p>
          <${Button} class="mt-4" onClick=${() => navigate('#/')}>Start a new thread</${Button}>
        </div>
      </div>
    </div>`;
  }

  return html`<div class="flex h-full min-w-0">
  <div class="flex-1 flex flex-col h-full min-w-0">
    <${Header} room=${r} browser=${browser} pane=${pane} setPane=${setPane} />
    ${r.infoStatus === 'error' && html`<div class="px-4 pt-3"><${ErrorBlock} title="Could not load thread details" message=${r.infoError} onRetry=${() => openRoom(r.id)} /></div>`}
    <div ref=${scroller} class="flex-1 min-h-0 overflow-y-auto">
      <div class="mx-auto w-full max-w-3xl px-4 sm:px-6">
        <${Transcript} room=${r} scroller=${scroller} />
      </div>
    </div>
    <${SelectionBar} room=${r} scroller=${scroller} />
    ${follow.away && html`<div class="relative h-0">
      <button type="button" onClick=${follow.jump}
        class="absolute bottom-3 left-1/2 -translate-x-1/2 h-8 pl-2.5 pr-3 inline-flex items-center gap-1.5 rounded-full border border-line bg-raised text-[13px] text-fg shadow-pop hover:bg-hover">
        <${Icon} name="chevronDown" size=${15} /> ${isBusy(r) ? 'Follow latest' : 'Jump to latest'}
      </button>
    </div>`}
    <div class="mx-auto w-full max-w-3xl px-4 sm:px-6 empty:hidden"><${QueuedLine} room=${r} /></div>
    <div class="mx-auto w-full max-w-3xl px-2 sm:px-4">
      <${Composer} room=${r} />
    </div>
  </div>
  <${SidePane} room=${r} browser=${browser} pane=${pane} setPane=${setPane} />
  </div>`;
}

// MissingView answers an address the app cannot route. The server serves
// the shell for a browser navigation to an unknown path, so the visitor
// sees what happened — and a way back — instead of a dead page or the
// home screen under a URL that never resolves.
export function MissingView({ path }) {
  return html`<div class="flex flex-col h-full">
    <header class="flex items-center h-12 px-2 border-b border-line"><${MenuButton} /></header>
    <div class="flex-1 grid place-items-center p-6 text-center">
      <div class="max-w-md">
        <p class="font-medium">Page not found</p>
        <p class="text-muted text-[13px] mt-1">Nothing lives at this address.</p>
        ${path && html`<p class="mt-2 font-mono text-[12px] text-faint break-all">${path}</p>`}
        <${Button} class="mt-4" onClick=${() => navigate('#/')}>Go to threads</${Button}>
      </div>
    </div>
  </div>`;
}

export function HomeView() {
  const s = useStore();
  const r = freshRoom();
  const recent = s.convs.items.slice(0, 4);
  const [held, setHeld] = useState(false);
  const setup = held || needsSetup(s);
  // Both lists still loading on a first visit: hold the composer back so a
  // fresh install does not flash it before the wizard.
  const waiting = !setup && !s.providers.items.length && !s.agents.items.length
    && (s.providers.status === 'loading' || s.agents.status === 'loading');
  return html`<div class="flex flex-col h-full">
    <header class="flex items-center h-12 px-2 md:hidden"><${MenuButton} /></header>
    <div class="flex-1 min-h-0 overflow-y-auto">
      <div class="min-h-full flex flex-col justify-center mx-auto w-full max-w-2xl px-3 sm:px-4 py-10">
        ${setup ? html`<${SetupWizard} onHold=${() => setHeld(true)} onDone=${() => setHeld(false)} />`
        : waiting ? html`<div class="flex items-center justify-center gap-2 text-muted"><${Spinner} /> Loading…</div>`
        : html`<div>
        <div class="flex items-center gap-2.5 mb-5 px-1">
          <${Logo} size=${28} />
          <h1 class="text-xl font-semibold tracking-tight">${r.sideOf ? 'Side chat' : 'What are we building?'}</h1>
        </div>
        ${r.sideOf && html`<div class="flex items-center gap-2 mb-2 pl-3 pr-1 h-9 rounded-xl border border-line bg-panel text-[13px] text-muted min-w-0">
          <${Icon} name="sideChat" size=${15} class="text-accent" />
          <span class="shrink-0">From</span>
          <a href=${'#/c/' + encodeURIComponent(r.sideOf.id)} title="Back to that thread" class="min-w-0 truncate text-fg hover:underline">${r.sideOf.title}</a>
          <span class="flex-1"></span>
          <${IconButton} icon="x" size=${14} label="Discard the side chat" class="size-7 shrink-0"
            onClick=${() => { r.sideOf = null; r.draft = ''; r.refs = []; r.quotes = []; emit(); }} />
        </div>`}
        ${r.live || r.pendingOpen ? html`<div class="flex items-center gap-2 text-muted text-[13px] px-1 mb-3"><${Spinner} /> Starting thread…</div>` : null}
        <${Composer} room=${r} hero />
        ${recent.length > 0 && html`<div class="mt-8 px-1">
          <div class="text-xs text-faint mb-2">Recent</div>
          <div class="grid sm:grid-cols-2 gap-2">
            ${recent.map((c) => html`<a key=${c.id} href=${'#/c/' + encodeURIComponent(c.id)}
              class="flex items-center gap-2 rounded-xl border border-line px-3 py-2.5 text-[13px] hover:bg-hover min-w-0">
              ${c.activeTurnId ? html`<span class="orb live"></span>` : html`<${Icon} name="chevronRight" size=${14} class="text-faint" />`}
              <span class="truncate">${c.title || 'Untitled thread'}</span>
            </a>`)}
          </div>
        </div>`}
        </div>`}
      </div>
    </div>
  </div>`;
}
