// Memory: what threads remember about the owner and their work. Models and
// agents save memories as they work, and after a compaction the utility
// model suggests the ones a thread missed; here the owner reviews them —
// reads, corrects, verifies, flags, forgets, restores, deletes, accepts or
// dismisses suggestions — and turns memory or suggestions off. A memory is
// one fact: a name, a one-line description the index shows, and a body
// read on demand.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore, toast, askConfirm } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { Icon } from './icons.js';
import { cx, Button, Dialog, ErrorBlock, Select, Spinner, Toggle, relTime } from './ui.js';

const inputCls = 'h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent w-full';
const PAGE = 50;

const TYPES = [
  { value: 'feedback', text: 'Feedback', hint: 'How to work: corrections and approaches the owner confirmed.' },
  { value: 'user', text: 'User', hint: 'Who the owner is: role, expertise, preferences.' },
  { value: 'project', text: 'Project', hint: 'Decisions, constraints, and deadlines the code does not record. Ages after 30 days.' },
  { value: 'reference', text: 'Reference', hint: 'Where something lives outside the workspace. Ages after 90 days.' },
];
const STATUSES = [['', 'In use'], ['stale', 'Stale'], ['archived', 'Forgotten']];
const SOURCES = { owner: 'Written by you', agent: 'Saved by a thread', consolidation: 'Suggested from a thread, accepted by you' };

// Segmented is a small radio group of text buttons.
function Segmented({ label, value, options, onChange, disabled }) {
  return html`<div class="flex gap-1 p-0.5 rounded-lg bg-raised border border-line w-fit text-[13px]" role="radiogroup" aria-label=${label}>
    ${options.map(([v, t]) => html`<button key=${v} type="button" role="radio" aria-checked=${value === v} disabled=${disabled}
      class=${cx('px-3 h-7 rounded-md flex items-center gap-1.5 disabled:opacity-60', value === v ? 'bg-panel text-fg shadow-sm' : 'text-muted hover:text-fg')}
      onClick=${() => onChange(v)}>${t}</button>`)}
  </div>`;
}

// scopeLabel names where a memory applies, from the projects and threads
// the sidebar already knows.
function scopeLabel(s, scope) {
  if (scope === 'global') return { text: 'Everywhere', icon: 'globe' };
  const [kind, sid] = [scope.slice(0, scope.indexOf(':')), scope.slice(scope.indexOf(':') + 1)];
  if (kind === 'project') {
    const p = s.projects.items.find((x) => x.id === sid);
    return { text: p ? p.name : 'A deleted project', icon: 'folder', faint: !p };
  }
  const c = s.convs.items.find((x) => x.id === sid);
  return { text: c?.title || 'One thread', icon: 'message', href: '#/c/' + encodeURIComponent(sid) };
}

function Freshness({ m }) {
  if (m.status === 'archived') return html`<span class="text-xs text-faint">Forgotten ${relTime(m.archivedAt)}</span>`;
  if (m.freshness === 'stale') return html`<span class="text-xs text-warn" title=${m.reason || 'Not verified for a long time'}>Stale</span>`;
  if (m.freshness === 'aging') return html`<span class="text-xs text-muted" title="Not verified for a while">Aging</span>`;
  return null;
}

