// Sidebar: projects first (pinned ones lead, each folds open onto its
// threads), then the threads outside any project grouped by recency.
// Search filters both by name.
import { html, useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore, setPref } from '../lib/store.js';
import { loadConversations, loadProjects, loadProjectThreads, PROJECT_THREADS, newThreadIn, annotateRoom, removeRoomById, watching } from '../lib/chat.js';
import { api, errorText } from '../lib/api.js';
import { toast } from '../lib/store.js';
import { Icon, Logo } from './icons.js';
import { cx, Spinner, ErrorBlock, IconButton, Popover, Dialog, Button, relTime } from './ui.js';
import { editProject } from './projects.js';

const DAY = 864e5;

// groups splits threads outside any project into Pinned / Today /
// Yesterday / Earlier, keeping the list's own order inside each group.
function groups(threads, now = Date.now()) {
  const today = new Date(now); today.setHours(0, 0, 0, 0);
  const t0 = today.getTime();
  const out = [['Pinned', []], ['Today', []], ['Yesterday', []], ['Earlier', []]];
  for (const c of threads) {
    const at = new Date(c.updatedAt).getTime();
    const g = c.pinned ? 0 : at >= t0 ? 1 : at >= t0 - DAY ? 2 : 3;
    out[g][1].push(c);
  }
  return out.filter(([, list]) => list.length);
}

