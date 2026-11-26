// The agent side of the composer: the room's agent session options (its
// mode first). Its slash commands come from the room's command catalog, with
// the native commands — see lib/commands.js.
import { html, useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { toast, useStore } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { selection, isBusy, loadAgentOptions, sessionModeName, setSessionOption, sessionOptionValue, agentOptionMap, agentChipVisible } from '../lib/chat.js';
import { Icon } from './icons.js';
import { Popover, Select, Spinner, Toggle, usePopoverOpen } from './ui.js';

// AgentOptions shows the session's mode and other options of an agent room.
// The list is the room's own (lib/chat.js): this control reads it, the picker
// reads the same one, and neither keeps a copy — which is why a pick made in
// either place shows in both.
export function AgentOptions({ room: r }) {
  const sel = selection(r);
  useStore(); // the room's list is mutated in place, then announced
  const [busy, setBusy] = useState('');
  const [rules, setRules] = useState([]);
  const busyTurn = isBusy(r);
  const opts = r.agentOptions;
  const visible = !!r.id && !!sel.agentId && agentChipVisible(opts);
  const [open, setOpen] = usePopoverOpen(visible);
  const load = async () => {
    if (!r.id || !sel.agentId) return;
    loadAgentOptions(r);
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
  // The chip shows when there is something to act on: options to pick, or a
  // transient empty the owner can refresh. It hides only when the agent's
  // definitive answer is an empty list — nothing a click could change.
  const empty = opts.status === 'ready' && !opts.list.length;
  if (!visible) return null;
  const modeName = sessionModeName(r);
  const set = async (o, value) => {
    setBusy(o.id);
    await setSessionOption(r, o.id, value);
    setBusy('');
  };
  // A pick while a turn runs is stored and applies to the next one — the
  // controls stay open and say so, instead of refusing a change the room
  // can already accept. A pick the record is holding (pending) gets its
  // own marker on the option it names.
  const pendOpts = agentOptionMap(r.info?.pending?.options);
  const hint = Object.keys(pendOpts).length ? 'Applies to the next turn' : '';
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
      ${empty && opts.report === 'empty' && html`<div class="flex items-center gap-2 px-3 py-2 text-faint text-xs">
        <span class="flex-1">The agent reports no options right now.</span>
        <button type="button" class="text-accent shrink-0" onClick=${() => loadAgentOptions(r, { force: true })}>Refresh</button>
      </div>`}
      ${opts.list.length > 0 && html`<div class="grid grid-cols-2 gap-x-3 gap-y-2.5 px-3 py-2.5 text-[13px]">
        ${opts.list.map((o) => html`<label key=${o.id} class="flex flex-col gap-1 min-w-0">
          <span class="flex items-center gap-1.5 text-xs text-muted min-w-0" title=${o.description || o.name}>
            <span class="truncate">${o.name}</span>
            ${busy === o.id && html`<${Spinner} size=${11} />`}
            ${pendOpts[o.id] !== undefined && html`<span class="text-[11px] text-faint shrink-0">next turn</span>`}
          </span>
          ${o.type === 'boolean'
            ? html`<span class="h-7 flex items-center"><${Toggle} checked=${sessionOptionValue(r, o) === 'true'} label=${o.name} onChange=${(v) => set(o, String(v))} /></span>`
            : html`<${Select} dense label=${o.name} class="w-full" value=${sessionOptionValue(r, o)}
                options=${(o.choices || []).map((c) => ({ value: c.value, text: c.name }))} onChange=${(v) => set(o, v)} />`}
        </label>`)}
      </div>`}
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
      ${!(empty && opts.report === 'empty') && html`<p class="shrink-0 border-t border-line px-3 py-2 text-xs text-faint">${hint || 'Kept for this thread and applied before each turn.'}</p>`}
    </${Popover}>
  </div>`;
}

