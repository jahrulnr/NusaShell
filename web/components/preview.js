// File preview: a dialog over the app for a file a thread links, uploads,
// or the side pane lists. Text is read through a Range request so the
// server sends at most the window shown; media and PDFs load directly.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import hljs from '/vendor/highlight/highlight.min.mjs';
import { useStore, state, emit } from '../lib/store.js';
import { renderInto, disposeTree, handleCopyClick } from '../lib/markdown.js';
import { Icon } from './icons.js';
import { Player } from './player.js';
import { cx, Dialog, Spinner } from './ui.js';

const CAP = 1024 * 1024; // text window, 1 MB
const LINE_CAP = 8 * 1024 * 1024; // larger window when jumping to a line

// openPreview shows a file; url serves it, line (1-based) is highlighted.
export function openPreview(url, name, line = 0) {
  state.preview = { url, name: name || url.split('/').pop() || 'File', line, key: Date.now() };
  emit();
}

const fmtSize = (n) => (n >= 1 << 20 ? (n / (1 << 20)).toFixed(1) + ' MB' : Math.max(1, Math.ceil(n / 1024)) + ' KB');
const ext = (name) => (/\.([a-z0-9]+)$/i.exec(name)?.[1] || '').toLowerCase();
const LANG = { js: 'javascript', mjs: 'javascript', cjs: 'javascript', ts: 'typescript', tsx: 'typescript', jsx: 'javascript', py: 'python', rb: 'ruby', rs: 'rust', sh: 'bash', zsh: 'bash', yml: 'yaml', md: 'markdown', htm: 'xml', html: 'xml', svg: 'xml', vue: 'xml', h: 'c', hpp: 'cpp', cc: 'cpp', kt: 'kotlin', cs: 'csharp', ps1: 'powershell', toml: 'ini', conf: 'ini', env: 'ini', dockerfile: 'dockerfile', mod: 'go', sum: 'plaintext' };
function language(name) {
  const e = ext(name) || name.toLowerCase();
  const l = LANG[e] || e;
  return hljs.getLanguage(l) ? l : '';
}

function kindOf(name, type) {
  if (type.startsWith('image/')) return 'image';
  if (type.startsWith('video/')) return 'video';
  if (type.startsWith('audio/')) return 'audio';
  if (type === 'application/pdf') return 'pdf';
  if (ext(name) === 'md' || ext(name) === 'markdown') return 'markdown';
  return 'text';
}

function usePreview(p) {
  const [st, setSt] = useState({ status: 'loading' });
  useEffect(() => {
    if (!p) return;
    setSt({ status: 'loading' });
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(new DOMException('timeout', 'TimeoutError')), 20_000);
    const cap = p.line > 0 ? LINE_CAP : CAP;
    (async () => {
      try {
        const res = await fetch(p.url, { signal: ctl.signal, headers: { Range: `bytes=0-${cap - 1}` } });
        if (!res.ok) {
          let msg = `Could not open the file (HTTP ${res.status}).`;
          try { const d = await res.json(); msg = d.detail || d.title || msg; } catch { /* not JSON */ }
          throw new Error(msg);
        }
        const type = (res.headers.get('content-type') || '').split(';')[0].trim();
        const kind = kindOf(p.name, type);
        if (kind === 'image' || kind === 'video' || kind === 'audio') {
          await res.body?.cancel();
          setSt({ status: 'ready', kind });
          return;
        }
        const total = Number((res.headers.get('content-range') || '').split('/')[1] || res.headers.get('content-length') || 0);
        const buf = await res.arrayBuffer();
        const body = buf.byteLength > cap ? buf.slice(0, cap) : buf;
        const truncated = buf.byteLength > cap || total > body.byteLength;
        if (kind === 'pdf') {
          if (truncated) throw new Error(`This PDF is too large to preview (${fmtSize(total)}). Open or download it instead.`);
          setSt({ status: 'ready', kind, src: URL.createObjectURL(new Blob([body], { type })) });
          return;
        }
        let text;
        try { text = new TextDecoder('utf-8', { fatal: !truncated }).decode(body); }
        catch { throw new Error('This file is not text, so there is no preview. Open or download it instead.'); }
        if (/\u0000/.test(text.slice(0, 4096))) throw new Error('This file is not text, so there is no preview. Open or download it instead.');
        setSt({ status: 'ready', kind: kind === 'markdown' && !truncated && !p.line ? 'markdown' : 'text', text, truncated, shown: body.byteLength, total });
      } catch (e) {
        if (ctl.signal.aborted && !(ctl.signal.reason instanceof DOMException && ctl.signal.reason.name === 'TimeoutError')) return;
        setSt({ status: 'error', error: ctl.signal.aborted ? 'The file took too long to load.' : e.message || 'Could not open the file.' });
      } finally { clearTimeout(timer); }
    })();
    return () => { clearTimeout(timer); ctl.abort(); };
  }, [p?.key]);
  useEffect(() => () => { if (st.src) URL.revokeObjectURL(st.src); }, [st.src]);
  return st;
}

