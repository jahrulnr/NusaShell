// Small shared primitives: popover, dialog, select, spinner, state blocks.
import { html, useEffect, useLayoutEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import SlimSelect from '/vendor/slim-select/slimselect.es.js';
import { state, emit, useStore, dismissToast } from '../lib/store.js';
import { chooseSide } from '../lib/placement.js';
import { Icon } from './icons.js';

export const cx = (...a) => a.filter(Boolean).join(' ');

export function Spinner({ size = 14, class: cls = '' }) {
  return html`<svg class=${cx('animate-spin shrink-0', cls)} width=${size} height=${size} viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <circle cx="12" cy="12" r="9" stroke="currentColor" stroke-opacity=".25" stroke-width="3" />
    <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" stroke-width="3" stroke-linecap="round" />
  </svg>`;
}

export function IconButton({ icon, label, onClick, class: cls = '', disabled, size = 16, active, ...rest }) {
  return html`<button type="button" title=${label} aria-label=${label} onClick=${onClick} disabled=${disabled}
    class=${cx('inline-flex items-center justify-center rounded-md size-8 text-muted hover:bg-hover hover:text-fg disabled:opacity-40 disabled:hover:bg-transparent transition-colors', active && 'bg-hover text-fg', cls)} ...${rest}>
    <${Icon} name=${icon} size=${size} />
  </button>`;
}

export function Button({ children, onClick, kind = 'default', class: cls = '', disabled, busy, type = 'button', ...rest }) {
  const kinds = {
    default: 'bg-raised border border-line hover:bg-hover text-fg',
    primary: 'bg-accent text-accent-fg hover:brightness-110',
    danger: 'bg-bad/15 text-bad hover:bg-bad/25 border border-bad/30',
    ghost: 'text-muted hover:text-fg hover:bg-hover',
  };
  return html`<button type=${type} onClick=${onClick} disabled=${disabled || busy}
    class=${cx('inline-flex items-center justify-center gap-1.5 whitespace-nowrap shrink-0 rounded-lg px-3 h-8 text-[13px] font-medium disabled:opacity-50 transition', kinds[kind], cls)} ...${rest}>
    ${busy && html`<${Spinner} size=${13} />`}${children}
  </button>`;
}

// Popover anchored to its wrapper; a bottom sheet on narrow screens.
// Closes on outside pointerdown and Escape. Beside its wrapper it opens on the
// side `placement` asks for unless its content does not fit there and the other
// side is clearly better (lib/placement.js), and it is capped at the room on the
// side it took, so a tall panel never runs off the screen. The side and the cap
// are measured after every render while open, because the content and the
// anchor move without the panel's props changing. The panel is the one scroll
// owner: its parts do not scroll themselves, and what must stay reachable is
// sticky.
export function Popover({ open, onClose, children, class: cls = '', placement = 'top-start', label }) {
  const ref = useRef();
  const [fit, setFit] = useState(null); // { side, room } beside the wrapper, null on a bottom sheet
  const [prefer, align] = placement.split('-');
  const measure = () => {
    const anchor = ref.current?.parentElement;
    if (!anchor || matchMedia('(max-width: 639px)').matches) { setFit(null); return; }
    const r = anchor.getBoundingClientRect();
    const above = r.top - 16, below = innerHeight - r.bottom - 16;
    // The content's own height, read with the cap lifted for an instant (and
    // put back exactly, so the framework's idea of the style stays true).
    const el = ref.current;
    const capped = el.style.maxHeight;
    el.style.maxHeight = '';
    const need = el.offsetHeight;
    el.style.maxHeight = capped;
    const side = chooseSide(prefer, above, below, need);
    const room = Math.floor(Math.max(160, Math.min(side === 'top' ? above : below, innerHeight * 0.75)));
    // The same side and room hand back the previous value, so a render that
    // changes nothing does not render again.
    setFit((f) => (f && f.side === side && f.room === room ? f : { side, room }));
  };
  // Divergence, not change, asks for the measure (the rule Select and
  // selectionbar follow): it runs after every render while open, and closing
  // forgets the old side and room.
  useLayoutEffect(() => { if (open) measure(); else setFit(null); });
  useLayoutEffect(() => {
    if (!open) return;
    addEventListener('resize', measure);
    return () => removeEventListener('resize', measure);
  }, [open, placement]);
  useEffect(() => {
    if (!open) return;
    const down = (e) => { if (e.target.closest?.('.ss-content')) return; if (ref.current && !ref.current.contains(e.target) && !ref.current.parentElement.contains(e.target)) onClose(); };
    const key = (e) => { if (e.key === 'Escape') { e.stopPropagation(); onClose(); } };
    document.addEventListener('pointerdown', down, true);
    document.addEventListener('keydown', key, true);
    return () => { document.removeEventListener('pointerdown', down, true); document.removeEventListener('keydown', key, true); };
  }, [open]);
  if (!open) return null;
  const pos = cx(
    (fit?.side || prefer) === 'top' ? 'sm:bottom-full sm:mb-2' : 'sm:top-full sm:mt-1',
    align === 'end' ? 'sm:right-0' : 'sm:left-0',
  );
  return html`
    <div class="fixed inset-0 z-40 bg-black/40 sm:hidden" aria-hidden="true" onClick=${onClose}></div>
    <div ref=${ref} role="dialog" aria-label=${label} style=${fit ? { maxHeight: fit.room + 'px' } : undefined}
      class=${cx('z-50 bg-raised border border-line rounded-xl shadow-pop text-[13px] overflow-auto',
        'fixed inset-x-2 bottom-2 max-h-[75dvh] sm:absolute sm:inset-x-auto sm:bottom-auto', pos, cls)}>
      ${children}
    </div>`;
}

// usePopoverOpen is a popover's open flag, owned by the anchor that shows it: a
// flag that outlives its anchor reopens the panel the next time the anchor
// shows. Every anchor that can hide while open takes it. Adopted, not
// deduplicated: the send options already reset their flag inline, while the
// workspace picker and the linked chats had no reset and gain one here.
export function usePopoverOpen(show) {
  const [open, setOpen] = useState(false);
  useEffect(() => { if (!show) setOpen(false); }, [show]);
  return [open && !!show, setOpen];
}

// A dialog is dismissed by Escape and its close button; backdrop also closes
// it on a click outside the panel, for the dialogs that only show something
// (a preview), never the ones that hold a form.
export function Dialog({ open, onClose, title, children, footer, backdrop = false, class: cls = '' }) {
  const ref = useRef();
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);
  // The dialog element is its own backdrop: a click whose target is the
  // element, not anything inside, landed outside the panel.
  const outside = (e) => { if (backdrop && e.target === ref.current) onClose(); };
  return html`<dialog ref=${ref} onClose=${onClose} onCancel=${(e) => { e.preventDefault(); onClose(); }} onClick=${outside}
    class=${cx('bg-panel text-fg border border-line rounded-2xl shadow-pop p-0 backdrop:bg-black/50 w-[min(640px,calc(100vw-24px))] max-h-[min(85dvh,900px)] m-auto', cls)}>
    ${open && html`<div class="flex flex-col max-h-[min(85dvh,900px)]">
      <header class="flex items-center justify-between gap-3 px-5 py-3.5 border-b border-line">
        <h2 class="text-[15px] font-semibold">${title}</h2>
        <${IconButton} icon="x" label="Close" onClick=${onClose} />
      </header>
      <div class="flex-1 min-h-0 overflow-y-auto px-5 py-4">${children}</div>
      ${footer && html`<footer class="flex items-center justify-end gap-2 px-5 py-3 border-t border-line">${footer}</footer>`}
    </div>`}
  </dialog>`;
}

