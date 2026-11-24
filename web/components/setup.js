// First-run setup: with no provider and no agent there is nothing to talk
// to, so the home view walks through connecting one — a model over an API
// or an agent CLI over ACP — in place, without a trip to Settings.
import { html, useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore, setPref, toast } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { loadProviders, loadAgents, loadAgentModels } from '../lib/chat.js';
import { Icon, Logo } from './icons.js';
import { cx, Button, ErrorBlock, Select, Spinner } from './ui.js';
import { API_STYLES } from '../lib/providers.js';

const inputCls = 'h-9 rounded-lg bg-raised border border-line px-3 text-[13px] outline-none focus:border-accent w-full';

// Presets fill what is the same for everyone on a host; the key is the
// owner's. Ollama runs locally and asks for none.
const PRESETS = [
  { id: 'openrouter', name: 'OpenRouter', style: 'completion', endpoint: 'https://openrouter.ai/api/v1', note: 'Hundreds of models behind one key.' },
  { id: 'openai', name: 'OpenAI', style: 'responses', endpoint: 'https://api.openai.com/v1', note: 'GPT models on the Responses API.' },
  { id: 'anthropic', name: 'Anthropic', style: 'messages', endpoint: 'https://api.anthropic.com/v1', note: 'Claude models on the Messages API.' },
  { id: 'gemini', name: 'Gemini', style: 'generatecontent', endpoint: 'https://generativelanguage.googleapis.com/v1beta', note: 'Google Gemini models.' },
  { id: 'ollama', name: 'Ollama', style: 'completion', endpoint: 'http://127.0.0.1:11434/v1', note: 'Models running on this machine.', keyless: true },
  { id: 'custom', name: 'Other', style: 'completion', endpoint: '', note: 'Any OpenAI-compatible, Responses, Messages, or Gemini endpoint.', custom: true },
];

const slug = (s) => s.toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/^[^a-z0-9]+|-+$/g, '');

// needsSetup is true once both lists have loaded empty.
export const needsSetup = (s) => s.providers.status === 'ready' && !s.providers.items.length
  && s.agents.status === 'ready' && !s.agents.items.length;

// SetupWizard runs the steps; onHold keeps it on screen once a step starts
// (saving the first provider would otherwise swap it for the composer
// before the owner picks a model), and onDone releases it.
export function SetupWizard({ onHold, onDone }) {
  const [step, setStep] = useState({ id: 'choose' });
  const go = (next) => { onHold(); setStep(next); };
  return html`<div class="w-full flex flex-col gap-6">
    <div class="flex items-center gap-2.5 px-1">
      <${Logo} size=${28} />
      <div>
        <h1 class="text-xl font-semibold tracking-tight">${step.id === 'done' ? 'Ready to go' : 'Connect something to talk to'}</h1>
        <p class="text-muted text-[13px] mt-0.5">${step.id === 'done' ? 'Add more any time in Settings.' : 'Set up one now. Providers and agents sit side by side in the model picker.'}</p>
      </div>
    </div>
    ${step.id === 'choose' && html`<${Choose} onPick=${(id) => go({ id })} />`}
    ${step.id === 'api' && html`<${ApiStep} onBack=${() => setStep({ id: 'choose' })} onSaved=${(p) => setStep({ id: 'done', provider: p })} />`}
    ${step.id === 'acp' && html`<${AcpStep} onBack=${() => setStep({ id: 'choose' })} onSaved=${(a) => setStep({ id: 'done', agent: a })} />`}
    ${step.id === 'done' && html`<${Done} provider=${step.provider} agent=${step.agent} onDone=${onDone} />`}
  </div>`;
}

