// A thread's side pane: its live browser, a file view of the folders the
// thread works in, and a terminal there. Tabs stay mounted once opened so
// a terminal keeps its session while another tab shows.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { api, errorText, fileURL } from '../lib/api.js';
import { state } from '../lib/store.js';
import { Icon } from './icons.js';
import { cx, IconButton, Select, Spinner, ErrorBlock } from './ui.js';
import { PaneSplitter } from './splitter.js';
import { BrowserPanel } from './browser.js';
import { TerminalPane } from './terminal.js';
import { openPreview } from './preview.js';

const TABS = [
  { id: 'browser', icon: 'globe', label: 'Browser' },
  { id: 'files', icon: 'folder', label: 'Files' },
  { id: 'terminal', icon: 'terminal', label: 'Terminal' },
];

// useRoots loads the folders the thread works in, primary first.
export function useRoots(r) {
  const [roots, setRoots] = useState({ status: 'loading', list: [] });
  const load = () => {
    setRoots((x) => ({ ...x, status: 'loading' }));
    api.roots(r.id ? { conversationId: r.id } : {}).then((res) => setRoots({ status: 'ready', list: res.roots || [] }),
      (e) => setRoots({ status: 'error', list: [], error: errorText(e) }));
  };
  useEffect(load, [r.id, r.info?.workspace, r.info?.projectId]);
  return { ...roots, reload: load };
}

export function PaneButtons({ pane, setPane }) {
  return html`${TABS.slice(1).map((t) => html`<${IconButton} key=${t.id} icon=${t.icon} label=${t.label} active=${pane === t.id}
    aria-pressed=${pane === t.id} onClick=${() => setPane(pane === t.id ? null : t.id)} />`)}`;
}

export function SidePane({ room: r, browser, pane, setPane }) {
  const [mounted, setMounted] = useState({});
  const aside = useRef();
  const roots = useRoots(r);
  useEffect(() => { if (pane && !mounted[pane]) setMounted((m) => ({ ...m, [pane]: true })); }, [pane]);
  useEffect(() => { setMounted({}); }, [r.id]);
  if (!pane) return null;
  return html`<aside id="thread-sidepane" ref=${aside} aria-label="Side pane"
    style=${{ '--pane-width': state.prefs.threadSidePaneWidth ? `${state.prefs.threadSidePaneWidth}px` : undefined }}
    class="thread-sidepane fixed inset-0 z-30 flex flex-col bg-bg lg:relative lg:z-auto lg:shrink-0 lg:border-l lg:border-line">
    <${PaneSplitter} edge="right" label="Resize thread side pane" panelRef=${aside} pref="threadSidePaneWidth" />
    <div class="flex items-center gap-1 h-12 px-2 border-b border-line shrink-0" role="tablist">
      ${TABS.map((t) => html`<button key=${t.id} type="button" role="tab" aria-selected=${pane === t.id} onClick=${() => setPane(t.id)}
        class=${cx('inline-flex items-center gap-1.5 h-8 px-2.5 rounded-md text-[13px]', pane === t.id ? 'bg-hover text-fg' : 'text-muted hover:text-fg hover:bg-hover')}>
        <${Icon} name=${t.icon} size=${14} />${t.label}
        ${t.id === 'browser' && browser.open && html`<span class="size-1.5 rounded-full bg-ok"></span>`}
      </button>`)}
      <div class="flex-1"></div>
      <${IconButton} icon="x" label="Close the side pane" onClick=${() => setPane(null)} />
    </div>
    ${pane === 'browser' && html`<${BrowserPanel} room=${r} browser=${browser} />`}
    ${(mounted.files || pane === 'files') && html`<div class=${cx('flex-1 min-h-0 flex-col', pane === 'files' ? 'flex' : 'hidden')}><${FilesPane} room=${r} roots=${roots} /></div>`}
    ${(mounted.terminal || pane === 'terminal') && html`<div class=${cx('flex-1 min-h-0 flex-col', pane === 'terminal' ? 'flex' : 'hidden')}><${TerminalPane} room=${r} roots=${roots} active=${pane === 'terminal'} /></div>`}
  </aside>`;
}

const fmtSize = (n) => (n < 1024 ? n + ' B' : n < 1 << 20 ? (n / 1024).toFixed(1) + ' KB' : (n / (1 << 20)).toFixed(1) + ' MB');

