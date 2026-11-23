// A draggable divider for a dock panel. The handle lives inside the
// controlled aside, on the edge opposite the outer dock: a pointer drag or
// the arrow keys resizes the panel within the bounds its CSS declares
// (--pane-min/--pane-max/--pane-reserve), and the result is remembered.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { setPref } from '../lib/store.js';

export function PaneSplitter({ edge, label, panelRef, pref }) {
  const drag = useRef(null);
  const [range, setRange] = useState(null);
  const dir = edge === 'left' ? 1 : -1;
  const measure = () => {
    const panel = panelRef.current;
    if (!panel?.offsetWidth) return null;
    const css = getComputedStyle(panel);
    const min = parseFloat(css.getPropertyValue('--pane-min'));
    const cap = parseFloat(css.getPropertyValue('--pane-max')) || Infinity;
    const reserve = parseFloat(css.getPropertyValue('--pane-reserve'));
    if (!Number.isFinite(min) || !Number.isFinite(reserve)) return null;
    return { min, max: Math.max(min, Math.min(cap, panel.parentElement.clientWidth - reserve)), now: panel.offsetWidth };
  };
  useEffect(() => {
    const panel = panelRef.current;
    const observer = new ResizeObserver(() => {
      const next = measure();
      setRange((old) => next && old?.min === next.min && old?.max === next.max && old?.now === next.now ? old : next);
    });
    observer.observe(panel);
    observer.observe(panel.parentElement);
    return () => { observer.disconnect(); if (drag.current) cancelAnimationFrame(drag.current.frame); };
  }, []);
  const apply = (width, limits) => {
    const next = Math.round(Math.max(limits.min, Math.min(limits.max, width)));
    panelRef.current.style.setProperty('--pane-width', `${next}px`);
    return next;
  };
  const start = (e) => {
    if (e.button !== 0 || drag.current) return;
    const limits = measure();
    if (!limits || limits.min === limits.max) return;
    const handle = e.currentTarget;
    handle.focus({ preventScroll: true });
    handle.setPointerCapture(e.pointerId);
    drag.current = { x: e.clientX, id: e.pointerId, limits, next: limits.now, frame: 0 };
    handle.dataset.dragging = '';
    e.preventDefault();
  };
  const move = (e) => {
    const d = drag.current;
    if (!d || d.id !== e.pointerId) return;
    d.next = d.limits.now + (e.clientX - d.x) * dir;
    if (!d.frame) d.frame = requestAnimationFrame(() => { d.frame = 0; apply(d.next, d.limits); });
  };
  const finish = (e) => {
    const d = drag.current;
    if (!d || d.id !== e.pointerId) return;
    drag.current = null;
    delete e.currentTarget.dataset.dragging;
    cancelAnimationFrame(d.frame);
    const limits = measure();
    if (!limits) return;
    if (d.next === d.limits.now && limits.now === d.limits.now) return;
    const next = apply(d.next, limits);
    if (next !== d.limits.now) setPref(pref, next);
  };
  const keyboard = (e) => {
    if (drag.current || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) return;
    const limits = measure();
    if (!limits || limits.min === limits.max) return;
    e.preventDefault();
    const next = e.key === 'Home' ? limits.min : e.key === 'End' ? limits.max
      : limits.now + (e.key === 'ArrowRight' ? 1 : -1) * dir * (e.shiftKey ? 48 : 16);
    setPref(pref, apply(next, limits));
  };
  const canResize = !!range && range.min < range.max;
  return html`<div class=${`pane-splitter pane-splitter--${edge}`} role="separator" tabindex=${canResize ? 0 : -1} aria-disabled=${!canResize} aria-orientation="vertical"
    aria-label=${label} aria-controls=${edge === 'left' ? 'thread-sidebar' : 'thread-sidepane'}
    aria-valuemin=${range?.min} aria-valuemax=${range?.max} aria-valuenow=${range?.now}
    onPointerDown=${start} onPointerMove=${move} onPointerUp=${finish} onPointerCancel=${finish} onLostPointerCapture=${finish} onKeyDown=${keyboard}></div>`;
}
