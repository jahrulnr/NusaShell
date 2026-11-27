// The agent side of the composer: the room's agent session options (its
// mode first). Its slash commands come from the room's command catalog, with
// the native commands — see lib/commands.js.
import { html, useEffect, useLayoutEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { toast, useStore } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { selection, isBusy, loadAgentOptions, sessionModeName, setSessionOption, sessionOptionValue, agentOptionMap, agentChipVisible, showOptionsInline } from '../lib/chat.js';
import { Icon } from './icons.js';
import { Button, ErrorBlock, Popover, Select, Spinner, Toggle, usePopoverOpen, usePopoverTrigger } from './ui.js';

// AgentOptionsNotice says what the list cannot say by itself. A read that failed
// with no list to show is the failure, with its retry. A refresh that failed after
// a good read keeps the list and says it is from the last read. Both count the
// failures in a row: a Retry that fails again changes that count, and no toast
// says it again. The notice reads the room's state and sets none of it; it only
// tells the controller it is on screen, which is what keeps a toast from repeating it.
export function AgentOptionsNotice({ room: r, class: cls = '' }) {
  const o = r.agentOptions;
  // The notice mounts only while its panel is open, so mounting it makes this
  // panel an inline surface for the room's outage, and unmounting releases it.
  // It registers in the commit itself (a layout effect), not a frame later: a read
  // already in flight can fail the moment the panel opens, and must find it there.
  useLayoutEffect(() => { showOptionsInline(r, true); return () => showOptionsInline(r, false); }, [r]);
  // This button says Retry because a read failed and this is the attempt again.
  // The transient empty answer in AgentOptions says Refresh instead: nothing
  // failed there, the agent just has not reported options yet. Keep the two
  // labels apart. Merged, a failure would read as "not yet", and a "not yet"
  // would read as a broken read.
  const retry = () => loadAgentOptions(r, { force: true });
  const failed = `Failed ${o.failures} ${o.failures === 1 ? 'time' : 'times'} in a row.`;
  if (o.status === 'error' && !o.list.length) {
    return html`<${ErrorBlock} class=${cls} title="Session options can't be read right now." message=${`${o.error}. ${failed}`} onRetry=${retry} />`;
  }
  if (o.error && o.list.length) {
    return html`<div role="status" class=${'flex items-center gap-2 px-3 py-2 rounded-lg bg-warn/10 text-warn text-xs ' + cls}>
      <span class="flex-1">Session options can't be read right now, so the values below are from the last read. ${failed}</span>
      <${Button} kind="ghost" onClick=${retry}>Retry</${Button}>
    </div>`;
  }
  return null;
}

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
  const press = usePopoverTrigger(open, setOpen);
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
  // definitive answer is an empty list — nothing a click could change. The footer
  // describes a list, so with no list to describe it stays hidden.
  // The empty answer below offers Refresh, not Retry: nothing failed, so asking
  // again is a refresh. A failed read offers Retry (AgentOptionsNotice above).
  // Do not merge the two labels; see the comment there.
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
    <button type="button" onPointerDownCapture=${press.onPointerDownCapture} onClick=${press.onClick} aria-expanded=${open} title="Agent session options"
      class="flex items-center gap-1.5 h-8 px-2 rounded-lg text-[13px] text-muted hover:text-fg hover:bg-hover max-w-40">
      <${Icon} name="sliders" size=${14} class="shrink-0" />
      <span class="truncate">${modeName || 'Options'}</span>
    </button>
    <${Popover} open=${open} onClose=${() => setOpen(false)} layer="top" placement="top-end" label="Agent session options" class="sm:w-[400px] flex flex-col">
      <div class="shrink-0 px-3 pt-2.5 pb-1 text-xs text-faint">Agent session</div>
      ${opts.status === 'loading' && html`<div class="flex items-center gap-2 px-3 py-3 text-muted"><${Spinner} size=${12} /> Asking the agent…</div>`}
      <${AgentOptionsNotice} room=${r} class="mx-3 my-2.5" />
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
      ${opts.list.length > 0 && html`<p class="shrink-0 border-t border-line px-3 py-2 text-xs text-faint">${hint || 'Kept for this thread and applied before each turn.'}</p>`}
    </${Popover}>
  </div>`;
}

