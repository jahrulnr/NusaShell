// The composer's reference picker: "@" offers threads and projects from
// what is loaded, "/" offers the agent's commands (at the start of the
// message) and searches files, folders, skills and MCP servers on the
// server.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { state } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { slug, KIND_LABEL } from '../lib/references.js';
import { Icon } from './icons.js';
import { cx, Spinner } from './ui.js';

const KIND_ICON = { file: 'file', folder: 'folder', skill: 'spark', mcp: 'plug', command: 'slash', conversation: 'message', project: 'layers' };

function mentionChoices(query, currentId) {
  const q = (query || '').toLowerCase();
  const rooms = state.convs.items
    .filter((c) => c.label && c.id !== currentId)
    .filter((c) => !q || c.label.toLowerCase().startsWith(q) || String(c.title || '').toLowerCase().includes(q))
    .slice(0, 8)
    .map((c) => ({ kind: 'conversation', id: c.id, name: c.label, detail: c.title || 'Untitled' }));
  const projects = state.projects.items
    .filter((p) => p.name && (!q || p.name.toLowerCase().includes(q) || slug(p.name).startsWith(q)))
    .slice(0, 4)
    .map((p) => ({ kind: 'project', id: p.id, name: slug(p.name), detail: p.name }));
  return [...rooms, ...projects];
}

// placeParams names where the owner is composing, for the server search.
export function placeParams(r) {
  if (r.id) return { conversationId: r.id };
  if (state.newProjectId) return { projectId: state.newProjectId };
  return r.sel.workspace ? { workspace: r.sel.workspace } : {};
}

// useReferenceChoices lists what the trigger at the caret can select.
export function useReferenceChoices(r, trigger, commands) {
  const [search, setSearch] = useState({ key: '', items: [], status: 'idle' });
  const place = placeParams(r);
  const key = trigger && trigger.prefix === '/' ? JSON.stringify([place, trigger.query]) : '';
  const ctl = useRef(null);
  useEffect(() => {
    ctl.current?.abort();
    if (!key) { setSearch({ key: '', items: [], status: 'idle' }); return; }
    setSearch({ key, items: [], status: 'loading' });
    const c = new AbortController();
    ctl.current = c;
    const t = setTimeout(async () => {
      try {
        const res = await api.references({ ...place, q: trigger.query }, c.signal);
        if (!c.signal.aborted) setSearch({ key, items: res.items || [], truncated: res.truncated, status: 'ready' });
      } catch (e) {
        if (!c.signal.aborted) setSearch({ key, items: [], status: 'error', error: errorText(e) });
      }
    }, 180);
    return () => { clearTimeout(t); c.abort(); };
  }, [key]);
  if (!trigger) return { items: [], message: '' };
  if (trigger.prefix === '@') {
    const items = mentionChoices(trigger.query, r.id);
    return { items, message: items.length ? '' : 'No threads or projects match' };
  }
  const q = trigger.query.toLowerCase();
  const cmds = trigger.start === 0
    ? commands.filter((c) => c.name.toLowerCase().startsWith(q)).slice(0, 8)
      .map((c) => ({ kind: 'command', id: c.name, name: c.name, detail: [c.description, c.hint].filter(Boolean).join(' · ') }))
    : [];
  const found = search.key === key ? search.items : [];
  const items = [...cmds, ...found];
  let message = '';
  if (search.key === key) {
    if (search.status === 'loading') message = 'Searching…';
    else if (search.status === 'error') message = search.error;
    else if (!items.length) message = 'No matches';
    else if (search.truncated) message = 'Showing the first matches — type more to narrow.';
  }
  return { items, message, loading: search.status === 'loading' && search.key === key };
}

export function ReferenceMenu({ prefix, items, message, loading, active, onPick }) {
  const list = useRef();
  useEffect(() => { list.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' }); }, [active]);
  if (!items.length && !message) return null;
  return html`<div ref=${list} id="reference-menu" role="listbox" aria-label="References"
    class="mx-3 mb-1 rounded-xl border border-line bg-panel shadow-pop py-1 max-h-64 overflow-y-auto">
    ${items.map((c, i) => html`<button key=${c.kind + c.id} id=${'ref-opt-' + i} type="button" role="option" aria-selected=${i === active}
      onMouseDown=${(e) => { e.preventDefault(); onPick(c); }}
      class=${cx('w-full flex items-center gap-2 px-3 py-1.5 text-left hover:bg-hover min-w-0', i === active && 'bg-hover')}>
      <${Icon} name=${KIND_ICON[c.kind] || 'link'} size=${14} class="shrink-0 text-muted" />
      <span class="font-mono text-[13px] shrink-0 max-w-[45%] truncate">${prefix}${c.name}</span>
      <span class="text-xs text-faint shrink-0">${KIND_LABEL[c.kind] || c.kind}</span>
      ${c.detail && html`<span class="text-xs text-muted truncate min-w-0">${c.detail}</span>`}
    </button>`)}
    ${message && html`<div class="flex items-center gap-2 px-3 py-1.5 text-xs text-muted">${loading && html`<${Spinner} size=${11} />`}${message}</div>`}
  </div>`;
}