// Select wraps slim-select; options: [{ value, text }].
export function Select({ value, options, onChange, search = false, class: cls = '', label, disabled = false, dense = false, deferSelection = false }) {
  const el = useRef();
  const ss = useRef();
  const cb = useRef(onChange);
  cb.current = onChange;
  useEffect(() => {
    ss.current = new SlimSelect({
      select: el.current,
      // A modal <dialog> sits in the top layer, so the list must live
      // inside it; fixed positioning keeps viewport coordinates either way.
      settings: { showSearch: search, contentLocation: el.current.closest('dialog') || document.body, contentPosition: 'fixed', openPosition: 'auto', modal: 'off' },
      events: {
        // deferSelection submits the pick without letting slim-select mark
        // it: the value shows only when the server reflects it back.
        beforeChange: deferSelection ? (sel) => { const v = sel[0]?.value; if (v && v !== el.current._value) { cb.current(v); ss.current.close(); } return false; } : undefined,
        afterChange: (sel) => { const v = sel[0]?.value; if (v !== undefined && v !== el.current._value) { el.current._value = v; cb.current(v); } },
      },
    });
    // A scroll inside a dialog's container never reaches slim-select's window
    // scroll handler, so the fixed list would stay behind, detached from its
    // select. Close on any scroll outside the list, like a native dropdown.
    const onScroll = (e) => {
      const c = ss.current;
      if (c?.settings.isOpen && !(e.target instanceof Element && c.render.content.main.contains(e.target))) c.close();
    };
    document.addEventListener('scroll', onScroll, true);
    return () => { document.removeEventListener('scroll', onScroll, true); ss.current?.destroy(); };
  }, []);
  useEffect(() => {
    if (!ss.current) return;
    if (disabled) ss.current.disable(); else ss.current.enable();
  }, [disabled]);
  // Slim-select marks a user's pick at once; a change the server refused
  // changes nothing upstream, so the display can diverge from `value`
  // without `value` ever moving. The resync runs after every render —
  // divergence, not change, is what asks for it.
  useEffect(() => {
    if (ss.current && el.current) selectResync(el.current, ss.current, value, options);
  });
  return html`<div class=${cx('min-w-40', dense && 'ss-dense', cls)}><select ref=${el} aria-label=${label}></select></div>`;
}

