// The chrome the renderer adds to a message — copy buttons, diagram boxes,
// footnote references, media placeholders — is marked with a nonce drawn for
// each render pass. A message cannot guess it, so raw HTML that merely names
// the same attributes (<div data-diagram>, <button data-copy>) is told apart
// from what the renderer built and loses them: HTML from a model or the owner
// must not be able to fake the app's own controls, and a fake diagram box must
// not take the transcript down with it.
//
// The strip is one rule over two lists, kept here so the attributes the
// renderer writes and the attributes it strips cannot drift apart.

export const NONCE_ATTR = 'data-ns';

// Attributes that make an element behave like renderer chrome. A message may
// still write them — data-* is allowed so pasted HTML keeps its meaning — but
// they only count when the nonce matches.
export const CHROME_ATTRS = ['data-copy', 'data-lang', 'data-diagram', 'data-fn', 'data-fn-def', 'data-media', 'data-kind'];

// Class names that make an element look like chrome; only the renderer's own
// survive, matching what the sanitizer already does to utility classes.
export const CHROME_CLASSES = new Set(['codeblock', 'copybtn', 'diagram', 'canvas', 'md-image', 'md-media', 'md-fnref', 'md-footnotes']);

let seq = 0;

// newNonce answers a token no message can predict: random, and different for
// every render pass, so a block rendered earlier never looks like this one's.
export function newNonce() {
  seq += 1;
  const rand = globalThis.crypto?.randomUUID?.()
    ?? (Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2));
  return 'ns' + seq.toString(36) + rand.replace(/[^a-zA-Z0-9]/g, '').slice(0, 20);
}

const classes = (el) => (el.getAttribute('class') || '').split(/\s+/).filter(Boolean);

// stripForeignChrome removes, from one sanitized block, every chrome attribute
// and chrome class that this render did not write. Text and structure stay —
// a message that mentions <div data-diagram> still reads as written, it just
// is not a diagram.
export function stripForeignChrome(root, nonce) {
  if (!root?.querySelectorAll) return;
  for (const el of root.querySelectorAll('*')) {
    if (el.getAttribute(NONCE_ATTR) === nonce) continue;
    const had = CHROME_ATTRS.some((a) => el.hasAttribute(a));
    const cls = classes(el);
    const chrome = cls.some((c) => CHROME_CLASSES.has(c));
    if (el.hasAttribute(NONCE_ATTR)) el.removeAttribute(NONCE_ATTR);
    if (!had && !chrome) continue;
    for (const a of CHROME_ATTRS) if (el.hasAttribute(a)) el.removeAttribute(a);
    const keep = cls.filter((c) => !CHROME_CLASSES.has(c));
    if (keep.length) el.setAttribute('class', keep.join(' '));
    else el.removeAttribute('class');
  }
}

// diagramParts reads a diagram box only when it is the box the renderer
// builds: a canvas to mount into and a <pre> holding the source. Anything else
// — a message's own <div data-diagram>, a block rebuilt by a different
// renderer — answers null, and the caller skips it instead of throwing halfway
// through a reconcile.
export function diagramParts(box) {
  if (!box?.querySelector) return null;
  const canvas = box.querySelector('.canvas');
  const pre = box.querySelector('details pre');
  if (!canvas || !pre) return null;
  return { canvas, details: box.querySelector('details'), source: pre.textContent ?? '' };
}
