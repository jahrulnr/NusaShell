// The user bubble's Markdown profile — the same sanitized renderer family
// as assistant prose (lib/markdown.js), tuned for typed text: single
// newlines become line breaks, footnote definitions and diagram mounting
// stay off so a typed "[^id]:" or ```mermaid fence shows exactly what was
// typed. Nothing here touches the DOM at import, so tests can drive the
// profile in Node.
//
// References ride through the parse as private-use sentinels wrapped
// around each label in a copy of the source — never the raw text, which
// stays the single source of truth for copy/edit/send. After sanitize,
// applyRefHighlights wraps the rendered span; where a label landed inside
// markup the sentinels strip out and the label stays as written.
import { Marked } from '/vendor/marked/marked.esm.js';
import { KIND_LABEL } from './references.js';
import { renderImage } from './mdlinks.js';
import { NONCE_ATTR } from './mdchrome.js';

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

// userMarked is the owner-typed profile for one render pass. The chrome its
// renderers write (the code block, its copy button, a local picture) carries
// that pass's nonce, as the assistant profile's does in lib/markdown.js.
// Without it, stripForeignChrome (lib/mdchrome.js) takes the class and data
// attributes off the renderer's own output, and the block loses its header,
// background, and copy button. An empty nonce writes no mark; markedUser, which
// only lexes and backs the string-level tests, uses that form.
export function userMarked(nonce = '') {
  const ns = nonce ? ` ${NONCE_ATTR}="${esc(nonce)}"` : '';
  const profile = new Marked({ gfm: true, breaks: true });
  profile.use({
    renderer: {
      code({ text, lang }) {
        const language = (lang || '').trim().split(/\s+/)[0].toLowerCase();
        return `<div class="codeblock"${ns}><header><span>${esc(language || 'text')}</span>`
          + `<button type="button" class="copybtn" data-copy${ns}>Copy</button></header>`
          + `<pre><code data-lang="${esc(language)}"${ns}>${esc(text)}</code></pre></div>`;
      },
      // A typed ![](path) shows a local file the same way assistant prose
      // does; a remote address stays a link and is never fetched.
      image: (token) => renderImage(token, nonce),
    },
  });
  return profile;
}

export const markedUser = userMarked();

// lexUser is the lexing step of the owner profile. A lexer that throws is the
// last resort: the whole text becomes one plain paragraph. That failure has to
// be visible, so the caller marks the message and the console names it.
export function lexUser(src, profile = markedUser) {
  try {
    return { tokens: profile.lexer(src), fallback: false };
  } catch (err) {
    console.warn('[markdown] an owner message fell back to plain text:', err?.message ?? err);
    return {
      tokens: [{ type: 'paragraph', raw: src, text: src, tokens: [{ type: 'text', raw: src, text: src }] }],
      fallback: true,
    };
  }
}

// Sentinels are private-use bytes nobody types and the parser treats as
// ordinary text. Reference N's label is wrapped by a start and an end
// marker; the pair always wraps the label in source order, and markup
// between them is the label's own content.
const markStart = (i) => '\uE000' + i + '\uE001';
const markEnd = (i) => '\uE000' + i + '\uE002';
// In a link target the parser percent-encodes the bytes — strip that
// shape too so a label inside a URL keeps the typed text.
const MARK = /\uE000\d+[\uE001\uE002]|%EE%80%80\d+%EE%80%8[12]/gi;

// stripRefSentinels removes every sentinel, raw or percent-encoded.
export const stripRefSentinels = (s) => String(s ?? '').replace(MARK, '');

// withRefSentinels returns a copy of source with each reference label
// bracketed by its sentinel pair. Overlapping or empty selections get no
// pair — they simply never highlight.
export function withRefSentinels(source, refs) {
  const src = String(source ?? '');
  if (!refs?.length) return src;
  let out = '';
  let at = 0;
  refs.forEach((r, i) => {
    if (r.start < at || r.start < 0 || r.end <= r.start || r.end > src.length) return;
    out += src.slice(at, r.start) + markStart(i) + src.slice(r.start, r.end) + markEnd(i);
    at = r.end;
  });
  return out + src.slice(at);
}

// applyRefHighlights turns each surviving sentinel pair into a .useref
// span. A pair split across an element edge (the label held real markup)
// falls back to stripping the sentinels — the label text stays where the
// user typed it. Stray sentinels in attributes (a label inside a link
// target) strip the same way, restoring the typed target.
export function applyRefHighlights(root, refs) {
  if (!refs?.length) return;
  const texts = () => {
    const out = [];
    const walk = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    for (let n = walk.nextNode(); n; n = walk.nextNode()) out.push(n);
    return out;
  };
  refs.forEach((r, i) => {
    const s = markStart(i);
    const e = markEnd(i);
    const sn = texts().find((n) => n.data.includes(s));
    const en = texts().find((n) => n.data.includes(e));
    if (!sn || !en) return;
    const range = document.createRange();
    range.setStart(sn, sn.data.indexOf(s) + s.length);
    range.setEnd(en, en.data.indexOf(e));
    if (range.collapsed) return;
    const span = document.createElement('span');
    span.className = 'useref';
    span.title = (KIND_LABEL[r.kind] || r.kind) + ': ' + r.id;
    try { range.surroundContents(span); } catch { /* crosses an element edge — strip below */ }
  });
  for (const n of texts()) n.data = stripRefSentinels(n.data);
  for (const el of root.querySelectorAll('*')) {
    for (const a of Array.from(el.attributes)) {
      const clean = stripRefSentinels(a.value);
      if (clean !== a.value) el.setAttribute(a.name, clean);
    }
  }
}
