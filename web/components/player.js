// The player for video and audio in the preview overlay. The browser's own
// controls differ from one browser to the next and sit outside the app's look,
// so the media element plays with no controls and these draw the rest.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { Icon } from './icons.js';
import { cx } from './ui.js';

const clock = (s) => {
  s = Number.isFinite(s) ? Math.floor(s) : 0;
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return `${h ? h + ':' + String(m).padStart(2, '0') : m}:${String(s % 60).padStart(2, '0')}`;
};

// Slider is a bar the pointer drags and the arrow keys step.
function Slider({ label, value, max, step, onChange, text, class: cls = '' }) {
  const bar = useRef();
  const at = (e) => {
    const r = bar.current.getBoundingClientRect();
    return Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)) * max;
  };
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  const key = (e) => {
    const d = { ArrowLeft: -step, ArrowDown: -step, ArrowRight: step, ArrowUp: step, PageDown: -step * 4, PageUp: step * 4 }[e.key];
    if (d !== undefined) onChange(Math.min(max, Math.max(0, value + d)));
    else if (e.key === 'Home') onChange(0);
    else if (e.key === 'End') onChange(max);
    else return;
    e.preventDefault();
  };
  return html`<div ref=${bar} role="slider" tabIndex="0" aria-label=${label} aria-valuemin="0" aria-valuemax=${max} aria-valuenow=${value}
    aria-valuetext=${text} onKeyDown=${key}
    onPointerDown=${(e) => { bar.current.setPointerCapture(e.pointerId); onChange(at(e)); }}
    onPointerMove=${(e) => { if (bar.current.hasPointerCapture(e.pointerId)) onChange(at(e)); }}
    class=${cx('group relative h-5 flex items-center cursor-pointer touch-none outline-none', cls)}>
    <div class="relative w-full h-1 rounded-full bg-line group-focus-visible:ring-2 ring-accent">
      <div class="absolute inset-y-0 left-0 rounded-full bg-accent" style=${{ width: pct + '%' }}></div>
      <div class="absolute top-1/2 size-3 -translate-x-1/2 -translate-y-1/2 rounded-full bg-accent shadow" style=${{ left: pct + '%' }}></div>
    </div>
  </div>`;
}

function Btn({ icon, label, onClick }) {
  return html`<button type="button" onClick=${onClick} aria-label=${label} title=${label}
    class="size-8 shrink-0 inline-flex items-center justify-center rounded-lg text-muted hover:text-fg hover:bg-hover"><${Icon} name=${icon} size=${16} /></button>`;
}

export function Player({ kind, src }) {
  const box = useRef();
  const el = useRef();
  const [st, setSt] = useState({ playing: false, time: 0, length: 0, volume: 1, muted: false, failed: false });
  const [full, setFull] = useState(false);
  const video = kind === 'video';

  useEffect(() => {
    const m = el.current;
    const sync = () => setSt((o) => ({ ...o, playing: !m.paused, time: m.currentTime, length: m.duration, volume: m.volume, muted: m.muted }));
    const events = ['play', 'pause', 'ended', 'timeupdate', 'durationchange', 'loadedmetadata', 'volumechange'];
    events.forEach((n) => m.addEventListener(n, sync));
    sync(); // the element may have loaded before this ran
    const fail = () => setSt((o) => ({ ...o, failed: true }));
    m.addEventListener('error', fail);
    const fs = () => setFull(document.fullscreenElement === box.current);
    document.addEventListener('fullscreenchange', fs);
    return () => {
      events.forEach((n) => m.removeEventListener(n, sync));
      m.removeEventListener('error', fail);
      document.removeEventListener('fullscreenchange', fs);
    };
  }, [src]);

  const toggle = () => { const m = el.current; if (m.paused) m.play().catch(() => {}); else m.pause(); };
  const seek = (t) => { el.current.currentTime = t; };
  const volume = (v) => { el.current.volume = v; el.current.muted = v === 0; };
  const fullscreen = () => { if (document.fullscreenElement) document.exitFullscreen(); else box.current.requestFullscreen?.(); };
  const length = Number.isFinite(st.length) ? st.length : 0;

  const media = video
    ? html`<video ref=${el} src=${src} playsinline preload="metadata" onClick=${toggle}
        class=${cx('mx-auto bg-black cursor-pointer', full ? 'max-w-full max-h-full min-h-0 flex-1' : 'max-w-full max-h-[62dvh] rounded-t-lg')}></video>`
    : html`<audio ref=${el} src=${src} preload="metadata" class="hidden"></audio>`;
  return html`<div ref=${box} class=${cx('flex flex-col rounded-lg bg-panel', full && 'h-full bg-black')}>
    ${media}
    ${st.failed && html`<p class="px-3 py-2 text-[13px] text-warn">This file cannot be played here. Download it to open it elsewhere.</p>`}
    <div class=${cx('flex items-center gap-1.5 px-2 py-1.5 border border-line', video ? 'rounded-b-lg border-t-0' : 'rounded-lg', full && 'bg-panel')}>
      <${Btn} icon=${st.playing ? 'pause' : 'play'} label=${st.playing ? 'Pause' : 'Play'} onClick=${toggle} />
      <span class="w-11 text-right text-xs tabular-nums text-muted">${clock(st.time)}</span>
      <${Slider} label="Seek" value=${Math.min(st.time, length)} max=${length} step=${5} onChange=${seek} text=${clock(st.time) + ' of ' + clock(length)} class="flex-1 min-w-0" />
      <span class="w-11 text-xs tabular-nums text-muted">${clock(length)}</span>
      <${Btn} icon=${st.muted || st.volume === 0 ? 'volumeOff' : 'volume'} label=${st.muted ? 'Unmute' : 'Mute'}
        onClick=${() => { el.current.muted = !el.current.muted; }} />
      <${Slider} label="Volume" value=${st.muted ? 0 : st.volume} max=${1} step=${0.05} onChange=${volume}
        text=${Math.round((st.muted ? 0 : st.volume) * 100) + '%'} class="w-16 hidden sm:flex" />
      ${video && html`<${Btn} icon=${full ? 'shrink' : 'expand'} label=${full ? 'Exit full screen' : 'Full screen'} onClick=${fullscreen} />`}
    </div>
  </div>`;
}
