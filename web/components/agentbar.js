// The agent side of the composer: the room's agent session options (its
// mode first) and the slash commands it offers.
import { html, useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { toast } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { selection, isBusy } from '../lib/chat.js';
import { Icon } from './icons.js';
import { cx, Popover, Select, Spinner, Toggle } from './ui.js';

// AgentOptions shows the session's mode and other options of an agent
// room. Picks are kept on the room and apply to its session.
export function AgentOptions({ room: r }) {
  const sel = selection(r);
  const [open, setOpen] = useState(false);
  const [opts, setOpts] = useState({ status: 'idle', list: [] });
  const [busy, setBusy] = useState('');
  const [rules, setRules] = useState([]);
  const busyTurn = isBusy(r);
  const load = async () => {
    if (!r.id || !sel.agentId) return;
    setOpts((o) => ({ ...o, status: o.list.length ? 'ready' : 'loading' }));
    try { const res = await api.agentOptions(r.id); setOpts({ status: 'ready', list: res.data || [] }); }
    catch (e) { setOpts({ status: 'error', list: [], error: errorText(e) }); }
    api.approvalRules(r.id).then((res) => setRules(res.data || []), () => setRules([]));
  };
  const forget = async (rule) => {
    try { await api.deleteApprovalRule(r.id, rule.id); setRules((l) => l.filter((x) => x.id !== rule.id)); }
    catch (e) { toast(errorText(e), 'error'); }
  };
  // Refresh when the room changes or a turn ends: the agent may change
  // its own mode.
  useEffect(() => { load(); }, [r.id, sel.agentId]);
  useEffect(() => { if (!busyTurn) load(); }, [busyTurn]);
  if (!r.id || !sel.agentId || (opts.status === 'ready' && !opts.list.length)) return null;
  const mode = opts.list.find((o) => o.category === 'mode');
  const modeName = mode && (mode.choices || []).find((c) => c.value === mode.current)?.name;
  const set = async (o, value) => {
    setBusy(o.id);
    try { const res = await api.setAgentOption(r.id, o.id, value); setOpts({ status: 'ready', list: res.data || [] }); }
    catch (e) { toast(errorText(e), 'error'); }
    setBusy('');
  };
  return html`<div class="relative">
    <button type="button" onClick=${() => setOpen(!open)} aria-expanded=${open} title="Agent session options"
      class="flex items-center gap-1.5 h-8 px-2 rounded-lg text-[13px] text-muted hover:text-fg hover:bg-hover max-w-40">
      <${Icon} name="sliders" size=${14} class="shrink-0" />
      <span class="truncate">${modeName || 'Options'}</span>
    </button>
    <${Popover} open=${open} onClose=${() => setOpen(false)} placement="top-end" label="Agent session options" class="sm:w-[400px] flex flex-col">
      <div class="shrink-0 px-3 pt-2.5 pb-1 text-xs text-faint">Agent session</div>
      ${opts.status === 'loading' && html`<div class="flex items-center gap-2 px-3 py-3 text-muted"><${Spinner} size=${12} /> Asking the agent…</div>`}
      ${opts.status === 'error' && html`<p class="px-3 py-2 text-bad text-xs">${opts.error}</p>`}
      <div class="grid grid-cols-2 gap-x-3 gap-y-2.5 px-3 py-2.5 text-[13px]">
        ${opts.list.map((o) => html`<label key=${o.id} class="flex flex-col gap-1 min-w-0">
          <span class="flex items-center gap-1.5 text-xs text-muted min-w-0" title=${o.description || o.name}>
            <span class="truncate">${o.name}</span>
            ${busy === o.id && html`<${Spinner} size=${11} />`}
          </span>
          ${o.type === 'boolean'
            ? html`<span class="h-7 flex items-center"><${Toggle} checked=${o.current === 'true'} label=${o.name} onChange=${(v) => set(o, String(v))} /></span>`
            : html`<${Select} dense label=${o.name} class="w-full" value=${o.current}
                options=${(o.choices || []).map((c) => ({ value: c.value, text: c.name }))} onChange=${(v) => set(o, v)} />`}
        </label>`)}
      </div>
      ${rules.length > 0 && html`<div class="shrink-0 border-t border-line px-3 py-2.5 text-[13px]">
        <div class="text-xs text-faint mb-1.5">Always allowed in this thread</div>
        <div class="flex flex-wrap gap-1.5">
          ${rules.map((x) => html`<span key=${x.id} class="inline-flex items-center gap-1 h-6 pl-2 pr-1 rounded-md border border-line text-xs">
            ${x.action}
            <button type="button" aria-label=${'Ask again for ' + x.action} title="Ask again" onClick=${() => forget(x)}
              class="size-4 inline-flex items-center justify-center rounded text-faint hover:text-fg"><${Icon} name="x" size=${11} /></button>
          </span>`)}
        </div>
      </div>`}
      <p class="shrink-0 border-t border-line px-3 py-2 text-xs text-faint">Kept for this thread and applied before each turn.</p>
    </${Popover}>
  </div>`;
}

// useCommands loads the slash commands of an agent room.
export function useCommands(r) {
  const sel = selection(r);
  const [cmds, setCmds] = useState([]);
  const busyTurn = isBusy(r);
  useEffect(() => {
    if (!r.id || !sel.agentId) { setCmds([]); return; }
    api.agentCommands(r.id).then((res) => setCmds(res.data || []), () => setCmds([]));
  }, [r.id, sel.agentId, busyTurn]);
  return cmds;
}
