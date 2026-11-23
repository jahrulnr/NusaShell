// Settings → Agents: external agent CLIs that speak ACP. Lists configured
// agents, discovers installed CLIs, and edits one agent at a time. Launch
// environment values are write-only: a stored value comes back as its
// reference and is kept unless the user replaces or removes it.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore, toast, state, emit } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { loadAgents, loadAgentModels, navigate } from '../lib/chat.js';
import { Icon } from './icons.js';
import { Button, Dialog, Select, ErrorBlock, Spinner, Toggle } from './ui.js';
import { useXterm } from './terminal.js';

const inputCls = 'h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent w-full';
const areaCls = 'rounded-lg bg-raised border border-line px-2.5 py-2 text-[13px] font-mono outline-none focus:border-accent w-full';

const POLICIES = [
  { value: 'ask', text: 'Ask for every permission' },
  { value: 'auto_safe', text: 'Allow reads, ask for changes' },
  { value: 'auto_all', text: 'Allow everything' },
];
const policyText = (p) => POLICIES.find((x) => x.value === p)?.text || p;

export function Agents({ arg }) {
  const s = useStore();
  const a = s.agents;
  const editing = arg === 'new' ? { isNew: true } : a.items.find((x) => x.id === arg);
  const close = () => navigate('#/settings/agents');
  return html`<div>
    <div class="flex items-end justify-between gap-4 mb-4">
      <div>
        <h2 class="text-[17px] font-semibold">Agents</h2>
        <p class="text-muted text-[13px] mt-0.5">Agent CLIs that speak the Agent Client Protocol. They run their own tools; you approve what they ask for.</p>
      </div>
      <${Button} kind="primary" onClick=${() => navigate('#/settings/agents/new')}><${Icon} name="plus" size=${14} /> Add agent</${Button}>
    </div>
    ${a.status === 'loading' && html`<div class="flex flex-col gap-2">${[1, 2].map((i) => html`<div key=${i} class="skeleton h-[64px] rounded-xl"></div>`)}</div>`}
    ${a.status === 'error' && html`<${ErrorBlock} title="Could not load agents" message=${a.error} onRetry=${loadAgents} />`}
    <div class="flex flex-col gap-2">
      ${a.items.map((x) => html`<button key=${x.id} type="button" onClick=${() => navigate('#/settings/agents/' + encodeURIComponent(x.id))}
        class="text-left rounded-xl border border-line bg-panel hover:bg-hover px-4 py-3 flex items-center gap-4">
        <${Icon} name="bot" class="text-faint" />
        <div class="flex-1 min-w-0">
          <div class="flex items-center gap-2"><span class="font-medium truncate">${x.name || x.id}</span>
            ${x.name && x.name !== x.id && html`<span class="text-xs text-faint font-mono">${x.id}</span>`}</div>
          <div class="text-xs text-muted truncate mt-0.5 font-mono">${[x.command, ...x.args].join(' ')}</div>
        </div>
        <span class="hidden sm:block text-xs text-faint">${policyText(x.policy)}</span>
        <${Icon} name="chevronRight" class="text-faint" />
      </button>`)}
    </div>
    <${Detected} configured=${a.items} />
    ${arg && a.status === 'ready' && !editing && html`<${ErrorBlock} class="mt-4" title="Agent not found" message=${`No agent named “${arg}”.`} />`}
    <${AgentDialog} key=${(arg || '') + (editing ? ':1' : ':0')} agent=${editing} onClose=${close} />
  </div>`;
}

