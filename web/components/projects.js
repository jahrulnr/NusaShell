// Projects: named groups of threads that work on the same folders. The
// first folder is the primary one — every thread in the project works
// there; agents may also reach the others.
import { html, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore, toast, state, emit } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { loadProjects, loadConversations, newThreadIn } from '../lib/chat.js';
import { Icon } from './icons.js';
import { Button, Dialog, ErrorBlock, Toggle } from './ui.js';
import { FolderBrowser } from './folders.js';

const inputCls = 'h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent w-full';
const base = (p) => (p ? p.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || p : '');

// editProject opens the project editor over whatever view is showing:
// { isNew: true } for a new project, or a project to edit. Projects are
// part of the thread list, not of Settings, so the editor has no page.
export function editProject(project) {
  state.projectEdit = project;
  emit();
}

// ProjectEditor hosts the editor once for the whole app.
export function ProjectEditor() {
  const s = useStore();
  const p = s.projectEdit;
  // An edit follows the stored project, so a reload elsewhere shows here.
  const project = p && !p.isNew ? s.projects.items.find((x) => x.id === p.id) || p : p;
  const close = () => editProject(null);
  return html`<${ProjectDialog} key=${p ? (p.isNew ? 'new' : p.id) : 'none'} project=${project} onClose=${close} />`;
}

export function ProjectDialog({ project, onClose }) {
  const open = !!project;
  const isNew = !!project?.isNew;
  const [name, setName] = useState(project?.name || '');
  const [folders, setFolders] = useState(project?.folders || []);
  const [pinned, setPinned] = useState(!!project?.pinned);
  const [browsing, setBrowsing] = useState(isNew);
  const [busy, setBusy] = useState('');
  const [err, setErr] = useState('');
  const [confirmDelete, setConfirmDelete] = useState(false);
  const added = isNew ? folders : folders.filter((f) => !(project?.folders || []).includes(f));

  const save = async () => {
    setBusy('save'); setErr('');
    try {
      const doc = { name: name.trim(), folders, pinned };
      const saved = isNew ? await api.createProject(doc) : await api.updateProject(project.id, { ...doc, updatedAt: project.updatedAt });
      await loadProjects();
      toast(`Saved ${saved.name}.`);
      onClose();
      if (isNew) newThreadIn(saved.id);
    } catch (e) { setErr(errorText(e)); }
    setBusy('');
  };
  const remove = async () => {
    setBusy('delete'); setErr('');
    try {
      const r = await api.deleteProject(project.id);
      await Promise.all([loadProjects(), loadConversations()]);
      toast(`Removed ${project.name}. ${r.released.length} ${r.released.length === 1 ? 'thread was' : 'threads were'} kept outside any project.`,
        'info', 12000, { label: 'Undo', run: () => restoreProject(project, r.released) });
      onClose();
    } catch (e) { setErr(errorText(e)); setConfirmDelete(false); }
    setBusy('');
  };
  const move = (i, to) => setFolders((fs) => { const n = fs.slice(); const [f] = n.splice(i, 1); n.splice(to, 0, f); return n; });

  return html`<${Dialog} open=${open} onClose=${() => !busy && onClose()} title=${isNew ? 'New project' : `Edit ${project?.name || ''}`}
    footer=${html`
      ${!isNew && (confirmDelete
        ? html`<span class="mr-auto flex items-center gap-2 text-[13px]">Delete this project?
            <${Button} kind="danger" busy=${busy === 'delete'} onClick=${remove}>Delete</${Button}>
            <${Button} kind="ghost" onClick=${() => setConfirmDelete(false)}>Keep</${Button}></span>`
        : html`<${Button} kind="danger" class="mr-auto" disabled=${!!busy} onClick=${() => setConfirmDelete(true)}>Delete</${Button}>`)}
      <${Button} kind="ghost" disabled=${!!busy} onClick=${onClose}>Cancel</${Button}>
      <${Button} kind="primary" busy=${busy === 'save'} disabled=${!folders.length || !!busy} onClick=${save}>${isNew ? 'Create project' : 'Save'}</${Button}>`}>
    <form noValidate class="flex flex-col gap-4" onSubmit=${(e) => { e.preventDefault(); if (folders.length) save(); }}>
      ${err && html`<${ErrorBlock} title="Could not save" message=${err} />`}
      <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Name</span>
        <input class=${inputCls} value=${name} onInput=${(e) => setName(e.target.value)} placeholder=${base(folders[0]) || 'Defaults to the primary folder’s name'} />
      </label>
      <div class="flex flex-col gap-1.5 text-[13px]">
        <span class="text-muted">Folders</span>
        ${folders.length > 0 && html`<div class="rounded-lg border border-line divide-y divide-line">
          ${folders.map((f, i) => html`<div key=${f} class="flex items-center gap-2 px-3 py-1.5">
            <${Icon} name="folder" size=${15} class=${i === 0 ? 'text-accent' : 'text-faint'} />
            <span class="flex-1 min-w-0 flex flex-col" title=${f}>
              <span class="truncate">${base(f)}</span>
              <span class="truncate font-mono text-[11px] text-faint" dir="rtl"><bdi>${f}</bdi></span>
            </span>
            ${i === 0
              ? html`<span class="text-[11px] text-accent">primary</span>`
              : html`<button type="button" class="text-[11px] text-muted hover:text-fg" onClick=${() => move(i, 0)}>Make primary</button>`}
            <button type="button" aria-label=${'Remove ' + f} title="Remove folder" class="size-7 inline-flex items-center justify-center rounded-md text-muted hover:bg-hover hover:text-bad"
              onClick=${() => setFolders((fs) => fs.filter((x) => x !== f))}><${Icon} name="x" size=${14} /></button>
          </div>`)}
        </div>`}
        ${browsing
          ? html`<div class="rounded-lg border border-line bg-panel">
              <${FolderBrowser} scroll="list" start=${folders[folders.length - 1] || ''} title="Add a folder" pickLabel="Add folder"
                onPick=${(p) => { setFolders((fs) => (fs.includes(p) ? fs : [...fs, p])); setBrowsing(false); }} onCancel=${() => setBrowsing(false)} />
            </div>`
          : html`<div><${Button} onClick=${() => setBrowsing(true)}><${Icon} name="plus" size=${14} /> Add folder</${Button}></div>`}
        ${added.length > 0 && html`<p class="text-xs text-warn">Threads in this project — and the agents they run — will be able to read and change files in ${added.length === 1 ? 'this folder' : 'these folders'}.</p>`}
      </div>
      <div class="flex items-center justify-between text-[13px]">
        <div><div>Pin to the top</div><div class="text-xs text-muted">Pinned projects lead the sidebar.</div></div>
        <${Toggle} checked=${pinned} onChange=${setPinned} label="Pin project" />
      </div>
    </form>
  </${Dialog}>`;
}

// restoreProject undoes a delete: the project comes back under its id with
// the threads the delete released, except ones moved or deleted since.
async function restoreProject(project, released) {
  try {
    const r = await api.restoreProject(project.id, { name: project.name, folders: project.folders, pinned: !!project.pinned, released });
    await Promise.all([loadProjects(), loadConversations()]);
    const left = released.length - r.rejoined.length;
    toast(left ? `Restored ${project.name}. ${left} ${left === 1 ? 'thread' : 'threads'} had moved and stayed put.` : `Restored ${project.name}.`);
  } catch (e) { toast(`Could not restore ${project.name}: ${errorText(e)}`, 'error'); }
}