// selectResync pushes the props into slim-select when its display differs
// from them: on mount, on a real change, and after a refused pick left its
// selection ahead of `value`. `el._value` holds what slim-select last showed
// (set here, or by the picker's afterChange) and `el._data` what was last
// pushed; both matching means nothing moved and the call is free. Returns
// whether it pushed.
export function selectResync(el, ss, value, options) {
  const data = JSON.stringify(options);
  if (el._value === value && el._data === data) return false;
  el._data = data;
  el._value = value;
  // An entry with options is a labelled group of them.
  const opt = (o) => ({ value: o.value, text: o.text, selected: o.value === value });
  ss.setData(options.map((o) => (o.options ? { label: o.label, options: o.options.map(opt) } : opt(o))));
  return true;
}

// ConfirmHost shows the question askConfirm is asking; the app renders it
// once. No answer but a button, Escape, or a click outside is possible.
export function ConfirmHost() {
  const s = useStore();
  const q = s.confirm;
  if (!q) return null;
  const answer = (yes) => { state.confirm = null; emit(); q.resolve(yes); };
  return html`<${Dialog} open=${true} onClose=${() => answer(false)} backdrop=${true} title=${q.title} class="w-[min(440px,calc(100vw-24px))]"
    footer=${html`<${Button} kind="ghost" autofocus onClick=${() => answer(false)}>Cancel</${Button}>
      <${Button} kind=${q.danger ? 'danger' : 'primary'} onClick=${() => answer(true)}>${q.confirm}</${Button}>`}>
    ${q.body && html`<p class="text-muted">${q.body}</p>`}
  </${Dialog}>`;
}

// Checkbox is a labelled yes-or-no in a line of text, drawn here rather than
// by the browser.
export function Checkbox({ checked, onChange, children, class: cls = '' }) {
  return html`<button type="button" role="checkbox" aria-checked=${checked} onClick=${() => onChange(!checked)}
    class=${cx('inline-flex items-center gap-1.5 text-left', cls)}>
    <span class=${cx('size-4 shrink-0 rounded border flex items-center justify-center transition-colors', checked ? 'bg-accent border-accent text-accent-fg' : 'bg-raised border-line')}>
      ${checked && html`<${Icon} name="check" size=${12} />`}
    </span>
    <span>${children}</span>
  </button>`;
}