// Detected lists known agent CLIs found on this machine's PATH.
function Detected({ configured }) {
  const [st, setSt] = useState({ status: 'loading', list: [], error: '' });
  const [busy, setBusy] = useState(false);
  const load = async () => {
    setSt((x) => ({ ...x, status: 'loading' }));
    try { const r = await api.detectAgents(); setSt({ status: 'ready', list: r.data || [], error: '' }); }
    catch (e) { setSt({ status: 'error', list: [], error: errorText(e) }); }
  };
  useEffect(() => { load(); }, [configured.length]);
  const fresh = st.list.filter((c) => !c.registered);
  const addAll = async () => {
    setBusy(true);
    try {
      const r = await api.importAgents();
      await loadAgents();
      toast(`Added ${r.data.length} agent${r.data.length === 1 ? '' : 's'}.`);
    } catch (e) { toast('Could not add agents: ' + errorText(e), 'error'); }
    setBusy(false);
  };
  return html`<div class="mt-8">
    <div class="flex items-end justify-between gap-4 mb-3">
      <div>
        <h3 class="text-[14px] font-semibold">Found on this machine</h3>
        <p class="text-muted text-xs mt-0.5">Known agent CLIs on PATH. Log in with each agent's own CLI before first use.</p>
      </div>
      ${fresh.length > 0 && html`<${Button} busy=${busy} onClick=${addAll}>Add ${fresh.length === 1 ? 'it' : `all ${fresh.length}`}</${Button}>`}
    </div>
    ${st.status === 'loading' && html`<div class="flex items-center gap-2 text-muted text-[13px]"><${Spinner} /> Looking for agent CLIs…</div>`}
    ${st.status === 'error' && html`<${ErrorBlock} title="Detection failed" message=${st.error} onRetry=${load} />`}
    ${st.status === 'ready' && !st.list.length && html`<p class="text-muted text-[13px]">No known agent CLIs found. Install one (Claude Agent, Codex, Gemini CLI, …) or add a command by hand.</p>`}
    ${st.status === 'ready' && st.list.length > 0 && html`<div class="rounded-xl border border-line bg-panel divide-y divide-line">
      ${st.list.map((c) => html`<div key=${c.id} class="flex items-center gap-3 px-4 py-2.5 text-[13px]">
        <div class="flex-1 min-w-0">
          <div class="font-medium">${c.name}</div>
          <div class="text-xs text-faint truncate font-mono">${[c.command, ...c.args].join(' ')}</div>
        </div>
        ${c.registered ? html`<span class="text-xs text-ok">Added</span>` : html`<span class="text-xs text-faint">Not added</span>`}
      </div>`)}
    </div>`}
  </div>`;
}

// envText renders stored variables as NAME=<ref> lines; the ref round-trips
// unchanged, so saving without touching a line keeps its secret.
const envText = (env) => (env || []).map((e) => `${e.name}=${e.ref}`).join('\n');
function parseEnv(text) {
  const env = {};
  for (const line of text.split('\n')) {
    const t = line.trim();
    if (!t) continue;
    const i = t.indexOf('=');
    if (i <= 0) throw new Error(`“${t}” is not NAME=VALUE`);
    env[t.slice(0, i).trim()] = t.slice(i + 1);
  }
  return env;
}
// Arguments are one per line so values with spaces need no quoting.
const parseArgs = (text) => text.split('\n').map((x) => x.trim()).filter(Boolean);

// DefaultOptions picks what a thread of this agent starts with, for the
// session options the agent offers (its mode, a model parameter, …). The
// options come from the agent itself, so reading them starts it; a thread
// that picks its own keeps its pick.
function DefaultOptions({ id, value, onChange }) {
  const [doc, setDoc] = useState({ status: 'idle' });
  const load = async () => {
    setDoc({ status: 'loading' });
    try { setDoc({ status: 'ready', options: (await api.agentOptionsFor(id)).data || [] }); }
    catch (e) { setDoc({ status: 'error', error: errorText(e) }); }
  };
  if (doc.status === 'idle') {
    return html`<div class="flex items-center gap-3"><${Button} kind="ghost" onClick=${load}>Choose defaults</${Button}>
      <span class="text-xs text-faint">${Object.keys(value).length ? `${Object.keys(value).length} set. ` : ''}Starts the agent to read its options.</span></div>`;
  }
  if (doc.status === 'loading') return html`<div class="flex items-center gap-2 text-muted"><${Spinner} /> Asking the agent for its options…</div>`;
  if (doc.status === 'error') return html`<${ErrorBlock} title="Could not read the agent's options" message=${doc.error} onRetry=${load} />`;
  if (!doc.options?.length) return html`<p class="text-xs text-faint">This agent offers no session options.</p>`;
  return html`<div class="flex flex-col gap-2">${doc.options.map((o) => {
    const choices = o.type === 'boolean' ? [{ value: 'true', text: 'On' }, { value: 'false', text: 'Off' }] : (o.choices || []).map((c) => ({ value: c.value, text: c.name || c.value }));
    return html`<div key=${o.id} class="flex items-center gap-3">
      <span class="flex-1 min-w-0 truncate" title=${o.description || o.name}>${o.name || o.id}</span>
      <${Select} dense label=${o.name || o.id} class="w-44" value=${value[o.id] || ''}
        options=${[{ value: '', text: 'Agent’s default' }, ...choices]}
        onChange=${(v) => { const next = { ...value }; if (v) next[o.id] = v; else delete next[o.id]; onChange(next); }} />
    </div>`;
  })}</div>`;
}