export function Sidebar() {
  const s = useStore();
  const [q, setQ] = useState('');
  const [searching, setSearching] = useState(false);
  const [confirm, setConfirm] = useState(null); // thread pending delete
  const c = s.convs;
  const activeId = s.route.view === 'chat' ? s.route.id : null;
  const needle = q.trim().toLowerCase();
  const match = (t) => !needle || (t || '').toLowerCase().includes(needle);

  const projects = s.projects.items;
  const known = new Set(projects.map((p) => p.id));
  const inProject = (x) => x.projectId && known.has(x.projectId);
  const loose = c.items.filter((x) => !inProject(x) && match(x.title || 'Untitled thread'));
  const collapsed = new Set(s.prefs.collapsedProjects || []);
  const toggle = (id) => {
    const next = new Set(collapsed);
    if (next.has(id)) next.delete(id); else next.add(id);
    setPref('collapsedProjects', [...next]);
  };
  // A project's threads: the ones the main list paged in, plus its own
  // fetched list when it has more, newest first.
  const projectThreads = (p) => {
    const byId = new Map();
    for (const x of [...(s.projectThreads[p.id] || []), ...c.items]) if (x.projectId === p.id) byId.set(x.id, x);
    return [...byId.values()].sort((a, b) => new Date(b.updatedAt) - new Date(a.updatedAt));
  };
  const shownProjects = projects
    .map((p) => {
      const threads = projectThreads(p);
      const hits = threads.filter((x) => match(x.title || 'Untitled thread'));
      return { p, threads: needle && !match(p.name) ? hits : threads, show: !needle || match(p.name) || hits.length > 0 };
    })
    .filter((x) => x.show);
  // An open project with threads the main list has not paged in fetches
  // its own list once; refreshes keep it current.
  useEffect(() => {
    for (const p of projects) {
      const open = needle || !collapsed.has(p.id);
      if (open && !s.projectThreads[p.id] && (p.threads || 0) > c.items.filter((x) => x.projectId === p.id).length) loadProjectThreads(p.id);
    }
  });

  const thread = (x, compact) => html`<${ThreadRow} key=${x.id} thread=${x} active=${x.id === activeId} compact=${compact}
    projects=${projects} onDelete=${() => setConfirm(x)} />`;

  return html`<nav aria-label="Threads" class="flex flex-col h-full w-full bg-panel">
    <div class="flex items-center gap-2 px-3 h-12 shrink-0">
      <${Logo} size=${22} />
      <span class="font-semibold tracking-tight">NusaShell</span>
      <span class=${cx('ml-1 size-1.5 rounded-full', s.conn === 'open' ? 'bg-ok' : s.conn === 'connecting' ? 'bg-warn' : 'bg-bad')}
        title=${s.conn === 'open' ? 'Connected' : s.conn === 'connecting' ? 'Connecting…' : 'Disconnected — retrying'}></span>
      <div class="ml-auto flex">
        <${IconButton} icon="search" label="Search threads and projects" active=${searching} onClick=${() => { setSearching(!searching); setQ(''); }} />
      </div>
    </div>

    <div class="px-2 flex flex-col gap-0.5">
      <button type="button" onClick=${() => newThreadIn('')}
        class=${cx('flex items-center gap-2.5 h-8 px-2.5 rounded-lg hover:bg-hover text-left', s.route.view === 'home' && !s.newProjectId && 'bg-hover')}>
        <${Icon} name="plus" /> New thread
      </button>
      <a href="#/settings" class=${cx('flex items-center gap-2.5 h-8 px-2.5 rounded-lg hover:bg-hover text-muted hover:text-fg', s.route.view === 'settings' && 'bg-hover text-fg')}>
        <${Icon} name="settings" /> Settings
      </a>
    </div>

    ${searching && html`<div class="px-3 pt-3">
      <input autofocus value=${q} onInput=${(e) => setQ(e.target.value)} placeholder="Search threads and projects…" aria-label="Search threads and projects"
        class="w-full h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent" />
    </div>`}

    <div class="flex-1 min-h-0 overflow-y-auto px-2 pb-2">
      <div class="flex items-center justify-between pl-2 pr-1 pt-5 pb-1 text-xs text-faint">
        <span>Projects</span>
        <${IconButton} icon="plus" size=${14} label="New project" class="size-6" onClick=${() => editProject({ isNew: true })} />
      </div>
      ${s.projects.status === 'loading' && html`<div class="flex flex-col gap-2 px-2 pt-1">${[60, 45].map((w) => html`<div key=${w} class="skeleton h-4" style=${{ width: w + '%' }}></div>`)}</div>`}
      ${s.projects.status === 'error' && html`<${ErrorBlock} class="mx-1" title="Projects unavailable" message=${s.projects.error} onRetry=${loadProjects} />`}
      ${s.projects.status === 'ready' && !projects.length && html`<button type="button" onClick=${() => editProject({ isNew: true })}
        class="w-full text-left px-2.5 py-1.5 text-[13px] text-faint hover:text-fg">Group threads by folder — create a project.</button>`}
      <ul class="flex flex-col gap-px">
        ${shownProjects.map(({ p, threads }) => {
          const open = needle ? true : !collapsed.has(p.id);
          return html`<li key=${p.id}>
            <${ProjectRow} project=${p} open=${open} count=${needle ? threads.length : Math.max(p.threads || 0, threads.length)} onToggle=${() => toggle(p.id)}
              target=${s.route.view === 'home' && s.newProjectId === p.id} />
            ${open && html`<ul class="ml-3 pl-2 border-l border-line flex flex-col gap-px my-0.5">
              ${threads.map((x) => html`<li key=${x.id}>${thread(x, true)}</li>`)}
              ${!needle && (p.threads || 0) > threads.length && threads.length >= PROJECT_THREADS && html`<li class="px-2 py-1 text-[12px] text-faint">${p.threads - threads.length} older threads not shown.</li>`}
              <li><button type="button" onClick=${() => newThreadIn(p.id)}
                class="w-full flex items-center gap-2 h-7 px-2 rounded-md text-[12px] text-faint hover:text-fg hover:bg-hover">
                <${Icon} name="plus" size=${13} /> New thread
              </button></li>
            </ul>`}
          </li>`;
        })}
      </ul>

      <div class="flex items-center justify-between px-2 pt-5 pb-1 text-xs text-faint">
        <span>Threads</span>
        ${c.total > 0 && html`<span title="Loaded threads of total">${c.items.length}/${c.total}</span>`}
      </div>
      ${c.status === 'loading' && html`<div class="flex flex-col gap-2 px-2 pt-1">
        ${[70, 85, 60, 78, 66].map((w) => html`<div key=${w} class="skeleton h-4" style=${{ width: w + '%' }}></div>`)}
      </div>`}
      ${c.status === 'error' && html`<${ErrorBlock} class="mx-1" title="Threads unavailable" message=${c.error} onRetry=${() => loadConversations()} />`}
      ${c.status === 'ready' && !c.items.length && html`<p class="px-2.5 py-2 text-[13px] text-faint">No threads yet. Start one from New thread.</p>`}
      ${c.status === 'ready' && needle && !loose.length && !shownProjects.length && html`<p class="px-2.5 py-2 text-[13px] text-faint">Nothing loaded matches “${q}”.</p>`}
      ${groups(loose).map(([label, list]) => html`<div key=${label}>
        <div class="px-2.5 pt-2 pb-0.5 text-[11px] text-faint">${label}</div>
        <ul class="flex flex-col gap-px">${list.map((x) => html`<li key=${x.id}>${thread(x, false)}</li>`)}</ul>
      </div>`)}
      ${c.hasMore && html`<button onClick=${() => loadConversations(true)} disabled=${c.more}
        class="mt-1 w-full flex items-center justify-center gap-2 h-8 rounded-lg text-[13px] text-muted hover:bg-hover hover:text-fg disabled:opacity-60">
        ${c.more && html`<${Spinner} />`} Load more
      </button>`}
    </div>

    ${s.update?.available && html`<a href="#/settings/about" class="flex items-center gap-2 mx-2 mb-2 px-3 h-9 rounded-lg text-[13px] text-accent hover:bg-hover">
      <${Icon} name="download" /> Update available · ${s.update.latest}
    </a>`}

    <${Dialog} open=${!!confirm} onClose=${() => setConfirm(null)} title="Delete this thread?"
      footer=${html`<${Button} kind="ghost" autofocus onClick=${() => setConfirm(null)}>Keep</${Button}>
        <${Button} kind="danger" onClick=${() => { const id = confirm.id; setConfirm(null); removeRoomById(id); }}>Delete</${Button}>`}>
      <p class="text-muted">“${confirm?.title || 'Untitled thread'}” and its history are removed permanently.</p>
    </${Dialog}>
  </nav>`;
}

