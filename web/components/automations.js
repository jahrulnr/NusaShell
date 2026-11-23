// Automations: a prompt sent to an agent or a model on a schedule. Each run
// is a turn in a thread — a new one per run, or one the automation keeps —
// and its reply can also go to a WhatsApp or Telegram chat.
import { html, useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore, toast } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { navigate } from '../lib/chat.js';
import { Icon } from './icons.js';
import { cx, Button, Dialog, ErrorBlock, Select, Spinner, Toggle, relTime } from './ui.js';
import { DateTimeField, NumberField, TimeField } from './fields.js';

const inputCls = 'h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent w-full';
const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
const pad = (n) => String(n).padStart(2, '0');

// ---- time zones: datetime-local values are wall-clock times in `zone` ----

function zoned(ms, zone) {
  try {
    const p = Object.fromEntries(new Intl.DateTimeFormat('en-CA', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(new Date(ms)).map((x) => [x.type, x.value]));
    return `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}`;
  } catch { return ''; }
}

const WALL = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/;
const wallUTC = (m) => Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]);

// fromZoned turns a wall-clock time in zone into an instant. It walks the
// zone offset a few times, so it settles across DST changes.
function fromZoned(value, zone) {
  const m = WALL.exec(value || '');
  if (!m) throw new Error('Pick a date and time.');
  const want = wallUTC(m);
  let t = want;
  for (let i = 0; i < 3; i++) {
    const got = WALL.exec(zoned(t, zone));
    if (!got) throw new Error(`Unknown time zone “${zone}”.`);
    t += want - wallUTC(got);
  }
  if (zoned(t, zone) !== value) throw new Error('That time does not exist in this time zone (a clock change skips it).');
  return new Date(t).toISOString();
}