function AgentDialog({ agent, onClose }) {
  const open = !!agent;
  const isNew = !!agent?.isNew;
  const [f, setF] = useState(() => ({
    id: agent?.id || '', name: agent?.name || '', command: agent?.command || '',
    args: (agent?.args || []).join('\n'), env: envText(agent?.env), policy: agent?.policy || 'ask',
    autoStart: !!agent?.autoStart, defaults: agent?.defaults || {},
  }));
  const [busy, setBusy] = useState('');
  const [err, setErr] = useState('');
  const [confirmDelete, setConfirmDelete] = useState(false);
  const set = (k) => (v) => setF((x) => ({ ...x, [k]: v }));
  const idOk = /^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(f.id);
  const valid = idOk && f.command.trim() !== '';

  const save = async () => {
    setBusy('save'); setErr('');
    try {
      const doc = { name: f.name.trim(), command: f.command.trim(), args: parseArgs(f.args), env: parseEnv(f.env), policy: f.policy, autoStart: f.autoStart, defaults: f.defaults };
      const saved = await api.saveAgent(f.id, doc);
      await loadAgents();
      // The definition changed; its model list may have too.
      delete state.agentModels[saved.id];
      emit();
      loadAgentModels(saved.id, true);
      toast(`Saved ${saved.name || saved.id}.`);
      onClose();
    } catch (e) { setErr(errorText(e)); }
    setBusy('');
  };
  const remove = async () => {
    setBusy('delete'); setErr('');
    try {
      await api.deleteAgent(f.id);
      await loadAgents();
      delete state.agentModels[f.id];
      toast(`Removed ${f.name || f.id}.`);
      onClose();
    } catch (e) { setErr(errorText(e)); setConfirmDelete(false); }
    setBusy('');
  };

  return html`<${Dialog} open=${open} onClose=${() => !busy && onClose()} title=${isNew ? 'Add agent' : `Edit ${agent?.name || agent?.id || ''}`}
    footer=${html`
      ${!isNew && (confirmDelete
        ? html`<span class="mr-auto flex items-center gap-2 text-[13px]">Delete this agent?
            <${Button} kind="danger" busy=${busy === 'delete'} onClick=${remove}>Delete</${Button}>
            <${Button} kind="ghost" onClick=${() => setConfirmDelete(false)}>Keep</${Button}></span>`
        : html`<${Button} kind="danger" class="mr-auto" disabled=${!!busy} onClick=${() => setConfirmDelete(true)}>Delete</${Button}>`)}
      <${Button} kind="ghost" disabled=${!!busy} onClick=${onClose}>Cancel</${Button}>
      <${Button} kind="primary" busy=${busy === 'save'} disabled=${!valid || !!busy} onClick=${save}>${isNew ? 'Add agent' : 'Save'}</${Button}>`}>
    <form noValidate class="flex flex-col gap-4" onSubmit=${(e) => { e.preventDefault(); if (valid) save(); }}>
      ${err && html`<${ErrorBlock} title="Could not save" message=${err} />`}
      <div class="grid sm:grid-cols-2 gap-3">
        <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">ID</span>
          <input class=${inputCls} value=${f.id} disabled=${!isNew} onInput=${(e) => set('id')(e.target.value)} placeholder="claude" autofocus=${isNew} />
          ${isNew && f.id && !idOk && html`<span class="text-xs text-bad">Letters, digits, dot, dash, underscore.</span>`}
        </label>
        <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Display name</span>
          <input class=${inputCls} value=${f.name} onInput=${(e) => set('name')(e.target.value)} placeholder="Optional" />
        </label>
      </div>
      <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Command</span>
        <input class=${inputCls + ' font-mono'} value=${f.command} onInput=${(e) => set('command')(e.target.value)} placeholder="claude-agent-acp" />
      </label>
      <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Arguments <span class="text-faint">· one per line</span></span>
        <textarea class=${areaCls} rows="2" value=${f.args} onInput=${(e) => set('args')(e.target.value)} placeholder="--acp"></textarea>
      </label>
      <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Environment <span class="text-faint">· NAME=value per line, stored encrypted; env:VAR reads the server's environment</span></span>
        <textarea class=${areaCls} rows="3" value=${f.env} onInput=${(e) => set('env')(e.target.value)} placeholder="ANTHROPIC_API_KEY=…"></textarea>
      </label>
      <div class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Approvals</span>
        <${Select} label="Approval policy" value=${f.policy} options=${POLICIES} onChange=${set('policy')} />
      </div>
      <div class="flex items-center justify-between gap-3 text-[13px]">
        <div><div>Start with the server</div>
          <div class="text-xs text-faint">Keeps the agent running. Otherwise it stops after a few idle minutes and the next message starts it again.</div></div>
        <${Toggle} checked=${f.autoStart} onChange=${set('autoStart')} label="Start with the server" />
      </div>
      <div class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Session defaults</span>
        ${isNew ? html`<p class="text-xs text-faint">Save the agent first; its options come from the agent itself.</p>`
          : open && html`<${DefaultOptions} id=${agent.id} value=${f.defaults} onChange=${set('defaults')} />`}
      </div>
    </form>
    ${!isNew && open && html`<${AgentRuntime} id=${agent.id} />`}
  </${Dialog}>`;
}