// RootPicker shows the folder in use and switches between the thread's
// folders when it has more than one.
export function RootPicker({ roots, path, onPick }) {
  const list = roots.list || [];
  if (list.length < 2) return html`<span class="flex items-center gap-1.5 min-w-0 text-[12px] text-muted" title=${path}><${Icon} name="folder" size=${13} class="shrink-0" /><bdi class="truncate">${path}</bdi></span>`;
  const root = list.find((x) => path === x || path.startsWith(x + '/')) || '';
  return html`<span class="flex items-center gap-1.5 min-w-0 text-[12px] text-muted">
    <${Icon} name="folder" size=${13} class="shrink-0" />
    <${Select} dense label="Folder" class="w-40" value=${root} onChange=${onPick}
      options=${[...(!root ? [{ value: '', text: 'Other folder' }] : []), ...list.map((x) => ({ value: x, text: x.split('/').pop() || x }))]} />
    ${path !== root && html`<bdi class="truncate" title=${path}>${root ? path.slice(root.length) : path}</bdi>`}
  </span>`;
}

function FilesPane({ room: r, roots }) {
  const [path, setPath] = useState('');
  const [dir, setDir] = useState({ status: 'idle' });
  const [hidden, setHidden] = useState(false);
  useEffect(() => { if (!path && roots.list[0]) setPath(roots.list[0]); }, [roots.list[0]]);
  const load = async (p = path) => {
    if (!p) return;
    setDir((d) => ({ ...d, status: 'loading', path: p }));
    try { setDir({ status: 'ready', ...(await api.list(p)) }); }
    catch (e) { setDir({ status: 'error', path: p, error: errorText(e) }); }
  };
  useEffect(() => { load(path); }, [path]);
  const preview = (f) => openPreview(r.id ? fileURL(r.id, f.path) : '/api/files?path=' + encodeURIComponent(f.path), f.name);
  const visible = (x) => hidden || !x.name.startsWith('.');
  if (roots.status === 'loading' && !path) return html`<div class="flex items-center gap-2 p-4 text-muted text-[13px]"><${Spinner} size=${12} /> Finding the thread's folders…</div>`;
  if (roots.status === 'error') return html`<div class="p-3"><${ErrorBlock} title="Could not find the thread's folders" message=${roots.error} onRetry=${roots.reload} /></div>`;
  if (!path) return html`<p class="p-4 text-[13px] text-muted">This thread has no folder to show. Pick a workspace for it first.</p>`;
  return html`
    <div class="flex items-center gap-1 h-9 px-2 border-b border-line shrink-0">
      <${IconButton} icon="chevronUp" size=${14} label="Up one folder" disabled=${!dir.parent} onClick=${() => setPath(dir.parent)} class="size-7" />
      <div class="flex-1 min-w-0"><${RootPicker} roots=${roots} path=${dir.path || path} onPick=${setPath} /></div>
      <${IconButton} icon="eye" size=${14} label=${hidden ? 'Hide dotfiles' : 'Show dotfiles'} active=${hidden} aria-pressed=${hidden} onClick=${() => setHidden(!hidden)} class="size-7" />
      <${IconButton} icon="refresh" size=${14} label="Refresh" onClick=${() => load()} class="size-7" />
    </div>
    <div class="flex-1 min-h-0 overflow-y-auto py-1 text-[13px]">
      ${dir.status === 'loading' && !dir.dirs && html`<div class="flex items-center gap-2 px-3 py-3 text-muted"><${Spinner} size=${12} /> Loading…</div>`}
      ${dir.status === 'error' && html`<div class="p-3"><${ErrorBlock} title="Could not open this folder" message=${dir.error} onRetry=${() => load()} /></div>`}
      ${dir.dirs && html`
        ${dir.dirs.filter(visible).map((d) => html`<button key=${d.path} type="button" onClick=${() => setPath(d.path)} title=${d.path}
          class="w-full flex items-center gap-2 px-3 h-8 hover:bg-hover text-left"><${Icon} name="folder" size=${14} class="text-muted shrink-0" /><span class="truncate">${d.name}</span></button>`)}
        ${dir.files.filter(visible).map((f) => html`<button key=${f.path} type="button" onClick=${() => preview(f)} title=${f.path}
          class="w-full flex items-center gap-2 px-3 h-8 hover:bg-hover text-left"><${Icon} name="file" size=${14} class="text-faint shrink-0" /><span class="flex-1 truncate">${f.name}</span><span class="text-xs text-faint shrink-0">${fmtSize(f.size)}</span></button>`)}
        ${!dir.dirs.filter(visible).length && !dir.files.filter(visible).length && html`<p class="px-3 py-3 text-muted">This folder is empty.</p>`}
        ${dir.truncated && html`<p class="px-3 py-2 text-xs text-faint">Showing the first 1000 entries.</p>`}`}
    </div>`;
}