export function Memory() {
  const s = useStore();
  const [enabled, setEnabled] = useState({ status: 'loading' });
  const [view, setView] = useState('memories');
  const [pending, setPending] = useState(0);
  const [f, setF] = useState({ q: '', type: '', status: '', scope: '' });
  const [query, setQuery] = useState('');
  const [list, setList] = useState({ status: 'loading', items: [], total: 0, hasMore: false, error: '' });
  const [limit, setLimit] = useState(PAGE);
  const [open, setOpen] = useState(null);
  const [editing, setEditing] = useState(null);
  const seq = useRef(0);

  const settled = (d) => ({ status: 'ready', on: d?.memory?.enabled !== 'off', suggest: d?.memory?.consolidation !== 'off', utility: !!d?.models?.utility?.model });
  useEffect(() => {
    api.settings().then((d) => setEnabled(settled(d)), (e) => setEnabled({ status: 'error', error: errorText(e) }));
    api.memorySuggestions({ limit: 1 }).then((r) => setPending(r.pending), () => {});
  }, []);
  // Typing narrows the list after a short pause, not on every key.
  useEffect(() => { const t = setTimeout(() => setF((x) => ({ ...x, q: query.trim() })), 250); return () => clearTimeout(t); }, [query]);
  const load = async (quiet) => {
    const mine = ++seq.current;
    if (!quiet) setList((l) => ({ ...l, status: l.status === 'ready' ? 'refreshing' : 'loading' }));
    try {
      const r = await api.memories({ ...f, limit });
      if (mine !== seq.current) return;
      setList({ status: 'ready', items: r.data, total: r.page.total, hasMore: r.page.hasMore, error: '' });
    } catch (e) {
      if (mine !== seq.current) return;
      if (quiet) toast(errorText(e), 'error');
      else setList((l) => ({ ...l, status: 'error', error: errorText(e) }));
    }
  };
  useEffect(() => { load(); }, [f.q, f.type, f.status, f.scope, limit]);
  useEffect(() => { setLimit(PAGE); }, [f.q, f.type, f.status, f.scope]);

  const toggle = async (on) => {
    setEnabled((x) => ({ ...x, busy: true }));
    try {
      const d = await api.setSetting('memory.enabled', on ? 'on' : 'off');
      setEnabled(settled(d));
      toast(on ? 'Memory is on. Threads see it from their next step.' : 'Memory is off. Nothing is deleted.');
    } catch (e) { setEnabled((x) => ({ ...x, busy: false })); toast(errorText(e), 'error'); }
  };
  const setSuggest = async (on) => {
    setEnabled((x) => ({ ...x, busy: true }));
    try {
      setEnabled(settled(await api.setSetting('memory.consolidation', on ? 'suggest' : 'off')));
      toast(on ? 'Compacted threads will suggest memories.' : 'No more suggestions. Waiting ones stay.');
    } catch (e) { setEnabled((x) => ({ ...x, busy: false })); toast(errorText(e), 'error'); }
  };

  const filtered = f.q || f.type || f.scope || f.status;
  const scopeOptions = [{ value: '', text: 'Anywhere' }, { value: 'global', text: 'Everywhere (global)' },
    ...s.projects.items.map((p) => ({ value: 'project:' + p.id, text: p.name }))];
  if (f.scope && !scopeOptions.some((o) => o.value === f.scope)) scopeOptions.push({ value: f.scope, text: scopeLabel(s, f.scope).text });

  return html`<div>
    <div class="flex flex-col sm:flex-row sm:items-end justify-between gap-3 sm:gap-4 mb-4">
      <div>
        <h2 class="text-[17px] font-semibold">Memory</h2>
        <p class="text-muted text-[13px] mt-0.5">What models and agents remember about you and your work, across threads. Every thread gets a one-line index; bodies are read when they matter.</p>
      </div>
      <${Button} kind="primary" class="shrink-0" onClick=${() => setEditing({ isNew: true })}><${Icon} name="plus" size=${14} /> New memory</${Button}>
    </div>

    <div class="rounded-xl border border-line bg-panel divide-y divide-line mb-6">
      <div class="px-4 py-3.5 flex items-center gap-4">
        <div class="flex-1 min-w-0">
          <div class="text-[13px] font-medium">Use memory</div>
          <div class="text-xs text-muted mt-0.5">${enabled.status === 'error' ? enabled.error
            : 'Off: threads get no index and cannot save or recall. What is saved stays here. A single thread can be kept out from its menu.'}</div>
        </div>
        ${enabled.status === 'loading' ? html`<${Spinner} size=${14} />`
          : enabled.status === 'ready' && html`<${Toggle} label="Use memory" checked=${enabled.on} disabled=${enabled.busy} onChange=${toggle} />`}
      </div>
      ${enabled.status === 'ready' && enabled.on && html`<div class="px-4 py-3.5 flex items-center gap-4">
        <div class="flex-1 min-w-0">
          <div class="text-[13px] font-medium">Suggest memories after compaction</div>
          <div class="text-xs text-muted mt-0.5">${!enabled.utility
            ? html`Needs a utility model — <a class="text-accent hover:underline" href="#/settings/models">assign one in Models</a>.`
            : 'When a thread compacts, the utility model reads what you said in the part that leaves its context and suggests memories here. Nothing is saved until you accept it; what you said is sent to that model, with credentials masked.'}</div>
        </div>
        <${Toggle} label="Suggest memories after compaction" checked=${enabled.suggest} disabled=${enabled.busy} onChange=${setSuggest} />
      </div>`}
    </div>

    <div class="mb-4">
      <${Segmented} label="Show" value=${view} onChange=${setView} options=${[['memories', 'Memories'],
        ['suggested', html`Suggested${pending > 0 && html`<span class="min-w-4 h-4 px-1 rounded-full bg-accent text-accent-fg text-[10px] leading-4 tabular-nums">${pending}</span>`}`]]} />
    </div>
    ${view === 'suggested' ? html`<${Suggestions} onPending=${setPending} />` : html`<div>

    <div class="flex flex-col sm:flex-row gap-2 mb-3">
      <label class="relative flex-1 min-w-0">
        <${Icon} name="search" size=${14} class="absolute left-2.5 top-1/2 -translate-y-1/2 text-faint" />
        <input class=${cx(inputCls, 'pl-8')} type="search" aria-label="Search memories" placeholder="Search names, descriptions, and bodies"
          value=${query} onInput=${(e) => setQuery(e.target.value)} />
      </label>
      <div class="flex gap-2">
        <${Select} label="Type" class="w-36" value=${f.type} onChange=${(v) => setF((x) => ({ ...x, type: v }))}
          options=${[{ value: '', text: 'All types' }, ...TYPES.map((t) => ({ value: t.value, text: t.text }))]} />
        <${Select} label="Scope" class="w-44" value=${f.scope} search=${scopeOptions.length > 8} onChange=${(v) => setF((x) => ({ ...x, scope: v }))} options=${scopeOptions} />
      </div>
    </div>
    <div class="flex items-center justify-between gap-3 mb-3">
      <${Segmented} label="Status" value=${f.status} options=${STATUSES} onChange=${(v) => setF((x) => ({ ...x, status: v }))} />
      <span class="text-xs text-faint flex items-center gap-1.5">
        ${list.status === 'refreshing' && html`<${Spinner} size=${11} />`}
        ${list.status !== 'loading' && list.status !== 'error' && `${list.total} ${list.total === 1 ? 'memory' : 'memories'}`}
      </span>
    </div>

    ${list.status === 'loading' && html`<div class="flex flex-col gap-2">${[1, 2, 3].map((i) => html`<div key=${i} class="skeleton h-[68px] rounded-xl"></div>`)}</div>`}
    ${list.status === 'error' && html`<${ErrorBlock} title="Could not load memories" message=${list.error} onRetry=${() => load()} />`}
    ${list.status !== 'loading' && list.status !== 'error' && !list.items.length && (filtered
      ? html`<div class="rounded-xl border border-dashed border-line p-6 text-center text-[13px]">
          <p class="font-medium">Nothing matches</p>
          <p class="text-muted mt-1">${f.status === 'archived' ? 'Forgotten memories are kept for 30 days, then deleted.' : 'Try other words, or clear the filters.'}</p>
          <${Button} kind="ghost" class="mt-3" onClick=${() => { setQuery(''); setF({ q: '', type: '', status: '', scope: '' }); }}>Clear filters</${Button}>
        </div>`
      : html`<div class="rounded-xl border border-dashed border-line p-8 text-center">
          <${Icon} name="layers" size=${22} class="text-faint mx-auto" />
          <p class="font-medium mt-2">Nothing remembered yet</p>
          <p class="text-muted text-[13px] mt-1 max-w-md mx-auto">Tell a thread “remember that …”, or correct how it works. It saves what should carry over to later threads, and it shows up here.</p>
          <${Button} class="mt-4" onClick=${() => setEditing({ isNew: true })}>Write one yourself</${Button}>
        </div>`)}

    <div class=${cx('flex flex-col gap-2', list.status === 'refreshing' && 'opacity-70')}>
      ${list.items.map((m) => html`<${MemoryRow} key=${m.id} m=${m} open=${open === m.id}
        onToggle=${() => setOpen(open === m.id ? null : m.id)} onEdit=${() => setEditing(m)} onChanged=${() => load(true)} />`)}
    </div>
    ${list.hasMore && list.status !== 'loading' && html`<div class="flex justify-center mt-4">
      <${Button} busy=${list.status === 'refreshing'} onClick=${() => setLimit(limit + PAGE)}>Show more</${Button}>
    </div>`}
    </div>`}
    <${MemoryDialog} key=${editing ? editing.id || 'new' : 'closed'} memory=${editing} onClose=${() => setEditing(null)} onSaved=${() => load(true)} />
  </div>`;
}

