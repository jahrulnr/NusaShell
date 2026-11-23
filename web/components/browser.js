// A thread's browser, live: the agent or model drives it through the
// browser tools; the owner watches here and can take over the input, or
// opens a page of their own before any agent has. The header always has
// the button, and the panel opens by itself when a running turn starts a
// session.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { toast, emit } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { isBusy, annotate, nextMarker, annotationLabel } from '../lib/chat.js';
import { wasStable } from '../lib/socket.js';
import { Icon } from './icons.js';
import { cx, Button, Checkbox, ErrorBlock, IconButton, Marker, Select } from './ui.js';
import { openPreview } from './preview.js';

// useBrowser follows the thread's browser session: often while a turn
// runs or its tab shows, rarely otherwise. pane/setPane is the thread's
// side pane; a session a running turn opens brings its tab up once.
export function useBrowser(r, pane, setPane) {
  const [st, setSt] = useState({ open: false, page: null });
  const seen = useRef(false);
  // claimed: the owner just started this session from the panel, so they
  // take the input over as soon as the live view connects.
  const claimed = useRef(false);
  const busy = isBusy(r);
  const panel = pane === 'browser';
  // fromTurn: a turn is running or just ended, so a session that appeared
  // now is the turn's doing and worth showing once.
  const load = async (fromTurn) => {
    if (!r.id) return;
    try {
      const res = await api.browser(r.id);
      setSt({ open: !!res.open, page: res.page || null });
      if (res.open && !seen.current && fromTurn) setPane('browser');
      // A session that ended leaves the panel on its start form.
      seen.current = !!res.open;
    } catch { /* status is best effort */ }
  };
  // liveConnected is set by the panel while its live-view socket is open:
  // that socket already pushes a `state` frame on every change (page,
  // device, lease, profile), so polling the status route on top of it is
  // asking the same question twice.
  const liveConnected = useRef(false);
  useEffect(() => { seen.current = false; setSt({ open: false, page: null }); load(busy); }, [r.id]);
  const ran = useRef(false);
  useEffect(() => {
    if (busy) ran.current = true;
    else if (ran.current) { ran.current = false; load(true); }
    const t = setInterval(() => {
      if (panel && liveConnected.current) return;
      load(busy);
    }, busy || panel ? 2500 : 15000);
    return () => clearInterval(t);
  }, [r.id, busy, panel]);
  return { ...st, panel, claimed, liveConnected, setPanel: (v) => setPane(v ? 'browser' : null), reload: () => load(false) };
}

export function BrowserButton({ browser: b }) {
  const label = b.open ? "This thread's browser" : "Open this thread's browser";
  return html`<button type="button" onClick=${() => b.setPanel(!b.panel)} aria-pressed=${b.panel} title=${label} aria-label=${label}
    class=${cx('relative inline-flex items-center justify-center size-8 rounded-md hover:bg-hover', b.panel ? 'text-accent' : 'text-muted hover:text-fg')}>
    <${Icon} name="globe" size=${16} />
    ${b.open && html`<span class="absolute top-1.5 right-1.5 size-1.5 rounded-full bg-ok"></span>`}
  </button>`;
}