function showTime(iso, zone) {
  if (!iso) return '';
  try { return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short', timeZone: zone }).format(new Date(iso)); }
  catch { return new Date(iso).toLocaleString(); }
}

// ---- cron presets ----

function parseCron(expr) {
  const f = (expr || '').trim().split(/\s+/);
  if (f.length !== 5) return { preset: 'custom' };
  const [mi, h, dom, mon, dow] = f;
  const num = (x) => /^\d+$/.test(x);
  const time = num(h) && num(mi) ? `${pad(h)}:${pad(mi)}` : '';
  let m;
  if ((m = /^\*\/(\d+)$/.exec(mi)) && h === '*' && dom === '*' && mon === '*' && dow === '*') return { preset: 'minutes', every: +m[1] };
  if (num(mi) && h === '*' && dom === '*' && mon === '*' && dow === '*') return { preset: 'hourly', minute: +mi };
  if (time && dom === '*' && mon === '*' && dow === '*') return { preset: 'daily', time };
  if (time && dom === '*' && mon === '*' && dow === '1-5') return { preset: 'weekdays', time };
  if (time && dom === '*' && mon === '*' && /^[0-6]$/.test(dow)) return { preset: 'weekly', time, day: +dow };
  if (time && num(dom) && mon === '*' && dow === '*') return { preset: 'monthly', time, date: +dom };
  return { preset: 'custom' };
}

function buildCron(c) {
  const [h, mi] = (c.time || '09:00').split(':').map(Number);
  switch (c.preset) {
    case 'minutes': return `*/${c.every} * * * *`;
    case 'hourly': return `${c.minute} * * * *`;
    case 'daily': return `${mi} ${h} * * *`;
    case 'weekdays': return `${mi} ${h} * * 1-5`;
    case 'weekly': return `${mi} ${h} * * ${c.day}`;
    case 'monthly': return `${mi} ${h} ${c.date} * *`;
    default: return (c.cron || '').trim();
  }
}

function describe(a) {
  if (a.schedule === 'once') return 'Once · ' + showTime(a.runAt, a.timezone);
  const c = parseCron(a.cron);
  switch (c.preset) {
    case 'minutes': return c.every === 1 ? 'Every minute' : `Every ${c.every} minutes`;
    case 'hourly': return `Every hour at :${pad(c.minute)}`;
    case 'daily': return `Every day at ${c.time}`;
    case 'weekdays': return `Weekdays at ${c.time}`;
    case 'weekly': return `${DAYS[c.day]}s at ${c.time}`;
    case 'monthly': return `Monthly on day ${c.date} at ${c.time}`;
    default: return `Cron ${a.cron}`;
  }
}

// ---- runtimes ----

function runtimeOptions(s) {
  const out = [];
  for (const a of s.agents.items) out.push({ value: 'a:' + a.id, text: `${a.name || a.id} · ACP` });
  for (const g of s.providers.items) {
    if (g.broken) continue;
    for (const m of g.models.filter((x) => x.listed !== false)) out.push({ value: `p:${g.id}\n${m.id}`, text: `${m.name || m.id} · ${g.name || g.id} (API)` });
  }
  return out;
}

const runtimeValue = (a) => (a.agentId ? 'a:' + a.agentId : a.providerId ? `p:${a.providerId}\n${a.modelId}` : '');

function runtimeName(s, a) {
  if (a.agentId) {
    const ag = s.agents.items.find((x) => x.id === a.agentId);
    return `${ag?.name || a.agentId}${a.modelId ? ' · ' + a.modelId : ''} (ACP)`;
  }
  const g = s.providers.items.find((x) => x.id === a.providerId);
  const m = g?.models.find((x) => x.id === a.modelId);
  return `${m?.name || a.modelId} (API)`;
}

const chatKey = (c) => (c ? `${c.channel}:${c.chatId}:${c.thread || ''}` : '');

function state(a) {
  if (!a.enabled) return ['Paused', 'text-faint'];
  if (a.expired) return ['Expired', 'text-faint'];
  if (!a.nextRunAt) return ['Done', 'text-faint'];
  return ['Active', 'text-ok'];
}

const RUN_STYLE = { running: 'text-accent', completed: 'text-ok', failed: 'text-bad', cancelled: 'text-faint', interrupted: 'text-warn' };

// ---- list ----

export function Automations({ arg }) {
  const s = useStore();
  const [list, setList] = useState({ status: 'loading', items: [], zone: '' });
  const [open, setOpen] = useState(null);
  const [busy, setBusy] = useState('');
  const load = async (quiet) => {
    if (!quiet) setList((l) => ({ ...l, status: l.items.length ? 'ready' : 'loading' }));
    try { const r = await api.automations(); setList({ status: 'ready', items: r.data, zone: r.timezone }); }
    catch (e) { if (!quiet) setList((l) => ({ ...l, status: 'error', error: errorText(e) })); }
  };
  // Runs start and end on the server's clock; keep the list's next/last run
  // fresh, and follow a run that is going closely.
  const running = list.items.some((a) => a.lastRun?.status === 'running');
  useEffect(() => { load(); }, []);
  useEffect(() => { const t = setInterval(() => load(true), running ? 2_000 : 10_000); return () => clearInterval(t); }, [running]);
  const editing = arg === 'new' ? { isNew: true } : list.items.find((x) => x.id === arg);
  const close = () => navigate('#/settings/automations');
  const act = async (key, fn, done) => {
    setBusy(key);
    try { await fn(); if (done) toast(done); await load(true); } catch (e) { toast(errorText(e), 'error'); }
    setBusy('');
  };

  return html`<div>
    <div class="flex items-end justify-between gap-4 mb-4">
      <div>
        <h2 class="text-[17px] font-semibold">Automations</h2>
        <p class="text-muted text-[13px] mt-0.5">Send a prompt to an agent or model on a schedule. Times follow each automation’s time zone.</p>
      </div>
      <${Button} kind="primary" onClick=${() => navigate('#/settings/automations/new')}><${Icon} name="plus" size=${14} /> New automation</${Button}>
    </div>
    ${list.status === 'loading' && html`<div class="flex flex-col gap-2">${[1, 2].map((i) => html`<div key=${i} class="skeleton h-[76px] rounded-xl"></div>`)}</div>`}
    ${list.status === 'error' && html`<${ErrorBlock} title="Could not load automations" message=${list.error} onRetry=${() => load()} />`}
    ${list.status === 'ready' && !list.items.length && html`<div class="rounded-xl border border-dashed border-line p-8 text-center">
      <${Icon} name="clock" size=${22} class="text-faint mx-auto" />
      <p class="font-medium mt-2">No automations yet</p>
      <p class="text-muted text-[13px] mt-1">Ask for a morning summary, a nightly check, or a one-off reminder.</p>
      <${Button} class="mt-4" kind="primary" onClick=${() => navigate('#/settings/automations/new')}>New automation</${Button}>
    </div>`}
    <div class="flex flex-col gap-2">
      ${list.items.map((a) => {
        const [label, tone] = state(a);
        const last = a.lastRun;
        return html`<div key=${a.id} class="rounded-xl border border-line bg-panel">
          <div class="px-4 py-3 flex flex-col sm:flex-row sm:items-center gap-3">
            <button type="button" class="flex-1 min-w-0 text-left" onClick=${() => navigate('#/settings/automations/' + encodeURIComponent(a.id))}>
              <div class="flex items-center gap-2"><span class="font-medium truncate">${a.name}</span><span class=${cx('text-xs', tone)}>${label}</span></div>
              <div class="text-xs text-muted truncate mt-0.5">${describe(a)} · ${runtimeName(s, a)}</div>
              <div class="text-xs text-faint truncate mt-0.5">
                ${a.enabled && a.nextRunAt && !a.expired ? `Next ${showTime(a.nextRunAt, a.timezone)}` : ''}
                ${last && html`${a.enabled && a.nextRunAt && !a.expired ? ' · ' : ''}Last run <span class=${RUN_STYLE[last.status]}>${last.status}</span> ${relTime(last.startedAt)}`}
                ${a.notify && ' · reply goes to a chat'}
              </div>
            </button>
            <div class="flex items-center gap-1.5 shrink-0">
              <${Button} busy=${busy === 'run:' + a.id} onClick=${() => act('run:' + a.id, () => api.runAutomation(a.id), `Started ${a.name}.`)}><${Icon} name="play" size=${13} /> Run now</${Button}>
              <${Button} kind="ghost" busy=${busy === 'pause:' + a.id} onClick=${() => act('pause:' + a.id, () => api.enableAutomation(a.id, !a.enabled))}>
                <${Icon} name=${a.enabled ? 'pause' : 'play'} size=${13} /> ${a.enabled ? 'Pause' : 'Resume'}</${Button}>
              <${Button} kind="ghost" aria-expanded=${open === a.id} onClick=${() => setOpen(open === a.id ? null : a.id)}>History</${Button}>
            </div>
          </div>
          ${open === a.id && html`<${History} auto=${a} />`}
        </div>`;
      })}
    </div>
    ${arg && list.status === 'ready' && !editing && html`<${ErrorBlock} class="mt-4" title="Automation not found" message=${`No automation with id “${arg}”.`} />`}
    <${AutomationDialog} key=${(arg || '') + (editing ? ':1' : ':0')} auto=${editing} zone=${list.zone} onClose=${close} onSaved=${() => load(true)} />
  </div>`;
}

function History({ auto: a }) {
  const [runs, setRuns] = useState({ status: 'loading', items: [] });
  const load = async () => {
    try { const r = await api.automationRuns(a.id); setRuns({ status: 'ready', items: r.data }); }
    catch (e) { setRuns({ status: 'error', items: [], error: errorText(e) }); }
  };
  // The list refreshes every few seconds; follow its last run.
  useEffect(() => { load(); }, [a.lastRun?.id, a.lastRun?.status]);
  return html`<div class="border-t border-line px-4 py-2">
    ${runs.status === 'loading' && html`<div class="flex items-center gap-2 py-2 text-muted text-[13px]"><${Spinner} size=${12} /> Loading runs…</div>`}
    ${runs.status === 'error' && html`<p class="py-2 text-bad text-xs">${runs.error}</p>`}
    ${runs.status === 'ready' && !runs.items.length && html`<p class="py-2 text-muted text-[13px]">No runs yet. Run now to try it.</p>`}
    ${runs.items.map((r) => html`<div key=${r.id} class="flex items-start gap-3 py-2 text-[13px] border-b border-line last:border-0">
      <span class=${cx('w-20 shrink-0 flex items-center gap-1.5', RUN_STYLE[r.status])}>${r.status === 'running' && html`<${Spinner} size=${11} />`}${r.status}</span>
      <div class="flex-1 min-w-0">
        <div>${showTime(r.startedAt, a.timezone)}${r.endedAt ? html` <span class="text-faint">· ${Math.max(1, Math.round((new Date(r.endedAt) - new Date(r.startedAt)) / 1000))}s</span>` : ''}</div>
        ${r.note && html`<div class="text-xs text-muted mt-0.5 break-words">${r.note}</div>`}
      </div>
      ${r.conversationId && html`<a class="text-xs text-accent hover:underline shrink-0" href=${'#/c/' + encodeURIComponent(r.conversationId)}>Open thread</a>`}
    </div>`)}
  </div>`;
}

// ---- editor ----

function AutomationDialog({ auto: a, zone: serverZone, onClose, onSaved }) {
  const s = useStore();
  const open = !!a;
  const isNew = !!a?.isNew;
  const tz = a?.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || serverZone || 'UTC';
  const parsed = a?.schedule === 'cron' ? parseCron(a.cron) : { preset: 'daily' };
  const [f, setF] = useState(() => ({
    name: a?.name || '', prompt: a?.prompt || '', runtime: a ? runtimeValue(a) : '',
    schedule: a?.schedule || 'cron', zone: tz,
    runAt: a?.runAt ? zoned(Date.parse(a.runAt), tz) : zoned(Date.now() + 3600_000, tz),
    preset: parsed.preset, every: parsed.every || 15, minute: parsed.minute ?? 0, time: parsed.time || '09:00',
    day: parsed.day ?? 1, date: parsed.date || 1, cron: a?.cron || '0 9 * * *',
    thread: a?.thread || 'new', projectId: a?.projectId || '', notify: chatKey(a?.notify),
    expires: a?.expiresAt ? zoned(Date.parse(a.expiresAt), tz) : '', enabled: a ? a.enabled !== false : true,
  }));
  const set = (k) => (v) => setF((x) => ({ ...x, [k]: v }));
  const [chats, setChats] = useState({ status: 'idle', items: [] });
  const [busy, setBusy] = useState('');
  const [err, setErr] = useState('');
  const [confirmDelete, setConfirmDelete] = useState(false);
  useEffect(() => {
    if (!open) return;
    setChats({ status: 'loading', items: [] });
    api.chats().then((r) => setChats({ status: 'ready', items: r.data }), () => setChats({ status: 'error', items: [] }));
  }, [open]);
  const runtimes = runtimeOptions(s);
  // An edit keeps the automation's runtime even when the mode, a broken
  // provider, or a removed agent leaves it out of the list.
  if (open && !isNew && f.runtime === runtimeValue(a) && !runtimes.some((o) => o.value === f.runtime)) runtimes.unshift({ value: f.runtime, text: runtimeName(s, a) });
  useEffect(() => { if (open && !f.runtime && runtimes.length) set('runtime')(runtimes[0].value); }, [open, runtimes.length]);
  const cron = buildCron(f);

  const chatOptions = [{ value: '', text: 'Only the thread' }, ...chats.items.map((c) => ({
    value: c.key, text: `${c.display || c.chatId} · ${c.channel === 'whatsapp' ? 'WhatsApp' : c.channel === 'telegram' ? 'Telegram' : c.channel}${c.group ? ' group' : ''}`,
  }))];
  if (f.notify && !chats.items.some((c) => c.key === f.notify)) chatOptions.push({ value: f.notify, text: f.notify.split(':')[1] + ' (no longer linked)' });

  const save = async () => {
    setErr('');
    let doc;
    try {
      const [kind, rest] = [f.runtime.slice(0, 2), f.runtime.slice(2)];
      const [providerId, modelId] = kind === 'p:' ? rest.split('\n') : ['', ''];
      const notify = f.notify ? (() => {
        const c = chats.items.find((x) => x.key === f.notify);
        if (c) return { channel: c.channel, chatId: c.chatId, thread: c.thread || '' };
        const [channel, chatId, thread] = f.notify.split(':');
        return { channel, chatId, thread };
      })() : null;
      doc = {
        name: f.name.trim(), prompt: f.prompt.trim(),
        agentId: kind === 'a:' ? rest : '', providerId, modelId,
        schedule: f.schedule, timezone: f.zone.trim(), thread: f.thread, projectId: f.projectId, notify,
        enabled: f.enabled, expiresAt: f.expires ? fromZoned(f.expires, f.zone.trim()) : null,
        runAt: f.schedule === 'once' ? fromZoned(f.runAt, f.zone.trim()) : null,
        cron: f.schedule === 'cron' ? cron : '',
      };
    } catch (e) { setErr(e.message); return; }
    setBusy('save');
    try {
      const saved = isNew ? await api.createAutomation(doc) : await api.updateAutomation(a.id, { ...doc, updatedAt: a.updatedAt });
      toast(saved.nextRunAt && saved.enabled ? `Saved. Next run ${showTime(saved.nextRunAt, saved.timezone)}.` : 'Saved.');
      onSaved(); onClose();
    } catch (e) { setErr(errorText(e)); }
    setBusy('');
  };
  const remove = async () => {
    setBusy('delete'); setErr('');
    try { await api.deleteAutomation(a.id); toast(`Deleted ${a.name}. Its threads stay.`); onSaved(); onClose(); }
    catch (e) { setErr(errorText(e)); setConfirmDelete(false); }
    setBusy('');
  };
  const testSend = async () => {
    setBusy('test'); setErr('');
    try { await api.testAutomation(a.id); toast('Test message sent. Check the chat.'); } catch (e) { setErr(errorText(e)); }
    setBusy('');
  };

  const field = (label, body, hint) => html`<label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">${label}</span>${body}${hint && html`<span class="text-xs text-faint">${hint}</span>`}</label>`;
  const projectOptions = [{ value: '', text: 'No project' }, ...s.projects.items.map((p) => ({ value: p.id, text: p.name }))];

  return html`<${Dialog} open=${open} onClose=${() => !busy && onClose()} title=${isNew ? 'New automation' : `Edit ${a?.name || ''}`}
    footer=${html`
      ${!isNew && (confirmDelete
        ? html`<span class="mr-auto flex items-center gap-2 text-[13px]">Delete it?
            <${Button} kind="danger" busy=${busy === 'delete'} onClick=${remove}>Delete</${Button}>
            <${Button} kind="ghost" onClick=${() => setConfirmDelete(false)}>Keep</${Button}></span>`
        : html`<${Button} kind="danger" class="mr-auto" disabled=${!!busy} onClick=${() => setConfirmDelete(true)}>Delete</${Button}>`)}
      <${Button} kind="ghost" disabled=${!!busy} onClick=${onClose}>Cancel</${Button}>
      <${Button} kind="primary" busy=${busy === 'save'} disabled=${!!busy || !f.runtime} onClick=${save}>${isNew ? 'Create' : 'Save'}</${Button}>`}>
    <form noValidate class="flex flex-col gap-4" onSubmit=${(e) => { e.preventDefault(); save(); }}>
      ${err && html`<${ErrorBlock} title="Could not save" message=${err} />`}
      ${field('Name', html`<input class=${inputCls} value=${f.name} onInput=${(e) => set('name')(e.target.value)} placeholder="Morning summary" />`)}
      ${field('Prompt', html`<textarea class=${cx(inputCls, 'h-28 py-2 resize-y')} value=${f.prompt} onInput=${(e) => set('prompt')(e.target.value)}
        placeholder="Summarize what changed in the repo since yesterday and list anything that needs me."></textarea>`)}
      ${field('Runs on', runtimes.length
        ? html`<${Select} label="Runs on" search=${runtimes.length > 8} value=${f.runtime} options=${runtimes} onChange=${set('runtime')} />`
        : html`<p class="text-warn text-xs">Add a provider or an agent first.</p>`)}

      <div class="flex flex-col gap-2 text-[13px]">
        <span class="text-muted">When</span>
        <div class="flex gap-1 p-0.5 rounded-lg bg-raised border border-line w-fit" role="radiogroup" aria-label="Schedule">
          ${[['cron', 'Repeat'], ['once', 'Once']].map(([v, t]) => html`<button key=${v} type="button" role="radio" aria-checked=${f.schedule === v}
            class=${cx('px-3 h-7 rounded-md', f.schedule === v ? 'bg-panel text-fg shadow-sm' : 'text-muted hover:text-fg')} onClick=${() => set('schedule')(v)}>${t}</button>`)}
        </div>
        ${f.schedule === 'once'
          ? html`<${DateTimeField} class="w-56" label="Run at" value=${f.runAt} onChange=${set('runAt')} />`
          : html`<div class="flex flex-wrap items-center gap-2">
              <${Select} label="Repeat" class="w-44" value=${f.preset} onChange=${set('preset')} options=${[
                { value: 'minutes', text: 'Every few minutes' }, { value: 'hourly', text: 'Every hour' }, { value: 'daily', text: 'Every day' },
                { value: 'weekdays', text: 'Every weekday' }, { value: 'weekly', text: 'Every week' }, { value: 'monthly', text: 'Every month' },
                { value: 'custom', text: 'Cron expression' }]} />
              ${f.preset === 'minutes' && html`<span class="flex items-center gap-2 whitespace-nowrap">every <${NumberField} label="Minutes" min=${1} max=${59} value=${f.every} onChange=${set('every')} /> minutes</span>`}
              ${f.preset === 'hourly' && html`<span class="flex items-center gap-2 whitespace-nowrap">at minute <${NumberField} label="Minute" min=${0} max=${59} value=${f.minute} onChange=${set('minute')} /></span>`}
              ${f.preset === 'weekly' && html`<${Select} label="Day" class="w-36" value=${String(f.day)} onChange=${(v) => set('day')(+v)} options=${DAYS.map((d, i) => ({ value: String(i), text: d }))} />`}
              ${f.preset === 'monthly' && html`<span class="flex items-center gap-2 whitespace-nowrap">on day <${NumberField} label="Day of month" min=${1} max=${31} value=${f.date} onChange=${set('date')} /></span>`}
              ${['daily', 'weekdays', 'weekly', 'monthly'].includes(f.preset) && html`<span class="flex items-center gap-2 whitespace-nowrap">at <${TimeField} label="Time" value=${f.time} onChange=${set('time')} /></span>`}
              ${f.preset === 'custom' && html`<input class=${cx(inputCls, 'w-44 font-mono')} aria-label="Cron expression" value=${f.cron} onInput=${(e) => set('cron')(e.target.value)} placeholder="0 9 * * 1-5" />`}
            </div>
            <span class="text-xs text-faint">${f.preset === 'custom' ? 'minute · hour · day of month · month · day of week' : html`Cron <code class="font-mono">${cron}</code>`}. A run missed while the server was off happens once when it is back.</span>`}
        <label class="flex items-center gap-2 text-xs text-muted whitespace-nowrap">Time zone
          <input class=${cx(inputCls, 'max-w-56 h-7 text-xs')} value=${f.zone} onInput=${(e) => set('zone')(e.target.value)} placeholder="Asia/Jakarta" /></label>
      </div>

      <div class="grid sm:grid-cols-2 gap-4">
        ${field('Thread', html`<${Select} label="Thread" value=${f.thread} onChange=${set('thread')} options=${[
          { value: 'new', text: 'A new thread each run' }, { value: 'same', text: 'One thread for every run' }]} />`,
          f.thread === 'same' ? 'Each run sees the earlier ones.' : 'Runs start fresh.')}
        ${field('Project', html`<${Select} label="Project" value=${f.projectId} onChange=${set('projectId')} options=${projectOptions} />`, 'New threads work in its folders.')}
        ${field('Also send the reply to', html`<${Select} label="Result chat" value=${f.notify} onChange=${set('notify')} options=${chatOptions} />`,
          chats.status === 'ready' && !chats.items.length ? 'Chats appear here after they message NusaShell once.' : 'The final reply only; tool work stays on the web.')}
        ${field('Stop after', html`<${DateTimeField} clearable label="Expires at" value=${f.expires} onChange=${set('expires')} />`, 'Optional. No run starts after this.')}
      </div>
      ${!isNew && html`<div class="flex items-center justify-between text-[13px] border-t border-line pt-4">
        <div><div>Enabled</div><div class="text-xs text-muted">A paused automation keeps its history.</div></div>
        <div class="flex items-center gap-3">
          ${a?.notify && html`<${Button} busy=${busy === 'test'} onClick=${testSend}>Send a test to the chat</${Button}>`}
          <${Toggle} checked=${f.enabled} onChange=${set('enabled')} label="Enabled" />
        </div>
      </div>`}
    </form>
  </${Dialog}>`;
}