function Choose({ onPick }) {
  const [found, setFound] = useState(null);
  useEffect(() => {
    api.detectAgents().then((r) => setFound((r.data || []).length), () => setFound(-1));
  }, []);
  const card = (id, icon, title, tag, desc, extra) => html`<button type="button" onClick=${() => onPick(id)}
    class="group flex flex-col gap-3 rounded-xl border border-line bg-panel p-4 text-left transition-colors hover:border-accent/60 hover:bg-hover">
    <span class="flex items-center gap-2.5">
      <span class="size-8 shrink-0 rounded-lg bg-raised flex items-center justify-center text-muted group-hover:text-fg"><${Icon} name=${icon} size=${16} /></span>
      <span class="font-medium">${title}</span>
      <span class="rounded px-1 border border-line text-[10px] leading-4 text-faint">${tag}</span>
      <${Icon} name="chevronRight" size=${15} class="ml-auto text-faint group-hover:text-fg" />
    </span>
    <span class="text-muted text-[13px]">${desc}</span>
    ${extra}
  </button>`;
  return html`<div class="grid sm:grid-cols-2 gap-3">
    ${card('api', 'server', 'A model', 'API', 'OpenRouter, OpenAI, Anthropic, Gemini, a local Ollama, or any compatible endpoint. Needs an API key.',
      html`<span class="text-xs text-faint mt-auto">Tools run inside NusaShell.</span>`)}
    ${card('acp', 'bot', 'An agent CLI', 'ACP', 'Claude Agent, Codex, Gemini CLI and others installed here. They use their own login.',
      html`<span class=${cx('text-xs mt-auto', found > 0 ? 'text-ok' : 'text-faint')}>
        ${found === null ? html`<span class="inline-flex items-center gap-1.5"><${Spinner} size=${11} /> Looking on this machine…</span>`
          : found > 0 ? `${found} found on this machine` : found === 0 ? 'None found on this machine yet' : 'Could not look on this machine'}
      </span>`)}
  </div>`;
}

function Back({ onBack, disabled }) {
  return html`<button type="button" onClick=${onBack} disabled=${disabled}
    class="self-start -ml-1 flex items-center gap-1 px-1 text-[13px] text-muted hover:text-fg disabled:opacity-50">
    <${Icon} name="chevronLeft" size=${14} /> Back
  </button>`;
}

function ApiStep({ onBack, onSaved }) {
  const [preset, setPreset] = useState(PRESETS[0]);
  const [f, setF] = useState({ id: '', endpoint: PRESETS[0].endpoint, style: PRESETS[0].style, key: '' });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const set = (k) => (v) => setF((x) => ({ ...x, [k]: v }));
  const pick = (p) => { setPreset(p); setErr(''); setF((x) => ({ ...x, endpoint: p.endpoint, style: p.style })); };

  const id = preset.custom ? slug(f.id) : preset.id;
  const valid = !!id && /^https?:\/\/\S+$/.test(f.endpoint.trim()) && (preset.keyless || preset.custom || f.key.trim() !== '');

  const save = async () => {
    setBusy(true); setErr('');
    const doc = { name: preset.custom ? f.id.trim() : preset.name, style: f.style, endpoint: f.endpoint.trim(), stream: true, models: [] };
    if (f.key.trim()) doc.apiKey = f.key.trim();
    try {
      const res = await api.saveProvider(id, doc);
      await loadProviders();
      const models = (res.models || []).filter((m) => m.listed !== false);
      if (!models.length) {
        // Saved, but nothing to pick: almost always a wrong key or endpoint.
        setErr(res.catalogError ? `Saved, but its model list could not be fetched: ${res.catalogError}` : 'Saved, but it lists no models.');
        setBusy(false);
        return;
      }
      onSaved({ id: res.id, name: res.name || res.id, models });
    } catch (e) { setErr(errorText(e)); setBusy(false); }
  };

  return html`<form noValidate class="flex flex-col gap-4" onSubmit=${(e) => { e.preventDefault(); if (valid && !busy) save(); }}>
    <${Back} onBack=${onBack} disabled=${busy} />
    <div class="grid grid-cols-2 sm:grid-cols-3 gap-2" role="radiogroup" aria-label="Provider">
      ${PRESETS.map((p) => html`<button key=${p.id} type="button" role="radio" aria-checked=${p === preset} onClick=${() => pick(p)} disabled=${busy}
        class=${cx('rounded-xl border px-3 py-2.5 text-left transition-colors', p === preset ? 'border-accent bg-hover' : 'border-line bg-panel hover:bg-hover')}>
        <span class="block font-medium text-[13px]">${p.name}</span>
        <span class="block text-xs text-muted mt-0.5 line-clamp-2">${p.note}</span>
      </button>`)}
    </div>
    <div class="flex flex-col gap-3 rounded-xl border border-line bg-panel p-4">
      ${preset.custom && html`<div class="grid sm:grid-cols-2 gap-3">
        <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Name</span>
          <input class=${inputCls} value=${f.id} onInput=${(e) => set('id')(e.target.value)} placeholder="My server" />
        </label>
        <div class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">API style</span>
          <${Select} label="API style" value=${f.style} options=${API_STYLES} onChange=${set('style')} />
        </div>
      </div>`}
      <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Endpoint</span>
        <input class=${inputCls + ' font-mono'} value=${f.endpoint} onInput=${(e) => set('endpoint')(e.target.value)} placeholder="https://api.example.com/v1" inputmode="url" />
      </label>
      <label class="flex flex-col gap-1.5 text-[13px]">
        <span class="text-muted">API key ${preset.keyless || preset.custom ? html`<span class="text-faint">· optional</span>` : ''}</span>
        <input class=${inputCls} type="password" autocomplete="off" value=${f.key} onInput=${(e) => set('key')(e.target.value)}
          placeholder="Paste a key, or env:VAR_NAME" autofocus=${!preset.custom} />
        <span class="text-xs text-faint">Stored encrypted on the server; it never comes back to the browser.</span>
      </label>
    </div>
    ${err && html`<${ErrorBlock} title="Could not connect" message=${err} />`}
    <div class="flex items-center justify-end gap-3">
      ${busy && html`<span class="text-xs text-muted">Fetching the model list…</span>`}
      <${Button} type="submit" kind="primary" busy=${busy} disabled=${!valid || busy}>Connect ${preset.custom ? '' : preset.name}</${Button}>
    </div>
  </form>`;
}

