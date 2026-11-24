// Composer pickers: model and workspace. The model picker lists every
// provider side by side — API providers and ACP agents — each with its
// models, so a room can move between them in one click. Provider models add
// effort and upstream choices; agents add the session options they report
// (modes such as plan/build/bypass, and their own config). The product mode
// hides the runtime it leaves out.
import { html, useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { toast } from '../lib/store.js';
import { selection, setSelection, loadProviders, loadAgents, loadAgentModels } from '../lib/chat.js';
import { Icon } from './icons.js';
import { FolderBrowser } from './folders.js';
import { cx, Popover, Spinner, ErrorBlock, Select, fmtTokens } from './ui.js';
import { editProject } from './projects.js';

const ALL_EFFORTS = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'];
const upstreamCache = new Map();

// agentOptionValue is what a control shows: the room's own pick when the
// thread exists (the agent may have changed it mid-thread), else a choice
// staged for the thread this message will start, else the agent's default.
function agentOptionValue(room, o) {
  if (room.id) return o.current || '';
  const staged = room.sel?.agentOptions || {};
  return staged[o.id] ?? o.current ?? '';
}

// setAgentOption applies a choice to the room, or stages it when no room
// exists yet — a fresh thread takes staged options with its first turn, so
// a mode picked here is the one the agent starts in.
async function setAgentOption(room, o, value, setOpts) {
  if (!room.id) {
    setSelection(room, { agentOptions: { ...(room.sel?.agentOptions || {}), [o.id]: value } });
    return;
  }
  try {
    const res = await api.setAgentOption(room.id, o.id, value);
    setOpts({ status: 'ready', list: res.data || [] });
  } catch (e) {
    toast('Could not set ' + o.name + ': ' + errorText(e), 'error');
  }
}

// agentModelLabel names an agent choice: its model when one is picked.
function agentModelLabel(s, agentId, modelId) {
  const a = s.agents.items.find((x) => x.id === agentId);
  const m = s.agentModels[agentId]?.list.find((x) => x.id === modelId);
  const name = a?.name || agentId;
  return modelId ? `${m?.name || modelId}` : name;
}

export function ModelPicker({ room }) {
  const s = useStore();
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  const [upstreams, setUpstreams] = useState({ status: 'idle', list: [] });
  const sel = selection(room);
  const provs = s.providers;
  const agents = s.agents;
  const cur = sel.agentId ? null : provs.items.find((g) => g.id === sel.providerId)?.models.find((m) => m.id === sel.modelId);

  // Agents report their models from a live session; ask each once the
  // picker opens.
  useEffect(() => {
    if (!open) return;
    for (const a of agents.items) loadAgentModels(a.id);
  }, [open, agents.items.length]);
  // An agent room names its model in the button; fetch the names for it.
  useEffect(() => { if (sel.agentId) loadAgentModels(sel.agentId); }, [sel.agentId]);
  const efforts = cur?.supportedEfforts?.length ? cur.supportedEfforts : ALL_EFFORTS;

  // Agent session options: every agent defines its own set — Claude Code's
  // plan/accept-edits/bypass modes, Devin's, and whatever config each
  // offers — so the controls are generated from what the agent reports
  // rather than from a list we would have to keep in step.
  const [agentOpts, setAgentOpts] = useState({ status: 'idle', list: [] });
  useEffect(() => {
    if (!open || !sel.agentId) return;
    let live = true;
    setAgentOpts((o) => ({ status: o.list.length ? 'ready' : 'loading', list: o.list }));
    const req = room.id ? api.agentOptions(room.id) : api.agentOptionsFor(sel.agentId);
    req.then(
      (res) => { if (live) setAgentOpts({ status: 'ready', list: res.data || [] }); },
      (e) => { if (live) setAgentOpts({ status: 'error', list: [], error: errorText(e) }); },
    );
    return () => { live = false; };
  }, [open, sel.agentId, room.id]);

  useEffect(() => {
    if (!open || sel.agentId || !sel.providerId || !sel.modelId) return;
    const key = sel.providerId + '|' + sel.modelId;
    if (upstreamCache.has(key)) { setUpstreams({ status: 'ready', list: upstreamCache.get(key) }); return; }
    setUpstreams({ status: 'loading', list: [] });
    let live = true;
    api.upstreams(sel.providerId, sel.modelId).then((r) => {
      upstreamCache.set(key, r.data || []);
      if (live) setUpstreams({ status: 'ready', list: r.data || [] });
    }, () => live && setUpstreams({ status: 'error', list: [] }));
    return () => { live = false; };
  }, [open, sel.providerId, sel.modelId]);

  const needle = q.trim().toLowerCase();
  const hit = (...xs) => !needle || xs.some((x) => String(x || '').toLowerCase().includes(needle));
  const groups = provs.items.map((g) => ({
    kind: 'provider', id: g.id, name: g.name || g.id, broken: g.broken,
    models: g.models.filter((m) => m.listed !== false && hit(m.name, m.id, g.id, g.name)),
  })).filter((x) => x.models.length);
  for (const a of agents.items) {
    const st = s.agentModels[a.id] || { status: 'idle', list: [] };
    // An agent with no model choice still runs: its default is the row.
    const list = st.status === 'ready' && !st.list.length ? [{ id: '', name: 'Agent default' }] : st.list;
    const models = list.filter((m) => hit(m.name, m.id, a.id, a.name));
    if (!needle || models.length || st.status !== 'ready') groups.push({ kind: 'agent', id: a.id, name: a.name || a.id, status: st.status, error: st.error, models });
  }
  const loading = provs.status === 'loading' || agents.status === 'loading';
  const nothing = !loading && provs.status === 'ready' && !provs.items.length && agents.status === 'ready' && !agents.items.length;

  const label = sel.agentId ? agentModelLabel(s, sel.agentId, sel.modelId) : cur ? (cur.name || cur.id) : sel.modelId || 'Choose model';
  const chosen = !!(sel.agentId || sel.modelId);
  return html`<div class="relative min-w-0">
    <button type="button" onClick=${() => setOpen(!open)} aria-haspopup="dialog" aria-expanded=${open}
      class=${cx('flex min-w-0 items-center gap-1.5 h-8 px-2.5 rounded-lg text-[13px] hover:bg-hover max-w-[44vw] sm:max-w-64', chosen ? 'text-fg' : 'text-warn')}>
      ${sel.agentId && html`<${Icon} name="bot" size=${14} class="text-faint shrink-0" />`}
      <span class="truncate">${label}</span>
      ${!sel.agentId && sel.effort && sel.effort !== 'default' && html`<span class="text-faint shrink-0">${sel.effort}</span>`}
      <${Icon} name="chevronDown" size=${14} class="text-faint" />
    </button>
    <${Popover} open=${open} onClose=${() => setOpen(false)} placement="top-end" label="Choose model" class="sm:w-[400px] flex flex-col">
      <div class="shrink-0 sticky top-0 z-10 bg-raised flex items-center gap-2 px-3 h-11 border-b border-line">
        <${Icon} name="search" class="text-faint" />
        <input autofocus value=${q} onInput=${(e) => setQ(e.target.value)} placeholder="Search models"
          class="flex-1 bg-transparent outline-none text-[13px]" />
      </div>
      <div class=${cx('flex-1 min-h-[5.5rem] overflow-y-auto py-1', cur || sel.agentId ? 'max-h-[min(15rem,32dvh)]' : 'max-h-[min(20rem,40dvh)]')}>
        ${loading && !groups.length && html`<div class="flex items-center gap-2 px-3 py-3 text-muted"><${Spinner} /> Loading providers…</div>`}
        ${provs.status === 'error' && html`<div class="p-2"><${ErrorBlock} title="Providers unavailable" message=${provs.error} onRetry=${loadProviders} /></div>`}
        ${agents.status === 'error' && html`<div class="p-2"><${ErrorBlock} title="Agents unavailable" message=${agents.error} onRetry=${loadAgents} /></div>`}
        ${nothing && html`<div class="px-3 py-4 text-muted">
          Nothing to chat with yet. <a class="text-accent underline" href="#/" onClick=${() => setOpen(false)}>Set one up</a>.
        </div>`}
        ${!loading && !nothing && !groups.length && html`<div class="px-3 py-4 text-muted">No model matches “${q}”.</div>`}
        ${groups.map((grp) => html`<div key=${grp.kind + ':' + grp.id}>
          <div class="px-3 pt-2 pb-1 text-[11px] text-faint flex items-center gap-1.5">
            <span class="truncate">${grp.name}</span>
            <span class="shrink-0 rounded px-1 border border-line text-[10px] leading-4">${grp.kind === 'agent' ? 'ACP' : 'API'}</span>
            ${grp.broken && html`<span class="text-bad">· broken</span>`}
            ${grp.status === 'loading' && html`<${Spinner} size=${11} />`}
          </div>
          ${grp.status === 'loading' && !grp.models.length && html`<div class="px-3 h-8 flex items-center text-faint text-[12px]">Starting agent…</div>`}
          ${grp.status === 'error' && html`<div class="px-3 py-1.5 text-[12px] text-bad flex items-start gap-2">
            <span class="flex-1 break-words">${grp.error}</span>
            <button type="button" class="text-accent shrink-0" onClick=${() => loadAgentModels(grp.id, true)}>Retry</button>
          </div>`}
          ${grp.models.map((m) => {
            const active = grp.kind === 'agent' ? sel.agentId === grp.id && (sel.modelId || '') === m.id : !sel.agentId && grp.id === sel.providerId && m.id === sel.modelId;
            const pick = grp.kind === 'agent'
              ? () => setSelection(room, { agentId: grp.id, providerId: '', modelId: m.id, effort: 'default', upstream: '' })
              : () => setSelection(room, { agentId: '', providerId: grp.id, modelId: m.id, effort: 'default', upstream: '' });
            return html`<button key=${m.id || 'default'} type="button" onClick=${pick}
              class=${cx('w-full flex items-center gap-2 px-3 h-8 text-left hover:bg-hover', active && 'bg-hover')}>
              <span class="flex-1 truncate">${m.name || m.id}</span>
              ${m.contextWindow > 0 && html`<span class="text-[11px] text-faint">${fmtTokens(m.contextWindow)}</span>`}
              ${active && html`<${Icon} name="check" size=${14} class="text-accent" />`}
            </button>`;
          })}
        </div>`)}
      </div>
      ${cur && html`<div class="shrink-0 border-t border-line px-3 py-2.5 grid grid-cols-2 gap-x-3 gap-y-2">
        <label class="flex flex-col gap-1 min-w-0 only-of-type:col-span-full">
          <span class="text-xs text-muted truncate">Reasoning effort</span>
          <${Select} dense label="Reasoning effort" class="w-full" value=${sel.effort || 'default'}
            options=${[{ value: 'default', text: 'Default' }, ...efforts.map((e) => ({ value: e, text: e[0].toUpperCase() + e.slice(1) }))]}
            onChange=${(v) => setSelection(room, { effort: v })} />
        </label>
        ${upstreams.status === 'ready' && upstreams.list.length > 0 && html`<label class="flex flex-col gap-1 min-w-0">
          <span class="text-xs text-muted truncate">Upstream</span>
          <${Select} dense label="Upstream" class="w-full" search value=${sel.upstream || ''}
            options=${[{ value: '', text: 'Auto' }, ...upstreams.list.map((p) => ({ value: p.slug, text: p.name }))]}
            onChange=${(v) => setSelection(room, { upstream: v })} />
        </label>`}
        ${upstreams.status === 'loading' && html`<div class="col-span-2 flex items-center gap-2 text-faint text-xs"><${Spinner} size=${12} /> Checking upstreams…</div>`}
      </div>`}
      ${sel.agentId && html`<div class="shrink-0 border-t border-line px-3 py-2.5 flex flex-col gap-2">
        ${agentOpts.status === 'loading' && html`<div class="flex items-center gap-2 text-faint text-xs"><${Spinner} size=${12} /> Asking the agent…</div>`}
        ${agentOpts.status === 'error' && html`<p class="text-bad text-xs">${agentOpts.error}</p>`}
        ${agentOpts.status === 'ready' && !agentOpts.list.length && html`<p class="text-faint text-xs">This agent offers no session options.</p>`}
        ${agentOpts.list.length > 0 && html`<div class="grid grid-cols-2 gap-x-3 gap-y-2">
          ${agentOpts.list.map((o) => html`<label class="flex flex-col gap-1 min-w-0 only-of-type:col-span-full" key=${o.id}>
            <span class="text-xs text-muted truncate" title=${o.description || o.name}>${o.name}</span>
            ${o.type === 'boolean'
              ? html`<${Select} dense label=${o.name} class="w-full" value=${agentOptionValue(room, o)}
                  options=${[{ value: 'true', text: 'On' }, { value: 'false', text: 'Off' }]}
                  onChange=${(v) => setAgentOption(room, o, v, setAgentOpts)} />`
              : html`<${Select} dense label=${o.name} class="w-full" search=${(o.choices || []).length > 8} value=${agentOptionValue(room, o)}
                  options=${(o.choices || []).map((c) => ({ value: c.value, text: c.name || c.value }))}
                  onChange=${(v) => setAgentOption(room, o, v, setAgentOpts)} />`}
          </label>`)}
        </div>`}
        ${!room.id && agentOpts.list.length > 0 && html`<p class="text-[11px] text-faint">Applies to the thread this message starts.</p>`}
      </div>`}
      <div class="shrink-0 sticky bottom-0 z-10 bg-raised flex border-t border-line">
        <a href=${'#/settings/providers' + (sel.providerId ? '/' + encodeURIComponent(sel.providerId) : '')} onClick=${() => setOpen(false)}
          class="flex-1 flex items-center gap-2 px-3 h-9 text-muted hover:text-fg hover:bg-hover">
          <${Icon} name="server" size=${14} /> Providers
        </a>
        <a href="#/settings/agents" onClick=${() => setOpen(false)}
          class="flex-1 flex items-center gap-2 px-3 h-9 text-muted hover:text-fg hover:bg-hover border-l border-line">
          <${Icon} name="bot" size=${14} /> Agents
        </a>
      </div>
    </${Popover}>
  </div>`;
}

const base = (p) => (p ? p.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || p : '');

export function WorkspacePicker({ room }) {
  const s = useStore();
  const [open, setOpen] = useState(false);
  const sel = selection(room);
  // A room in a project works in the project's folders; the chip names the
  // project and opens its editor instead of a folder browser.
  const projectId = room.id ? room.info?.projectId : s.newProjectId;
  const project = projectId && s.projects.items.find((p) => p.id === projectId);
  if (project) {
    return html`<button type="button" onClick=${() => editProject(project)} title=${project.folders.join('\n')}
      class="flex items-center gap-1.5 h-8 px-2.5 rounded-lg text-[13px] text-muted hover:text-fg hover:bg-hover max-w-[40vw] sm:max-w-56">
      <${Icon} name="layers" size=${15} /><span class="truncate">${project.name}</span>
    </button>`;
  }
  return html`<div class="relative">
    <button type="button" onClick=${() => setOpen(!open)} aria-haspopup="dialog" aria-expanded=${open} title=${sel.workspace || 'Choose a working directory'}
      class="flex items-center gap-1.5 h-8 px-2.5 rounded-lg text-[13px] text-muted hover:text-fg hover:bg-hover max-w-[40vw] sm:max-w-56">
      <${Icon} name="folder" size=${15} />
      <span class="truncate">${base(sel.workspace) || 'Workspace'}</span>
    </button>
    <${Popover} open=${open} onClose=${() => setOpen(false)} placement="top-start" label="Working directory" class="sm:w-[420px] flex flex-col">
      ${open && html`<${FolderBrowser} start=${sel.workspace || ''} title="Working directory"
        onPick=${(path) => { setSelection(room, { workspace: path }); setOpen(false); }} onCancel=${() => setOpen(false)} />`}
    </${Popover}>
  </div>`;
}

// fmtCost writes an agent's reported session cost in its own currency; a code
// the browser does not know is shown as given.
function fmtCost({ amount, currency }) {
  try {
    return new Intl.NumberFormat(undefined, { style: 'currency', currency, maximumFractionDigits: amount < 1 ? 4 : 2 }).format(amount);
  } catch { return `${amount} ${currency}`; }
}

export function ContextMeter({ room }) {
  const c = room.ctx;
  if (!c || !c.total) return null;
  const used = c.live ?? c.estimate ?? 0;
  const pct = Math.min(100, Math.round((used / c.total) * 100));
  const r = 7, len = 2 * Math.PI * r;
  const color = pct >= 90 ? 'var(--bad)' : pct >= 70 ? 'var(--warn)' : 'var(--accent)';
  const text = `${pct}% (${fmtTokens(used)} / ${fmtTokens(c.total)}) context used${c.live != null ? ' · live' : ' · estimated'}${c.cost ? ' · ' + fmtCost(c.cost) : ''}`;
  return html`<span class="group relative inline-flex items-center justify-center size-8" tabindex="0" aria-label=${text}>
    <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true">
      <circle cx="9" cy="9" r=${r} fill="none" stroke="var(--line)" stroke-width="2.5" />
      <circle cx="9" cy="9" r=${r} fill="none" stroke=${color} stroke-width="2.5" stroke-linecap="round"
        stroke-dasharray=${`${(pct / 100) * len} ${len}`} transform="rotate(-90 9 9)" />
    </svg>
    <span role="tooltip" class="pointer-events-none absolute bottom-full right-0 mb-2 whitespace-nowrap rounded-lg border border-line bg-raised px-2.5 py-1.5 text-xs shadow-pop opacity-0 group-hover:opacity-100 group-focus:opacity-100 transition-opacity">${text}</span>
  </span>`;
}