function MemoryRow({ m, open, onToggle, onEdit, onChanged }) {
  const s = useStore();
  const [busy, setBusy] = useState('');
  const [staling, setStaling] = useState(false);
  const [reason, setReason] = useState('');
  const where = scopeLabel(s, m.scope);
  const type = TYPES.find((t) => t.value === m.type);
  const act = async (action, done, why = '') => {
    setBusy(action);
    try { await api.memoryAction(m.id, action, why); toast(done); setStaling(false); setReason(''); onChanged(); }
    catch (e) { toast(errorText(e), 'error'); }
    setBusy('');
  };
  const remove = async () => {
    const yes = await askConfirm({ title: `Delete ${m.name} for good?`, body: 'It cannot be restored. Forget it instead to keep it restorable for 30 days.', confirm: 'Delete', danger: true });
    if (!yes) return;
    setBusy('delete');
    try { await api.deleteMemory(m.id); toast(`Deleted ${m.name}.`); onChanged(); } catch (e) { toast(errorText(e), 'error'); }
    setBusy('');
  };
  const live = m.status !== 'archived';

  return html`<div class=${cx('rounded-xl border bg-panel', m.freshness === 'stale' && live ? 'border-warn/40' : 'border-line')}>
    <button type="button" class="w-full text-left px-4 py-3 flex items-start gap-3" aria-expanded=${open} onClick=${onToggle}>
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-2 min-w-0">
          <span class=${cx('font-mono text-[13px] truncate', !live && 'text-muted line-through decoration-faint')}>${m.name}</span>
          <span class="text-[11px] px-1.5 py-px rounded-md bg-raised border border-line text-muted shrink-0">${type?.text || m.type}</span>
          <${Freshness} m=${m} />
        </div>
        <div class="text-[13px] text-muted mt-0.5 ${open ? '' : 'truncate'}">${m.description}</div>
      </div>
      <span class=${cx('text-xs shrink-0 flex items-center gap-1 mt-0.5', where.faint ? 'text-faint' : 'text-muted')}>
        <${Icon} name=${where.icon} size=${12} /><span class="max-w-36 truncate">${where.text}</span>
      </span>
      <${Icon} name="chevronRight" size=${14} class=${cx("text-muted mt-0.5 transition-transform", open && "rotate-90")} />
    </button>
    ${open && html`<div class="border-t border-line px-4 py-3 flex flex-col gap-3">
      <div class="text-[13px] whitespace-pre-wrap break-words leading-relaxed">${m.body}</div>
      ${m.reason && html`<div class=${cx('text-xs rounded-lg px-3 py-2', live ? 'bg-warn/10 text-warn' : 'bg-raised text-muted')}>
        ${live ? 'Marked stale: ' : 'Why it was forgotten: '}${m.reason}</div>`}
      <div class="text-xs text-faint flex flex-wrap gap-x-3 gap-y-1">
        <span>${SOURCES[m.source] || 'Saved by a thread'}${m.origin && html` · <a class="text-accent hover:underline" href=${'#/c/' + encodeURIComponent(m.origin)}>open it</a>`}</span>
        <span>Updated ${relTime(m.updatedAt)}</span>
        <span>Verified ${relTime(m.verifiedAt)}</span>
        <span>${m.recalls ? `Read ${m.recalls}× · last ${relTime(m.recalledAt)}` : 'Never read yet'}</span>
        ${where.href && html`<a class="text-accent hover:underline" href=${where.href}>Thread-only memory</a>`}
      </div>
      ${staling && html`<form class="flex gap-2" onSubmit=${(e) => { e.preventDefault(); act('stale', `Marked ${m.name} stale.`, reason); }}>
        <input class=${inputCls} autofocus aria-label="Why it is stale" placeholder="Why it may no longer hold" value=${reason} onInput=${(e) => setReason(e.target.value)} />
        <${Button} type="submit" busy=${busy === 'stale'} disabled=${!reason.trim()}>Mark stale</${Button}>
        <${Button} kind="ghost" onClick=${() => setStaling(false)}>Cancel</${Button}>
      </form>`}
      <div class="flex flex-wrap items-center gap-1.5">
        ${live ? html`
          <${Button} busy=${busy === 'verify'} onClick=${() => act('verify', `Verified ${m.name}.`)}><${Icon} name="check" size=${13} /> Still true</${Button}>
          ${m.status !== 'stale' && !staling && html`<${Button} kind="ghost" onClick=${() => setStaling(true)}><${Icon} name="alert" size=${13} /> Mark stale</${Button}>`}
          <${Button} kind="ghost" onClick=${onEdit}><${Icon} name="pencil" size=${13} /> Edit</${Button}>
          <${Button} kind="ghost" busy=${busy === 'archive'} onClick=${() => act('archive', `Forgot ${m.name}. Restore it within 30 days from Forgotten.`)}><${Icon} name="x" size=${13} /> Forget</${Button}>`
        : html`<${Button} busy=${busy === 'restore'} onClick=${() => act('restore', `Restored ${m.name}.`)}><${Icon} name="undo" size=${13} /> Restore</${Button}>`}
        <${Button} kind="ghost" class="ml-auto text-bad" busy=${busy === 'delete'} onClick=${remove}><${Icon} name="trash" size=${13} /> Delete</${Button}>
      </div>
    </div>`}
  </div>`;
}