function AcpStep({ onBack, onSaved }) {
  const [st, setSt] = useState({ status: 'loading', list: [], error: '' });
  const [busy, setBusy] = useState('');
  const [err, setErr] = useState('');
  const [manual, setManual] = useState(false);
  const [m, setM] = useState({ name: '', command: '', args: '' });
  const load = async () => {
    setSt((x) => ({ ...x, status: 'loading' }));
    try { const r = await api.detectAgents(); setSt({ status: 'ready', list: r.data || [], error: '' }); }
    catch (e) { setSt({ status: 'error', list: [], error: errorText(e) }); }
  };
  useEffect(() => { load(); }, []);
  useEffect(() => { if (st.status === 'ready' && !st.list.length) setManual(true); }, [st.status]);

  const add = async (key, id, doc) => {
    setBusy(key); setErr('');
    try {
      const saved = await api.saveAgent(id, { env: {}, policy: 'ask', ...doc });
      await loadAgents();
      onSaved({ id: saved.id, name: saved.name || saved.id });
    } catch (e) { setErr(errorText(e)); setBusy(''); }
  };
  const manualId = slug(m.name || m.command.split('/').pop() || '');
  const manualOk = !!manualId && m.command.trim() !== '';

  return html`<div class="flex flex-col gap-4">
    <${Back} onBack=${onBack} disabled=${!!busy} />
    ${st.status === 'loading' && html`<div class="flex flex-col gap-2">${[1, 2].map((i) => html`<div key=${i} class="skeleton h-[60px] rounded-xl"></div>`)}</div>`}
    ${st.status === 'error' && html`<${ErrorBlock} title="Could not look for agent CLIs" message=${st.error} onRetry=${load} />`}
    ${st.status === 'ready' && st.list.length > 0 && html`<div class="rounded-xl border border-line bg-panel divide-y divide-line">
      ${st.list.map((c) => html`<div key=${c.id} class="flex items-center gap-3 px-4 py-3">
        <span class="size-8 shrink-0 rounded-lg bg-raised flex items-center justify-center text-muted"><${Icon} name="bot" size=${16} /></span>
        <div class="flex-1 min-w-0">
          <div class="font-medium text-[13px]">${c.name}</div>
          <div class="text-xs text-faint truncate font-mono">${[c.command, ...c.args].join(' ')}</div>
        </div>
        <${Button} busy=${busy === c.id} disabled=${!!busy}
          onClick=${() => add(c.id, c.id, { name: c.name, command: c.command, args: c.args })}>Use</${Button}>
      </div>`)}
    </div>`}
    ${st.status === 'ready' && !st.list.length && html`<div class="rounded-xl border border-dashed border-line p-5 text-center">
      <p class="font-medium">No agent CLI found on this machine</p>
      <p class="text-muted text-[13px] mt-1">Install one that speaks ACP (Claude Agent, Codex, Gemini CLI, …) and log in with it, then look again — or enter its command below.</p>
      <${Button} class="mt-3" onClick=${load}><${Icon} name="refresh" size=${14} /> Look again</${Button}>
    </div>`}
    ${st.status === 'ready' && (manual
      ? html`<form noValidate class="flex flex-col gap-3 rounded-xl border border-line bg-panel p-4" onSubmit=${(e) => { e.preventDefault(); if (manualOk && !busy) add('manual', manualId, { name: m.name.trim(), command: m.command.trim(), args: m.args.split(/\s+/).filter(Boolean) }); }}>
          <div class="grid sm:grid-cols-2 gap-3">
            <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Command</span>
              <input class=${inputCls + ' font-mono'} value=${m.command} onInput=${(e) => setM({ ...m, command: e.target.value })} placeholder="claude-agent-acp" />
            </label>
            <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Arguments <span class="text-faint">· optional</span></span>
              <input class=${inputCls + ' font-mono'} value=${m.args} onInput=${(e) => setM({ ...m, args: e.target.value })} placeholder="--acp" />
            </label>
          </div>
          <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Name <span class="text-faint">· optional</span></span>
            <input class=${inputCls} value=${m.name} onInput=${(e) => setM({ ...m, name: e.target.value })} placeholder="My agent" />
          </label>
          <div class="flex justify-end"><${Button} type="submit" kind="primary" busy=${busy === 'manual'} disabled=${!manualOk || !!busy}>Add agent</${Button}></div>
        </form>`
      : html`<button type="button" onClick=${() => setManual(true)} class="self-start text-[13px] text-muted hover:text-fg underline">Enter a command by hand</button>`)}
    ${err && html`<${ErrorBlock} title="Could not add the agent" message=${err} />`}
    <p class="text-xs text-faint">Agents run their own tools and ask before acting; you answer in the thread.</p>
  </div>`;
}