const fmtUptime = (ms) => {
  const s = Math.floor(ms / 1000);
  if (s < 60) return s + 's';
  if (s < 3600) return Math.floor(s / 60) + 'm';
  return Math.floor(s / 3600) + 'h ' + Math.floor((s % 3600) / 60) + 'm';
};
const STATE = { running: ['bg-ok', 'Running'], stopped: ['bg-faint', 'Not running'], error: ['bg-bad', 'Failed'] };

// SignInTerminal runs an agent's interactive sign-in in a terminal here.
// The agent restarts when it ends, so its next turn uses the new login.
function SignInTerminal({ id, method, onClose }) {
  const box = useRef();
  const [gen, setGen] = useState(0);
  const { st } = useXterm(box, `/api/agents/${encodeURIComponent(id)}/sign-in`, { methodId: method.id }, gen,
    'Could not start the sign-in. The agent may have stopped offering it — check its logs.');
  const done = st.status === 'exited';
  return html`<${Dialog} open title=${'Sign in — ' + method.name} onClose=${onClose} class="w-[min(820px,calc(100vw-24px))]"
    footer=${html`
      <span class="flex-1 text-xs text-faint">${done ? '' : 'Type into the terminal. Links open in a new tab.'}</span>
      ${(done || st.status === 'error') && html`<${Button} kind="ghost" onClick=${() => setGen((n) => n + 1)}>Run again</${Button}>`}
      <${Button} kind=${done && st.ok ? 'primary' : 'ghost'} onClick=${onClose}>${done ? 'Done' : 'Close'}</${Button}>`}>
    ${st.status !== 'open' && st.status !== 'connecting' && html`<div role="status" class=${'mb-3 rounded-lg border px-3 py-2 text-[13px] ' + (done && st.ok ? 'border-ok/40 bg-ok/10' : 'border-warn/40 bg-warn/10')}>
      ${done && st.ok ? 'Sign-in finished. The agent restarts with the new login on its next turn.' : st.message}
    </div>`}
    <div class="relative h-[min(420px,55dvh)] rounded-lg border border-line bg-bg overflow-hidden">
      ${st.status === 'connecting' && html`<div class="absolute inset-0 grid place-items-center text-muted text-[13px] pointer-events-none"><span class="flex items-center gap-2"><${Spinner} size=${12} /> Starting the sign-in…</span></div>`}
      <div ref=${box} class="absolute inset-0 pl-2 pt-1"></div>
    </div>
  </${Dialog}>`;
}

