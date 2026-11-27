// Composer pickers: model and workspace. The model picker lists every
// provider side by side — API providers and ACP agents — each with its
// models, so a room can move between them in one click. Provider models add
// effort and upstream choices; agents add the session options they report
// (modes such as plan/build/bypass, and their own config). The product mode
// hides the runtime it leaves out.
import { html, useEffect, useMemo, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { state, useStore } from '../lib/store.js';
import { api } from '../lib/api.js';
import { selection, setSelection, loadProviders, loadAgents, loadAgentModels, isBusy, loadAgentOptions, sessionOptionValue, setSessionOption, agentOptionMap } from '../lib/chat.js';
import { Icon } from './icons.js';
import { AgentOptionsNotice } from './agentbar.js';
import { FolderBrowser } from './folders.js';
import { cx, Popover, Spinner, ErrorBlock, Select, fmtTokens, usePopoverOpen, usePopoverTrigger } from './ui.js';
import { editProject } from './projects.js';

const ALL_EFFORTS = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'];
const upstreamCache = new Map();

// upstreamControl decides what the upstream select renders from the loader
// state. Once a model's list ever came back non-empty (`had`), the control
// stays mounted for the rest of the panel session — across picks, refetches,
// and the frame between a pick and its effect — because an unmounted select
// is the flicker an owner reads as the popup closing and reopening. Its
// content stays honest instead: the list renders only for the key it was
// fetched for, and a load or a failure never keeps the previous model's
// list. A failure mounts the control even before any list has arrived, so
// the owner can retry; a load or an empty answer before that first list
// still hides it.
export const upstreamControl = (up, key) => ({
  show: !!up.had || (up.status === 'error' && up.key === key),
  list: up.key === key && up.status !== 'loading' && up.status !== 'error' ? up.list || [] : [],
  loading: up.status === 'loading' && up.key === key,
  error: up.status === 'error' && up.key === key,
});

// catalogDefaultLine is the default an unknown catalog names. The words
// match the read-only model list: unset, the supplier in parentheses, or
// the id and who supplies it.
function catalogDefaultLine(d) {
  if (!d || !d.by || d.by === 'unset') return 'default: unset';
  if (!d.id) return `default: (${d.by})`;
  return `default: ${d.id} (${d.by})`;
}

// agentModelLabel names an agent choice: the picked model when there is
// one, else the model the agent itself would run — the one it marks
// current. Until the list says which that is, the button names the agent
// and the state instead of lending the agent's name to a model. An unknown
// catalog names its default when that default has an id.
export function agentModelLabel(s, agentId, modelId) {
  const name = s.agents.items.find((x) => x.id === agentId)?.name || agentId;
  const st = s.agentModels[agentId];
  if (modelId) {
    const m = st?.list.find((x) => x.id === modelId);
    return m?.name || modelId;
  }
  const cur = st?.list.find((x) => x.current);
  if (cur) return cur.name || cur.id;
  if (st?.report === 'unknown' && (st.default?.name || st.default?.id)) return st.default.name || st.default.id;
  return `${name} · default`;
}

export function ModelPicker({ room }) {
  const s = useStore();
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  const bodyRef = useRef();
  // A changed filter is a new target: the list starts at its top. Anything
  // else that re-renders the picker — a store tick, effort, upstream — leaves
  // the scroll where the owner put it. The rows follow the same rule.
  useEffect(() => { if (bodyRef.current) bodyRef.current.scrollTop = 0; }, [q]);
  const [upstreams, setUpstreams] = useState({ status: 'idle', list: [] });
  // A failed read is not cached, and the provider/model key does not change
  // when Retry is pressed, so the effect needs its own token to run again.
  const [upToken, setUpToken] = useState(0);
  const sel = selection(room);
  const provs = s.providers;
  const agents = s.agents;
  const cur = sel.runtime === 'agent' ? null : provs.items.find((g) => g.id === sel.providerId)?.models.find((m) => m.id === sel.modelId);

  // Agents report their models from a live session; ask each once the
  // picker opens.
  useEffect(() => {
    if (!open) return;
    for (const a of agents.items) loadAgentModels(a.id);
  }, [open, agents.items.length]);
  // An agent room names its model in the button; fetch the names for it.
  useEffect(() => { if (sel.runtime === 'agent' && sel.agentId) loadAgentModels(sel.agentId); }, [sel.runtime, sel.agentId]);
  const efforts = cur?.supportedEfforts?.length ? cur.supportedEfforts : ALL_EFFORTS;
  const upCtl = upstreamControl(upstreams, sel.providerId + '|' + sel.modelId);

  // Agent session options: every agent defines its own set — Claude Code's
  // plan/accept-edits/bypass modes, Devin's, and whatever config each
  // offers — so the controls are generated from what the agent reports
  // rather than from a list we would have to keep in step.
  // The session options are the room's list (lib/chat.js), the same one the
  // composer's chip reads: opening this panel only asks the shared loader, so
  // a pick made in either control shows in both.
  // The picker's agent section answers the same way as the options panel: a failed
  // read says Retry (AgentOptionsNotice), an empty answer says Refresh. The labels
  // stay apart on purpose; see the comment in agentbar.js.
  const agentOpts = room.agentOptions;
  // A pick while a turn runs is stored and applies to the next one — the
  // controls stay open and say so, instead of refusing a change the room
  // can already accept.
  const busy = isBusy(room);
  useEffect(() => {
    if (!open || sel.runtime !== 'agent') return;
    loadAgentOptions(room);
  }, [open, sel.runtime, room.id]);

  useEffect(() => {
    if (!open || sel.runtime === 'agent' || !sel.providerId || !sel.modelId) return;
    const key = sel.providerId + '|' + sel.modelId;
    if (upstreamCache.has(key)) {
      const list = upstreamCache.get(key);
      setUpstreams((u) => ({ status: 'ready', key, list, had: u.had || list.length > 0 }));
      return;
    }
    setUpstreams((u) => ({ status: 'loading', key, list: [], had: u.had }));
    let live = true;
    api.upstreams(sel.providerId, sel.modelId).then((r) => {
      const list = r.data || [];
      upstreamCache.set(key, list);
      if (live) setUpstreams((u) => ({ status: 'ready', key, list, had: u.had || list.length > 0 }));
    }, () => { if (live) setUpstreams((u) => ({ status: 'error', key, list: [], had: u.had })); });
    return () => { live = false; };
  }, [open, sel.providerId, sel.modelId, upToken]);

  const needle = q.trim().toLowerCase();
  const hit = (...xs) => !needle || xs.some((x) => String(x || '').toLowerCase().includes(needle));
  const groups = provs.items.map((g) => ({
    kind: 'provider', id: g.id, name: g.name || g.id, broken: g.broken,
    models: g.models.filter((m) => m.listed !== false && hit(m.name, m.id, g.id, g.name)),
  })).filter((x) => x.models.length);
  for (const a of agents.items) {
    const st = s.agentModels[a.id] || { status: 'idle', list: [] };
    // unknown is an answer with no rows. A data array on that body is not
    // the catalog, and it must not become the empty-list row.
    const unknown = st.report === 'unknown';
    const models = (unknown ? [] : st.list || []).filter((m) => hit(m.name, m.id, a.id, a.name));
    // An agent whose list came back empty still runs — on a model only it
    // can name. The row says so instead of offering a fake model. That is
    // a known empty catalog, not an unknown one.
    const bare = st.status === 'ready' && !unknown && !(st.list || []).length;
    const shown = !needle || models.length || st.status !== 'ready' || (bare && hit(a.id, a.name))
      || (unknown && hit(a.id, a.name, st.reason, st.default?.id, st.default?.name, st.default?.by));
    if (shown) {
      groups.push({
        kind: 'agent', id: a.id, name: a.name || a.id, status: st.status, error: st.error, models, bare, unknown,
        reason: st.reason || '', deflt: st.default || null,
      });
    }
  }
  const loading = provs.status === 'loading' || agents.status === 'loading';
  const nothing = !loading && provs.status === 'ready' && !provs.items.length && agents.status === 'ready' && !agents.items.length;
  // The open list is rebuilt only when the model ids or their order change,
  // or the checked row does. A store tick that leaves those alone keeps the
  // same row nodes, so the scroll is not thrown back to the top.
  const listKey = groups.map((g) => g.kind + ':' + g.id + '=' + g.models.map((m) => m.id).join(',')
    + (g.unknown ? '|unknown:' + (g.deflt?.by || '') + ':' + (g.deflt?.id || '') + ':' + (g.reason || '') : '')
    + (g.bare ? '|bare' : '')).join(';')
    + '>' + (sel.agentId || '') + '/' + (sel.providerId || '') + '/' + (sel.modelId || '');
  const modelRows = useMemo(() => groups.map((grp) => html`<div key=${grp.kind + ':' + grp.id}>
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
            // An agent room without a stored model runs the one the agent
            // marks current: that row carries the check, so the pick the
            // button names is the same one the list shows.
            const active = grp.kind === 'agent'
              ? sel.runtime === 'agent' && sel.agentId === grp.id && (sel.modelId ? sel.modelId === m.id : !!m.current)
              : sel.runtime === 'harness' && grp.id === sel.providerId && m.id === sel.modelId;
            const pick = grp.kind === 'agent'
              ? () => setSelection(room, { runtime: 'agent', agentId: grp.id, providerId: '', modelId: m.id, effort: 'default', upstream: '' })
              : () => setSelection(room, { runtime: 'harness', agentId: '', providerId: grp.id, modelId: m.id, effort: 'default', upstream: '' });
            return html`<button key=${m.id || 'default'} type="button" onClick=${pick}
              class=${cx('w-full flex items-center gap-2 px-3 h-8 text-left hover:bg-hover', active && 'bg-hover')}>
              <span class="flex-1 truncate">${m.name || m.id}</span>
              ${m.contextWindow > 0 && html`<span class="text-[11px] text-faint">${fmtTokens(m.contextWindow)}</span>`}
              ${active && html`<${Icon} name="check" size=${14} class="text-accent" />`}
            </button>`;
          })}
          ${grp.unknown && html`<div class="px-3 py-1.5 text-[12px] text-muted flex flex-col gap-0.5">
            <span>unknown</span>
            <span>${catalogDefaultLine(grp.deflt)}</span>
            ${grp.reason ? html`<span class="break-words">${grp.reason}</span>` : null}
          </div>`}
          ${grp.bare && html`<button type="button"
            onClick=${() => setSelection(room, { runtime: 'agent', agentId: grp.id, providerId: '', modelId: '', effort: 'default', upstream: '' })}
            class=${cx('w-full flex items-center gap-2 px-3 h-8 text-left hover:bg-hover', sel.runtime === 'agent' && sel.agentId === grp.id && !sel.modelId && 'bg-hover')}>
            <span class="flex-1 truncate text-muted">This agent lists no models; it runs its own choice</span>
            ${sel.runtime === 'agent' && sel.agentId === grp.id && !sel.modelId && html`<${Icon} name="check" size=${14} class="text-accent" />`}
          </button>`}
        </div>`), [listKey]);

  const press = usePopoverTrigger(open, setOpen);
  const label = sel.runtime === 'agent' ? agentModelLabel(s, sel.agentId, sel.modelId) : cur ? (cur.name || cur.id) : sel.modelId || 'Choose model';
  const chosen = sel.runtime === 'agent' || !!(sel.runtime === 'harness' && sel.modelId);
  // The record's pending is the pick that applies to the next turn — the
  // chip names it already, and this small marker says it is not running yet.
  const pending = room.info?.pending;
  const pendOpts = agentOptionMap(pending?.options);
  return html`<div class="relative min-w-0">
    <button type="button" onPointerDownCapture=${press.onPointerDownCapture} onClick=${press.onClick} aria-haspopup="dialog" aria-expanded=${open}
      class=${cx('flex min-w-0 items-center gap-1.5 h-8 px-2.5 rounded-lg text-[13px] hover:bg-hover max-w-[44vw] sm:max-w-64', chosen ? 'text-fg' : 'text-warn')}>
      ${sel.runtime === 'agent' && html`<${Icon} name="bot" size=${14} class="text-faint shrink-0" />`}
      <span class="truncate">${label}</span>
      ${sel.runtime === 'harness' && sel.effort && sel.effort !== 'default' && html`<span class="text-faint shrink-0">${sel.effort}</span>`}
      ${(pending?.runtime === 'agent' || pending?.runtime === 'harness') && html`<span class="text-faint shrink-0 text-[11px]">next turn</span>`}
      <${Icon} name="chevronDown" size=${14} class="text-faint" />
    </button>
    <${Popover} open=${open} onClose=${() => setOpen(false)} layer="top" placement="top-end" label="Choose model" scroll="list" class="sm:w-[400px]">
      <div class="shrink-0 bg-raised flex items-center gap-2 px-3 h-11 border-b border-line">
        <${Icon} name="search" class="text-faint" />
        <input autofocus value=${q} onInput=${(e) => setQ(e.target.value)} placeholder="Search models"
          class="flex-1 bg-transparent outline-none text-[13px]" />
      </div>
      <div ref=${bodyRef} data-scroll="list" class="flex-1 min-h-0 overflow-y-auto">
      <div class="py-1">
        ${loading && !groups.length && html`<div class="flex items-center gap-2 px-3 py-3 text-muted"><${Spinner} /> Loading providers…</div>`}
        ${provs.status === 'error' && html`<div class="p-2"><${ErrorBlock} title="Providers unavailable" message=${provs.error} onRetry=${loadProviders} /></div>`}
        ${agents.status === 'error' && html`<div class="p-2"><${ErrorBlock} title="Agents unavailable" message=${agents.error} onRetry=${loadAgents} /></div>`}
        ${nothing && html`<div class="px-3 py-4 text-muted">
          Nothing to chat with yet. <a class="text-accent underline" href="#/" onClick=${() => setOpen(false)}>Set one up</a>.
        </div>`}
        ${!loading && !nothing && !groups.length && html`<div class="px-3 py-4 text-muted">${q.trim() ? `No model matches “${q}”.` : 'No models are listed for these providers.'}</div>`}
        ${modelRows}
      </div>
      </div>
      ${(cur || sel.runtime === 'agent') && html`<div data-scroll="controls" class="shrink-0 min-h-0 overflow-y-auto border-t border-line bg-raised" style=${{ maxHeight: 'calc(var(--panel-room) * 0.4)' }}>
      ${cur && html`<div class="px-3 py-2.5 grid grid-cols-2 gap-x-3 gap-y-2">
        <label class="flex flex-col gap-1 min-w-0 only-of-type:col-span-full">
          <span class="text-xs text-muted truncate">Reasoning effort</span>
          <${Select} dense label="Reasoning effort" class="w-full" value=${sel.effort || 'default'}
            options=${[{ value: 'default', text: 'Default' }, ...efforts.map((e) => ({ value: e, text: e[0].toUpperCase() + e.slice(1) }))]}
            onChange=${(v) => setSelection(room, { effort: v })} />
        </label>
        ${upCtl.show && html`<label class="flex flex-col gap-1 min-w-0">
          <span class="text-xs text-muted truncate flex items-center gap-1.5">Upstream ${upCtl.loading && html`<${Spinner} size=${11} class="text-faint" />`}</span>
          <${Select} dense label="Upstream" class="w-full" search value=${sel.upstream || ''}
            options=${[{ value: '', text: 'Auto' }, ...upCtl.list.map((p) => ({ value: p.slug, text: p.name }))]}
            onChange=${(v) => setSelection(room, { upstream: v })} />
          ${upCtl.error && html`<span class="flex items-center gap-2 text-[11px]">
            <span class="flex-1 text-bad">Upstreams unavailable</span>
            <button type="button" class="text-accent shrink-0" onClick=${() => setUpToken((n) => n + 1)}>Retry</button>
          </span>`}
        </label>`}
      </div>`}
      ${sel.runtime === 'agent' && html`<div class="px-3 py-2.5 flex flex-col gap-2">
        ${agentOpts.status === 'loading' && html`<div class="flex items-center gap-2 text-faint text-xs"><${Spinner} size=${12} /> Asking the agent…</div>`}
        <${AgentOptionsNotice} room=${room} />
        ${agentOpts.status === 'ready' && !agentOpts.list.length && (agentOpts.report === 'empty'
          ? html`<div class="flex items-center gap-2 text-faint text-xs">
              <span class="flex-1">The agent reports no options right now.</span>
              <button type="button" class="text-accent shrink-0" onClick=${() => loadAgentOptions(room, { force: true })}>Refresh</button>
            </div>`
          : html`<p class="text-faint text-xs">This agent offers no session options.</p>`)}
        ${agentOpts.list.length > 0 && html`<div class="grid grid-cols-2 gap-x-3 gap-y-2">
          ${agentOpts.list.map((o) => html`<label class="flex flex-col gap-1 min-w-0 only-of-type:col-span-full" key=${o.id}>
            <span class="flex items-center gap-1.5 text-xs text-muted min-w-0" title=${o.description || o.name}>
              <span class="truncate">${o.name}</span>
              ${pendOpts[o.id] !== undefined && html`<span class="text-[11px] text-faint shrink-0">next turn</span>`}
            </span>
            ${o.deferred != null
              // A stored pick the session does not offer: shown as itself,
              // never as the active choice, and kept for when it appears.
              ? html`<${Select} dense label=${o.name} class="w-full" disabled value=${o.deferred}
                    options=${[{ value: o.deferred, text: o.deferred }]} />
                  <span class="text-[11px] text-faint">Not offered — saved for when it is.</span>`
              : o.type === 'boolean'
                ? html`<${Select} dense label=${o.name} class="w-full" value=${sessionOptionValue(room, o)}
                    options=${[{ value: 'true', text: 'On' }, { value: 'false', text: 'Off' }]}
                    onChange=${(v) => setSessionOption(room, o.id, v)} />`
                : html`<${Select} dense label=${o.name} class="w-full" search=${(o.choices || []).length > 8} value=${sessionOptionValue(room, o)}
                    options=${(o.choices || []).map((c) => ({ value: c.value, text: c.name || c.value }))}
                    onChange=${(v) => setSessionOption(room, o.id, v)} />`}
          </label>`)}
        </div>`}
        ${agentOpts.list.length > 0 && html`<p class="text-[11px] text-faint">${busy || Object.keys(pendOpts).length
          ? 'Applies to the next turn.'
          : !room.id ? 'Applies to the thread this message starts.' : ''}</p>`}
      </div>`}
      </div>`}
      <div class="shrink-0 bg-raised flex border-t border-line">
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
  const sel = selection(room);
  // A room in a project works in the project's folders; the chip names the
  // project and opens its editor instead of a folder browser.
  const projectId = room.id ? room.info?.projectId : s.newProjectId;
  const project = projectId && s.projects.items.find((p) => p.id === projectId);
  const [open, setOpen] = usePopoverOpen(!project);
  const press = usePopoverTrigger(open, setOpen);
  if (project) {
    return html`<button type="button" onClick=${() => editProject(project)} title=${project.folders.join('\n')}
      class="flex items-center gap-1.5 h-8 px-2.5 rounded-lg text-[13px] text-muted hover:text-fg hover:bg-hover max-w-[40vw] sm:max-w-56">
      <${Icon} name="layers" size=${15} /><span class="truncate">${project.name}</span>
    </button>`;
  }
  return html`<div class="relative">
    <button type="button" onPointerDownCapture=${press.onPointerDownCapture} onClick=${press.onClick} aria-haspopup="dialog" aria-expanded=${open} title=${sel.workspace || 'Choose a working directory'}
      class="flex items-center gap-1.5 h-8 px-2.5 rounded-lg text-[13px] text-muted hover:text-fg hover:bg-hover max-w-[40vw] sm:max-w-56">
      <${Icon} name="folder" size=${15} />
      <span class="truncate">${base(sel.workspace) || 'Workspace'}</span>
    </button>
    <${Popover} open=${open} onClose=${() => setOpen(false)} layer="top" placement="top-start" label="Working directory" class="sm:w-[420px] flex flex-col">
      ${open && html`<${FolderBrowser} scroll="panel" start=${sel.workspace || ''} title="Working directory"
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

const finite = (n) => (typeof n === 'number' && Number.isFinite(n) ? n : null);

// meterAgent is the name already on the client, or the room's agent id.
// A model id is not a stand-in for it.
function meterAgent(room) {
  const id = room.info?.agentId || selection(room).agentId || '';
  const named = id && state.agents.items.find((a) => a.id === id)?.name;
  return (named && String(named).trim()) || id;
}

function costClause(cost) {
  if (!cost || typeof cost.amount !== 'number' || !Number.isFinite(cost.amount)) return '';
  if (typeof cost.currency !== 'string' || !cost.currency.trim()) return '';
  return ` · session cost ${fmtCost(cost)}`;
}

// contextRing is the one footer ring. A missing figure is not a zero: an
// agent room with no report is a hollow ring, and a provider room with no
// window is nothing. The percent is used/size, rounded, and not capped.
// The count is the agent's live report, or the server's estimate for a
// provider model. A missing estimate is not a zero, and a turn's own
// usage is not the session figure.
export function contextRing(room) {
  const agent = selection(room).runtime === 'agent';
  const c = room.ctx;
  const total = finite(c?.total) > 0 ? c.total : null;
  const used = agent ? finite(c?.live) : finite(c?.estimate);
  const who = meterAgent(room);
  if (total == null || used == null) {
    if (!agent) return null;
    return { hollow: true, text: `Context not reported by ${who}. Nothing has arrived from it yet.` };
  }
  const pct = Math.round((used / total) * 100);
  const qualifier = pct > 100 ? 'over' : agent ? 'live' : 'estimated';
  let text = `${pct}% (${fmtTokens(used)} / ${fmtTokens(total)}) context used · ${qualifier}`;
  text += costClause(c.cost);
  if (agent) text += ` · Quota not reported by ${who}`;
  return { hollow: false, pct, text };
}

export function ContextMeter({ room }) {
  useStore();
  const view = contextRing(room);
  if (!view) return null;
  const r = 7, len = 2 * Math.PI * r;
  const pct = view.hollow ? 0 : view.pct;
  const color = pct >= 90 ? 'var(--bad)' : pct >= 70 ? 'var(--warn)' : 'var(--accent)';
  return html`<span class="group relative inline-flex items-center justify-center size-8" tabindex="0" role="img" aria-label=${view.text}>
    <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true">
      <circle cx="9" cy="9" r=${r} fill="none" stroke="var(--line)" stroke-width="2.5" />
      ${!view.hollow && pct > 0 && html`<circle cx="9" cy="9" r=${r} fill="none" stroke=${color} stroke-width="2.5" stroke-linecap="round"
        stroke-dasharray=${`${(pct / 100) * len} ${len}`} transform="rotate(-90 9 9)" />`}
    </svg>
    <span role="tooltip" class="pointer-events-none absolute bottom-full right-0 mb-2 w-64 whitespace-normal text-left rounded-lg border border-line bg-raised px-2.5 py-1.5 text-xs shadow-pop opacity-0 group-hover:opacity-100 group-focus:opacity-100 transition-opacity">${view.text}</span>
  </span>`;
}