// Done confirms what was connected and seeds the composer's choice: a
// provider's model picked here, or the agent (which may take a moment to
// start and list its models).
function Done({ provider, agent, onDone }) {
  const s = useStore();
  const [model, setModel] = useState(provider?.models[0]?.id || '');
  useEffect(() => { if (agent) loadAgentModels(agent.id, true); }, []);
  const am = agent && s.agentModels[agent.id];
  const start = () => {
    setPref('lastModel', provider ? { providerId: provider.id, modelId: model } : { agentId: agent.id });
    toast(`Connected ${(provider || agent).name}.`);
    onDone();
  };
  return html`<div class="flex flex-col gap-4">
    <div class="rounded-xl border border-line bg-panel p-4 flex flex-col gap-3">
      <div class="flex items-center gap-2.5">
        <span class="size-8 shrink-0 rounded-lg bg-raised flex items-center justify-center text-ok"><${Icon} name="check" size=${16} /></span>
        <div class="min-w-0">
          <div class="font-medium">${(provider || agent).name} is connected</div>
          <div class="text-xs text-muted">
            ${provider ? `${provider.models.length} model${provider.models.length === 1 ? '' : 's'} available`
              : !am || am.status === 'loading' ? html`<span class="inline-flex items-center gap-1.5"><${Spinner} size=${11} /> Starting the agent…</span>`
              : am.status === 'error' ? html`<span class="text-bad">It did not start: ${am.error}</span>`
              : `${am.list.length || 'Its default'} model${am.list.length === 1 ? '' : 's'} ready`}
          </div>
        </div>
      </div>
      ${provider && provider.models.length > 1 && html`<div class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Start with</span>
        <${Select} label="Model" search value=${model} options=${provider.models.map((x) => ({ value: x.id, text: x.name || x.id }))} onChange=${setModel} />
      </div>`}
      ${am?.status === 'error' && html`<p class="text-xs text-muted">Log in with the agent's own CLI on this machine, then retry from the thread. Its command and environment are in Settings → Agents.</p>`}
    </div>
    <div class="flex justify-end"><${Button} kind="primary" onClick=${start}>Start chatting</${Button}></div>
  </div>`;
}