// AddressInput is an address bar that suggests pages the profile visited:
// recent ones on focus, matching ones while typing. Arrows move through
// them, Enter goes to the one picked (or to what was typed), Esc closes.
function AddressInput({ value, onInput, onGo, profile, readOnly, class: klass, ...rest }) {
  const [items, setItems] = useState([]);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const asked = useRef(0);
  const timer = useRef(0);
  const list = useRef();
  const suggest = (q) => {
    clearTimeout(timer.current);
    const n = ++asked.current;
    timer.current = setTimeout(() => {
      api.browserHistory(profile, q.trim()).then((res) => {
        if (n !== asked.current) return;
        // The page already in the bar is not a suggestion.
        setItems((res.data || []).filter((v) => v.url !== q.trim()));
        setActive(-1);
      }, () => { if (n === asked.current) setItems([]); });
    }, 120);
  };
  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(() => { list.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' }); }, [active]);
  const close = () => { clearTimeout(timer.current); asked.current++; setOpen(false); setActive(-1); };
  const go = (url) => { close(); onGo(url); };
  const shown = open && !readOnly && items.length > 0;
  const onKeyDown = (e) => {
    if (e.key === 'Escape' && shown) { e.preventDefault(); close(); return; }
    if (!shown || (e.key !== 'ArrowDown' && e.key !== 'ArrowUp' && e.key !== 'Enter')) return;
    if (e.key === 'Enter') {
      if (active < 0) { close(); return; } // the form submits what was typed
      e.preventDefault();
      go(items[active].url);
      return;
    }
    e.preventDefault();
    const step = e.key === 'ArrowDown' ? 1 : -1;
    // -1 is the typed address; the steps wrap through it.
    setActive((i) => { const n = items.length + 1; return ((i + 1 + step + n) % n) - 1; });
  };
  return html`<div class="relative flex-1 min-w-0">
    <input ...${rest} value=${value} readOnly=${readOnly} spellcheck="false" autocomplete="off" role="combobox"
      aria-expanded=${shown} aria-controls="address-suggestions" aria-autocomplete="list"
      aria-activedescendant=${shown && active >= 0 ? 'address-opt-' + active : undefined}
      onFocus=${(e) => { rest.onFocus?.(e); if (!readOnly) { setOpen(true); suggest(''); } }}
      onBlur=${(e) => { rest.onBlur?.(e); close(); }}
      onInput=${(e) => { onInput(e.target.value); setOpen(true); suggest(e.target.value); }}
      onKeyDown=${onKeyDown}
      class=${klass} />
    ${shown && html`<div ref=${list} id="address-suggestions" role="listbox" aria-label="Pages you visited"
      class="absolute z-30 left-0 right-0 top-full mt-1 rounded-lg border border-line bg-panel shadow-pop py-1 max-h-72 overflow-y-auto text-left">
      ${items.map((v, i) => html`<button key=${v.url} id=${'address-opt-' + i} type="button" role="option" aria-selected=${i === active} tabIndex="-1"
        onMouseDown=${(e) => { e.preventDefault(); go(v.url); }} onMouseEnter=${() => setActive(i)}
        class=${cx('w-full flex items-center gap-2 px-2.5 py-1.5 text-left min-w-0', i === active && 'bg-hover')}>
        <${Icon} name="globe" size=${13} class="shrink-0 text-faint" />
        <span class="min-w-0 flex-1">
          <span class="block truncate text-[12px]">${v.title || v.url.replace(/^https?:\/\/(www\.)?/, '')}</span>
          <span class="block truncate text-[11px] font-mono text-muted">${v.url}</span>
        </span>
      </button>`)}
    </div>`}
  </div>`;
}

// BrowserStart is the panel before any session: the owner opens a page
// here, and an agent's browser tools open the same browser.
function BrowserStart({ room: r, browser: b }) {
  const [url, setUrl] = useState(r.browserUrl || '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [offer, setOffer] = useState(null);
  const checkOffer = () => api.browserInstallOffer().then(setOffer, () => { /* the start call says what is wrong */ });
  useEffect(() => { checkOffer(); }, []);
  const missing = !!offer?.missing;
  const open = async (to) => {
    setBusy(true);
    setError('');
    try {
      await api.startBrowser(r.id, to.trim());
      b.claimed.current = true;
      b.reload();
    } catch (err) {
      setError(errorText(err));
      checkOffer();
    }
    setBusy(false);
  };
  return html`<div class="flex-1 min-h-0 overflow-y-auto grid place-items-center p-6">
    <div class="w-full max-w-sm text-center">
      <span class="mx-auto size-10 rounded-xl bg-raised flex items-center justify-center text-muted"><${Icon} name="globe" size=${18} /></span>
      <h3 class="mt-3 font-medium">This thread's browser</h3>
      <p class="text-muted text-[13px] mt-1">Nothing is open yet. Open a page to watch it and take over the keyboard and mouse. The agent drives the same browser with its browser tools.</p>
      ${missing && html`<p class="mt-3 text-[13px] text-warn" role="status">No Chrome or Chromium was found on this machine.
        <a href="#/settings/tools" class="underline">Install one in Settings → Tools</a>.</p>`}
      <form noValidate onSubmit=${(e) => { e.preventDefault(); open(url); }} class="mt-4 flex gap-2">
        <${AddressInput} value=${url} onInput=${setUrl} onGo=${(u) => { setUrl(u); open(u); }} aria-label="Address" placeholder="example.com" autofocus
          class="w-full h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent" />
        <${Button} kind="primary" type="submit" busy=${busy} disabled=${missing}>${url.trim() ? 'Open' : 'Open blank'}</${Button}>
      </form>
      ${error && html`<${ErrorBlock} class="mt-4 text-left" title="Could not open the browser" message=${error} />`}
    </div>
  </div>`;
}

// useLiveView connects to the live view socket: frames become an object
// URL, state messages the page, device, lease, and profile.
function useLiveView(convID, on, onClosed) {
  const [v, setV] = useState({ frame: '', state: null, viewer: '', conn: 'connecting' });
  const ws = useRef(null);
  const keyboard = useRef(() => {});
  useEffect(() => {
    if (!on) return;
    setV({ frame: '', state: null, viewer: '', conn: 'connecting' });
    let closed = false, retry = 0, openedAt = 0, url = '';
    const disconnect = () => {
      if (url) URL.revokeObjectURL(url);
      url = '';
      setV({ frame: '', state: null, viewer: '', conn: 'connecting' });
    };
    const open = () => {
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const sock = new WebSocket(`${proto}//${location.host}/api/conversations/${encodeURIComponent(convID)}/browser/ws`);
      sock.binaryType = 'blob';
      ws.current = sock;
      sock.onopen = () => { if (closed) return; openedAt = Date.now(); setV((x) => ({ ...x, conn: 'open' })); };
      sock.onmessage = (e) => {
        if (closed) return;
        if (typeof e.data !== 'string') {
          const next = URL.createObjectURL(e.data);
          setV((x) => ({ ...x, frame: next }));
          if (url) URL.revokeObjectURL(url);
          url = next;
          return;
        }
        const m = JSON.parse(e.data);
        if (m.type === 'hello') setV((x) => ({ ...x, viewer: m.viewer }));
        else if (m.type === 'state') setV((x) => ({ ...x, state: { page: m.page, device: m.device, lease: m.lease, profile: m.profile, loginOffer: m.loginOffer, recording: m.recording, agentPointer: m.agentPointer } }));
        else if (m.type === 'refused') toast(m.message, 'error');
        else if (m.type === 'keyboard') keyboard.current(m.editable);
        else if (m.type === 'closed') { onClosed(); sock.close(); }
      };
      sock.onclose = () => {
        if (closed) return;
        disconnect();
        // A profile switch ends the old session: reconnect to the new
        // one. The streak resets only after a link proved stable — and
        // a socket that never opened proves nothing — so a socket the
        // server keeps dropping paces itself instead of hammering every
        // half second.
        if (wasStable(openedAt, 5000)) retry = 0;
        const delay = Math.min(500 * 2 ** retry++, 5000) * (0.75 + Math.random() * 0.5);
        setTimeout(() => { if (!closed) open(); }, delay);
      };
    };
    open();
    return () => { closed = true; ws.current?.close(); if (url) URL.revokeObjectURL(url); };
  }, [convID, on]);
  const send = (m) => { if (ws.current?.readyState === 1) ws.current.send(JSON.stringify(m)); };
  return { ...v, send, keyboard };
}

const MOD = (e) => (e.altKey ? 1 : 0) | (e.ctrlKey ? 2 : 0) | (e.metaKey ? 4 : 0) | (e.shiftKey ? 8 : 0);
const BUTTON = ['left', 'middle', 'right'];

// The viewport choices. Responsive makes the tab as large as this panel and
// follows it, so the page fills the panel; the presets pin one device. The
// state event names the active one; Custom appears only while it is.
const DEVICES = [
  { value: 'responsive', text: 'Responsive' },
  { value: 'desktop', text: 'Desktop' },
  { value: 'mobile', text: 'Phone' },
  { value: 'tablet', text: 'Tablet' },
];
const FIT_INTERVAL_MS = 80;
const MIN_SIDE = 200;
const MAX_SIDE = 4096;

export function BrowserPanel({ room: r, browser: b }) {
  const live = useLiveView(r.id, b.open && b.panel, () => b.reload());
  // Tell the status poll it can stand down: while this socket is open it
  // is the source of truth for the page, device, lease, and profile.
  useEffect(() => {
    if (b.liveConnected) b.liveConnected.current = live.conn === 'open';
  }, [live.conn]);
  useEffect(() => () => { if (b.liveConnected) b.liveConnected.current = false; }, []);
  const [url, setUrl] = useState('');
  const [editingUrl, setEditingUrl] = useState(false);
  const [consoleOpen, setConsoleOpen] = useState(false);
  const img = useRef();
  const typing = useRef();
  const lastMove = useRef(0);
  const st = live.state;
  const page = st?.page || b.page || {};
  useEffect(() => { if (!editingUrl) setUrl(page.url || ''); }, [page.url, editingUrl]);
  // The last real page is kept with the thread's view, so the start form
  // offers it again once this session has closed.
  useEffect(() => {
    if (page.url && /^https?:/i.test(page.url) && r.browserUrl !== page.url) { r.browserUrl = page.url; emit(); }
  }, [page.url]);
  // Saved logins for the page the tab is on, to fill on the owner's pick.
  const [logins, setLogins] = useState([]);
  // A pick resets its list to its prompt, which a new key remounts.
  const [fillKey, setFillKey] = useState(0);
  useEffect(() => {
    if (!b.open || !b.panel || !page.url) return;
    api.pageLogins(r.id).then((res) => setLogins(res.data || []), () => setLogins([]));
  }, [page.url, st?.profile, st?.loginOffer?.id, b.open, b.panel]);
  live.keyboard.current = (editable) => { if (editable) typing.current?.focus(); };
  const view = useRef();
  const mark = useAnnotate(r, live.conn === 'open' && !!b.open && !!b.panel);
  const lease = st?.lease || {};
  const driving = !!live.viewer && lease.controller === live.viewer && !lease.agentLock;
  // A session the owner started from the panel is theirs to drive.
  useEffect(() => {
    if (!b.claimed.current || live.conn !== 'open' || !live.viewer || lease.controller || lease.agentLock) return;
    b.claimed.current = false;
    live.send({ type: 'takeover' });
  }, [live.conn, live.viewer, lease.controller, lease.agentLock]);
  // Responsive: the tab is as large as the live view pane and follows it as
  // it resizes. Fitting is the server's device, not the lease — a passive
  // viewer reports its pane only while no other viewer drives.
  const mode = st?.device?.preset || '';
  const fitting = mode === 'responsive';
  const followsPane = fitting && !!live.viewer && (!lease.controller || driving) && live.conn === 'open';
  useEffect(() => {
    const el = view.current;
    if (!followsPane || !el) return;
    let timer = 0, last = 0, sent = '';
    const ro = new ResizeObserver(([entry]) => {
      const width = Math.max(MIN_SIDE, Math.min(MAX_SIDE, Math.floor(entry.contentRect.width) - 2));
      const height = Math.max(MIN_SIDE, Math.min(MAX_SIDE, Math.floor(entry.contentRect.height) - 2));
      const key = `${width}x${height}`;
      clearTimeout(timer);
      if (key === sent || !entry.contentRect.width || !entry.contentRect.height) return;
      timer = setTimeout(() => {
        last = Date.now();
        sent = key;
        live.send({ type: 'fit', width, height });
      }, Math.max(0, FIT_INTERVAL_MS - (Date.now() - last)));
    });
    ro.observe(el);
    return () => { clearTimeout(timer); ro.disconnect(); };
  }, [followsPane, lease.controller, b.open, b.panel]);
  if (!b.panel) return null;
  if (!b.open) return html`<${BrowserStart} room=${r} browser=${b} />`;

  const device = st?.device || { width: page.width, height: page.height };
  const point = (e) => {
    const rect = img.current.getBoundingClientRect();
    return { x: ((e.clientX - rect.left) / rect.width) * device.width, y: ((e.clientY - rect.top) / rect.height) * device.height };
  };
  // onView places a rect in page CSS px over the live view.
  const onView = (box) => {
    if (!img.current || !view.current || !box) return null;
    const ir = img.current.getBoundingClientRect();
    const vr = view.current.getBoundingClientRect();
    const k = ir.width / (device.width || ir.width);
    return { left: ir.left - vr.left + box.x * k, top: ir.top - vr.top + box.y * k, width: box.width * k, height: box.height * k };
  };
  const chooseDevice = (v) => live.send({ type: 'emulate', preset: v });
  const navigate = (to) => { setEditingUrl(false); typing.current?.focus({ preventScroll: true }); live.send({ type: 'navigate', url: to }); };
  const end = async () => {
    try { await api.closeBrowser(r.id); b.reload(); } catch (e) { toast(errorText(e), 'error'); }
  };
  const pointer = mark.on ? mark.pointer(point) : {
    onPointerDown: (e) => { if (!driving) return; e.preventDefault(); img.current.setPointerCapture(e.pointerId); typing.current?.focus({ preventScroll: true }); live.send({ type: 'down', ...point(e), button: BUTTON[e.button] || 'left', clickCount: e.detail || 1 }); },
    onPointerUp: (e) => { if (!driving) return; live.send({ type: 'up', ...point(e), button: BUTTON[e.button] || 'left', clickCount: e.detail || 1 }); },
    onPointerMove: (e) => {
      if (!driving || Date.now() - lastMove.current < 40) return;
      lastMove.current = Date.now();
      live.send({ type: 'move', ...point(e), buttons: e.buttons });
    },
    onWheel: (e) => { if (!driving) return; e.preventDefault(); live.send({ type: 'wheel', ...point(e), deltaX: e.deltaX, deltaY: e.deltaY }); },
    onContextMenu: (e) => { if (driving) e.preventDefault(); },
  };
  // Keys go to the page while driving: printable characters as text,
  // the rest (Enter, arrows, shortcuts) as key presses.
  const onKeyDown = (e) => {
    if (!driving || e.isComposing) return;
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) return; // arrives as input
    e.preventDefault();
    live.send({ type: 'key', key: e.key, modifiers: MOD(e) });
  };
  const onInput = (e) => { if (driving && e.target.value) live.send({ type: 'text', text: e.target.value }); e.target.value = ''; };
  const onPaste = (e) => { if (!driving) return; e.preventDefault(); const t = e.clipboardData.getData('text'); if (t) live.send({ type: 'text', text: t }); };

  return html`<div class="browser-split-view flex-1 min-h-0 flex flex-col">
    <div class="flex items-center gap-2 h-9 px-3 border-b border-line shrink-0">
      <div class="flex-1 min-w-0 truncate text-[12px] text-muted">${page.title || page.url || 'Browser'}</div>
      ${st?.profile && st.profile !== 'default' && html`<span class="text-[11px] text-faint border border-line rounded px-1.5 shrink-0" title="Browser profile">${st.profile}</span>`}
      <button type="button" aria-pressed=${consoleOpen} onClick=${() => setConsoleOpen(!consoleOpen)} title="The page's console: its logs and errors, and JavaScript you run in it"
        class=${cx('h-6 px-2 rounded-md text-[12px] inline-flex items-center gap-1 hover:bg-hover shrink-0', consoleOpen ? 'text-accent' : 'text-muted hover:text-fg')}>
        <${Icon} name="terminal" size=${12} /> Console</button>
      <button type="button" onClick=${end} class="h-6 px-2 rounded-md text-[12px] text-muted hover:text-bad hover:bg-hover shrink-0">End session</button>
    </div>
    <div class="browser-toolbar flex items-center gap-1 px-2 h-10 border-b border-line shrink-0">
      <${IconButton} icon="arrowLeft" size=${15} label="Back" disabled=${!driving} onClick=${() => live.send({ type: 'back' })} />
      <${IconButton} icon="arrowLeft" size=${15} label="Forward" class="rotate-180" disabled=${!driving} onClick=${() => live.send({ type: 'forward' })} />
      <form noValidate class="flex-1 min-w-0 flex" onSubmit=${(e) => { e.preventDefault(); navigate(url); }}>
        <${AddressInput} value=${url} readOnly=${!driving} aria-label="Address" profile=${st?.profile}
          onFocus=${() => driving && setEditingUrl(true)} onBlur=${() => setEditingUrl(false)} onInput=${setUrl}
          onGo=${(u) => { setUrl(u); navigate(u); }}
          class=${cx('w-full h-7 rounded-md bg-raised border border-line px-2 text-[12px] font-mono outline-none focus:border-accent', !driving && 'text-faint')} />
      </form>
      <div class="browser-device-tools flex items-center gap-1 shrink-0">
        ${driving && logins.length > 0 && html`<${Select} dense key=${'login' + fillKey} label="Fill a saved login" class="min-w-0! w-24" value=""
          onChange=${(id) => { if (id) { live.send({ type: 'autofill', id }); setFillKey((k) => k + 1); } }}
          options=${[{ value: '', text: 'Fill login…' }, ...logins.map((l) => ({ value: l.id, text: l.username || '(no username)' }))]} />`}
        <div title=${mode === 'responsive' ? 'The page is sized to the live view pane' : ''}>
          <${Select} dense label="Device" class="min-w-0! w-32" deferSelection disabled=${!driving || live.conn !== 'open' || !mode} value=${mode} onChange=${chooseDevice}
            options=${[...(mode === 'custom' ? [{ value: 'custom', text: 'Custom' }] : []), ...(mode === '' ? [{ value: '', text: 'Loading device…' }] : []), ...DEVICES.map((d) => ({ value: d.value, text: d.text }))]} />
        </div>
      </div>
    </div>
    <div class="flex items-center gap-2 px-3 py-1.5 border-b border-line text-[12px] shrink-0">
      <span class=${cx('size-1.5 rounded-full', live.conn === 'open' ? 'bg-ok' : 'bg-warn')}></span>
      <span class="flex-1 min-w-0 truncate text-muted">
        ${live.conn !== 'open' ? 'Connecting…' : mark.on ? (mark.picking ? 'Marking…' : 'Click an element or drag a region to annotate it. Esc to stop.') : lease.agentLock ? 'The agent locked input while it works.' : driving ? 'You are driving. Keys and clicks go to the page.' : lease.controller ? 'Someone else took over.' : 'Watching. Take over to click and type.'}
      </span>
      ${mark.on && html`<button type="button" class="h-6 px-2 rounded-md border border-line hover:bg-hover disabled:opacity-50" disabled=${mark.picking || !!mark.pending}
        onClick=${mark.page} title="Annotate the whole page">Whole page</button>`}
      <button type="button" aria-pressed=${mark.on} disabled=${live.conn !== 'open' || !!st?.recording}
        title=${st?.recording ? 'Stop recording to annotate' : 'Mark elements, regions or the page for your next message'}
        onClick=${() => mark.set(!mark.on)}
        class=${cx('h-6 px-2 rounded-md border inline-flex items-center gap-1 disabled:opacity-50', mark.on ? 'border-accent bg-accent/15 text-accent' : 'border-line hover:bg-hover')}>
        <${Icon} name="pin" size=${12} /> Annotate</button>
      ${mark.on ? null : driving
        ? html`<button type="button" class="h-6 px-2 rounded-md border border-line hover:bg-hover" onClick=${() => live.send({ type: 'release' })}>Release</button>`
        : html`<button type="button" class="h-6 px-2 rounded-md bg-accent text-accent-fg hover:opacity-90" onClick=${() => live.send({ type: 'takeover' })}>Take over</button>`}
    </div>
    <${RecordingBar} room=${r} rec=${st?.recording} send=${live.send} disabled=${mark.on} />
    ${st?.loginOffer && html`<div class="flex items-center gap-2 px-3 py-2 border-b border-line bg-accent/10 text-[12px] shrink-0" role="alert">
      <${Icon} name="key" size=${14} class="text-accent shrink-0" />
      <span class="flex-1 min-w-0">Save the login${st.loginOffer.username ? html` for <b>${st.loginOffer.username}</b>` : ''} at <span class="font-mono">${st.loginOffer.origin}</span> in the ${st.profile} profile?</span>
      <button type="button" class="h-6 px-2 rounded-md text-muted hover:bg-hover" onClick=${() => live.send({ type: 'login', id: st.loginOffer.id, save: false })}>Not now</button>
      <button type="button" class="h-6 px-2 rounded-md bg-accent text-accent-fg" onClick=${() => live.send({ type: 'login', id: st.loginOffer.id, save: true })}>Save</button>
    </div>`}
    <div ref=${view} class=${cx('flex-1 min-h-0 flex items-center justify-center bg-raised/40 relative overflow-hidden', fitting ? 'p-0' : 'p-3')}>
      ${live.frame
        ? html`<img ref=${img} src=${live.frame} alt=${'Live view of ' + (page.title || page.url || 'the browser')} draggable="false" ...${pointer}
            class=${cx('max-w-full max-h-full border bg-white select-none touch-none', !fitting && 'rounded-lg', mark.on ? 'border-accent cursor-crosshair' : driving ? 'border-accent cursor-default' : 'border-line')} />`
        : html`<span class="text-muted text-[13px]">Waiting for the first frame…</span>`}
      ${mark.on && html`<${MarkOverlay} mark=${mark} onView=${onView} room=${r} />`}
      ${live.frame && st?.agentPointer && html`<${AgentPointer} pointer=${st.agentPointer} onView=${onView} />`}
      <textarea ref=${typing} aria-label="Type into the page" autocomplete="off" autocapitalize="off" spellcheck="false"
        onKeyDown=${onKeyDown} onInput=${onInput} onPaste=${onPaste}
        class="absolute left-0 bottom-0 w-px h-px opacity-0 pointer-events-none"></textarea>
    </div>
    ${consoleOpen && html`<${ConsolePanel} room=${r} page=${page} onClose=${() => setConsoleOpen(false)} />`}
  </div>`;
}

// AgentPointer shows where the agent acts in the live view, as a cursor
// labeled "Agent". It appears with each action and fades when the agent
// has been still for a few seconds.
const POINTER_SHOWN_MS = 3500;
const ACTION_LABEL = { click: 'Agent clicks', hover: 'Agent points', scroll: 'Agent scrolls' };
function AgentPointer({ pointer: p, onView }) {
  const [shown, setShown] = useState(0);
  useEffect(() => {
    if (!p) return;
    setShown(p.seq);
    const t = setTimeout(() => setShown(0), POINTER_SHOWN_MS);
    return () => clearTimeout(t);
  }, [p?.seq]);
  const at = p && onView({ x: p.x, y: p.y, width: 0, height: 0 });
  if (!at) return null;
  return html`<div class="agent-pointer" style=${{ left: at.left + 'px', top: at.top + 'px', opacity: shown === p.seq ? 1 : 0 }} aria-hidden="true">
    ${p.action === 'click' && shown === p.seq && html`<span key=${p.seq} class="ring"></span>`}
    <svg width="20" height="22" viewBox="0 0 20 22" class="block drop-shadow" style="margin:-2px 0 0 -2px">
      <path d="M2 2 L2 18 L6.5 13.8 L9.6 20.5 L12.6 19.2 L9.6 12.6 L16 12.6 Z" fill="var(--accent)" stroke="white" stroke-width="1.6" stroke-linejoin="round" />
    </svg>
    <span class="absolute left-4 top-5 whitespace-nowrap rounded-md bg-accent text-accent-fg text-[11px] font-medium px-1.5 py-0.5 shadow-pop">${ACTION_LABEL[p.action] || 'Agent'}</span>
  </div>`;
}

// ConsolePanel is the page's console: what it logged and threw since it
// loaded, and a prompt that runs JavaScript in it. A new page starts a new
// log; what the owner ran stays until they clear it.
const LEVEL_CLASS = { error: 'text-bad bg-bad/5', warn: 'text-warn bg-warn/5', result: 'text-fg', input: 'text-muted' };
function ConsolePanel({ room: r, page, onClose }) {
  const [logged, setLogged] = useState([]);
  const [ran, setRan] = useState([]);
  const [since, setSince] = useState(0);
  const [error, setError] = useState('');
  const [script, setScript] = useState('');
  const [running, setRunning] = useState(false);
  const typed = useRef({ list: [], at: -1 });
  const list = useRef();
  const stick = useRef(true);
  useEffect(() => {
    let live = true;
    const read = () => api.browserConsole(r.id).then(
      (res) => { if (live) { setLogged(res.data || []); setError(''); } },
      (e) => { if (live) setError(errorText(e)); });
    read();
    const t = setInterval(read, 2000);
    return () => { live = false; clearInterval(t); };
  }, [r.id, page.url]);
  const lines = [...logged.filter((l) => l.ts > since), ...ran].sort((a, b) => a.ts - b.ts);
  useEffect(() => { if (stick.current && list.current) list.current.scrollTop = list.current.scrollHeight; }, [lines.length]);
  const run = async () => {
    const expr = script.trim();
    if (!expr || running) return;
    const h = typed.current;
    if (h.list.at(-1) !== expr) h.list.push(expr);
    h.at = -1;
    setScript('');
    setRunning(true);
    const at = Date.now();
    let line;
    try {
      const res = await api.browserEval(r.id, expr);
      line = { level: 'result', text: JSON.stringify(res.value, null, 2) ?? 'undefined' };
    } catch (e) {
      line = { level: 'error', text: errorText(e) };
    }
    // The answer comes after what the script logged while it ran.
    line.ts = Math.max(Date.now(), at + 1);
    stick.current = true;
    setRan((x) => [...x, { level: 'input', text: expr, ts: at }, line]);
    setRunning(false);
  };
  const onKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); run(); return; }
    const h = typed.current;
    // Up and Down walk what was run before, from a one-line prompt.
    if ((e.key === 'ArrowUp' || e.key === 'ArrowDown') && !script.includes('\n') && h.list.length) {
      e.preventDefault();
      h.at = e.key === 'ArrowUp' ? (h.at < 0 ? h.list.length - 1 : Math.max(0, h.at - 1)) : (h.at < 0 ? -1 : h.at + 1);
      if (h.at >= h.list.length) h.at = -1;
      setScript(h.at < 0 ? '' : h.list[h.at]);
    }
  };
  return html`<section aria-label="Console" class="browser-console shrink-0 h-[40%] min-h-36 max-h-80 flex flex-col border-t border-line bg-panel">
    <div class="flex items-center gap-2 h-8 px-3 border-b border-line shrink-0 text-[12px]">
      <${Icon} name="terminal" size=${13} class="text-muted" />
      <span class="font-medium">Console</span>
      <span class="text-faint truncate min-w-0 flex-1">${error || (lines.length ? '' : 'Nothing logged on this page yet')}</span>
      <button type="button" class="h-6 px-2 rounded-md text-muted hover:text-fg hover:bg-hover" title="Clear what is shown"
        onClick=${() => { setSince(Date.now()); setRan([]); }}>Clear</button>
      <${IconButton} icon="x" size=${14} label="Close the console" onClick=${onClose} />
    </div>
    <div ref=${list} role="log" aria-live="polite" class="flex-1 min-h-0 overflow-y-auto font-mono text-[12px] leading-[1.45]"
      onScroll=${(e) => { const el = e.currentTarget; stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 8; }}>
      ${lines.map((l, i) => html`<div key=${i} class=${cx('flex gap-2 px-3 py-0.5 border-b border-line/50', LEVEL_CLASS[l.level] || 'text-fg')}>
        <span class="w-3 shrink-0 text-faint select-none">${l.level === 'input' ? '›' : l.level === 'result' ? '‹' : ''}</span>
        <pre class="flex-1 min-w-0 whitespace-pre-wrap break-words m-0 font-[inherit]">${l.text}</pre>
      </div>`)}
    </div>
    <div class="flex items-start gap-2 px-3 py-1.5 border-t border-line shrink-0">
      <span class="text-accent font-mono text-[12px] leading-6 select-none">›</span>
      <textarea value=${script} rows=${Math.min(6, script.split('\n').length)} aria-label="Run JavaScript in the page" placeholder="Run JavaScript in the page — Enter runs, Shift+Enter adds a line"
        spellcheck="false" autocomplete="off" autocapitalize="off" onInput=${(e) => setScript(e.target.value)} onKeyDown=${onKeyDown}
        class="flex-1 min-w-0 resize-none bg-transparent outline-none font-mono text-[12px] leading-6 placeholder:text-faint"></textarea>
      ${running && html`<span class="text-[11px] text-muted leading-6">Running…</span>`}
    </div>
  </section>`;
}

