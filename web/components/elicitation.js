// ElicitationCard: an agent asks the owner for input mid-turn — a form
// described by a JSON schema, or a URL to visit before it continues.
import { html, useState } from '/vendor/preact/htm-preact.mjs';
import { answerElicitation } from '../lib/chat.js';
import { Icon } from './icons.js';
import { cx, Button, Select, Toggle } from './ui.js';
import { NumberField } from './fields.js';

const inputCls = 'h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent w-full';

// choices lists a field's allowed values with their labels.
function choices(spec) {
  if (Array.isArray(spec.enum)) return spec.enum.map((v, i) => ({ value: v, label: spec.enumNames?.[i] || v }));
  if (Array.isArray(spec.oneOf)) return spec.oneOf.map((o) => ({ value: o.const, label: o.title || o.const }));
  return null;
}

function itemChoices(spec) {
  const items = spec.items || {};
  if (Array.isArray(items.enum)) return items.enum.map((v) => ({ value: v, label: v }));
  if (Array.isArray(items.anyOf)) return items.anyOf.map((o) => ({ value: o.const, label: o.title || o.const }));
  return [];
}

function Field({ rid, name, spec, required, value, onChange }) {
  const label = spec.title || name;
  const opts = choices(spec);
  const id = `eli-${rid}-${name}`;
  let control;
  if (spec.type === 'boolean') {
    return html`<label class="flex items-center justify-between gap-3 py-1">
      <span>${label}${spec.description && html`<span class="block text-xs text-faint">${spec.description}</span>`}</span>
      <${Toggle} checked=${!!value} onChange=${onChange} label=${label} />
    </label>`;
  }
  if (spec.type === 'array') {
    const list = Array.isArray(value) ? value : [];
    control = html`<div class="flex flex-wrap gap-1.5">
      ${itemChoices(spec).map((c) => {
        const on = list.includes(c.value);
        return html`<button type="button" key=${c.value} aria-pressed=${on}
          onClick=${() => onChange(on ? list.filter((x) => x !== c.value) : [...list, c.value])}
          class=${cx('h-7 px-2.5 rounded-full border text-xs', on ? 'border-accent bg-accent/15 text-fg' : 'border-line text-muted hover:bg-hover')}>${c.label}</button>`;
      })}
    </div>`;
  } else if (opts) {
    control = html`<${Select} label=${label} value=${value ?? ''} onChange=${(v) => onChange(v || undefined)}
      options=${[{ value: '', text: 'Choose…' }, ...opts.map((c) => ({ value: c.value, text: c.label }))]} />`;
  } else if (spec.type === 'number' || spec.type === 'integer') {
    control = html`<${NumberField} label=${label} width="w-24" optional integer=${spec.type === 'integer'} value=${value}
      min=${spec.minimum ?? -Infinity} max=${spec.maximum ?? Infinity} onChange=${(v) => onChange(v)} />`;
  } else {
    const secret = /password|secret|token/i.test(spec.format || name);
    control = html`<input id=${id} type=${secret ? 'password' : spec.format === 'email' ? 'email' : spec.format === 'uri' ? 'url' : 'text'}
      class=${inputCls} value=${value ?? ''} maxlength=${spec.maxLength} autocomplete="off"
      onInput=${(e) => onChange(e.target.value === '' ? undefined : e.target.value)} />`;
  }
  return html`<div class="flex flex-col gap-1">
    <label for=${id} class="text-xs text-muted">${label}${required && html`<span class="text-warn"> *</span>`}</label>
    ${control}
    ${spec.description && html`<span class="text-xs text-faint">${spec.description}</span>`}
  </div>`;
}

const OUTCOME = { accept: 'Answered', decline: 'Declined', cancel: 'Cancelled' };

export function ElicitationCard({ room, seg }) {
  const r = seg.request;
  const pending = seg.status === 'pending';
  const props = r.schema?.properties || {};
  const required = r.schema?.required || [];
  const [values, setValues] = useState(() => {
    const v = {};
    for (const [k, s] of Object.entries(props)) if (s.default !== undefined) v[k] = s.default;
    return v;
  });
  const missing = required.some((k) => values[k] === undefined || values[k] === '');
  const isURL = r.mode === 'url';
  let host = '';
  try { host = isURL ? new URL(r.url).host : ''; } catch {}

  const submit = (e) => {
    e?.preventDefault();
    if (isURL) { answerElicitation(room, seg, 'accept'); return; }
    const content = {};
    for (const [k, v] of Object.entries(values)) if (v !== undefined && k in props) content[k] = v;
    answerElicitation(room, seg, 'accept', content);
  };

  return html`<form noValidate onSubmit=${submit} role=${pending ? 'alert' : null}
    class=${cx('rounded-xl border px-4 py-3 text-[13px]', pending ? 'border-accent/40 bg-accent/5' : 'border-line bg-panel')}>
    <div class="flex items-start gap-2.5">
      <${Icon} name=${isURL ? 'link' : 'message'} class=${pending ? 'text-accent mt-0.5' : 'text-faint mt-0.5'} />
      <div class="flex-1 min-w-0">
        <div class="font-medium break-words">${r.message || (isURL ? 'The agent asks you to open a page' : 'The agent needs some input')}</div>
        ${!pending && html`<div class="text-muted mt-1">
          ${seg.status === 'answered' ? OUTCOME[seg.action] || seg.action : 'Withdrawn — the turn ended before an answer.'}
        </div>`}
      </div>
    </div>
    ${pending && isURL && html`<div class="mt-2.5 flex flex-col gap-1.5">
      <a href=${r.url} target="_blank" rel="noopener noreferrer"
        class="self-start inline-flex items-center gap-1.5 h-8 px-3 rounded-lg border border-line hover:bg-hover">
        <${Icon} name="globe" size=${14} /> Open ${host}
      </a>
      <span class="text-xs text-faint break-all">${r.url}</span>
    </div>`}
    ${pending && !isURL && html`<div class="mt-3 flex flex-col gap-2.5">
      ${Object.entries(props).map(([k, s]) => html`<${Field} key=${k} rid=${r.id} name=${k} spec=${s} required=${required.includes(k)}
        value=${values[k]} onChange=${(v) => setValues({ ...values, [k]: v })} />`)}
    </div>`}
    ${pending && seg.error && html`<div class="mt-2 text-xs text-bad">${seg.error}</div>`}
    ${pending && html`<div class="flex flex-wrap justify-end gap-2 mt-3">
      <${Button} kind="ghost" disabled=${!!seg.answering} busy=${seg.answering === 'decline'} onClick=${() => answerElicitation(room, seg, 'decline')}>Decline</${Button}>
      <${Button} type="submit" kind="primary" disabled=${!!seg.answering || (!isURL && missing)} busy=${seg.answering === 'accept'}>${isURL ? 'I’m done' : 'Submit'}</${Button}>
    </div>`}
  </form>`;
}
