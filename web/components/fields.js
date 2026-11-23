// Number, time, and date-time inputs drawn here: the browser's own spinners
// and calendar popups are not used anywhere in the app. Values keep the shape
// the native inputs gave (a number; "HH:MM"; "YYYY-MM-DDTHH:mm", a wall-clock
// time with no zone), so a form switches over without changing what it saves.
import { html, useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { Icon } from './icons.js';
import { cx, Popover } from './ui.js';

const field = 'h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent';
const step = 'size-8 shrink-0 inline-flex items-center justify-center text-muted hover:text-fg hover:bg-hover disabled:opacity-30';

// NumberField is a whole number (or a decimal with integer={false}) between
// min and max. onChange fires with each valid value as it is typed; an invalid
// draft shows as such and falls back to the last value when the field is left.
// onCommit fires when the field is left or Enter is pressed on a valid value
// that differs from the one shown, for a setting saved on its own.
export function NumberField({ value, onChange, onCommit, min = -Infinity, max = Infinity, integer = true, label, class: cls = '', width = 'w-16', optional = false }) {
  const show = (v) => (v === undefined || v === null ? '' : String(v));
  const [draft, setDraft] = useState(show(value));
  useEffect(() => setDraft(show(value)), [value]);
  const n = draft.trim() === '' ? null : Number(draft);
  const valid = n !== null && Number.isFinite(n) && (!integer || Number.isInteger(n)) && n >= min && n <= max;
  const optionalEmpty = n === null && optional; // an optional number may be left empty
  const apply = (next) => {
    const clamped = Math.min(max, Math.max(min, next));
    setDraft(String(clamped));
    onChange?.(clamped);
    return clamped;
  };
  const bump = (d) => apply((valid ? n : Number(value) || 0) + d);
  const commit = () => {
    if (valid) { if (n !== value) onCommit?.(n); return; }
    if (optionalEmpty) { onChange(undefined); return; }
    setDraft(show(value));
  };
  return html`<span class=${cx('inline-flex items-center rounded-lg border bg-raised overflow-hidden', !valid && !optionalEmpty ? 'border-bad' : 'border-line focus-within:border-accent', cls)}>
    <button type="button" tabindex="-1" aria-label=${'Decrease ' + (label || 'value')} class=${step} disabled=${valid && n <= min} onClick=${() => bump(-1)}><${Icon} name="minus" size=${13} /></button>
    <input type="text" inputmode=${integer ? 'numeric' : 'decimal'} autocomplete="off" aria-label=${label} aria-invalid=${valid || optionalEmpty ? 'false' : 'true'}
      value=${draft} class=${cx('h-8 bg-transparent text-center text-[13px] outline-none min-w-0', width)}
      onInput=${(e) => {
        setDraft(e.target.value);
        const v = e.target.value.trim() === '' ? null : Number(e.target.value);
        if (v !== null && Number.isFinite(v) && (!integer || Number.isInteger(v)) && v >= min && v <= max) onChange?.(v);
      }}
      onBlur=${commit}
      onKeyDown=${(e) => {
        if (e.key === 'ArrowUp') { e.preventDefault(); bump(1); }
        else if (e.key === 'ArrowDown') { e.preventDefault(); bump(-1); }
        else if (e.key === 'Enter') { e.preventDefault(); commit(); e.target.blur(); }
        else if (e.key === 'Escape') setDraft(show(value));
      }} />
    <button type="button" tabindex="-1" aria-label=${'Increase ' + (label || 'value')} class=${step} disabled=${valid && n >= max} onClick=${() => bump(1)}><${Icon} name="plus" size=${13} /></button>
  </span>`;
}

const pad = (n) => String(n).padStart(2, '0');

// TimeField is "HH:MM", 24-hour: an hour and a minute, each a small number
// field that steps with the arrow keys.
export function TimeField({ value, onChange, label }) {
  const [h, m] = /^(\d{1,2}):(\d{2})$/.test(value || '') ? value.split(':').map(Number) : [0, 0];
  const part = (v, max, what, set) => html`<input type="text" inputmode="numeric" maxlength="2" autocomplete="off" aria-label=${label + ' ' + what} value=${pad(v)}
    class="h-8 w-9 rounded-lg bg-raised border border-line text-center text-[13px] outline-none focus:border-accent"
    onFocus=${(e) => e.target.select()}
    onInput=${(e) => { const d = e.target.value.replace(/\D/g, ''); if (d !== '') set(Math.min(max, +d)); }}
    onKeyDown=${(e) => {
      if (e.key === 'ArrowUp' || e.key === 'ArrowDown') { e.preventDefault(); set((v + (e.key === 'ArrowUp' ? 1 : -1) + max + 1) % (max + 1)); }
    }} />`;
  return html`<span class="inline-flex items-center gap-1" role="group" aria-label=${label}>
    ${part(h, 23, 'hour', (v) => onChange(`${pad(v)}:${pad(m)}`))}<span class="text-muted">:</span>${part(m, 59, 'minute', (v) => onChange(`${pad(h)}:${pad(v)}`))}
  </span>`;
}

const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
const WEEKDAYS = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'];
const parse = (v) => {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(v || '');
  return m ? { y: +m[1], mo: +m[2] - 1, d: +m[3], h: +m[4], mi: +m[5] } : null;
};
const format = (p) => `${p.y}-${pad(p.mo + 1)}-${pad(p.d)}T${pad(p.h)}:${pad(p.mi)}`;
const show = (p) => `${p.d} ${MONTHS[p.mo].slice(0, 3)} ${p.y} · ${pad(p.h)}:${pad(p.mi)}`;

// DateTimeField is a button that shows a wall-clock date and time and opens a
// calendar with a time under it. clearable lets the field be emptied.
export function DateTimeField({ value, onChange, label, clearable = false, class: cls = '' }) {
  const [open, setOpen] = useState(false);
  const p = parse(value);
  const today = new Date();
  const [view, setView] = useState({ y: p?.y ?? today.getFullYear(), mo: p?.mo ?? today.getMonth() });
  useEffect(() => { if (open && p) setView({ y: p.y, mo: p.mo }); }, [open]);
  const first = (new Date(Date.UTC(view.y, view.mo, 1)).getUTCDay() + 6) % 7; // Monday first
  const days = new Date(Date.UTC(view.y, view.mo + 1, 0)).getUTCDate();
  const cells = [...Array(first).fill(0), ...Array.from({ length: days }, (_, i) => i + 1)];
  const base = p || { y: today.getFullYear(), mo: today.getMonth(), d: today.getDate(), h: 9, mi: 0 };
  const move = (d) => setView(({ y, mo }) => { const t = mo + d; return { y: y + Math.floor(t / 12), mo: ((t % 12) + 12) % 12 }; });
  const isToday = (d) => view.y === today.getFullYear() && view.mo === today.getMonth() && d === today.getDate();
  return html`<span class=${cx('relative inline-block', cls)}>
    <button type="button" aria-haspopup="dialog" aria-expanded=${open} aria-label=${label} onClick=${() => setOpen(!open)}
      class=${cx(field, 'w-full inline-flex items-center gap-2 text-left', !p && 'text-faint')}>
      <${Icon} name="clock" size=${14} class="text-muted shrink-0" />
      <span class="truncate">${p ? show(p) : 'Pick a date and time'}</span>
    </button>
    <${Popover} open=${open} onClose=${() => setOpen(false)} placement="bottom-start" label=${label} class="p-3 sm:w-72">
      <div class="flex items-center justify-between mb-2">
        <button type="button" aria-label="Previous month" class=${cx(step, 'rounded-md')} onClick=${() => move(-1)}><${Icon} name="chevronLeft" size=${15} /></button>
        <span class="font-medium">${MONTHS[view.mo]} ${view.y}</span>
        <button type="button" aria-label="Next month" class=${cx(step, 'rounded-md')} onClick=${() => move(1)}><${Icon} name="chevronRight" size=${15} /></button>
      </div>
      <div class="grid grid-cols-7 gap-0.5 text-center text-xs text-faint mb-1">${WEEKDAYS.map((w) => html`<span key=${w}>${w}</span>`)}</div>
      <div class="grid grid-cols-7 gap-0.5">
        ${cells.map((d, i) => d === 0 ? html`<span key=${'b' + i}></span>`
          : html`<button key=${d} type="button" aria-label=${`${d} ${MONTHS[view.mo]} ${view.y}`} aria-pressed=${p?.y === view.y && p?.mo === view.mo && p?.d === d}
              onClick=${() => onChange(format({ ...base, y: view.y, mo: view.mo, d }))}
              class=${cx('h-8 rounded-md text-[13px] hover:bg-hover',
                p?.y === view.y && p?.mo === view.mo && p?.d === d ? 'bg-accent text-accent-fg hover:bg-accent' : isToday(d) ? 'border border-accent/60' : '')}>${d}</button>`)}
      </div>
      <div class="flex items-center justify-between gap-2 mt-3 pt-3 border-t border-line">
        <span class="flex items-center gap-2 text-muted">Time <${TimeField} label=${label + ' time'} value=${`${pad(base.h)}:${pad(base.mi)}`}
          onChange=${(t) => { const [h, mi] = t.split(':').map(Number); onChange(format({ ...base, h, mi })); }} /></span>
        <span class="flex items-center gap-1">
          ${clearable && p && html`<button type="button" class="h-8 px-2.5 rounded-lg text-muted hover:text-fg hover:bg-hover" onClick=${() => { onChange(''); setOpen(false); }}>Clear</button>`}
          <button type="button" class="h-8 px-2.5 rounded-lg bg-accent text-accent-fg hover:brightness-110" onClick=${() => setOpen(false)}>Done</button>
        </span>
      </div>
    </${Popover}>
  </span>`;
}