export function Toggle({ checked, onChange, label, disabled }) {
  return html`<button type="button" role="switch" aria-checked=${checked} aria-label=${label} disabled=${disabled}
    onClick=${() => onChange(!checked)}
    class=${cx('relative inline-flex h-5 w-9 shrink-0 rounded-full transition-colors disabled:opacity-50', checked ? 'bg-accent' : 'bg-line')}>
    <span class=${cx('absolute top-0.5 size-4 rounded-full bg-white shadow transition-transform', checked ? 'translate-x-4.5' : 'translate-x-0.5')}></span>
  </button>`;
}

export function ErrorBlock({ title = 'Something went wrong', message, onRetry, class: cls = '' }) {
  return html`<div role="alert" class=${cx('rounded-xl border border-bad/30 bg-bad/10 px-4 py-3 text-[13px]', cls)}>
    <div class="flex items-start gap-2.5">
      <${Icon} name="alert" class="text-bad mt-0.5" />
      <div class="flex-1 min-w-0">
        <div class="font-medium">${title}</div>
        ${message && html`<div class="text-muted mt-0.5 break-words">${message}</div>`}
      </div>
      ${onRetry && html`<${Button} onClick=${onRetry}>Retry</${Button}>`}
    </div>
  </div>`;
}

export function Toasts() {
  const s = useStore();
  return html`<div class="fixed z-[60] bottom-4 right-4 left-4 sm:left-auto flex flex-col gap-2 items-end pointer-events-none" aria-live="polite">
    ${s.toasts.map((t) => html`<div key=${t.id} role=${t.kind === 'error' ? 'alert' : 'status'}
      class=${cx('pointer-events-auto max-w-sm w-full sm:w-auto flex items-start gap-2 rounded-xl border px-3.5 py-2.5 text-[13px] shadow-pop bg-raised',
        t.kind === 'error' ? 'border-bad/40' : t.kind === 'warning' ? 'border-warn/40' : 'border-line')}>
      ${(t.kind === 'error' || t.kind === 'warning') && html`<${Icon} name="alert" class=${cx('mt-0.5', t.kind === 'error' ? 'text-bad' : 'text-warn')} />`}
      <span class="flex-1 break-words">${t.message}</span>
      ${t.action && html`<button class="font-medium text-accent hover:underline" onClick=${() => { dismissToast(t.id); t.action.run(); }}>${t.action.label}</button>`}
      <button class="text-faint hover:text-fg" aria-label="Dismiss" onClick=${() => dismissToast(t.id)}><${Icon} name="x" size=${14} /></button>
    </div>`)}
  </div>`;
}

export function Kbd({ children }) {
  return html`<kbd class="inline-flex items-center rounded border border-line bg-panel px-1 text-[11px] font-sans text-faint">${children}</kbd>`;
}

export const fmtTokens = (n) => (n >= 1e6 ? (n / 1e6).toFixed(n >= 1e7 ? 0 : 1) + 'M' : n >= 1e3 ? Math.round(n / 1e3) + 'K' : String(n ?? 0));

export function relTime(iso) {
  if (!iso) return '';
  const d = (Date.now() - new Date(iso).getTime()) / 1000;
  if (d < 60) return 'now';
  if (d < 3600) return Math.floor(d / 60) + 'm';
  if (d < 86400) return Math.floor(d / 3600) + 'h';
  if (d < 7 * 86400) return Math.floor(d / 86400) + 'd';
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

export { state };

// Marker is the numbered badge of a live-page annotation — the same number
// painted on its screenshot.
export function Marker({ n, class: cls = '' }) {
  return html`<span class=${cx('inline-flex items-center justify-center min-w-5 h-5 px-1 rounded-full bg-accent text-accent-fg text-[11px] font-semibold tabular-nums shrink-0', cls)}>${n}</span>`;
}