function Code({ name, text, line }) {
  const ref = useRef();
  const rows = text.endsWith('\n') ? text.slice(0, -1).split('\n') : text.split('\n');
  const lang = text.length <= 512 * 1024 ? language(name) : '';
  let lines = rows.map((r) => ({ __html: null, text: r }));
  if (lang) {
    // Highlight each line on its own: spans never straddle rows, at the
    // cost of multi-line constructs (block comments) losing some color.
    lines = rows.map((r) => {
      try { return { __html: hljs.highlight(r, { language: lang, ignoreIllegals: true }).value }; } catch { return { text: r }; }
    });
  }
  // After the dialog has opened: a child's effect runs before its parent's.
  useEffect(() => {
    const id = requestAnimationFrame(() => ref.current?.querySelector('[data-selected]')?.scrollIntoView({ block: 'center' }));
    return () => cancelAnimationFrame(id);
  }, [line, text]);
  const width = String(rows.length).length;
  return html`<pre ref=${ref} class="text-[12.5px] leading-[1.55] font-mono overflow-auto"><code class="block min-w-max">${lines.map((l, i) => html`<div key=${i}
      class=${cx('flex', i + 1 === line && 'bg-[color-mix(in_srgb,var(--accent)_16%,transparent)]')} data-selected=${i + 1 === line ? '1' : undefined}>
      <span class="select-none text-faint text-right pr-3 shrink-0" style=${{ width: width + 2 + 'ch' }}>${i + 1}</span>
      ${l.__html != null ? html`<span class="whitespace-pre" dangerouslySetInnerHTML=${{ __html: l.__html }}></span>` : html`<span class="whitespace-pre">${l.text}</span>`}
    </div>`)}</code></pre>`;
}

function MarkdownDoc({ text, conversationId }) {
  const ref = useRef();
  useEffect(() => { renderInto(ref.current, text, { conversationId }); return () => disposeTree(ref.current); }, [text, conversationId]);
  // A picture or video the document shows inline opens on its own.
  const click = (e) => {
    const b = e.target.closest?.('[data-media-url]');
    if (b) openPreview(b.dataset.mediaUrl, b.dataset.mediaName); else handleCopyClick(e);
  };
  return html`<div ref=${ref} class="md" onClick=${click}></div>`;
}

export function FilePreview() {
  const s = useStore();
  const p = s.preview;
  const st = usePreview(p);
  const close = () => { state.preview = null; emit(); };
  if (!p) return null;
  // A thread's file URL names the room, so a relative path the document
  // shows resolves in that room's workspace rather than nowhere.
  const conversationId = /\/api\/conversations\/([^/?#]+)\/files/.exec(p.url || '')?.[1] || '';
  // A file still being attached is a local blob: it downloads as it is.
  const download = p.url.startsWith('blob:') ? p.url : p.url + (p.url.includes('?') ? '&' : '?') + 'download=1';
  const body = st.status === 'loading'
    ? html`<div class="flex items-center gap-2 text-muted text-[13px] py-8 justify-center"><${Spinner} /> Loading the file…</div>`
    : st.status === 'error'
      ? html`<div class="flex items-start gap-2 text-[13px] py-6"><${Icon} name="alert" size=${16} class="text-warn shrink-0 mt-0.5" /><span>${st.error}</span></div>`
      : st.kind === 'image' ? html`<img src=${p.url} alt=${p.name} class="max-w-full max-h-[70dvh] mx-auto rounded-lg bg-white" />`
      : st.kind === 'video' || st.kind === 'audio' ? html`<${Player} key=${p.url} kind=${st.kind} src=${p.url} />`
      : st.kind === 'pdf' ? html`<iframe src=${st.src} title=${p.name} class="w-full h-[70dvh] rounded-lg border border-line bg-white"></iframe>`
      : st.kind === 'markdown' ? html`<${MarkdownDoc} text=${st.text} conversationId=${conversationId} />`
      : html`<${Code} name=${p.name} text=${st.text} line=${p.line} />`;
  return html`<${Dialog} open=${true} onClose=${close} backdrop=${true} title=${p.name} class="w-[min(1000px,calc(100vw-24px))]"
    footer=${html`
      ${st.truncated && html`<span class="mr-auto text-xs text-faint">Showing the first ${fmtSize(st.shown)} of ${fmtSize(st.total)}.</span>`}
      <a href=${p.url} target="_blank" rel="noopener" class="h-8 px-3 inline-flex items-center rounded-lg border border-line text-[13px] hover:bg-hover">Open original</a>
      <a href=${download} download=${p.name} class="h-8 px-3 inline-flex items-center gap-1.5 rounded-lg bg-fg text-bg text-[13px] hover:opacity-90"><${Icon} name="download" size=${14} /> Download</a>`}>
    ${body}
  </${Dialog}>`;
}