function MemoryDialog({ memory: m, onClose, onSaved }) {
  const s = useStore();
  const open = !!m;
  const isNew = !!m?.isNew;
  const [f, setF] = useState(() => ({
    scope: m?.scope || 'global', type: m?.type || 'feedback', name: m?.name || '', description: m?.description || '', body: m?.body || '',
  }));
  const set = (k) => (v) => setF((x) => ({ ...x, [k]: v }));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const save = async () => {
    setErr(''); setBusy(true);
    try {
      const doc = { scope: f.scope, type: f.type, name: f.name.trim(), description: f.description.trim(), body: f.body.trim() };
      if (isNew) await api.createMemory(doc); else await api.updateMemory(m.id, doc);
      toast(isNew ? `Saved ${doc.name}.` : `Updated ${m.name}.`);
      onSaved(); onClose();
    } catch (e) { setErr(errorText(e)); }
    setBusy(false);
  };
  const field = (label, body, hint) => html`<label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">${label}</span>${body}${hint && html`<span class="text-xs text-faint">${hint}</span>`}</label>`;
  const count = (v, max) => html`<span class=${cx('tabular-nums', v.length > max && 'text-bad')}>${v.length}/${max}</span>`;
  const scopes = [{ value: 'global', text: 'Everywhere (global)' }, ...s.projects.items.map((p) => ({ value: 'project:' + p.id, text: 'Project · ' + p.name }))];
  const type = TYPES.find((t) => t.value === f.type);

  return html`<${Dialog} open=${open} onClose=${() => !busy && onClose()} title=${isNew ? 'New memory' : `Edit ${m?.name || ''}`}
    footer=${html`<${Button} kind="ghost" disabled=${busy} onClick=${onClose}>Cancel</${Button}>
      <${Button} kind="primary" busy=${busy} onClick=${save}>${isNew ? 'Save' : 'Save changes'}</${Button}>`}>
    <form noValidate class="flex flex-col gap-4" onSubmit=${(e) => { e.preventDefault(); save(); }}>
      ${err && html`<${ErrorBlock} title="Could not save" message=${err} />`}
      <div class="grid sm:grid-cols-2 gap-4">
        ${field('Type', html`<${Select} label="Type" value=${f.type} onChange=${set('type')} options=${TYPES.map((t) => ({ value: t.value, text: t.text }))} />`, type?.hint)}
        ${isNew
          ? field('Applies', html`<${Select} label="Applies" search=${scopes.length > 8} value=${f.scope} onChange=${set('scope')} options=${scopes} />`, 'A project memory reaches only that project’s threads.')
          : field('Applies', html`<div class="h-8 flex items-center text-muted">${scopeLabel(s, m?.scope || 'global').text}</div>`, 'Name and scope stay; forget it and write a new one to move it.')}
      </div>
      ${isNew && field('Name', html`<input class=${cx(inputCls, 'font-mono')} value=${f.name} onInput=${(e) => set('name')(e.target.value.toLowerCase().replace(/\s+/g, '-'))} placeholder="prefers-short-replies" />`,
        'A short kebab-case slug, unique where it applies.')}
      ${field(html`<span class="flex justify-between">Description ${count(f.description, 200)}</span>`,
        html`<input class=${inputCls} value=${f.description} onInput=${(e) => set('description')(e.target.value)} placeholder="Owner wants short replies without headings" />`,
        'The one line every thread sees. Say what it is and when it matters.')}
      ${field(html`<span class="flex justify-between">Body ${count(f.body, 4000)}</span>`,
        html`<textarea class=${cx(inputCls, 'h-36 py-2 resize-y leading-relaxed')} value=${f.body} onInput=${(e) => set('body')(e.target.value)}
          placeholder=${f.type === 'feedback' || f.type === 'project' ? 'The fact.\n\nWhy: …\nHow to apply: …' : 'The fact, in a few sentences.'}></textarea>`,
        'One fact. Never a password or key — those are refused.')}
    </form>
  </${Dialog}>`;
}

