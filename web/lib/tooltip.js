// Tooltips drawn by the app. The browser's own come from an element's title
// attribute and look like the operating system's, so the first time the
// pointer rests on an element with one, its text moves to data-tip and shows
// in the app's own bubble; nothing else in the code needs to change. An icon
// with no other name keeps the text as its aria-label.
//
// A title is a label for a control, not a place to explain a feature: a long
// instruction turns into a panel over the transcript the reader was reading.
// Keep them to a few words and put explanations in the interface itself.
const DELAY = 450;   // rest before a label appears
const DWELL = 6000;  // and it leaves on its own; nothing sits over content
let bubble;
let timer;
let dwell;
let current;

function host(el) {
  // A modal dialog sits in the top layer: a bubble outside it would be hidden.
  return el.closest('dialog[open]') || document.body;
}

function hide() {
  clearTimeout(timer);
  clearTimeout(dwell);
  timer = 0;
  dwell = 0;
  current = null;
  bubble?.remove();
}

function show(el, text) {
  hide();
  current = el;
  bubble = document.createElement('div');
  bubble.setAttribute('role', 'tooltip');
  bubble.className = 'fixed z-[10002] pointer-events-none max-w-72 rounded-lg border border-line bg-raised px-2.5 py-1.5 text-xs text-fg shadow-pop break-words';
  bubble.style.left = '0';
  bubble.style.top = '0';
  bubble.textContent = text;
  host(el).appendChild(bubble);
  const r = el.getBoundingClientRect();
  const b = bubble.getBoundingClientRect();
  const below = r.bottom + 6 + b.height <= innerHeight - 4;
  const left = Math.min(Math.max(4, r.left + r.width / 2 - b.width / 2), innerWidth - b.width - 4);
  bubble.style.left = left + 'px';
  bubble.style.top = (below ? r.bottom + 6 : Math.max(4, r.top - 6 - b.height)) + 'px';
  dwell = setTimeout(hide, DWELL);
}

// tipFor takes over an element's title and answers its text.
function tipFor(el) {
  const title = el.getAttribute('title');
  if (title !== null) {
    el.removeAttribute('title');
    if (title.trim()) {
      el.dataset.tip = title;
      if (!el.hasAttribute('aria-label') && !el.textContent.trim()) el.setAttribute('aria-label', title);
    } else {
      delete el.dataset.tip;
    }
  }
  return el.dataset.tip || '';
}

export function installTooltips() {
  const target = (e) => e.target.closest?.('[title], [data-tip]');
  document.addEventListener('pointerover', (e) => {
    // Touch has no hover, and a pointer that is dragging (selecting text,
    // moving a divider) is working, not asking for a label.
    if (e.pointerType === 'touch' || e.buttons) return;
    const el = target(e);
    if (!el || el === current) return;
    hide();
    const text = tipFor(el);
    if (text) timer = setTimeout(() => show(el, text), DELAY);
  }, true);
  document.addEventListener('pointerout', (e) => {
    if (current || timer) {
      const el = target(e);
      if (el && !el.contains(e.relatedTarget)) hide();
    }
  }, true);
  // Keyboard focus shows it at once; any press, scroll, or key hides it.
  document.addEventListener('focusin', (e) => {
    const el = target(e);
    if (!el || !el.matches(':focus-visible')) return;
    const text = tipFor(el);
    if (text) show(el, text);
  }, true);
  document.addEventListener('focusout', hide, true);
  document.addEventListener('pointerdown', hide, true);
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') hide(); }, true);
  document.addEventListener('scroll', hide, true);
  // A bubble anchored to a rect that moved (a resize, another window taking
  // focus) is worse than none.
  window.addEventListener('resize', hide);
  window.addEventListener('blur', hide);
}
