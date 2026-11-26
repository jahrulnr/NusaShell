// Thread header extras for chat channels: the room's @label (how a chat
// names it with /room @label) and the chats linked to it, with the room's
// reply routing.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { emit, toast } from '../lib/store.js';
import { applyRoomInfo } from '../lib/chat.js';
import { api, errorText } from '../lib/api.js';
import { Icon } from './icons.js';
import { cx, IconButton, Popover, Spinner, Toggle, usePopoverOpen } from './ui.js';

const DEFAULT_ROUTING = { replyToOrigin: true, mirrorFinal: false, notifyApprovals: true };

export function LabelChip({ room: r }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const done = useRef(false);
  const input = useRef();
  // autofocus only works on page load; an input that appears later is
  // focused by hand, or typing lands in the composer.
  useEffect(() => { if (editing) { input.current?.focus(); input.current?.select(); } }, [editing]);
  const label = r.info?.label || '';
  // Like the title, the field holds what is being typed: a refresh cannot
  // rewrite the handle under the cursor, and the room hears it on commit.
  const edit = () => { done.current = false; setDraft(label); setEditing(true); };
  const save = async (how) => {
    if (done.current) return;
    done.current = true;
    setEditing(false);
    const next = draft.trim().replace(/^@/, '').toLowerCase();
    if (how !== 'save' || next === label) return;
    try { applyRoomInfo(await api.annotate(r.id, { label: next })); emit(); }
    catch (e) { toast(errorText(e), 'error'); }
  };
  if (!r.id || !r.info) return null;
  if (editing) {
    return html`<input ref=${input} value=${draft} aria-label="Thread label" maxlength="24"
      onInput=${(e) => setDraft(e.target.value)}
      onKeyDown=${(e) => { if (e.key === 'Enter') save('save'); if (e.key === 'Escape') save('cancel'); }}
      onBlur=${() => save('save')}
      class="w-28 h-7 rounded-md bg-raised border border-accent px-1.5 text-[12px] font-mono outline-none" />`;
  }
  return html`<button type="button" onClick=${edit}
    title="Rename · /room @label switches a chat here"
    class="hidden sm:inline-flex items-center h-6 px-1.5 rounded-md border border-line text-[12px] font-mono text-muted hover:text-fg hover:bg-hover shrink-0">
    ${label ? '@' + label : '+ label'}
  </button>`;
}

export function LinksButton({ room: r }) {
  const [open, setOpen] = usePopoverOpen(!!r.id);
  const [links, setLinks] = useState({ status: 'idle', list: [] });
  const load = async () => {
    setLinks((l) => ({ ...l, status: 'loading' }));
    try { const res = await api.links(r.id); setLinks({ status: 'ready', list: res.data }); }
    catch (e) { setLinks({ status: 'error', list: [], error: errorText(e) }); }
  };
  useEffect(() => { if (r.id) load(); }, [r.id]);
  useEffect(() => { if (open) load(); }, [open]);
  if (!r.id) return null;
  const routing = { ...DEFAULT_ROUTING, ...(r.info?.routing || {}) };
  const setRouting = async (key, value) => {
    try { r.info = await api.annotate(r.id, { routing: { ...routing, [key]: value } }); emit(); }
    catch (e) { toast(errorText(e), 'error'); }
  };
  const unlink = async (key) => {
    try { await api.unlink(r.id, key); load(); } catch (e) { toast(errorText(e), 'error'); }
  };
  const count = links.list.length;
  return html`<div class="relative">
    <button type="button" onClick=${() => setOpen(!open)} aria-expanded=${open} title="Linked chats"
      class=${cx('inline-flex items-center gap-1 h-8 px-2 rounded-md text-muted hover:text-fg hover:bg-hover', count > 0 && 'text-fg')}>
      <${Icon} name="message" size=${15} />${count > 0 && html`<span class="text-[12px] tabular-nums">${count}</span>`}
    </button>
    <${Popover} open=${open} onClose=${() => setOpen(false)} placement="bottom-end" label="Linked chats" class="sm:w-80">
      <div class="px-3 pt-2.5 pb-1 text-xs text-faint">Linked chats</div>
      ${links.status === 'loading' && !count && html`<div class="flex items-center gap-2 px-3 py-2 text-muted"><${Spinner} size=${12} /> Loading…</div>`}
      ${links.status === 'error' && html`<p class="px-3 py-2 text-bad text-xs">${links.error}</p>`}
      ${links.status === 'ready' && !count && html`<p class="px-3 pb-2 text-[13px] text-muted">No chat talks to this thread yet. From WhatsApp or Telegram, send <code class="font-mono">/room @${r.info?.label || 'label'}</code>.</p>`}
      ${links.list.map((l) => html`<div key=${l.key} class="flex items-center gap-2 px-3 h-10">
        <span class="text-[11px] px-1 rounded border border-line text-faint">${l.channel === 'whatsapp' ? 'WA' : 'TG'}</span>
        <span class="flex-1 min-w-0 truncate">${l.display}${l.group ? html` <span class="text-faint">group</span>` : ''}</span>
        <${IconButton} icon="x" size=${13} label=${'Unlink ' + l.display} class="size-7" onClick=${() => unlink(l.key)} />
      </div>`)}
      <div class="border-t border-line mt-1 px-3 py-2.5 flex flex-col gap-2.5 text-[13px]">
        <div class="flex items-center justify-between gap-3"><span>Reply in the chat that asked</span>
          <${Toggle} checked=${routing.replyToOrigin} label="Reply to origin" onChange=${(v) => setRouting('replyToOrigin', v)} /></div>
        <div class="flex items-center justify-between gap-3"><span>Copy every reply to all linked chats</span>
          <${Toggle} checked=${routing.mirrorFinal} label="Mirror replies" onChange=${(v) => setRouting('mirrorFinal', v)} /></div>
        <div class="flex items-center justify-between gap-3"><span>Ask permissions in the chat</span>
          <${Toggle} checked=${routing.notifyApprovals} label="Approvals in chat" onChange=${(v) => setRouting('notifyApprovals', v)} /></div>
      </div>
    </${Popover}>
  </div>`;
}