// AgentRuntime shows the agent's process: state, sign-in, and its recent
// stderr. Reading it never starts the agent.
function AgentRuntime({ id }) {
  const [st, setSt] = useState({ status: 'loading', data: null, error: '' });
  const [busy, setBusy] = useState('');
  const [signIn, setSignIn] = useState(null);
  const [showCmd, setShowCmd] = useState('');
  const [logsOpen, setLogsOpen] = useState(false);
  const logRef = useRef();
  const load = async () => {
    try { setSt({ status: 'ready', data: await api.agentStatus(id), error: '' }); }
    catch (e) { setSt((x) => ({ ...x, status: x.data ? 'ready' : 'error', error: errorText(e) })); }
  };
  useEffect(() => {
    load();
    const t = setInterval(load, 3000);
    return () => clearInterval(t);
  }, [id]);
  useEffect(() => { if (logsOpen && logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight; }, [logsOpen, st.data?.logs?.length]);

  const run = async (key, fn, done) => {
    setBusy(key);
    try { await fn(); if (done) toast(done); }
    catch (e) { toast(errorText(e), 'error'); }
    setBusy('');
    load();
  };
  const copy = (text) => navigator.clipboard?.writeText(text).then(() => toast('Command copied.'), () => {});

  if (st.status === 'loading') return html`<div class="mt-5 skeleton h-16 rounded-xl"></div>`;
  if (st.status === 'error') return html`<${ErrorBlock} class="mt-5" title="Could not read the agent's status" message=${st.error} onRetry=${load} />`;
  const d = st.data;
  const [dot, label] = STATE[d.state] || STATE.stopped;
  const quote = (a) => (/^[\w@%+=:,./-]+$/.test(a) ? a : `'${a.replace(/'/g, `'\\''`)}'`);
  return html`<section class="mt-5 pt-4 border-t border-line flex flex-col gap-3 text-[13px]">
    <div class="flex items-center gap-2 flex-wrap">
      <span class="font-medium">Process</span>
      <span class="inline-flex items-center gap-1.5 text-muted"><span class=${'size-2 rounded-full ' + dot}></span>${label}</span>
      ${d.state === 'running' && html`<span class="text-faint text-xs">pid ${d.pid} · up ${fmtUptime(d.uptimeMs)}</span>`}
      <span class="flex-1"></span>
      ${d.canLogout && d.state === 'running' && html`<${Button} kind="ghost" busy=${busy === 'logout'} disabled=${!!busy}
        onClick=${() => run('logout', () => api.logoutAgent(id), 'Signed out.')}>Sign out</${Button}>`}
      ${d.state !== 'stopped' && html`<${Button} kind="ghost" busy=${busy === 'restart'} disabled=${!!busy}
        onClick=${() => run('restart', () => api.restartAgent(id), 'Stopped — the next turn starts it again.')}>Restart</${Button}>`}
    </div>
    ${d.lastError && html`<div class=${'rounded-lg border px-3 py-2 ' + (d.authNeeded ? 'border-warn/40 bg-warn/10' : 'border-bad/30 bg-bad/10')}>
      ${d.authNeeded ? 'The agent needs you to sign in before it can work.' : d.lastError}
    </div>`}
    ${d.authMethods.length > 0 && html`<div class="flex flex-col gap-2">
      <span class="text-muted">Sign in</span>
      ${d.authMethods.map((m) => m.type === 'terminal'
        ? html`<div key=${m.id} class="flex flex-col gap-1.5">
            <div class="flex items-center gap-3">
              <div class="flex-1 min-w-0">${m.name}
                <div class="text-xs text-faint">${m.description || 'Interactive: it runs in a terminal here.'}
                  ${' '}<button type="button" class="underline hover:text-fg" aria-expanded=${showCmd === m.id}
                    onClick=${() => setShowCmd(showCmd === m.id ? '' : m.id)}>${showCmd === m.id ? 'Hide the command' : 'Or run it yourself'}</button></div>
              </div>
              <${Button} disabled=${!!busy} onClick=${() => setSignIn(m)}><${Icon} name="terminal" size=${13} /> Sign in</${Button}>
            </div>
            ${showCmd === m.id && html`<div class="flex items-center gap-2">
              <code class="flex-1 min-w-0 truncate rounded bg-raised px-2 py-1 font-mono text-xs">${m.command.map(quote).join(' ')}</code>
              <${Button} kind="ghost" onClick=${() => copy(m.command.map(quote).join(' '))}><${Icon} name="copy" size=${13} /> Copy</${Button}>
            </div>`}
          </div>`
        : html`<div key=${m.id} class="flex items-center gap-3">
            <div class="flex-1 min-w-0">${m.name}${m.description && html`<div class="text-xs text-faint">${m.description}</div>`}</div>
            <${Button} busy=${busy === 'auth:' + m.id} disabled=${!!busy}
              onClick=${() => run('auth:' + m.id, () => api.authenticateAgent(id, m.id), 'Signed in.')}>Sign in</${Button}>
          </div>`)}
    </div>`}
    ${signIn && html`<${SignInTerminal} id=${id} method=${signIn} onClose=${() => { setSignIn(null); load(); }} />`}
    <div>
      <button type="button" class="inline-flex items-center gap-1 text-muted hover:text-fg" aria-expanded=${logsOpen} onClick=${() => setLogsOpen(!logsOpen)}>
        <${Icon} name=${logsOpen ? 'chevronDown' : 'chevronRight'} size=${13} /> Logs <span class="text-faint">(${d.logs.length})</span>
      </button>
      ${logsOpen && (d.logs.length
        ? html`<pre ref=${logRef} class="rawtext mt-2 max-h-64 overflow-auto text-xs">${d.logs.join('\n')}</pre>`
        : html`<p class="mt-2 text-faint text-xs">Nothing written to stderr yet.</p>`)}
    </div>
  </section>`;
}
