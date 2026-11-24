// Settings → Channels: WhatsApp and Telegram. Each card shows the live
// status, connects or pairs, and edits who may write in. Allowlists fail
// closed: an empty list lets nobody in.
import { html, useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { askConfirm, toast } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { Icon } from './icons.js';
import { cx, Button, Select, Toggle, ErrorBlock, Spinner, relTime } from './ui.js';
import { NumberField } from './fields.js';

const POLICIES = [
  { value: 'mention', text: 'Only when mentioned or replied to' },
  { value: 'all', text: 'Every message' },
  { value: 'disabled', text: 'Never in groups' },
];
const areaCls = 'rounded-lg bg-raised border border-line px-2.5 py-2 text-[13px] font-mono outline-none focus:border-accent w-full';
const inputCls = 'h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent w-full';
const lines = (text) => text.split(/[\n,]/).map((x) => x.trim()).filter(Boolean);

export function Channels() {
  const [list, setList] = useState({ status: 'loading', items: [], error: '' });
  const load = async () => {
    try { const r = await api.channels(); setList({ status: 'ready', items: r.data, error: '' }); }
    catch (e) { setList((l) => ({ ...l, status: l.items.length ? 'ready' : 'error', error: errorText(e) })); }
  };
  // Pairing and connecting change state on their own; poll while open.
  useEffect(() => { load(); const t = setInterval(load, 2500); return () => clearInterval(t); }, []);
  const status = (name) => list.items.find((x) => x.name === name);
  return html`<div class="flex flex-col gap-6">
    <div>
      <h2 class="text-[17px] font-semibold">Channels</h2>
      <p class="text-muted text-[13px] mt-0.5">Talk to your threads from WhatsApp and Telegram. Each chat gets its own thread; only the final reply goes back — thoughts and tool output stay here.</p>
    </div>
    ${list.status === 'loading' && html`<div class="flex flex-col gap-3">${[1, 2].map((i) => html`<div key=${i} class="skeleton h-40 rounded-xl"></div>`)}</div>`}
    ${list.status === 'error' && html`<${ErrorBlock} title="Could not load channels" message=${list.error} onRetry=${load} />`}
    ${list.status === 'ready' && html`
      <${WhatsAppCard} status=${status('whatsapp')} refresh=${load} />
      <${TelegramCard} status=${status('telegram')} refresh=${load} />`}
  </div>`;
}

function StatePill({ status }) {
  const tone = status?.connected ? 'text-ok' : status?.state === 'error' ? 'text-bad' : 'text-muted';
  return html`<span class=${cx('inline-flex items-center gap-1.5 text-xs', tone)}>
    <span class=${cx('size-1.5 rounded-full', status?.connected ? 'bg-ok' : status?.state === 'error' ? 'bg-bad' : 'bg-faint')}></span>
    ${status?.state || 'unknown'}${status?.account ? ` · ${status.account}` : ''}
  </span>`;
}

function Card({ title, icon, status, children, actions }) {
  return html`<section class="rounded-xl border border-line bg-panel">
    <header class="flex items-center gap-3 px-4 py-3 border-b border-line">
      <${Icon} name=${icon} class="text-muted" />
      <div class="flex-1 min-w-0">
        <div class="font-medium">${title}</div>
        <${StatePill} status=${status} />
      </div>
      <div class="flex items-center gap-2">${actions}</div>
    </header>
    ${status?.detail && html`<p class="px-4 pt-3 text-[13px] text-muted">${status.detail}</p>`}
    <div class="px-4 py-3 flex flex-col gap-4">${children}</div>
  </section>`;
}

// useSettings loads and saves one channel's settings document.
function useSettings(name, refresh) {
  const [doc, setDoc] = useState(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState('');
  const load = async () => {
    try { setDoc(await api.channelSettings(name)); setError(''); } catch (e) { setError(errorText(e)); }
  };
  useEffect(() => { load(); }, [name]);
  // save resolves true once stored, so a draft is kept when it was refused.
  const save = async (patch, what = 'save') => {
    setBusy(what);
    let ok = false;
    try { setDoc(await api.saveChannelSettings(name, patch)); refresh(); toast('Saved.'); ok = true; }
    catch (e) { toast(errorText(e), 'error'); }
    setBusy('');
    return ok;
  };
  const act = async (action) => {
    setBusy(action);
    try { await api.channelAction(name, action); refresh(); } catch (e) { toast(errorText(e), 'error'); }
    setBusy('');
  };
  return { doc, error, busy, load, save, act };
}

function Blocked({ status, onAllow, label }) {
  const list = status?.blocked || [];
  if (!list.length) return null;
  return html`<div class="flex flex-col gap-1.5 text-[13px]">
    <span class="text-muted">Recently turned away</span>
    <div class="rounded-lg border border-line divide-y divide-line">
      ${list.slice().reverse().map((b) => html`<div key=${b.senderId + b.chatId} class="flex items-center gap-3 px-3 py-2">
        <div class="flex-1 min-w-0">
          <div class="truncate">${b.name || b.senderId}${b.group ? html` <span class="text-faint">in a group</span>` : ''}</div>
          <div class="text-[11px] text-faint font-mono truncate">${label(b)} · ${relTime(b.at)}</div>
        </div>
        <${Button} onClick=${() => onAllow(b)}>Allow</${Button}>
      </div>`)}
    </div>
  </div>`;
}

function WhatsAppCard({ status, refresh }) {
  const s = useSettings('whatsapp', refresh);
  const [numbers, setNumbers] = useState(null);
  const [groups, setGroups] = useState({ status: 'idle', list: [] });
  const [qrTick, setQrTick] = useState(0);
  const doc = s.doc;
  // A draft is null until edited, so the field shows what is stored.
  const numbersText = numbers ?? doc?.allowedNumbers.join('\n') ?? '';
  useEffect(() => { if (!status?.hasQr) return; const t = setInterval(() => setQrTick((n) => n + 1), 3000); return () => clearInterval(t); }, [status?.hasQr]);
  const loadGroups = async () => {
    setGroups((g) => ({ ...g, status: 'loading' }));
    try { const r = await api.channelGroups('whatsapp'); setGroups({ status: 'ready', list: r.data }); }
    catch (e) { setGroups({ status: 'error', list: [], error: errorText(e) }); }
  };
  // Each time the account comes online, list its groups afresh.
  useEffect(() => { if (status?.connected) loadGroups(); }, [status?.connected]);
  const toggleGroup = async (jid) => {
    const set = new Set(doc.allowedGroups);
    if (set.has(jid)) set.delete(jid); else set.add(jid);
    await s.save({ allowedGroups: [...set] });
    setGroups((g) => ({ ...g, list: g.list.map((x) => (x.jid === jid ? { ...x, allowed: !x.allowed } : x)) }));
  };
  const paired = !!status?.account;
  const working = ['pairing', 'connecting', 'reconnecting'].includes(status?.state);
  const actions = html`
    ${!status?.connected && !working && doc?.enabled && html`<${Button} kind="primary" busy=${s.busy === 'connect'} onClick=${() => s.act('connect')}>${paired ? 'Connect' : 'Pair device'}</${Button}>`}
    ${working && html`<${Button} kind="ghost" busy=${s.busy === 'disconnect'} onClick=${() => s.act('disconnect')}>Cancel</${Button}>`}
    ${status?.connected && html`<${Button} kind="ghost" busy=${s.busy === 'disconnect'} onClick=${() => s.act('disconnect')}>Disconnect</${Button}>`}
    ${paired && html`<${Button} kind="danger" busy=${s.busy === 'logout'} onClick=${async () => { if (await askConfirm({ title: 'Unlink WhatsApp?', body: 'This device stops answering until you pair it again with a QR code.', confirm: 'Unlink', danger: true })) s.act('logout'); }}>Unlink</${Button}>`}`;
  return html`<${Card} title="WhatsApp" icon="message" status=${status} actions=${actions}>
    ${s.error && html`<${ErrorBlock} title="Could not load WhatsApp settings" message=${s.error} onRetry=${s.load} />`}
    ${!doc && !s.error && html`<div class="flex items-center gap-2 text-muted"><${Spinner} /> Loading…</div>`}
    ${status?.hasQr && html`<div class="flex flex-col items-center gap-2 py-2">
      <img src=${api.channelQR('whatsapp') + '?t=' + qrTick} alt="WhatsApp pairing QR code" class="size-56 rounded-lg bg-white p-2" />
      <p class="text-xs text-muted text-center">WhatsApp → Settings → Linked devices → Link a device, then scan.</p>
    </div>`}
    ${doc && html`
      <div class="flex items-center justify-between text-[13px]">
        <div><div>Enabled</div><div class="text-xs text-muted">Turning this off disconnects the device.</div></div>
        <${Toggle} checked=${doc.enabled} label="Enable WhatsApp" onChange=${(v) => s.save({ enabled: v })} />
      </div>
      <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Allowed numbers <span class="text-faint">· one per line, with country code; * allows everyone; empty allows nobody</span></span>
        <textarea class=${areaCls} rows="3" value=${numbersText} onInput=${(e) => setNumbers(e.target.value)} placeholder="+62 812 3456 7890"></textarea>
        <div><${Button} busy=${s.busy === 'save'} onClick=${() => s.save({ allowedNumbers: lines(numbersText) }).then((ok) => ok && setNumbers(null))}>Save numbers</${Button}></div>
      </label>
      <div class="flex flex-col gap-1.5 text-[13px]">
        <div class="flex items-center justify-between"><span class="text-muted">Allowed groups</span>
          ${status?.connected && html`<button type="button" class="text-xs text-accent" onClick=${loadGroups}>Reload groups</button>`}</div>
        ${!status?.connected && html`<p class="text-xs text-faint">Connect to pick from the groups this account has joined.</p>`}
        ${groups.status === 'loading' && html`<div class="flex items-center gap-2 text-muted text-xs"><${Spinner} size=${12} /> Loading groups…</div>`}
        ${groups.status === 'error' && html`<p class="text-xs text-bad">${groups.error}</p>`}
        ${groups.status === 'ready' && (groups.list.length
          ? html`<div class="rounded-lg border border-line divide-y divide-line max-h-56 overflow-y-auto">
              ${groups.list.map((g) => html`<div key=${g.jid} class="flex items-center gap-3 px-3 h-10">
                <span class="flex-1 min-w-0 truncate">${g.name || g.jid}</span>
                <span class="text-[11px] text-faint">${g.participants} members</span>
                <${Toggle} checked=${g.allowed} label=${'Allow ' + (g.name || g.jid)} onChange=${() => toggleGroup(g.jid)} />
              </div>`)}
            </div>`
          : html`<p class="text-xs text-faint">This account is in no groups.</p>`)}
      </div>
      <div class="flex items-center justify-between gap-4 text-[13px]">
        <span>Answer in groups</span>
        <${Select} label="Group policy" class="w-64" value=${doc.groupPolicy} options=${POLICIES} onChange=${(v) => s.save({ groupPolicy: v })} />
      </div>
      <div class="flex items-center justify-between text-[13px]">
        <span>Show “typing…” while working</span>
        <${Toggle} checked=${doc.sendTyping} label="Send typing" onChange=${(v) => s.save({ sendTyping: v })} />
      </div>
      <div class="flex items-center justify-between text-[13px]">
        <span>Send read receipts</span>
        <${Toggle} checked=${doc.readReceipts} label="Read receipts" onChange=${(v) => s.save({ readReceipts: v })} />
      </div>
      <${Blocked} status=${status} label=${(b) => b.senderId}
        onAllow=${(b) => b.group && b.chatId.endsWith('@g.us')
          ? s.save({ addGroups: [b.chatId] })
          : s.save({ addNumbers: [b.senderId] }).then((ok) => ok && setNumbers(null))} />
      <${Pacing} doc=${doc} save=${s.save} />`}
  </${Card}>`;
}

// Pacing is how WhatsApp sends and keeps messages: the gap between two
// sends to one chat, how many may wait, and how long kept messages stay
// searchable. Each number saves when it is left or Enter is pressed.
function Pacing({ doc, save }) {
  return html`<div class="flex flex-col gap-2 text-[13px] border-t border-line pt-3">
    <div class="text-muted">Sending and history</div>
    <${NumberSetting} label="Gap between two messages to one chat" hint="Looks less like a bot; 0 sends at once."
      unit="seconds" min=${0} max=${60} value=${doc.sendPacing} onSave=${(v) => save({ sendPacing: v })} />
    <${NumberSetting} label="Messages waiting per chat" hint="More are refused until the queue drains."
      unit="messages" min=${1} max=${200} value=${doc.queueCap} onSave=${(v) => save({ queueCap: v })} />
    <${NumberSetting} label="Keep messages for search" hint="What nusashell channel whatsapp search finds; 0 keeps them all."
      unit="days" min=${0} max=${3650} value=${doc.retentionDays} onSave=${(v) => save({ retentionDays: v })} />
  </div>`;
}

function NumberSetting({ label, hint, unit, min, max, value, onSave }) {
  return html`<label class="flex items-center justify-between gap-4">
    <span class="min-w-0"><span class="block">${label}</span><span class="block text-xs text-faint">${hint}</span></span>
    <span class="flex items-center gap-2 shrink-0">
      <${NumberField} label=${label} width="w-14" value=${value} min=${min} max=${max} onCommit=${onSave} />
      <span class="w-16 text-xs text-muted">${unit}</span>
    </span>
  </label>`;
}

function TelegramCard({ status, refresh }) {
  const s = useSettings('telegram', refresh);
  const [token, setToken] = useState('');
  const [users, setUsers] = useState(null);
  const [groupIDs, setGroupIDs] = useState(null);
  const doc = s.doc;
  // A draft is null until edited, so the field shows what is stored.
  const usersText = users ?? doc?.allowedUserIds.join('\n') ?? '';
  const groupsText = groupIDs ?? doc?.allowedGroupIds.join('\n') ?? '';
  const actions = html`
    ${doc?.tokenSet && doc?.enabled && !status?.connected && html`<${Button} kind="primary" busy=${s.busy === 'connect'} onClick=${() => s.act('connect')}>Connect</${Button}>`}
    ${status?.connected && html`<${Button} kind="ghost" busy=${s.busy === 'disconnect'} onClick=${() => s.act('disconnect')}>Disconnect</${Button}>`}`;
  return html`<${Card} title="Telegram" icon="message" status=${status} actions=${actions}>
    ${s.error && html`<${ErrorBlock} title="Could not load Telegram settings" message=${s.error} onRetry=${s.load} />`}
    ${!doc && !s.error && html`<div class="flex items-center gap-2 text-muted"><${Spinner} /> Loading…</div>`}
    ${doc && html`
      <div class="flex flex-col gap-1.5 text-[13px]">
        <span class="text-muted">Bot token <span class="text-faint">· from @BotFather; kept in the secret store and never shown again</span></span>
        ${doc.tokenSet
          ? html`<div class="flex items-center gap-2"><span class="text-ok text-xs">Token saved${doc.botUsername ? ` · @${doc.botUsername}` : ''}</span>
              <${Button} kind="ghost" busy=${s.busy === 'token'} onClick=${async () => { if (await askConfirm({ title: 'Remove the bot token?', body: 'The Telegram bot stops answering until a token is set again.', confirm: 'Remove', danger: true })) s.save({ token: '' }, 'token'); }}>Remove</${Button}></div>`
          : html`<div class="flex gap-2"><input type="password" autocomplete="off" class=${inputCls} value=${token} onInput=${(e) => setToken(e.target.value)} placeholder="123456:ABC…" />
              <${Button} kind="primary" busy=${s.busy === 'token'} disabled=${!token.trim()} onClick=${() => s.save({ token: token.trim() }, 'token').then((ok) => ok && setToken(''))}>Save</${Button}></div>`}
      </div>
      <div class="flex items-center justify-between text-[13px]">
        <span>Enabled</span>
        <${Toggle} checked=${doc.enabled} label="Enable Telegram" onChange=${(v) => s.save({ enabled: v })} />
      </div>
      <div class="grid sm:grid-cols-2 gap-3">
        <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Allowed user ids <span class="text-faint">· * allows everyone</span></span>
          <textarea class=${areaCls} rows="3" value=${usersText} onInput=${(e) => setUsers(e.target.value)} placeholder="123456789"></textarea>
        </label>
        <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Allowed group ids <span class="text-faint">· negative numbers</span></span>
          <textarea class=${areaCls} rows="3" value=${groupsText} onInput=${(e) => setGroupIDs(e.target.value)} placeholder="-1001234567890"></textarea>
        </label>
      </div>
      <div><${Button} busy=${s.busy === 'save'} onClick=${() => s.save({ allowedUserIds: lines(usersText), allowedGroupIds: lines(groupsText) }).then((ok) => { if (ok) { setUsers(null); setGroupIDs(null); } })}>Save allowlist</${Button}></div>
      <div class="flex items-center justify-between gap-4 text-[13px]">
        <span>Answer in groups</span>
        <${Select} label="Group policy" class="w-64" value=${doc.groupPolicy} options=${POLICIES} onChange=${(v) => s.save({ groupPolicy: v })} />
      </div>
      <${Blocked} status=${status} label=${(b) => (b.group ? `group ${b.chatId} · user ${b.senderId}` : `user ${b.senderId}`)}
        onAllow=${(b) => (b.group
          ? s.save({ addGroupIds: [b.chatId] }).then((ok) => ok && setGroupIDs(null))
          : s.save({ addUserIds: [b.senderId] }).then((ok) => ok && setUsers(null)))} />`}
  </${Card}>`;
}