// RecordingBar records the tab: start, pause, resume, stop, and the
// thread's earlier recordings with their summaries.
function RecordingBar({ room: r, rec, send, disabled }) {
  const [list, setList] = useState(null);
  const [openKey, setOpenKey] = useState(0); // a pick resets the list to its prompt
  const load = () => api.recordings(r.id).then((res) => setList(res.data || []), () => setList([]));
  useEffect(() => { if (list) load(); }, [rec?.status]);
  const done = (list || []).filter((x) => x.status === 'completed' || x.status === 'interrupted');
  return html`<div class="flex items-center gap-2 px-3 py-1.5 border-b border-line text-[12px] shrink-0">
    ${rec
      ? html`<span class=${cx('size-2 rounded-full', rec.status === 'recording' ? 'bg-bad animate-pulse' : 'bg-warn')}></span>
          <span class="flex-1 min-w-0 truncate">${rec.status === 'recording' ? 'Recording this tab' : 'Recording paused'}</span>
          ${rec.status === 'recording'
            ? html`<button type="button" class="h-6 px-2 rounded-md border border-line hover:bg-hover" onClick=${() => send({ type: 'pause_recording' })}>Pause</button>`
            : html`<button type="button" class="h-6 px-2 rounded-md border border-line hover:bg-hover" onClick=${() => send({ type: 'resume_recording' })}>Resume</button>`}
          <button type="button" class="h-6 px-2 rounded-md border border-line hover:bg-hover" onClick=${() => send({ type: 'stop_recording' })}>Stop</button>`
      : html`<button type="button" disabled=${disabled} title=${disabled ? 'Stop annotating to record' : ''} class="h-6 px-2 rounded-md border border-line hover:bg-hover inline-flex items-center gap-1.5 disabled:opacity-50" onClick=${() => send({ type: 'record' })}>
            <span class="size-2 rounded-full bg-bad"></span> Record</button>
          <span class="flex-1"></span>
          ${list === null
            ? html`<button type="button" class="h-6 px-2 rounded-md text-muted hover:text-fg hover:bg-hover" onClick=${load}>Recordings</button>`
            : done.length === 0 ? html`<span class="text-faint">No recordings yet</span>`
            : html`<${Select} dense key=${'rec' + openKey} label="Open a recording summary" class="w-40" value=""
                onChange=${(id) => { if (id) { openPreview(api.recordingSummaryURL(r.id, id), 'Recording summary.txt'); setOpenKey((k) => k + 1); } }}
                options=${[{ value: '', text: `Recordings (${done.length})…` },
                  ...done.map((x) => ({ value: x.id, text: `${new Date(x.createdAt).toLocaleString()} · ${x.counts?.events || 0} events${x.status === 'interrupted' ? ' · cut off' : ''}` }))]} />
                <${Select} dense key=${'attach' + openKey} label="Attach a recording to the next message" class="w-28" value=""
                onChange=${(id) => {
                  if (!id) return;
                  const x = done.find((d) => d.id === id);
                  r.recording = { id, status: x?.status || '', label: 'Recording ' + new Date(x?.createdAt || 0).toLocaleString() };
                  setOpenKey((k) => k + 1);
                  toast('Recording attached to your next message.', 'info');
                  emit();
                }}
                options=${[{ value: '', text: 'Attach…' },
                  ...done.map((x) => ({ value: x.id, text: new Date(x.createdAt).toLocaleString() }))]} />`}`}
  </div>`;
}

