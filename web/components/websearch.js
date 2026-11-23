// Web search: how the web_search and web_fetch tools reach the web. The
// owner turns them off, picks who serves searches, keys the sources that
// take a key, sets the limits, and runs a real search or fetch to see what
// a thread would get. Keys go in and never come back: only where each one
// lives (the vault or an environment variable) is shown.
import { html, useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { toast } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { Icon } from './icons.js';
import { cx, Button, Checkbox, ErrorBlock, Select, Spinner, Toggle } from './ui.js';

const inputCls = 'h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent w-full disabled:opacity-60';
const card = 'rounded-xl border border-line bg-panel';

const BACKENDS = [
  { value: 'auto', text: 'Automatic', hint: 'A thread on a provider with its own web search uses it; every other thread uses the built-in metasearch.' },
  { value: 'searchwire', text: 'Built-in metasearch', hint: 'Every thread searches through the sources below, whatever provider it runs on.' },
  { value: 'provider', text: 'One provider', hint: 'Every thread searches through the provider you pick, if it has web search; otherwise the metasearch.' },
];

const KEY_STATE = {
  vaulted: { text: 'Key in vault', cls: 'text-ok bg-ok/10' },
  env: { text: 'Key from environment', cls: 'text-ok bg-ok/10' },
  missing: { text: 'Key missing', cls: 'text-warn bg-warn/10' },
};

const kib = (n) => (n >= 1 << 20 ? `${+(n / (1 << 20)).toFixed(1)} MiB` : n >= 1024 ? `${Math.round(n / 1024)} KiB` : `${n} bytes`);

function Section({ title, desc, children, aside }) {
  return html`<section class="mb-7">
    <div class="flex items-end justify-between gap-3 mb-2.5">
      <div class="min-w-0">
        <h3 class="text-[14px] font-semibold">${title}</h3>
        ${desc && html`<p class="text-xs text-muted mt-0.5">${desc}</p>`}
      </div>
      ${aside}
    </div>
    ${children}
  </section>`;
}

export function WebSearch() {
  const [st, setSt] = useState({ status: 'loading' });
  const [busy, setBusy] = useState('');

  const load = () => {
    setSt((x) => ({ ...x, status: x.data ? 'ready' : 'loading' }));
    api.webSearch().then((d) => setSt({ status: 'ready', data: d }), (e) => setSt({ status: 'error', error: errorText(e) }));
  };
  useEffect(load, []);

  // change writes settings and shows what the server now holds: the
  // answer is the full status, so nothing on screen is guessed.
  const change = async (what, values, note) => {
    setBusy(what);
    try {
      setSt({ status: 'ready', data: await api.updateWebSearch(values) });
      if (note) toast(note);
      return true;
    } catch (e) { toast(errorText(e), 'error'); return false; } finally { setBusy(''); }
  };

  if (st.status === 'loading') return html`<div>
    <${Header} />
    <div class="flex flex-col gap-3">${[64, 140, 260].map((h) => html`<div key=${h} class="skeleton rounded-xl" style=${`height:${h}px`}></div>`)}</div>
  </div>`;
  if (st.status === 'error') return html`<div><${Header} /><${ErrorBlock} title="Could not load web search settings" message=${st.error} onRetry=${load} /></div>`;
  const d = st.data;

  return html`<div>
    <${Header} />
    <div class=${cx(card, 'px-4 py-3.5 flex items-center gap-4 mb-7')}>
      <div class="size-9 rounded-lg bg-raised flex items-center justify-center shrink-0 text-muted"><${Icon} name="globe" /></div>
      <div class="flex-1 min-w-0">
        <div class="text-[13px] font-medium">Let threads use the web</div>
        <div class="text-xs text-muted mt-0.5">${d.enabled
          ? html`Models are offered <code class="text-[12px]">web_search</code> and <code class="text-[12px]">web_fetch</code>.`
          : 'Off: models are not offered the web tools. Agents keep their own.'}</div>
      </div>
      ${busy === 'enabled' ? html`<${Spinner} size=${14} />` : html`<${Toggle} label="Let threads use the web" checked=${d.enabled}
        onChange=${(on) => change('enabled', { enabled: on ? 'on' : 'off' }, on ? 'Web tools are on from the next step.' : 'Web tools are off from the next step.')} />`}
    </div>

    <div class=${cx('transition-opacity', !d.enabled && 'opacity-60')}>
      <${Backend} d=${d} busy=${busy} change=${change} />
      <${Sources} d=${d} busy=${busy} change=${change} setSt=${setSt} />
      <${Limits} d=${d} busy=${busy} change=${change} />
      <${TryIt} enabled=${d.enabled} />
    </div>
  </div>`;
}

function Header() {
  return html`<div class="mb-4">
    <h2 class="text-[17px] font-semibold">Web search</h2>
    <p class="text-muted text-[13px] mt-0.5">How models search and read the web. Changes apply to the next tool call — no restart.</p>
  </div>`;
}

function Backend({ d, busy, change }) {
  const [model, setModel] = useState(d.model || '');
  const [models, setModels] = useState([]);
  useEffect(() => setModel(d.model || ''), [d.model]);
  useEffect(() => {
    setModels([]);
    if (d.backend !== 'provider' || !d.provider) return;
    api.provider(d.provider).then((p) => setModels((p.models || []).map((m) => m.id)), () => {});
  }, [d.backend, d.provider]);
  const current = BACKENDS.find((b) => b.value === d.backend) || BACKENDS[0];
  const chosen = d.providers.find((p) => p.id === d.provider);
  const native = d.providers.filter((p) => p.native);

  const pickBackend = (v) => {
    if (v === d.backend) return;
    if (v === 'provider') {
      const first = d.provider || native[0]?.id || d.providers[0]?.id;
      if (!first) { toast('Add a provider first — Settings → Providers.', 'error'); return; }
      change('backend', { backend: 'provider', provider: first });
      return;
    }
    change('backend', { backend: v });
  };

  return html`<${Section} title="Where searches run" desc=${current.hint}>
    <div class="flex flex-wrap gap-1 p-0.5 rounded-lg bg-raised border border-line w-fit text-[13px] mb-3" role="radiogroup" aria-label="Where searches run">
      ${BACKENDS.map((b) => html`<button key=${b.value} type="button" role="radio" aria-checked=${d.backend === b.value} disabled=${!!busy}
        class=${cx('px-3 h-7 rounded-md', d.backend === b.value ? 'bg-panel text-fg shadow-sm' : 'text-muted hover:text-fg')}
        onClick=${() => pickBackend(b.value)}>${b.text}</button>`)}
      ${busy === 'backend' && html`<span class="flex items-center px-2"><${Spinner} size=${12} /></span>`}
    </div>
    ${d.backend === 'provider' && html`<div class=${cx(card, 'p-4 grid gap-3 sm:grid-cols-2')}>
      <label class="block">
        <span class="block text-xs text-muted mb-1">Provider</span>
        <${Select} label="Provider" value=${d.provider} search=${d.providers.length > 8}
          onChange=${(v) => change('backend', { provider: v, model: '' })}
          options=${d.providers.map((p) => ({ value: p.id, text: p.id + (p.native ? ' · web search' : '') }))} />
      </label>
      <label class="block">
        <span class="block text-xs text-muted mb-1">Model${chosen?.needsModel ? '' : ' (optional)'}</span>
        <input class=${inputCls} list="websearch-models" value=${model} placeholder=${chosen?.needsModel ? 'Searches run through this model' : 'Not needed for this provider'}
          onInput=${(e) => setModel(e.target.value)}
          onBlur=${() => model.trim() !== (d.model || '') && change('backend', { model: model.trim() })}
          onKeyDown=${(e) => e.key === 'Enter' && e.currentTarget.blur()} />
        <datalist id="websearch-models">${models.map((m) => html`<option key=${m} value=${m} />`)}</datalist>
      </label>
      ${d.providerProblem && html`<p class="sm:col-span-2 text-xs text-warn flex gap-1.5"><${Icon} name="alert" size=${14} class="shrink-0 mt-px" />${d.providerProblem}</p>`}
    </div>`}
    ${d.backend !== 'provider' && html`<p class="text-xs text-faint">
      ${native.length
        ? html`With web search of their own: ${native.map((p, i) => html`${i ? ', ' : ''}<span class="text-muted">${p.id}</span>`)}.`
        : 'None of your providers has web search of its own (OmniRoute, 9Router, OpenCode Zen and Go do).'}
    </p>`}
  </${Section}>`;
}

function Sources({ d, busy, change, setSt }) {
  const [keying, setKeying] = useState('');
  const [value, setValue] = useState('');
  const [saving, setSaving] = useState('');

  const saveKey = async (id) => {
    if (!value.trim()) return;
    setSaving(id);
    try {
      setSt({ status: 'ready', data: await api.setWebSearchKey(id, value.trim()) });
      setKeying(''); setValue('');
      toast('Key saved.');
    } catch (e) { toast(errorText(e), 'error'); } finally { setSaving(''); }
  };
  const clearKey = async (id) => {
    setSaving(id);
    try {
      setSt({ status: 'ready', data: await api.clearWebSearchKey(id) });
      toast('Key removed.');
    } catch (e) { toast(errorText(e), 'error'); } finally { setSaving(''); }
  };
  const active = d.sources.filter((x) => x.active).length;

  return html`<${Section} title="Sources" desc="The built-in metasearch asks every active source at once and merges what they find."
    aside=${html`<span class="text-xs text-faint shrink-0">${active} of ${d.sources.length} active</span>`}>
    <ul class=${cx(card, 'divide-y divide-line')}>
      ${d.sources.map((x) => {
        const ks = KEY_STATE[x.keyState];
        const hasKey = x.keyState === 'vaulted' || x.keyState === 'env' || x.keyState === 'missing';
        const fromEnvDefault = !x.saved;
        return html`<li key=${x.id} class="px-4 py-3">
          <div class="flex items-start gap-3">
            <span class=${cx('mt-1.5 size-2 rounded-full shrink-0', x.active ? 'bg-ok' : 'bg-line')} title=${x.active ? 'Active' : 'Not active'}></span>
            <div class="flex-1 min-w-0">
              <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span class="text-[13px] font-medium">${x.label}</span>
                ${x.fixed && html`<span class="text-[11px] text-faint">Always on · no key</span>`}
                ${x.needsKey && !hasKey && html`<span class="text-[11px] px-1.5 rounded bg-raised text-muted">Needs a key</span>`}
                ${ks && html`<span class=${cx('text-[11px] px-1.5 rounded', ks.cls)}>${ks.text}</span>`}
              </div>
              ${x.note && html`<p class="text-xs text-muted mt-0.5">${x.note}</p>`}
              ${x.id === 'github' && html`<${Checkbox} class="text-xs text-muted mt-1.5" checked=${d.searchIssues}
                onChange=${(v) => change('issues', { 'github.search_issues': v ? 'on' : 'off' })}>Also search issues and pull requests</${Checkbox}>`}
              ${x.keyed && keying !== x.id && html`<div class="flex items-center gap-3 mt-1.5 text-xs">
                <button type="button" class="text-accent hover:underline disabled:opacity-50" disabled=${!!saving}
                  onClick=${() => { setKeying(x.id); setValue(''); }}>${hasKey && !fromEnvDefault ? 'Replace key' : x.id === 'github' ? 'Add token' : 'Add key'}</button>
                ${hasKey && !fromEnvDefault && (saving === x.id ? html`<${Spinner} size=${11} />`
                  : html`<button type="button" class="text-muted hover:text-bad disabled:opacity-50" disabled=${!!saving} onClick=${() => clearKey(x.id)}>Remove</button>`)}
              </div>`}
            </div>
            ${!x.fixed && html`<div class="shrink-0 pt-0.5">${busy === 'src:' + x.id ? html`<${Spinner} size=${14} />`
              : html`<${Toggle} label=${'Use ' + x.label} checked=${x.enabled}
                onChange=${(on) => change('src:' + x.id, { [x.id + '.enabled']: on ? 'on' : 'off' })} />`}</div>`}
          </div>
          ${keying === x.id && html`<form class="mt-3 ml-5" onSubmit=${(e) => { e.preventDefault(); saveKey(x.id); }}>
            <div class="flex flex-col sm:flex-row gap-2">
              <input class=${inputCls} type="password" autocomplete="off" spellcheck="false" autofocus
                aria-label=${x.label + ' key'} placeholder=${'Paste the key, or env:' + x.keyEnv}
                value=${value} onInput=${(e) => setValue(e.target.value)} />
              <div class="flex gap-2 shrink-0">
                <${Button} kind="primary" type="submit" busy=${saving === x.id} disabled=${!value.trim()}>Save key</${Button}>
                <${Button} kind="ghost" onClick=${() => { setKeying(''); setValue(''); }}>Cancel</${Button}>
              </div>
            </div>
            <p class="text-[11px] text-faint mt-1.5">A pasted key is kept in the vault and never shown again; <code>env:NAME</code> reads a variable of the server.
              ${!hasKey && ` Without either, ${x.keyEnv} is read if it is set.`}</p>
          </form>`}
        </li>`;
      })}
    </ul>
  </${Section}>`;
}

// LIMITS are the number fields: empty means the library's default, shown
// as the placeholder.
const LIMITS = [
  { key: 'limit', label: 'Results per search', unit: '', def: (df) => df.limit, show: (v) => v, parse: (s) => +s },
  { key: 'timeout', label: 'Timeout per source', unit: '', def: (df) => df.timeout, show: (v) => v, parse: (s) => s, text: true, hint: 'e.g. 15s or 1m' },
  { key: 'max_response_bytes', label: 'Page text kept by a fetch', unit: 'bytes', def: (df) => df.maxResponseBytes, show: (v) => v, parse: (s) => +s, field: 'maxResponseBytes' },
];
const ADVANCED = [
  { key: 'max_raw_bytes', label: 'Largest download', unit: 'bytes', def: (df) => df.maxRawBytes, parse: (s) => +s, field: 'maxRawBytes' },
  { key: 'user_agent', label: 'User agent', def: (df) => df.userAgent, parse: (s) => s, text: true, field: 'userAgent', wide: true },
];

function Limits({ d, busy, change }) {
  const fields = [...LIMITS, ...ADVANCED];
  const valueOf = (f) => {
    const v = d[f.field || f.key];
    return v === 0 || v === '' || v == null ? '' : String(v);
  };
  const initial = () => Object.fromEntries(fields.map((f) => [f.key, valueOf(f)]));
  const [form, setForm] = useState(initial);
  const [advanced, setAdvanced] = useState(() => ADVANCED.some((f) => valueOf(f) !== ''));
  useEffect(() => setForm(initial()), [d]);
  const dirty = fields.filter((f) => form[f.key].trim() !== valueOf(f));
  const custom = fields.some((f) => valueOf(f) !== '');

  const save = () => {
    const values = {};
    for (const f of dirty) {
      const s = form[f.key].trim();
      values[f.key] = s === '' ? (f.text ? '' : 0) : f.parse(s);
      if (!f.text && s !== '' && !Number.isFinite(values[f.key])) { toast(`${f.label} must be a number.`, 'error'); return; }
    }
    // A cleared timeout is the zero duration, which the config spells "0s".
    if (values.timeout === '') values.timeout = '0s';
    change('limits', values, 'Limits saved.');
  };
  const reset = () => {
    const values = {};
    for (const f of fields) if (valueOf(f) !== '') values[f.key] = f.key === 'timeout' ? '0s' : f.text ? '' : 0;
    change('limits', values, 'Back to the defaults.');
  };

  const field = (f) => html`<label key=${f.key} class=${cx('block', f.wide && 'sm:col-span-2')}>
    <span class="block text-xs text-muted mb-1">${f.label}</span>
    <div class="relative">
      <input class=${cx(inputCls, f.unit && 'pr-12')} inputmode=${f.text ? undefined : 'numeric'} value=${form[f.key]}
        placeholder=${`${f.def(d.defaults)}${f.unit && f.unit !== 'bytes' ? ' ' + f.unit : ''} (default)`}
        onInput=${(e) => setForm((x) => ({ ...x, [f.key]: e.target.value }))} />
      ${f.unit && html`<span class="absolute right-2.5 top-1/2 -translate-y-1/2 text-[11px] text-faint pointer-events-none">${f.unit}</span>`}
    </div>
    <span class="block text-[11px] text-faint mt-1">${f.hint || (f.unit === 'bytes' ? 'Default ' + kib(f.def(d.defaults)) : ' ')}</span>
  </label>`;

  return html`<${Section} title="Limits" desc="Leave a field empty to use the default shown in it.">
    <div class=${cx(card, 'p-4')}>
      <div class="grid gap-x-3 gap-y-2 sm:grid-cols-3">${LIMITS.map(field)}</div>
      <button type="button" class="mt-1 text-xs text-muted hover:text-fg flex items-center gap-1" aria-expanded=${advanced} onClick=${() => setAdvanced(!advanced)}>
        <${Icon} name="chevronRight" size=${12} class=${cx('transition-transform', advanced && 'rotate-90')} /> Advanced
      </button>
      ${advanced && html`<div class="grid gap-x-3 gap-y-2 sm:grid-cols-3 mt-2">${ADVANCED.map(field)}</div>`}
      <div class="flex items-center justify-end gap-2 mt-3 pt-3 border-t border-line">
        ${custom && html`<${Button} kind="ghost" class="mr-auto text-muted" disabled=${!!busy} onClick=${reset}>Use all defaults</${Button}>`}
        ${dirty.length > 0 && html`<${Button} kind="ghost" onClick=${() => setForm(initial())}>Discard</${Button}>`}
        <${Button} kind="primary" busy=${busy === 'limits'} disabled=${!dirty.length} onClick=${save}>Save limits</${Button}>
      </div>
    </div>
  </${Section}>`;
}

// pick reads a field whichever casing the backend used.
const pick = (o, ...names) => { for (const n of names) if (o?.[n] != null && o[n] !== '') return o[n]; return undefined; };

function TryIt({ enabled }) {
  const [tool, setTool] = useState('search');
  const [query, setQuery] = useState('');
  const [url, setURL] = useState('');
  const [run, setRun] = useState({ status: 'idle' });

  const go = async (e) => {
    e.preventDefault();
    setRun({ status: 'running', tool });
    try {
      const body = tool === 'search' ? { tool, query: query.trim() } : { tool, url: url.trim() };
      setRun({ status: 'done', tool, res: await api.testWebSearch(body) });
    } catch (err) { setRun({ status: 'failed', tool, error: errorText(err) }); }
  };
  const ready = tool === 'search' ? query.trim() : url.trim();

  return html`<${Section} title="Try it" desc="Runs the real tool with the settings above — what a thread would get.">
    <div class=${cx(card, 'p-4')}>
      <form class="flex flex-col sm:flex-row gap-2" onSubmit=${go}>
        <div class="flex gap-1 p-0.5 rounded-lg bg-raised border border-line w-fit text-[13px] shrink-0" role="radiogroup" aria-label="Tool">
          ${[['search', 'Search'], ['fetch', 'Fetch']].map(([v, t]) => html`<button key=${v} type="button" role="radio" aria-checked=${tool === v}
            class=${cx('px-3 h-7 rounded-md', tool === v ? 'bg-panel text-fg shadow-sm' : 'text-muted hover:text-fg')}
            onClick=${() => { setTool(v); setRun({ status: 'idle' }); }}>${t}</button>`)}
        </div>
        ${tool === 'search'
          ? html`<input class=${inputCls} type="search" aria-label="Search query" placeholder="What to search for" value=${query} onInput=${(e) => setQuery(e.target.value)} />`
          : html`<input class=${inputCls} type="url" aria-label="URL to fetch" placeholder="https://example.com/page" value=${url} onInput=${(e) => setURL(e.target.value)} />`}
        <${Button} kind="primary" type="submit" class="shrink-0" busy=${run.status === 'running'} disabled=${!ready || !enabled}>
          ${tool === 'search' ? 'Search' : 'Fetch'}</${Button}>
      </form>
      ${!enabled && html`<p class="text-xs text-muted mt-2">Turn the web tools on to try them.</p>`}
      ${run.status === 'running' && html`<div class="mt-4 flex items-center gap-2 text-xs text-muted"><${Spinner} size=${12} />
        ${run.tool === 'search' ? 'Asking every active source…' : 'Fetching the page…'} This can take up to the timeout.</div>`}
      ${run.status === 'failed' && html`<${ErrorBlock} class="mt-4" title="Could not run the test" message=${run.error} />`}
      ${run.status === 'done' && html`<${TestResult} tool=${run.tool} res=${run.res} />`}
    </div>
  </${Section}>`;
}

function TestResult({ tool, res }) {
  const [raw, setRaw] = useState(false);
  const out = res.output || {};
  const secs = (res.durationMs / 1000).toFixed(1) + 's';
  if (res.error) return html`<div class="mt-4">
    <${ErrorBlock} title=${'The ' + tool + ' failed after ' + secs} message=${res.error} />
  </div>`;
  const results = pick(out, 'results', 'Results') || [];
  const errors = pick(out, 'errors', 'Errors') || [];
  const backend = out.backend;
  return html`<div class="mt-4">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted mb-2">
      <span>${tool === 'search' ? `${results.length} result${results.length === 1 ? '' : 's'}` : `HTTP ${pick(out, 'statusCode') ?? '?'}`} in ${secs}</span>
      ${backend && html`<span>via <span class="text-fg">${backend === 'searchwire' ? 'built-in metasearch' : backend}</span></span>`}
      <button type="button" class="ml-auto hover:text-fg" onClick=${() => setRaw(!raw)}>${raw ? 'Show summary' : 'Show what the model reads'}</button>
    </div>
    ${out.backendFallback && html`<p class="text-xs text-warn mb-2">Provider search was not used: ${out.backendFallback}</p>`}
    ${errors.length > 0 && html`<ul class="mb-2 flex flex-col gap-1">${errors.map((e, i) => html`<li key=${i} class="text-xs rounded-lg bg-warn/10 text-warn px-2.5 py-1.5 break-words">
      <span class="font-medium">${pick(e, 'source', 'Source')}</span>: ${pick(e, 'error', 'Error')}</li>`)}</ul>`}
    ${raw ? html`<pre class="text-[11px] leading-4 bg-raised rounded-lg p-3 max-h-80 overflow-auto whitespace-pre-wrap break-words">${res.text || JSON.stringify(out, null, 2)}</pre>`
      : tool === 'search' ? (results.length
        ? html`<ol class="flex flex-col gap-2.5">${results.map((r, i) => {
            const link = pick(r, 'url', 'URL', 'link');
            const srcs = pick(r, 'sources', 'Sources') || [];
            return html`<li key=${i} class="min-w-0">
              <a href=${link} target="_blank" rel="noopener noreferrer" class="text-[13px] text-accent hover:underline break-words">${pick(r, 'title', 'Title') || link}</a>
              <div class="text-[11px] text-faint truncate">${link}${srcs.length ? ' · ' + srcs.join(', ') : ''}</div>
              ${pick(r, 'snippet', 'Snippet') && html`<p class="text-xs text-muted mt-0.5 line-clamp-2">${pick(r, 'snippet', 'Snippet')}</p>`}
            </li>`;
          })}</ol>`
        : html`<p class="text-[13px] text-muted">No results. ${errors.length ? 'Every source above failed.' : 'Try other words.'}</p>`)
      : html`<div>
          ${pick(out, 'title') && html`<div class="text-[13px] font-medium mb-1">${out.title}</div>`}
          <pre class="text-[12px] leading-5 bg-raised rounded-lg p-3 max-h-64 overflow-auto whitespace-pre-wrap break-words font-sans">${(pick(out, 'text') || '').slice(0, 4000) || 'The page had no text.'}</pre>
          ${out.truncated && html`<p class="text-[11px] text-faint mt-1">Cut at the page-text limit.</p>`}
        </div>`}
  </div>`;
}