const OPS = {
  add: { text: 'New memory', cls: 'bg-accent/10 text-accent' },
  update: { text: 'Update', cls: 'bg-raised text-fg border border-line' },
  stale: { text: 'Mark stale', cls: 'bg-warn/10 text-warn' },
};

// Suggestions lists what compacted threads suggested remembering, for the
// owner to accept, edit and accept, or dismiss.
function Suggestions({ onPending }) {
  const [status, setStatus] = useState('pending');
  const [list, setList] = useState({ status: 'loading', items: [], error: '' });
  const seq = useRef(0);
  const load = async (quiet) => {
    const mine = ++seq.current;
    if (!quiet) setList((l) => ({ ...l, status: 'loading' }));
    try {
      const r = await api.memorySuggestions({ status, limit: 100 });
      if (mine !== seq.current) return;
      setList({ status: 'ready', items: r.data, error: '' });
      onPending(r.pending);
    } catch (e) {
      if (mine !== seq.current) return;
      if (quiet) toast(errorText(e), 'error');
      else setList({ status: 'error', items: [], error: errorText(e) });
    }
  };
  useEffect(() => { load(); }, [status]);

  return html`<div>
    <div class="flex items-center justify-between gap-3 mb-3">
      <${Segmented} label="Suggestions" value=${status} options=${[['pending', 'Waiting'], ['decided', 'Decided']]} onChange=${setStatus} />
      <span class="text-xs text-faint">${list.status === 'ready' && (status === 'pending' ? `${list.items.length} waiting` : 'Kept 30 days')}</span>
    </div>
    ${list.status === 'loading' && html`<div class="flex flex-col gap-2">${[1, 2].map((i) => html`<div key=${i} class="skeleton h-[120px] rounded-xl"></div>`)}</div>`}
    ${list.status === 'error' && html`<${ErrorBlock} title="Could not load suggestions" message=${list.error} onRetry=${() => load()} />`}
    ${list.status === 'ready' && !list.items.length && html`<div class="rounded-xl border border-dashed border-line p-8 text-center">
      <${Icon} name="spark" size=${22} class="text-faint mx-auto" />
      <p class="font-medium mt-2">${status === 'pending' ? 'Nothing to review' : 'Nothing decided yet'}</p>
      <p class="text-muted text-[13px] mt-1 max-w-md mx-auto">${status === 'pending'
        ? 'When a thread compacts, what you said in the part that leaves its context is read for things worth remembering. They wait here until you decide.'
        : 'Suggestions you accept or dismiss are kept here for 30 days.'}</p>
    </div>`}
    <div class="flex flex-col gap-2">
      ${list.items.map((p) => html`<${SuggestionCard} key=${p.id} p=${p} onDecided=${() => load(true)} />`)}
    </div>
  </div>`;
}