function ProjectRow({ project: p, open, count, onToggle, target }) {
  const [menu, setMenu] = useState(false);
  const pin = async () => {
    setMenu(false);
    try { await api.pinProject(p.id, !p.pinned); await loadProjects(); } catch (err) { toast('Could not update the project: ' + errorText(err), 'error'); }
  };
  return html`<div class=${cx('group relative flex items-center rounded-lg hover:bg-hover', target && 'bg-hover')}>
    <button type="button" onClick=${onToggle} aria-expanded=${open} title=${p.folders.join('\n')}
      class="flex-1 min-w-0 flex items-center gap-1.5 h-8 pl-1.5 pr-1 text-[13px] text-left">
      <${Icon} name=${open ? 'chevronDown' : 'chevronRight'} size=${13} class="text-faint" />
      <${Icon} name="layers" size=${14} class="text-muted" />
      <span class="flex-1 truncate">${p.name}</span>
      ${p.pinned && html`<${Icon} name="pin" size=${12} class="text-faint" />`}
      ${count > 0 && html`<span class="text-[11px] text-faint tabular-nums group-hover:hidden">${count}</span>`}
    </button>
    <${IconButton} icon="more" size=${14} label=${'Project actions: ' + p.name} class="size-7 opacity-0 max-md:opacity-100 group-hover:opacity-100 focus:opacity-100" onClick=${() => setMenu(!menu)} />
    <${Popover} open=${menu} onClose=${() => setMenu(false)} placement="bottom-end" label="Project actions" class="sm:w-52 py-1">
      <${MenuItem} icon="plus" onClick=${() => { setMenu(false); newThreadIn(p.id); }}>New thread</${MenuItem}>
      <${MenuItem} icon="pin" onClick=${pin}>${p.pinned ? 'Unpin' : 'Pin to top'}</${MenuItem}>
      <${MenuItem} icon="pencil" onClick=${() => { setMenu(false); editProject(p); }}>Edit project</${MenuItem}>
    </${Popover}>
  </div>`;
}

