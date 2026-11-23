// MCP servers: the built-in ones every thread has, and the ones the owner
// adds. Agents get the enabled servers in their next session; the model
// runtime connects at once, and its connection state shows here.
import { html, useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { toast } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { Icon } from './icons.js';
import { cx, Button, Dialog, ErrorBlock, IconButton, Spinner, Toggle } from './ui.js';

const BUILT_IN = [
  { name: 'conversation', desc: 'List, read, wait on, send to, stop, and open threads.' },
  { name: 'browser', desc: 'Drive this thread’s browser tab; watch it live in the thread.' },
];

const inputCls = 'h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent w-full';

export function McpServers({ onChanged }) {
  const [list, setList] = useState({ status: 'loading', items: [] });
  const [editing, setEditing] = useState(null); // null | {} (new) | server
  const load = async () => {
    try { const r = await api.mcpServers(); setList({ status: 'ready', items: r.data }); }
    catch (e) { setList({ status: 'error', items: [], error: errorText(e) }); }
  };
  useEffect(() => { load(); }, []);
  // A new server connects in the background; follow it for a bit.
  useEffect(() => {
    if (!list.items.some((s) => s.enabled && !s.status)) return;
    const t = setTimeout(load, 2000);
    return () => clearTimeout(t);
  }, [list]);
  const saved = () => { setEditing(null); load(); onChanged?.(); };
  const toggle = async (s, on) => {
    try { await api.enableMcpServer(s.id, on); load(); onChanged?.(); } catch (e) { toast(errorText(e), 'error'); }
  };
  return html`<section class="mb-8">
    <div class="flex items-end justify-between gap-4 mb-4">
      <div><h2 class="text-[17px] font-semibold">MCP servers</h2>
        <p class="text-muted text-[13px] mt-0.5">Tool servers every agent session and model thread gets. Agents pick up changes in their next session.</p></div>
      <${Button} kind="primary" onClick=${() => setEditing({})}><${Icon} name="plus" size=${14} /> Add server</${Button}>
    </div>
    <div class="rounded-xl border border-line bg-panel divide-y divide-line">
      ${BUILT_IN.map((b) => html`<div key=${b.name} class="flex items-center gap-3 px-4 py-3">
        <span class="size-2 rounded-full shrink-0 bg-ok"></span>
        <span class="font-mono text-[13px]">${b.name}</span>
        <span class="flex-1 truncate text-xs text-muted">${b.desc}</span>
        <span class="text-[11px] text-faint border border-line rounded px-1.5">Built in</span>
      </div>`)}
      ${list.status === 'loading' && html`<div class="px-4 py-3 flex items-center gap-2 text-muted text-[13px]"><${Spinner} size=${12} /> Loading servers…</div>`}
      ${list.status === 'error' && html`<div class="p-3"><${ErrorBlock} title="Could not load MCP servers" message=${list.error} onRetry=${load} /></div>`}
      ${list.items.map((s) => {
        const st = s.status;
        const tone = !s.enabled ? 'bg-faint' : !st ? 'bg-warn' : st.connected ? 'bg-ok' : 'bg-bad';
        const note = !s.enabled ? 'Off' : !st ? 'Connecting…' : st.connected ? `${st.tools} tool${st.tools === 1 ? '' : 's'}` : (st.error || 'Not connected');
        const target = s.transport === 'stdio' ? [s.command, ...(s.args || [])].join(' ') : s.url;
        return html`<div key=${s.id} class="flex items-center gap-3 px-4 py-3">
          <span class=${cx('size-2 rounded-full shrink-0', tone)}></span>
          <button type="button" class="flex-1 min-w-0 text-left" onClick=${() => setEditing(s)}>
            <div class="flex items-center gap-2"><span class="font-mono text-[13px]">${s.name}</span>
              <span class="text-[11px] text-faint border border-line rounded px-1">${s.transport}</span></div>
            <div class="text-xs text-muted truncate font-mono mt-0.5">${target}</div>
            <div class=${cx('text-xs truncate mt-0.5', s.enabled && st && !st.connected ? 'text-bad' : 'text-faint')} title=${note}>${note}</div>
          </button>
          <${Toggle} checked=${s.enabled} label=${'Enable ' + s.name} onChange=${(v) => toggle(s, v)} />
        </div>`;
      })}
    </div>
    <${ServerDialog} key=${editing ? editing.id || 'new' : 'closed'} server=${editing} onClose=${() => setEditing(null)} onSaved=${saved} />
  </section>`;
}

const pairsOf = (names) => (names || []).map((name) => ({ name, value: '', kept: true }));

function Pairs({ label, rows, onChange, placeholder }) {
  const set = (i, k, v) => onChange(rows.map((r, j) => (j === i ? { ...r, [k]: v, ...(k === 'value' ? { kept: false } : {}) } : r)));
  return html`<div class="flex flex-col gap-1.5 text-[13px]">
    <span class="text-muted">${label}</span>
    ${rows.map((r, i) => html`<div key=${i} class="flex items-center gap-2">
      <input class=${cx(inputCls, 'font-mono w-2/5')} aria-label=${label + ' name'} placeholder=${placeholder} value=${r.name} onInput=${(e) => set(i, 'name', e.target.value)} />
      <input class=${cx(inputCls, 'font-mono')} type="password" aria-label=${label + ' value'} autocomplete="off"
        placeholder=${r.kept ? 'Saved — type to replace' : 'Value'} value=${r.value} onInput=${(e) => set(i, 'value', e.target.value)} />
      <${IconButton} icon="x" size=${13} label=${'Remove ' + (r.name || 'row')} class="size-8 shrink-0" onClick=${() => onChange(rows.filter((_, j) => j !== i))} />
    </div>`)}
    <button type="button" class="self-start text-xs text-accent" onClick=${() => onChange([...rows, { name: '', value: '', kept: false }])}>+ Add</button>
  </div>`;
}

function ServerDialog({ server: s, onClose, onSaved }) {
  const open = !!s;
  const isNew = open && !s.id;
  const [f, setF] = useState(() => ({
    name: s?.name || '', transport: s?.transport || 'stdio', command: s?.command || '', args: (s?.args || []).join('\n'),
    url: s?.url || '', env: pairsOf(s?.env), headers: pairsOf(s?.headers), enabled: s ? s.enabled !== false : true,
  }));
  const [busy, setBusy] = useState('');
  const [err, setErr] = useState('');
  const [confirm, setConfirm] = useState(false);
  const set = (k) => (v) => setF((x) => ({ ...x, [k]: v }));
  const remote = f.transport !== 'stdio';
  const save = async () => {
    setBusy('save'); setErr('');
    const rows = (list) => list.filter((r) => r.name.trim()).map((r) => ({ name: r.name.trim(), value: r.kept ? '' : r.value }));
    const body = {
      name: f.name.trim(), transport: f.transport, enabled: f.enabled,
      command: remote ? '' : f.command.trim(), args: remote ? [] : f.args.split('\n').map((a) => a.trim()).filter(Boolean),
      env: remote ? [] : rows(f.env), url: remote ? f.url.trim() : '', headers: remote ? rows(f.headers) : [],
    };
    try {
      if (isNew) await api.createMcpServer(body); else await api.updateMcpServer(s.id, { ...body, updatedAt: s.updatedAt });
      toast(isNew ? `Added ${body.name}.` : `Saved ${body.name}.`);
      onSaved();
    } catch (e) { setErr(errorText(e)); }
    setBusy('');
  };
  const remove = async () => {
    setBusy('delete');
    try { await api.deleteMcpServer(s.id); toast(`Removed ${s.name}.`); onSaved(); } catch (e) { setErr(errorText(e)); setBusy(''); }
  };
  return html`<${Dialog} open=${open} onClose=${onClose} title=${isNew ? 'Add MCP server' : 'Edit ' + (s?.name || '')}
    footer=${html`${!isNew && html`<${Button} kind="danger" class="mr-auto" busy=${busy === 'delete'} disabled=${!!busy}
        onClick=${() => (confirm ? remove() : setConfirm(true))}>${confirm ? 'Remove for good' : 'Remove'}</${Button}>`}
      <${Button} kind="ghost" onClick=${onClose}>Cancel</${Button}>
      <${Button} kind="primary" busy=${busy === 'save'} disabled=${!!busy || !f.name.trim() || (remote ? !f.url.trim() : !f.command.trim())} onClick=${save}>${isNew ? 'Add' : 'Save'}</${Button}>`}>
    <div class="flex flex-col gap-4">
      ${err && html`<${ErrorBlock} title="Could not save" message=${err} />`}
      <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Name</span>
        <input class=${cx(inputCls, 'font-mono')} value=${f.name} placeholder="github" onInput=${(e) => set('name')(e.target.value)} />
        <span class="text-xs text-faint">Lowercase letters, digits, - and _. Model tools are named <code class="font-mono">${(f.name || 'name') + '_tool'}</code>.</span></label>
      <div class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Transport</span>
        <div class="flex gap-1 p-0.5 rounded-lg bg-raised border border-line w-fit" role="radiogroup" aria-label="Transport">
          ${[['stdio', 'Command'], ['http', 'HTTP'], ['sse', 'SSE']].map(([v, t]) => html`<button key=${v} type="button" role="radio" aria-checked=${f.transport === v}
            class=${cx('px-3 h-7 rounded-md', f.transport === v ? 'bg-panel text-fg shadow-sm' : 'text-muted hover:text-fg')} onClick=${() => set('transport')(v)}>${t}</button>`)}
        </div></div>
      ${remote ? html`
        <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">URL</span>
          <input class=${cx(inputCls, 'font-mono')} value=${f.url} placeholder="https://example.com/mcp" onInput=${(e) => set('url')(e.target.value)} /></label>
        <${Pairs} label="Headers" placeholder="Authorization" rows=${f.headers} onChange=${set('headers')} />
        ${f.transport === 'sse' && html`<p class="text-xs text-faint">SSE is the older transport; agents that cannot speak it go without this server.</p>`}`
      : html`
        <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Command</span>
          <input class=${cx(inputCls, 'font-mono')} value=${f.command} placeholder="npx" onInput=${(e) => set('command')(e.target.value)} /></label>
        <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Arguments, one per line</span>
          <textarea rows="3" class="rounded-lg bg-raised border border-line px-2.5 py-1.5 text-[13px] font-mono outline-none focus:border-accent"
            value=${f.args} placeholder=${'-y\n@modelcontextprotocol/server-github'} onInput=${(e) => set('args')(e.target.value)}></textarea></label>
        <${Pairs} label="Environment" placeholder="GITHUB_TOKEN" rows=${f.env} onChange=${set('env')} />`}
      <div class="flex items-center justify-between gap-3 text-[13px] pt-1 border-t border-line">
        <span>Enabled<span class="block text-xs text-muted">Off keeps it here without offering its tools.</span></span>
        <${Toggle} checked=${f.enabled} label="Enabled" onChange=${set('enabled')} />
      </div>
    </div>
  </${Dialog}>`;
}

// Logins the browser profiles saved; the agent fills them without seeing
// the password.
// BrowserSetup shows the Chrome the threads' browser runs, or installs
// one: the one dependency the binary cannot carry.
export function BrowserSetup() {
  const [offer, setOffer] = useState({ status: 'loading' });
  const [busy, setBusy] = useState('');
  const [failure, setFailure] = useState(null);
  const load = async () => {
    try { setOffer({ status: 'ready', ...(await api.browserInstallOffer()) }); }
    catch (e) { setOffer({ status: 'error', error: errorText(e) }); }
  };
  useEffect(() => { load(); }, []);
  const install = async (m) => {
    setBusy(m.id); setFailure(null);
    try {
      const r = await api.browserInstall(m.id);
      if (r.error) setFailure(r);
      else { toast(`Installed. Threads now use ${r.execPath}.`); await load(); }
    } catch (e) { setFailure({ error: errorText(e), log: '' }); }
    setBusy('');
  };
  return html`<section class="mb-8">
    <div class="mb-4"><h2 class="text-[17px] font-semibold">Browser</h2>
      <p class="text-muted text-[13px] mt-0.5">The Chrome or Chromium that threads drive with the browser tools.</p></div>
    <div class="rounded-xl border border-line bg-panel">
      ${offer.status === 'loading' && html`<div class="px-4 py-3 flex items-center gap-2 text-muted text-[13px]"><${Spinner} size=${12} /> Looking for a browser…</div>`}
      ${offer.status === 'error' && html`<div class="p-3"><${ErrorBlock} title="Could not check the browser" message=${offer.error} onRetry=${load} /></div>`}
      ${offer.status === 'ready' && !offer.missing && html`<div class="flex items-center gap-3 px-4 py-3">
        <span class="size-2 rounded-full bg-ok shrink-0"></span>
        <div class="flex-1 min-w-0 text-[13px] font-mono truncate" title=${offer.execPath}>${offer.execPath}</div>
      </div>`}
      ${offer.status === 'ready' && offer.missing && html`<div class="px-4 py-3 flex flex-col gap-3">
        <div class="flex items-start gap-2 text-[13px]"><${Icon} name="alert" size=${14} class="text-warn mt-0.5 shrink-0" />
          <span>No Chrome or Chromium found, so threads cannot use a browser. Searched ${offer.searched}.</span></div>
        ${offer.methods.map((m) => html`<div key=${m.id} class="flex items-center gap-3 rounded-lg border border-line px-3 py-2">
          <div class="flex-1 min-w-0">
            <div class="text-[13px] font-medium">${m.label}${m.needsRoot ? ' (needs root)' : ''}</div>
            <div class="text-xs text-muted font-mono break-all">${m.detail}</div>
          </div>
          <${Button} kind=${m === offer.methods[0] ? 'primary' : 'default'} busy=${busy === m.id} disabled=${!!busy && busy !== m.id} onClick=${() => install(m)}>Install</${Button}>
        </div>`)}
        <p class="text-xs text-muted">${offer.methods.length ? 'Or install' : 'Install'} Chrome or Chromium yourself, then put it on PATH or set <code>browser.exec</code>.${offer.proot ? ' Under proot only a package manager works (there is no linux/arm64 Chrome build).' : ''}</p>
        ${busy && html`<p class="text-xs text-muted">This can take a few minutes; keep this page open.</p>`}
        ${failure && html`<div class="rounded-lg border border-bad/40 p-3 text-[13px]">
          <div class="text-bad">${failure.error}</div>
          ${failure.log && html`<pre class="mt-2 max-h-48 overflow-auto text-[11px] text-muted whitespace-pre-wrap">${failure.log}</pre>`}
        </div>`}
      </div>`}
    </div>
  </section>`;
}

export function BrowserLogins() {
  const [list, setList] = useState({ status: 'loading', items: [] });
  const load = async () => {
    try { const r = await api.browserLogins(); setList({ status: 'ready', items: r.data }); }
    catch (e) { setList({ status: 'error', items: [], error: errorText(e) }); }
  };
  useEffect(() => { load(); }, []);
  const remove = async (l) => {
    try { await api.deleteBrowserLogin(l.id); toast(`Removed the login for ${l.username || l.origin}.`); load(); }
    catch (e) { toast(errorText(e), 'error'); }
  };
  return html`<section class="mb-8">
    <div class="mb-4"><h2 class="text-[17px] font-semibold">Browser logins</h2>
      <p class="text-muted text-[13px] mt-0.5">Saved when you agree in a thread’s browser. They fill the page again; agents fill them with browser_vault_fill and never see the password.</p></div>
    <div class="rounded-xl border border-line bg-panel divide-y divide-line">
      ${list.status === 'loading' && html`<div class="px-4 py-3 flex items-center gap-2 text-muted text-[13px]"><${Spinner} size=${12} /> Loading logins…</div>`}
      ${list.status === 'error' && html`<div class="p-3"><${ErrorBlock} title="Could not load logins" message=${list.error} onRetry=${load} /></div>`}
      ${list.status === 'ready' && !list.items.length && html`<p class="px-4 py-3 text-[13px] text-muted">No saved logins yet.</p>`}
      ${list.items.map((l) => html`<div key=${l.id} class="flex items-center gap-3 px-4 py-2.5">
        <${Icon} name="key" size=${14} class="text-faint shrink-0" />
        <div class="flex-1 min-w-0">
          <div class="text-[13px] truncate">${l.username || '(no username)'}</div>
          <div class="text-xs text-muted truncate font-mono">${l.origin}</div>
        </div>
        <span class="text-[11px] text-faint border border-line rounded px-1.5">${l.profile}</span>
        <${IconButton} icon="trash" size=${14} label=${'Remove the login for ' + (l.username || l.origin)} onClick=${() => remove(l)} />
      </div>`)}
    </div>
  </section>`;
}