function SuggestionCard({ p, onDecided }) {
  const s = useStore();
  const [busy, setBusy] = useState('');
  const [editing, setEditing] = useState(false);
  const [f, setF] = useState({ type: p.type, description: p.description, body: p.body, reason: p.reason });
  const where = scopeLabel(s, p.scope);
  const op = OPS[p.op] || OPS.add;
  const type = TYPES.find((t) => t.value === p.type);
  const pending = p.status === 'pending';
  const decide = async (action) => {
    setBusy(action);
    try {
      const edit = action === 'accept' && editing
        ? (p.op === 'stale' ? { reason: f.reason.trim() } : { type: f.type, description: f.description.trim(), body: f.body.trim() })
        : undefined;
      await api.decideMemorySuggestion(p.id, action, edit);
      toast(action === 'accept' ? `Saved ${p.name}.` : `Dismissed ${p.name}.`);
      onDecided();
    } catch (e) { toast(errorText(e), 'error'); setBusy(''); }
  };
  const count = (v, max) => html`<span class=${cx('tabular-nums', v.length > max && 'text-bad')}>${v.length}/${max}</span>`;

  return html`<div class=${cx('rounded-xl border bg-panel', p.changed ? 'border-warn/40' : 'border-line')}>
    <div class="px-4 pt-3 pb-2 flex flex-wrap items-center gap-x-2 gap-y-1">
      <span class=${cx('text-[11px] px-1.5 py-px rounded-md shrink-0', op.cls)}>${op.text}</span>
      <span class="font-mono text-[13px] min-w-0 truncate">${p.name}</span>
      ${p.op !== 'stale' && html`<span class="text-[11px] px-1.5 py-px rounded-md bg-raised border border-line text-muted shrink-0">${type?.text || p.type}</span>`}
      <span class=${cx('text-xs flex items-center gap-1 ml-auto', where.faint ? 'text-faint' : 'text-muted')}>
        <${Icon} name=${where.icon} size=${12} /><span class="max-w-36 truncate">${where.text}</span>
      </span>
    </div>
    <div class="px-4 pb-3 flex flex-col gap-2.5">
      ${editing ? html`<div class="flex flex-col gap-2">
          ${p.op === 'stale'
            ? html`<input class=${inputCls} aria-label="Why it is stale" value=${f.reason} onInput=${(e) => setF((x) => ({ ...x, reason: e.target.value }))} />`
            : html`
              <div class="flex gap-2 items-center">
                <${Select} label="Type" class="w-36" value=${f.type} onChange=${(v) => setF((x) => ({ ...x, type: v }))} options=${TYPES.map((t) => ({ value: t.value, text: t.text }))} />
                <span class="text-xs text-faint ml-auto">Description ${count(f.description, 200)}</span>
              </div>
              <input class=${inputCls} aria-label="Description" value=${f.description} onInput=${(e) => setF((x) => ({ ...x, description: e.target.value }))} />
              <textarea class=${cx(inputCls, 'h-28 py-2 resize-y leading-relaxed')} aria-label="Body" value=${f.body} onInput=${(e) => setF((x) => ({ ...x, body: e.target.value }))}></textarea>`}
        </div>`
        : p.op === 'stale' ? html`<div class="text-[13px]">
            <div class="text-muted">${p.target?.description || 'The memory is gone.'}</div>
            <div class="text-xs rounded-lg px-3 py-2 mt-1.5 bg-warn/10 text-warn">Would be marked stale: ${p.reason}</div>
          </div>`
        : p.op === 'update' && p.target ? html`<div class="grid sm:grid-cols-2 gap-2 text-[13px]">
            <div class="rounded-lg bg-raised px-3 py-2 min-w-0">
              <div class="text-[11px] text-faint mb-1">Now</div>
              <div class="text-muted">${p.target.description}</div>
              <div class="text-xs text-faint mt-1 whitespace-pre-wrap break-words line-clamp-4">${p.target.body}</div>
            </div>
            <div class="rounded-lg border border-accent/30 px-3 py-2 min-w-0">
              <div class="text-[11px] text-accent mb-1">Suggested</div>
              <div>${p.description}</div>
              <div class="text-xs text-muted mt-1 whitespace-pre-wrap break-words">${p.body}</div>
            </div>
          </div>`
        : html`<div class="text-[13px]">
            <div>${p.description}</div>
            <div class="text-xs text-muted mt-1 whitespace-pre-wrap break-words">${p.body}</div>
          </div>`}
      <figure class="border-l-2 border-line pl-3">
        <blockquote class="text-[13px] italic text-muted break-words">“${p.evidence}”</blockquote>
        <figcaption class="text-xs text-faint mt-0.5">You said, in <a class="text-accent hover:underline" href=${'#/c/' + encodeURIComponent(p.conversationId)}>
          ${s.convs.items.find((c) => c.id === p.conversationId)?.title || 'a thread'}</a> · ${relTime(p.createdAt)}</figcaption>
      </figure>
      ${p.similar?.length > 0 && html`<div class="text-xs text-muted flex flex-wrap items-center gap-1.5">
        Close to: ${p.similar.map((m) => html`<span key=${m.id} class="font-mono px-1.5 rounded bg-raised border border-line" title=${m.description}>${m.name}</span>`)}
        <span class="text-faint">— maybe edit one of those instead.</span>
      </div>`}
      ${p.changed && html`<p class="text-xs text-warn flex gap-1.5"><${Icon} name="alert" size=${13} class="shrink-0 mt-px" />
        The memory was changed or forgotten since this was suggested, so it cannot be accepted. Dismiss it.</p>`}
      ${pending ? html`<div class="flex flex-wrap items-center gap-1.5 pt-1">
          <${Button} kind="primary" busy=${busy === 'accept'} disabled=${p.changed || !!busy} onClick=${() => decide('accept')}>
            <${Icon} name="check" size=${13} /> ${editing ? 'Save and accept' : 'Accept'}</${Button}>
          ${!editing && !p.changed && html`<${Button} kind="ghost" onClick=${() => setEditing(true)}><${Icon} name="pencil" size=${13} /> Edit first</${Button}>`}
          ${editing && html`<${Button} kind="ghost" onClick=${() => setEditing(false)}>Cancel edit</${Button}>`}
          <${Button} kind="ghost" class="ml-auto" busy=${busy === 'dismiss'} disabled=${!!busy} onClick=${() => decide('dismiss')}><${Icon} name="x" size=${13} /> Dismiss</${Button}>
        </div>`
        : html`<div class="text-xs text-faint">${p.status === 'accepted' ? 'Accepted' : 'Dismissed'} ${relTime(p.decidedAt)}</div>`}
    </div>
  </div>`;
}