function ThreadRow({ thread: x, active, compact, projects, onDelete }) {
  const s = useStore();
  const [menu, setMenu] = useState(false);
  const [moving, setMoving] = useState(false);
  const close = () => { setMenu(false); setMoving(false); };
  const deleting = s.deleting.includes(x.id);
  return html`<div class=${cx('group relative flex items-center rounded-lg', active ? 'bg-hover' : 'hover:bg-hover', deleting && 'opacity-50 pointer-events-none')}
    aria-busy=${deleting || undefined}>
    <a href=${'#/c/' + encodeURIComponent(x.id)} aria-current=${active ? 'page' : undefined}
      class=${cx('flex-1 min-w-0 flex items-center gap-2 pl-2.5 pr-1 text-[13px]', compact ? 'h-7' : 'h-8', active ? 'text-fg' : 'text-muted hover:text-fg')}>
      ${watching(x) ? html`<span class="orb live" title="Working"></span>` : x.turnActive ? html`<span class="orb idle" title="Working — no live view yet"></span>` : compact ? null : html`<span class="size-2 shrink-0"></span>`}
      ${x.agentId && html`<${Icon} name="bot" size=${13} class="text-faint" />`}
      <span class="flex-1 truncate">${x.title || 'Untitled thread'}</span>
      ${x.pinned && compact && html`<${Icon} name="pin" size=${12} class="text-faint" />`}
      ${deleting
        ? html`<span class="flex items-center gap-1 text-[11px] text-faint shrink-0"><${Spinner} size=${11} /> Deleting</span>`
        : html`<span class=${cx('text-[11px] text-faint shrink-0 max-md:hidden', menu ? 'hidden' : 'group-hover:hidden group-focus-within:hidden')}>${relTime(x.updatedAt)}</span>`}
    </a>
    <span class=${cx(deleting ? 'hidden' : menu ? 'flex' : 'hidden max-md:flex group-hover:flex group-focus-within:flex')}>
      <${IconButton} icon="more" size=${14} label=${'Thread actions: ' + (x.title || 'Untitled thread')} class="size-7" onClick=${() => setMenu(!menu)} />
    </span>
    <${Popover} open=${menu} onClose=${close} placement="bottom-end" label="Thread actions" class="sm:w-56 py-1">
      ${moving
        ? html`<div class="px-3 pt-1.5 pb-1 text-[11px] text-faint">Move to</div>
          ${projects.map((p) => html`<${MenuItem} key=${p.id} icon="layers" disabled=${x.projectId === p.id}
            onClick=${() => { close(); annotateRoom(x.id, { projectId: p.id }); }}>${p.name}</${MenuItem}>`)}
          ${x.projectId && html`<${MenuItem} icon="x" onClick=${() => { close(); annotateRoom(x.id, { projectId: '' }); }}>No project</${MenuItem}>`}
          ${!projects.length && html`<p class="px-3 py-1.5 text-[12px] text-faint">No projects yet.</p>`}`
        : html`<${MenuItem} icon="pin" onClick=${() => { close(); annotateRoom(x.id, { pinned: !x.pinned }); }}>${x.pinned ? 'Unpin' : 'Pin to top'}</${MenuItem}>
          <${MenuItem} icon="layers" onClick=${() => setMoving(true)}>Move to project…</${MenuItem}>
          <${MenuItem} icon="shield" onClick=${() => { close(); annotateRoom(x.id, { memoryOff: !x.memoryOff }); }}>${x.memoryOff ? 'Use memory here' : 'Keep memory out of here'}</${MenuItem}>
          <div class="my-1 border-t border-line"></div>
          <${MenuItem} icon="trash" danger onClick=${() => { close(); onDelete(); }}>Delete thread</${MenuItem}>`}
    </${Popover}>
  </div>`;
}

function MenuItem({ icon, children, onClick, danger, disabled }) {
  return html`<button type="button" onClick=${onClick} disabled=${disabled}
    class=${cx('w-full flex items-center gap-2.5 px-3 h-9 text-left hover:bg-hover disabled:opacity-40 disabled:hover:bg-transparent', danger && 'text-bad')}>
    <${Icon} name=${icon} size=${15} /><span class="truncate">${children}</span>
  </button>`;
}
