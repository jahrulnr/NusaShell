// SelectionBar: a small card over text the owner selects inside one
// message of the transcript (a reply's prose or a user bubble, each marked
// data-quote). It shows the passage and takes a reply to it: Enter puts the
// passage and the reply on the composer's draft as a quote, the mic
// dictates the reply, and the side-chat button starts a new thread about
// the passage instead. Typing while the selection is up starts the reply.
import { html, useEffect, useLayoutEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { addQuote, startSideChat } from '../lib/chat.js';
import { canDictate, useDictation } from './composer.js';
import { Icon } from './icons.js';
import { Spinner } from './ui.js';

const GAP = 8;
const EDGE = 8;

export function SelectionBar({ room, scroller }) {
  const [hit, setHit] = useState(null); // { text, range }
  const [pos, setPos] = useState(null);
  const [reply, setReply] = useState('');
  const [, remeasure] = useState(0);
  const bar = useRef();
  const input = useRef();
  const shown = useRef(null);
  shown.current = hit;
  // engaged: the owner is answering in the card — the page's selection has
  // gone to the reply box, so it no longer says what is quoted.
  const engaged = useRef(false);
  const voice = useDictation((t) => {
    setReply((d) => (!d || /\s$/.test(d) ? d + t : d + ' ' + t));
    input.current?.focus();
  }, room.key);
  const busyVoice = voice.phase !== 'idle';
  engaged.current = !!hit && (engaged.current || busyVoice);

  useEffect(() => {
    const root = scroller.current;
    if (!root) return;
    let dragging = false;
    let frame = 0;
    // read answers the selection when it is worth a bar: not collapsed, both
    // ends in the same quotable message, not in a control, with text.
    const read = () => {
      const sel = getSelection();
      if (!sel || sel.isCollapsed || !sel.rangeCount) return null;
      const range = sel.getRangeAt(0);
      const el = (n) => (n.nodeType === Node.ELEMENT_NODE ? n : n.parentElement);
      const start = el(range.startContainer), end = el(range.endContainer);
      const host = start?.closest('[data-quote]');
      if (!host || host !== end?.closest('[data-quote]') || !root.contains(host)) return null;
      if (start.closest('button, header, input, textarea') || end.closest('button, header, input, textarea')) return null;
      const text = sel.toString().trim();
      if (!text) return null;
      if (![...range.getClientRects()].some((r) => r.width > 0 && r.height > 0)) return null;
      return { text, range: range.cloneRange() };
    };
    const update = () => {
      if (engaged.current) return;
      const next = dragging ? null : read();
      setHit((cur) => (cur && next && cur.text === next.text ? cur : next));
    };
    const onSelection = () => { cancelAnimationFrame(frame); frame = requestAnimationFrame(update); };
    // A press outside the bar starts a new selection or clears this one: the
    // bar waits for the release, so it never chases a drag.
    const down = (e) => {
      if (bar.current?.contains(e.target)) return;
      dragging = true;
      setHit(null);
    };
    const up = () => {
      if (!dragging) return;
      dragging = false;
      setTimeout(update, 0);
    };
    // Scrolling moves an answer being written along with its passage;
    // a bar nobody has engaged with just goes.
    const hide = () => (engaged.current ? remeasure((n) => n + 1) : setHit(null));
    const key = (e) => {
      if (!shown.current) return;
      if (e.key === 'Escape') {
        const inside = bar.current?.contains(document.activeElement);
        setHit(null);
        if (inside) { e.preventDefault(); e.stopPropagation(); }
        return;
      }
      const at = document.activeElement;
      if (bar.current?.contains(at) || at?.closest?.('input, textarea, select, [contenteditable]')) return;
      // Tab from the selection reaches the card first, so it works without
      // a pointer; a typed character starts the reply.
      if (e.key === 'Tab' && !e.shiftKey) {
        e.preventDefault();
        input.current?.focus();
      } else if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey && !e.isComposing) {
        engaged.current = true;
        input.current?.focus();
      }
    };
    document.addEventListener('selectionchange', onSelection);
    document.addEventListener('pointerdown', down, true);
    document.addEventListener('pointerup', up, true);
    document.addEventListener('pointercancel', up, true);
    document.addEventListener('keydown', key, true);
    root.addEventListener('scroll', hide, { passive: true });
    addEventListener('resize', hide);
    return () => {
      cancelAnimationFrame(frame);
      document.removeEventListener('selectionchange', onSelection);
      document.removeEventListener('pointerdown', down, true);
      document.removeEventListener('pointerup', up, true);
      document.removeEventListener('pointercancel', up, true);
      document.removeEventListener('keydown', key, true);
      root.removeEventListener('scroll', hide);
      removeEventListener('resize', hide);
    };
  }, [room.key]);
  useEffect(() => setHit(null), [room.key]);
  // A new passage starts a new reply; a closed card drops its recording,
  // and the highlight that stood in for the page's selection.
  useEffect(() => {
    setReply('');
    if (hit) return;
    engaged.current = false;
    voice.discard();
    CSS.highlights?.delete('quote-pick');
  }, [hit]);

  // Above the selection, below it when that would cross the top of the
  // transcript. A touch screen draws its own selection menu above, so
  // there the bar prefers below.
  useLayoutEffect(() => {
    const el = bar.current;
    if (!hit || !el) { setPos(null); return; }
    const rects = [...hit.range.getClientRects()].filter((r) => r.width > 0 && r.height > 0);
    if (!rects.length) { setHit(null); return; }
    const box = hit.range.getBoundingClientRect();
    const sel = { top: rects[0].top, bottom: rects[rects.length - 1].bottom, left: box.left, right: box.right };
    const w = el.offsetWidth, h = el.offsetHeight;
    const vh = window.visualViewport?.height || innerHeight;
    const area = scroller.current?.getBoundingClientRect() || { top: 0, left: 0, right: innerWidth };
    const ceiling = Math.max(area.top, 0) + 4;
    const above = sel.top - h - GAP, below = sel.bottom + GAP;
    const fitsAbove = above >= ceiling, fitsBelow = below + h <= vh - EDGE;
    const preferBelow = matchMedia('(pointer: coarse)').matches;
    let top = preferBelow ? (fitsBelow || !fitsAbove ? below : above) : (fitsAbove || !fitsBelow ? above : below);
    top = Math.min(Math.max(top, ceiling), vh - h - EDGE);
    // Within the transcript's column where it is wide enough, else the screen.
    const lo = Math.max(area.left, 0) + EDGE, hi = Math.min(area.right, innerWidth) - w - EDGE;
    const [minX, maxX] = hi >= lo ? [lo, hi] : [EDGE, innerWidth - w - EDGE];
    const left = Math.min(Math.max((sel.left + sel.right) / 2 - w / 2, minX), maxX);
    setPos((p) => (p && p.top === top && p.left === left ? p : { top, left }));
  });

  if (!hit) return null;
  const close = () => {
    setHit(null);
    getSelection()?.removeAllRanges();
  };
  const quote = () => {
    if (busyVoice) return;
    if (addQuote(room, hit.text, reply)) close();
  };
  const side = () => {
    if (startSideChat(room, hit.text, reply)) close();
  };
  // The reply box takes the page's selection; a highlight keeps the
  // passage marked while the owner writes.
  const engage = () => {
    engaged.current = true;
    if (window.Highlight && CSS.highlights) CSS.highlights.set('quote-pick', new Highlight(hit.range));
  };
  const icon = 'size-8 shrink-0 inline-flex items-center justify-center rounded-lg text-muted hover:text-fg hover:bg-hover outline-none focus-visible:ring-2 focus-visible:ring-accent/60';
  return html`<div ref=${bar} role="dialog" aria-label="Reply to the selected text"
    onMouseDown=${(e) => { if (!e.target.closest('textarea')) e.preventDefault(); }}
    style=${pos ? { top: pos.top + 'px', left: pos.left + 'px' } : { top: '0px', left: '0px', visibility: 'hidden' }}
    class="selection-bar fixed z-40 w-[min(26rem,calc(100vw-16px))] flex flex-col gap-1 p-1.5 rounded-xl border border-line bg-raised shadow-pop">
    <p class="mx-1.5 mt-1 pl-2.5 border-l-2 border-line text-[12.5px] leading-snug text-muted line-clamp-2 break-words">${hit.text}</p>
    <div class="flex items-end gap-0.5">
      <textarea ref=${input} rows=${1} value=${reply} placeholder="Reply to this…" aria-label="Your reply to the selected text"
        onFocus=${engage}
        onInput=${(e) => { setReply(e.target.value); e.target.style.height = 'auto'; e.target.style.height = Math.min(e.target.scrollHeight, 120) + 'px'; }}
        onKeyDown=${(e) => {
          if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.keyCode !== 229) { e.preventDefault(); quote(); }
        }}
        class="flex-1 min-w-0 resize-none bg-transparent px-2 py-1.5 text-[13.5px] leading-5 outline-none placeholder:text-faint max-h-[120px]"></textarea>
      ${canDictate() && (voice.phase === 'recording'
        ? html`<button type="button" onClick=${() => { engage(); voice.stop(); }} aria-label="Stop and transcribe" title="Stop and transcribe"
            class="size-8 shrink-0 inline-flex items-center justify-center rounded-lg bg-bad/15 text-bad hover:bg-bad/25"><span class="size-2.5 rounded-[3px] bg-bad animate-pulse"></span></button>`
        : html`<button type="button" onClick=${() => { engage(); voice.start(); }} disabled=${busyVoice}
            aria-label=${voice.phase === 'transcribing' ? 'Transcribing' : 'Dictate a reply'} title=${voice.phase === 'transcribing' ? 'Transcribing…' : 'Dictate a reply'} class=${icon}>
            ${busyVoice ? html`<${Spinner} size=${14} />` : html`<${Icon} name="mic" size=${16} />`}
          </button>`)}
      <button type="button" onClick=${side} aria-label="Start a side chat about this" title="Start a side chat about this" class=${icon}>
        <${Icon} name="sideChat" size=${16} />
      </button>
      <button type="button" onClick=${quote} disabled=${busyVoice} aria-label="Add to your message" title="Add to your message (Enter)"
        class=${icon + ' disabled:opacity-40'}>
        <${Icon} name="enter" size=${16} />
      </button>
    </div>
  </div>`;
}
