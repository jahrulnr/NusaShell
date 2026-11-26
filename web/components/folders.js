// FolderBrowser: a directory-only browser over /api/fs/browse, used to
// pick a room's working directory and a project's folders. Its caller names
// the scroll owner: scroll="panel" inside a popover, where the host scrolls and
// the header and footer stick; scroll="list" inside a dialog body, where the
// list has a height cap and scrolls itself. There is no default.
import { html, useEffect, useLayoutEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { api, errorText } from '../lib/api.js';
import { Icon } from './icons.js';
import { cx, Checkbox, Spinner, ErrorBlock } from './ui.js';

export function FolderBrowser({ start = '', title = 'Folder', pickLabel = 'Use this folder', onPick, onCancel, scroll }) {
  const [dir, setDir] = useState({ status: 'idle' });
  const [showHidden, setShowHidden] = useState(false);
  const req = useRef(0);
  const root = useRef();
  const list = useRef();
  const browse = async (path) => {
    const n = ++req.current;
    setDir((d) => ({ ...d, status: 'loading' }));
    try {
      const r = await api.browse(path);
      if (n === req.current) setDir({ status: 'ready', ...r });
    } catch (err) {
      if (n === req.current) setDir((d) => ({ ...d, status: 'error', error: errorText(err), notFound: err.status === 404 }));
    }
  };
  useEffect(() => { browse(start); }, []);
  // A new listing starts at its top. The owner is the list in list mode, and in
  // panel mode the panel that hosts the browser, which is the browser's parent.
  useLayoutEffect(() => {
    const owner = scroll === 'panel' ? root.current?.parentElement : list.current;
    if (owner) owner.scrollTop = 0;
  }, [dir.path]);
  if (scroll !== 'panel' && scroll !== 'list') throw new Error('FolderBrowser: scroll must be "panel" or "list"');
  const entries = (dir.entries || []).filter((e) => showHidden || !e.hidden);
  return html`<div ref=${root} class="flex flex-col min-h-0">
    <div class=${cx('px-3 pt-2.5 pb-2 border-b border-line flex flex-col gap-2', scroll === 'panel' && 'sticky top-0 z-10 bg-raised')}>
      <div class="flex items-center justify-between">
        <span class="text-faint text-xs">${title}</span>
        <${Checkbox} checked=${showHidden} onChange=${setShowHidden} class="text-xs text-faint">Hidden</${Checkbox}>
      </div>
      <div class="flex items-center gap-1">
        <button type="button" disabled=${!dir.parent || dir.status === 'loading'} onClick=${() => browse(dir.parent)} title="Parent folder" aria-label="Parent folder"
          class="size-7 inline-flex items-center justify-center rounded-md text-muted hover:bg-hover hover:text-fg disabled:opacity-30"><${Icon} name="folderUp" size=${15} /></button>
        <span class="flex-1 truncate font-mono text-xs" title=${dir.path}>${dir.path || '…'}</span>
        ${(dir.roots || []).map((r) => html`<button key=${r.path} type="button" onClick=${() => browse(r.path)} class="px-1.5 h-6 rounded text-xs text-muted hover:bg-hover">${r.name}</button>`)}
      </div>
    </div>
    <div ref=${list} class=${cx('py-1', scroll === 'list' && 'flex-1 min-h-0 max-h-[min(18rem,34dvh)] overflow-y-auto')}>
      ${dir.status === 'loading' && !dir.entries && html`<div class="flex items-center gap-2 px-3 py-3 text-muted"><${Spinner} /> Reading folder…</div>`}
      ${dir.status === 'error' && html`<div class="p-2"><${ErrorBlock} title=${dir.notFound ? 'Folder not found' : 'Cannot open folder'} message=${dir.error} onRetry=${() => browse(dir.notFound ? '' : dir.path || '')} /></div>`}
      ${dir.status !== 'error' && dir.entries && !entries.length && html`<div class="px-3 py-3 text-faint">No subfolders here.</div>`}
      ${dir.status !== 'error' && entries.map((e) => html`<button key=${e.path} type="button" onClick=${() => browse(e.path)}
        class=${cx('w-full flex items-center gap-2 px-3 h-8 text-left hover:bg-hover', dir.status === 'loading' && 'opacity-60', e.hidden && 'text-muted')}>
        <${Icon} name="folder" size=${15} class="text-faint" /><span class="truncate">${e.name}</span>
      </button>`)}
      ${dir.truncated && html`<div class="px-3 py-2 text-xs text-faint">Showing the first 1000 folders.</div>`}
    </div>
    <div class=${cx('flex items-center justify-end gap-2 px-3 py-2 border-t border-line', scroll === 'panel' && 'sticky bottom-0 z-10 bg-raised')}>
      <button type="button" onClick=${onCancel} class="h-8 px-3 rounded-lg text-muted hover:bg-hover">Cancel</button>
      <button type="button" disabled=${!dir.path || dir.status !== 'ready'} onClick=${() => onPick(dir.path)}
        class="h-8 px-3 rounded-lg bg-accent text-accent-fg font-medium disabled:opacity-50">${pickLabel}</button>
    </div>
  </div>`;
}
