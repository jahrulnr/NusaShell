// The owner's terminals, shown with xterm.js: a thread's login shell in one
// of its folders, and an agent's interactive sign-in. xterm loads the first
// time a terminal opens.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { Icon } from './icons.js';
import { cx, IconButton, Spinner } from './ui.js';
import { RootPicker } from './sidepane.js';

let xterm = null;
function loadXterm() {
  if (xterm) return xterm;
  xterm = new Promise((resolve, reject) => {
    const css = document.createElement('link');
    css.rel = 'stylesheet';
    css.href = '/vendor/xterm/xterm.css';
    document.head.appendChild(css);
    const load = (src) => new Promise((ok, fail) => {
      const s = document.createElement('script');
      s.src = src;
      s.onload = ok;
      s.onerror = () => fail(new Error('Could not load the terminal.'));
      document.head.appendChild(s);
    });
    load('/vendor/xterm/xterm.js').then(() => load('/vendor/xterm/addon-web-links.js')).then(() => resolve(window.Terminal), (e) => { xterm = null; reject(e); });
  });
  return xterm;
}

const cssVar = (name) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();
const theme = () => ({ background: cssVar('--bg'), foreground: cssVar('--fg'), cursor: cssVar('--accent'), selectionBackground: cssVar('--accent') + '55' });

// fit sizes the terminal to its box in whole cells.
function fit(term, box) {
  const cell = term._core?._renderService?.dimensions?.css?.cell;
  const w = cell?.width || 7.8, h = cell?.height || 17;
  const cols = Math.max(20, Math.floor((box.clientWidth - 12) / w));
  const rows = Math.max(5, Math.floor((box.clientHeight - 8) / h));
  if (cols !== term.cols || rows !== term.rows) term.resize(cols, rows);
}

// useXterm runs xterm.js in box, joined to the terminal socket at path
// (query params added, the size last); a change of path, params or gen
// starts it again. It answers where the socket stands and the terminal.
export function useXterm(box, path, params, gen, failed) {
  const term = useRef(null);
  const [st, setSt] = useState({ status: 'connecting' });
  const key = path + '?' + new URLSearchParams(params);
  useEffect(() => {
    let closed = false;
    let observer;
    let ws;
    setSt({ status: 'connecting' });
    loadXterm().then((Terminal) => {
      if (closed) return;
      const t = new Terminal({ cursorBlink: true, fontSize: 13, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', theme: theme(), allowProposedApi: false });
      if (window.WebLinksAddon) t.loadAddon(new window.WebLinksAddon.WebLinksAddon((e, uri) => window.open(uri, '_blank', 'noopener')));
      t.open(box.current);
      term.current = t;
      fit(t, box.current);
      const q = new URLSearchParams(params);
      q.set('cols', t.cols);
      q.set('rows', t.rows);
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
      ws = new WebSocket(`${proto}//${location.host}${path}?${q}`);
      ws.binaryType = 'arraybuffer';
      const send = (m) => { if (ws.readyState === 1) ws.send(JSON.stringify(m)); };
      ws.onmessage = (e) => {
        if (typeof e.data !== 'string') { t.write(new Uint8Array(e.data)); return; }
        const m = JSON.parse(e.data);
        if (m.type === 'ready') { setSt({ status: 'open', cwd: m.cwd }); t.focus(); }
        else if (m.type === 'exit') setSt({ status: 'exited', message: m.message, ok: m.ok });
      };
      ws.onclose = () => {
        if (closed) return;
        setSt((s) => (s.status === 'exited' ? s : { status: 'error', message: s.status === 'connecting' ? failed : 'The connection to the terminal dropped.' }));
      };
      t.onData((data) => send({ type: 'input', data }));
      t.onResize(({ cols, rows }) => send({ type: 'resize', cols, rows }));
      observer = new ResizeObserver(() => { if (box.current?.clientWidth) fit(t, box.current); });
      observer.observe(box.current);
    }, (e) => { if (!closed) setSt({ status: 'error', message: e.message }); });
    return () => {
      closed = true;
      observer?.disconnect();
      ws?.close();
      term.current?.dispose();
      term.current = null;
    };
  }, [key, gen]);
  return { st, term };
}

export function TerminalPane({ room: r, roots, active }) {
  const box = useRef();
  const [cwd, setCwd] = useState('');
  const [gen, setGen] = useState(0);
  const path = r.id ? `/api/conversations/${encodeURIComponent(r.id)}/terminal` : '/api/terminal';
  const { st, term } = useXterm(box, path, cwd ? { cwd } : {}, gen,
    'Could not start the terminal here. The thread may work on another machine or have no folder.');
  useEffect(() => { if (active && term.current) { fit(term.current, box.current); term.current.focus(); } }, [active]);

  const where = st.cwd || cwd || roots.list?.[0] || '';
  return html`
    <div class="flex items-center gap-1 h-9 px-2 border-b border-line shrink-0">
      <span class=${cx('size-1.5 rounded-full shrink-0 ml-1', st.status === 'open' ? 'bg-ok' : st.status === 'connecting' ? 'bg-warn' : 'bg-faint')}></span>
      <div class="flex-1 min-w-0 ml-1"><${RootPicker} roots=${roots} path=${where} onPick=${(p) => setCwd(p)} /></div>
      <${IconButton} icon="refresh" size=${14} label="Restart the terminal" onClick=${() => setGen((n) => n + 1)} class="size-7" />
    </div>
    ${st.status !== 'open' && st.status !== 'connecting' && html`<div class="flex items-center gap-2 px-3 py-2 border-b border-line text-[12px] shrink-0" role="status">
      <${Icon} name=${st.status === 'exited' && st.ok ? 'info' : 'alert'} size=${14} class=${st.status === 'exited' && st.ok ? 'text-muted' : 'text-warn'} />
      <span class="flex-1 min-w-0">${st.message}</span>
      <button type="button" class="h-6 px-2 rounded-md border border-line hover:bg-hover" onClick=${() => setGen((n) => n + 1)}>Start again</button>
    </div>`}
    <div class="relative flex-1 min-h-0 bg-bg">
      ${st.status === 'connecting' && html`<div class="absolute inset-0 grid place-items-center text-muted text-[13px] pointer-events-none"><span class="flex items-center gap-2"><${Spinner} size=${12} /> Starting the terminal…</span></div>`}
      <div ref=${box} class="absolute inset-0 pl-2 pt-1"></div>
    </div>`;
}