// useAnnotate is the live view's annotate mode: a hover outline of the
// element under the pointer, a click to mark it, a drag to mark a region,
// or the whole page; each mark then takes a comment before it joins the
// draft. Marks run one at a time; a hover never waits for one.
function useAnnotate(r, ready) {
  const [on, setOn] = useState(false);
  const [hover, setHover] = useState(null);
  const [drag, setDrag] = useState(null);
  const [pending, setPending] = useState(null);
  const [picking, setPicking] = useState(false);
  const seq = useRef(0);
  const lastProbe = useRef(0);
  const set = (v) => {
    setOn(v); setHover(null); setDrag(null); setPending(null);
    if (!v && r.id) api.clearAnnotate(r.id).catch(() => {});
  };
  useEffect(() => { if (!ready && on) set(false); }, [ready]);
  useEffect(() => {
    if (!on) return;
    const onKey = (e) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return;
      if (pending) return; // the comment box handles its own Escape
      e.preventDefault(); set(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [on, pending]);
  const pick = async (body) => {
    if (picking || pending) return;
    setPicking(true); setHover(null);
    try {
      const res = await api.annotatePage(r.id, { ...body, marker: nextMarker(r) });
      setPending({ pick: res });
    } catch (e) {
      if (e.status === 422) toast('No element there. Click directly on a page element.', 'info');
      else if (e.status === 409) toast(e.message || 'The browser is busy. Try again.', 'info');
      else toast(errorText(e), 'error');
    } finally { setPicking(false); }
  };
  const pointer = (point) => ({
    onPointerDown: (e) => {
      if (e.button !== 0 || picking || pending) return;
      e.preventDefault(); e.currentTarget.setPointerCapture(e.pointerId);
      const p = point(e);
      setDrag({ x0: p.x, y0: p.y, x1: p.x, y1: p.y, cx: e.clientX, cy: e.clientY, active: false });
    },
    onPointerMove: (e) => {
      if (drag) {
        const p = point(e);
        setDrag({ ...drag, x1: p.x, y1: p.y, active: drag.active || Math.hypot(e.clientX - drag.cx, e.clientY - drag.cy) > 5 });
        return;
      }
      if (picking || pending || Date.now() - lastProbe.current < 100) return;
      lastProbe.current = Date.now();
      const p = point(e);
      const n = ++seq.current;
      api.previewAnnotate(r.id, p.x, p.y).then((res) => {
        if (n !== seq.current || res.skipped) return;
        setHover(res.missed ? null : res);
      }, () => {});
    },
    onPointerUp: (e) => {
      if (!drag) return;
      const d = drag;
      setDrag(null);
      if (d.active) {
        const box = { x: Math.min(d.x0, d.x1), y: Math.min(d.y0, d.y1), width: Math.abs(d.x1 - d.x0), height: Math.abs(d.y1 - d.y0) };
        if (box.width >= 2 && box.height >= 2) pick({ kind: 'region', ...box });
        return;
      }
      const p = point(e);
      pick({ kind: 'element', x: p.x, y: p.y });
    },
    onPointerLeave: () => { if (!drag) { seq.current++; setHover(null); } },
    onContextMenu: (e) => e.preventDefault(),
  });
  const page = () => pick({ kind: 'page' });
  const commit = (comment, screenshot) => {
    if (!pending) return;
    annotate(r, pending.pick, comment.trim(), screenshot);
    setPending(null);
  };
  const discard = () => setPending(null);
  return { on, set, hover, drag, pending, picking, pointer, page, commit, discard };
}

// MarkOverlay draws annotate mode over the live view: the hover outline,
// the region being dragged, and the mark awaiting its comment.
function MarkOverlay({ mark, onView }) {
  const d = mark.pending?.pick?.descriptor;
  const box = mark.pending ? (d?.kind === 'page' ? null : d?.rect)
    : mark.drag?.active ? { x: Math.min(mark.drag.x0, mark.drag.x1), y: Math.min(mark.drag.y0, mark.drag.y1), width: Math.abs(mark.drag.x1 - mark.drag.x0), height: Math.abs(mark.drag.y1 - mark.drag.y0) }
    : mark.hover?.rect;
  const at = onView(box);
  const dashed = mark.drag?.active || d?.kind === 'region';
  return html`
    ${at && html`<div class=${cx('absolute pointer-events-none rounded-[3px] border-2 border-accent bg-accent/10', dashed && 'border-dashed')}
      style=${{ left: at.left + 'px', top: at.top + 'px', width: Math.max(2, at.width) + 'px', height: Math.max(2, at.height) + 'px' }}>
      ${!mark.pending && !mark.drag && mark.hover && html`<span class="absolute left-0 -top-5 max-w-64 truncate px-1.5 h-4.5 leading-[18px] rounded bg-accent text-accent-fg text-[11px] font-mono whitespace-nowrap">
        ${'<' + (mark.hover.tagName || '') + '>'}${mark.hover.name ? ' ' + mark.hover.name : ''}</span>`}
    </div>`}
    ${mark.picking && html`<div class="absolute inset-0 flex items-center justify-center pointer-events-none"><span class="px-2.5 py-1 rounded-md bg-panel border border-line text-[12px] shadow">Marking…</span></div>`}
    ${mark.pending && html`<${MarkComment} mark=${mark} anchor=${at} />`}`;
}

// MarkComment takes the owner's comment on a fresh mark. It sits below
// the marked box, above it when there is no room, and at the top of the
// view for a page mark.
function MarkComment({ mark, anchor }) {
  const [comment, setComment] = useState('');
  const [shot, setShot] = useState(true);
  const box = useRef();
  const p = mark.pending.pick;
  const d = p.descriptor || {};
  const [pos, setPos] = useState(null);
  useEffect(() => {
    const el = box.current;
    const host = el?.parentElement;
    if (!el || !host) return;
    const w = el.offsetWidth, h = el.offsetHeight, hw = host.clientWidth, hh = host.clientHeight;
    let left = anchor ? anchor.left + anchor.width / 2 - w / 2 : (hw - w) / 2;
    let top = anchor ? anchor.top + anchor.height + 10 : 16;
    if (anchor && top + h > hh - 8) top = anchor.top - h - 10;
    top = Math.min(Math.max(8, top), hh - h - 8);
    left = Math.min(Math.max(8, left), hw - w - 8);
    setPos({ left, top });
    el.querySelector('textarea')?.focus();
  }, []);
  const submit = () => mark.commit(comment, shot);
  return html`<div ref=${box} role="dialog" aria-label="Annotation comment"
    class="absolute z-10 w-66 rounded-xl border border-line bg-panel shadow-lg p-2.5 flex flex-col gap-2"
    style=${pos ? { left: pos.left + 'px', top: pos.top + 'px' } : { opacity: 0, left: 0, top: 0 }}>
    <div class="flex items-start gap-2 min-w-0">
      <${Marker} n=${p.marker} />
      <span class="min-w-0 flex flex-col leading-tight">
        <b class="text-[12px] truncate">${annotationLabel(d)}</b>
        <small class="text-[11px] text-faint truncate font-mono">${d.kind === 'element' ? d.selector : d.pageUrl}</small>
      </span>
    </div>
    <textarea rows="2" placeholder="Comment for the agent…" aria-label="Comment" value=${comment}
      onInput=${(e) => setComment(e.target.value)}
      onKeyDown=${(e) => {
        if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); submit(); }
        else if (e.key === 'Escape') { e.preventDefault(); mark.discard(); }
      }}
      class="w-full resize-none rounded-md bg-raised border border-line px-2 py-1.5 text-[13px] outline-none focus:border-accent"></textarea>
    <${Checkbox} checked=${shot} onChange=${setShot} class="text-[12px] text-muted">
      Include screenshot <span class="text-faint">(badge ${p.marker})</span>
    </${Checkbox}>
    <div class="flex justify-end gap-1.5">
      <button type="button" class="h-7 px-2.5 rounded-md text-[12px] text-muted hover:bg-hover" onClick=${mark.discard}>Cancel</button>
      <button type="button" class="h-7 px-2.5 rounded-md text-[12px] bg-accent text-accent-fg hover:opacity-90" onClick=${submit}>Add to message</button>
    </div>
  </div>`;
}
