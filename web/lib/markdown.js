// Markdown → sanitized DOM. marked parses (GFM), DOMPurify sanitizes, and
// highlight.js colors finished code blocks. Rendering is block-incremental:
// the source is lexed into top-level blocks and only blocks whose raw text
// changed are re-rendered, so streaming deltas never rebuild the whole
// message, and finished diagrams/code keep their DOM (scroll, zoom).
import { Marked } from '/vendor/marked/marked.esm.js';
import DOMPurify from '/vendor/dompurify/purify.es.mjs';
import hljs from '/vendor/highlight/highlight.min.mjs';
import { mountDiagram, validateDiagramSource } from './diagram.js';
import { markedUser, withRefSentinels, applyRefHighlights } from './usermark.js';
import { renderImage, localTarget, baseName } from './mdlinks.js';
import { CHROME_ATTRS, CHROME_CLASSES, NONCE_ATTR, newNonce, stripForeignChrome, diagramParts } from './mdchrome.js';
import { fileURL } from './api.js';

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const DIAGRAM_LANGS = new Set(['mermaid', 'mmd']);

const marked = new Marked({ gfm: true, breaks: false });
marked.use({
  renderer: {
    code({ text, lang, raw }) {
      const language = (lang || '').trim().split(/\s+/)[0].toLowerCase();
      const closed = /\n[ \t]*(`{3,}|~{3,})[ \t]*\n*$/.test(raw ?? '');
      if (DIAGRAM_LANGS.has(language) && closed) {
        // Every element that carries a chrome class carries the nonce with
        // it: the strip takes the class off anything else, including our own
        // canvas if it were left unmarked (lib/mdchrome.js).
        return `<div class="diagram" data-diagram ${NONCE_ATTR}="${nonce}"><div class="canvas" ${NONCE_ATTR}="${nonce}"></div>`
          + `<details><summary>Diagram source</summary><pre>${esc(text)}</pre></details></div>`;
      }
      const label = DIAGRAM_LANGS.has(language) && !closed ? `${language} · streaming` : (language || 'text');
      return `<div class="codeblock" ${NONCE_ATTR}="${nonce}"><header><span>${esc(label)}</span>`
        + `<button type="button" class="copybtn" data-copy ${NONCE_ATTR}="${nonce}">Copy</button></header>`
        + `<pre><code data-lang="${esc(language)}" ${NONCE_ATTR}="${nonce}">${esc(text)}</code></pre></div>`;
    },
    // A local picture, video, or sound shows inline (lib/mdlinks.js); a
    // remote one is never fetched. Raw HTML passes through and is reduced
    // to the allowlist below by DOMPurify, so <img>/<video> never survive.
    image: (token) => renderImage(token, nonce),
  },
  extensions: [{
    // GitHub-style footnote reference: [^id]. Definitions are lifted out of
    // the source in renderInto; an id without a definition stays literal.
    name: 'fnref',
    level: 'inline',
    start(src) { const i = src.indexOf('[^'); return i < 0 ? undefined : i; },
    tokenizer(src) {
      const m = /^\[\^([^\]\s]+)\]/.exec(src);
      if (m && footnotes.has(m[1])) return { type: 'fnref', raw: m[0], id: m[1] };
      return undefined;
    },
    renderer(t) {
      const n = footnotes.get(t.id).n;
      return `<sup class="md-fnref" ${NONCE_ATTR}="${nonce}"><button type="button" data-fn="${esc(t.id)}" ${NONCE_ATTR}="${nonce}" title="Footnote ${n}">${n}</button></sup>`;
    },
  }],
});

// Footnotes of the message being rendered: id -> { n, text }.
let footnotes = new Map();

// The mark this render pass puts on the chrome it builds (lib/mdchrome.js).
// Set with the footnotes and the file href, before any block is rendered.
let nonce = newNonce();

// Lifts "[^id]: text" definitions (with indented continuation lines) out of
// the source and numbers them by first reference.
function extractFootnotes(src) {
  const defs = new Map();
  const lines = src.split('\n');
  const kept = [];
  const prose = []; // lines outside code fences, for reference numbering
  let fence = null;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const f = /^ {0,3}(`{3,}|~{3,})/.exec(line);
    if (fence) {
      if (f && f[1][0] === fence[0] && f[1].length >= fence.length && line.trim() === f[1]) fence = null;
      kept.push(line);
      continue;
    }
    if (f) { fence = f[1]; kept.push(line); continue; }
    const m = /^ {0,3}\[\^([^\]\s]+)\]:[ \t]?(.*)$/.exec(line);
    if (!m) { kept.push(line); prose.push(line); continue; }
    const body = [m[2]];
    while (i + 1 < lines.length && (/^(?: {2,}|\t)/.test(lines[i + 1]) || (lines[i + 1].trim() === '' && /^(?: {2,}|\t)/.test(lines[i + 2] ?? '')))) {
      body.push(lines[++i].replace(/^(?: {1,4}|\t)/, ''));
    }
    if (!defs.has(m[1])) defs.set(m[1], body.join('\n'));
  }
  const notes = new Map();
  let n = 0;
  for (const r of prose.join('\n').matchAll(/\[\^([^\]\s]+)\]/g)) {
    if (defs.has(r[1]) && !notes.has(r[1])) notes.set(r[1], { n: ++n, text: defs.get(r[1]) });
  }
  return { src: kept.join('\n'), notes };
}

// Tags Markdown produces, plus the inline/structural HTML GitHub allows.
const TAGS = [
  'a', 'abbr', 'b', 'blockquote', 'br', 'button', 'code', 'dd', 'del', 'details', 'div', 'dl', 'dt', 'em',
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'header', 'hr', 'i', 'input', 'ins', 'kbd', 'li', 'mark', 'ol', 'p',
  'pre', 's', 'samp', 'section', 'small', 'span', 'strike', 'strong', 'sub', 'summary', 'sup', 'table',
  'tbody', 'td', 'tfoot', 'th', 'thead', 'tr', 'u', 'ul', 'var',
];
const ATTRS = ['href', 'title', 'type', 'checked', 'disabled', 'align', 'start', 'open', 'colspan', 'rowspan', 'target', 'rel',
  'class', 'data-file', NONCE_ATTR, ...CHROME_ATTRS];
// Only the renderer's own class names survive, so pasted HTML cannot reach
// the app's utility classes (e.g. a fixed overlay over the UI).
const OWN_CLASSES = CHROME_CLASSES;

// Only http(s), mailto, and same-origin paths (the app's /#/ thread links)
// survive as they are. Bare #fragments are the app router's, so they are
// dropped too.
const SAFE_URI = /^(?:https?:|mailto:|\/(?!\/))/i;

// fileHref serves a local file for the message being rendered: through its
// thread, so a relative path resolves in the thread's workspace. Set per
// render, like the footnotes.
const globalFileHref = (path) => '/api/files?path=' + encodeURIComponent(path);
let fileHref = globalFileHref;

// A link to a local file (lib/mdlinks.js localTarget) opens the file
// preview; its href serves the file for a middle click. The path is read
// before sanitizing — the URI filter drops file: and scheme-shaped hrefs —
// and parked on the node here: DOMPurify validates every attribute value
// against its URI allowlist, so a data-file holding "notes.txt:3" would be
// removed however it is declared. The after hook writes it back once the
// attribute pass is over.
const localFile = new WeakMap();
DOMPurify.addHook('beforeSanitizeAttributes', (node) => {
  if (node.tagName !== 'A') return;
  const path = localTarget(node.getAttribute('href'));
  if (path) localFile.set(node, path); else localFile.delete(node);
});
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.hasAttribute?.('class')) {
    const keep = node.getAttribute('class').split(/\s+/).filter((c) => OWN_CLASSES.has(c));
    if (keep.length) node.setAttribute('class', keep.join(' ')); else node.removeAttribute('class');
  }
  if (node.tagName === 'INPUT' && node.getAttribute('type') !== 'checkbox') node.remove();
  if (node.tagName === 'A') {
    const href = node.getAttribute('href') || '';
    const path = localFile.get(node);
    if (path) {
      node.setAttribute('data-file', path);
      node.setAttribute('href', fileHref(path));
    } else {
      node.removeAttribute('data-file');
      if (!SAFE_URI.test(href)) node.removeAttribute('href');
    }
    node.setAttribute('target', '_blank');
    node.setAttribute('rel', 'noopener noreferrer');
  }
});
const PURIFY = { ALLOWED_TAGS: TAGS, ALLOWED_ATTR: ATTRS, ALLOW_DATA_ATTR: false };

function renderBlock(token, links, inst = marked) {
  const list = [token];
  list.links = links;
  const html = inst.parser(list);
  const wrap = document.createElement('div');
  wrap.className = 'contents';
  wrap.style.display = 'contents';
  wrap.innerHTML = DOMPurify.sanitize(html, PURIFY);
  stripForeignChrome(wrap, nonce);
  return wrap;
}

const svg = (d) => `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="${d}"/></svg>`;
const PLAY = svg('M7 5l12 7-12 7z');
const SOUND = svg('M4 10v4h4l5 4V6l-5 4zM16 9a4 4 0 0 1 0 6M18.5 6.5a8 8 0 0 1 0 11');
const ALERT = svg('M12 3l10 18H2zM12 10v5M12 18h.01');

// embedMedia builds what a ![caption](path) placeholder shows: a picture,
// a video's first frame with a play mark, or a sound chip, each a button
// the transcript opens in the preview overlay (data-media-url). Built
// after sanitizing, from the path alone, so nothing the message wrote
// reaches the element's attributes but the path.
function embedMedia(path, kind, caption) {
  const url = fileHref(path);
  const name = baseName(path);
  const box = document.createElement('span');
  box.className = 'md-embed';
  box.dataset.embed = url;
  box.setAttribute(NONCE_ATTR, nonce);
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'md-embed-' + kind;
  btn.dataset.mediaUrl = url;
  btn.dataset.mediaName = name;
  btn.title = caption ? caption + ' — ' + name : name;
  const broken = () => {
    btn.className = 'md-embed-audio md-embed-broken';
    btn.title = name + ' — the file could not be loaded';
    btn.innerHTML = ALERT;
    const label = document.createElement('span');
    label.textContent = name;
    btn.append(label);
  };
  if (kind === 'image') {
    const img = document.createElement('img');
    img.src = url;
    img.alt = caption || name;
    img.loading = 'lazy';
    img.addEventListener('error', broken, { once: true });
    btn.append(img);
  } else if (kind === 'video') {
    const video = document.createElement('video');
    video.src = url + '#t=0.1';
    video.muted = true;
    video.playsInline = true;
    video.preload = 'metadata';
    video.addEventListener('error', broken, { once: true });
    const mark = document.createElement('span');
    mark.className = 'md-embed-play';
    mark.innerHTML = PLAY;
    btn.append(video, mark);
  } else {
    btn.innerHTML = SOUND;
    const label = document.createElement('span');
    label.textContent = caption || name;
    btn.append(label);
  }
  box.append(btn);
  if (caption && caption !== name) {
    const cap = document.createElement('span');
    cap.className = 'md-embed-caption';
    cap.textContent = caption;
    box.append(cap);
  }
  return box;
}

// decorate finishes a sanitized block. reuse holds the embeds of the block
// it replaces, by URL: a streaming paragraph re-renders on every delta,
// and its pictures and videos keep their element instead of reloading.
function decorate(wrap, reuse) {
  for (const ph of wrap.querySelectorAll('.md-media[data-media]')) {
    if (ph.getAttribute(NONCE_ATTR) !== nonce) { ph.replaceWith(ph.textContent); continue; }
    const kind = ph.dataset.kind;
    if (!['image', 'video', 'audio'].includes(kind)) { ph.replaceWith(ph.textContent); continue; }
    const url = fileHref(ph.dataset.media);
    const old = reuse?.get(url);
    if (old) { reuse.delete(url); ph.replaceWith(old); continue; }
    ph.replaceWith(embedMedia(ph.dataset.media, kind, ph.getAttribute('title') || ''));
  }
  // A table gets its own sideways scroller, so a wide one scrolls instead
  // of squeezing its columns to the message's width. A cell that holds
  // nothing but one code span (a path, an identifier) is marked to read
  // whole rather than wrap.
  for (const table of wrap.querySelectorAll('table')) {
    const box = document.createElement('div');
    box.className = 'md-table';
    table.replaceWith(box);
    box.append(table);
    for (const cell of table.querySelectorAll('td')) {
      const code = cell.children.length === 1 && cell.firstElementChild.tagName === 'CODE' ? cell.firstElementChild : null;
      if (code && cell.textContent.trim() === code.textContent.trim()) cell.classList.add('md-code-cell');
    }
  }
  for (const code of wrap.querySelectorAll('.codeblock code')) {
    const lang = code.dataset.lang;
    if (lang && hljs.getLanguage(lang)) {
      try { code.innerHTML = hljs.highlight(code.textContent, { language: lang, ignoreIllegals: true }).value; } catch { /* plain */ }
    }
  }
  for (const box of wrap.querySelectorAll('[data-diagram]')) {
    // Only a box this renderer built has the shape below; anything else is
    // someone else's markup wearing the attribute, and is left alone.
    const parts = diagramParts(box);
    if (!parts) continue;
    const { canvas, source } = parts;
    if (!validateDiagramSource(source)) {
      canvas.textContent = 'Diagram uses unsupported features — showing source.';
      canvas.className = 'canvas text-xs text-muted';
      if (parts.details) parts.details.open = true;
      continue;
    }
    try {
      box._pele = mountDiagram(canvas, source, {
        onError: () => { canvas.dataset.error = '1'; },
      });
    } catch {
      canvas.textContent = 'Diagram could not be rendered.';
      canvas.className = 'canvas text-xs text-muted';
      if (parts.details) parts.details.open = true;
    }
  }
}

function dispose(node) {
  for (const box of node.querySelectorAll?.('[data-diagram]') ?? []) {
    try { box._pele?.destroy(); } catch { /* already gone */ }
  }
}

// marked rejects a closing fence followed by a tab ("```\t"), which
// CommonMark allows; trim trailing blanks on bare fence lines first.
const cleanFences = (s) => String(s ?? '').replace(/^( {0,3}(?:`{3,}|~{3,}))[ \t]+$/gm, '$1');

// reconcile reconciles `root` against the block list. Blocks keep their
// DOM when their raw text is unchanged; the trailing block re-renders
// while streaming.
function reconcile(root, blocks, sig, renderFn) {
  const kids = [...root.children];
  for (let i = 0; i < blocks.length; i++) {
    const t = blocks[i];
    const have = kids[i];
    if (have && have._raw === t.raw && have._sig === sig) continue;
    const next = renderFn(t);
    next._raw = t.raw;
    next._sig = sig;
    const reuse = new Map();
    for (const e of have?.querySelectorAll?.('[data-embed]') ?? []) reuse.set(e.dataset.embed, e);
    decorate(next, reuse);
    if (have) { dispose(have); root.replaceChild(next, have); } else root.appendChild(next);
  }
  for (let i = blocks.length; i < kids.length; i++) { dispose(kids[i]); kids[i].remove(); }
}

// fileHrefFor serves local files through a thread when there is one.
const fileHrefFor = (conversationId) => (conversationId ? (path) => fileURL(conversationId, path) : globalFileHref);

// renderInto reconciles `root` against `source`. Blocks keep their DOM when
// their raw text is unchanged; the trailing block re-renders while streaming.
// conversationId names the thread whose workspace local paths resolve in.
export function renderInto(root, source, { conversationId = '' } = {}) {
  let tokens;
  nonce = newNonce();
  fileHref = fileHrefFor(conversationId);
  const { src, notes } = extractFootnotes(cleanFences(source));
  footnotes = notes;
  // Blocks re-render when the footnote set changes, since references in
  // them resolve against it.
  const sig = conversationId + '\u0003' + [...notes].map(([id, v]) => id + '\u0001' + v.n + '\u0001' + v.text).join('\u0002');
  try { tokens = marked.lexer(src); } catch { tokens = [{ type: 'paragraph', raw: src, text: src, tokens: [{ type: 'text', raw: src, text: src }] }]; }
  const blocks = tokens.filter((t) => t.type !== 'space');
  if (notes.size) blocks.push({ type: 'footnotes', raw: '\u0000footnotes' });
  reconcile(root, blocks, sig, (t) => (t.type === 'footnotes' ? renderFootnotes(notes, tokens.links) : renderBlock(t, tokens.links)));
}

// renderUserInto is the owner-typed profile from lib/usermark.js: single
// newlines break, a typed "[^id]:" or ```mermaid fence stays literal,
// and reference selections ride the parse as sentinels so the same
// sanitized block cache can reconcile them before they become spans.
export function renderUserInto(root, source, refs, { conversationId = '' } = {}) {
  let tokens;
  nonce = newNonce();
  fileHref = fileHrefFor(conversationId);
  const src = cleanFences(withRefSentinels(source, refs));
  try { tokens = markedUser.lexer(src); } catch { tokens = [{ type: 'paragraph', raw: src, text: src, tokens: [{ type: 'text', raw: src, text: src }] }]; }
  const blocks = tokens.filter((t) => t.type !== 'space');
  reconcile(root, blocks, conversationId, (t) => renderBlock(t, tokens.links, markedUser));
  applyRefHighlights(root, refs);
}

function renderFootnotes(notes, links) {
  let html = `<section class="md-footnotes" ${NONCE_ATTR}="${nonce}"><hr><ol>`;
  for (const [id, v] of notes) {
    const body = marked.lexer(v.text);
    body.links = links;
    html += `<li data-fn-def="${esc(id)}" ${NONCE_ATTR}="${nonce}">${marked.parser(body)}</li>`;
  }
  html += '</ol></section>';
  const wrap = document.createElement('div');
  wrap.style.display = 'contents';
  wrap.innerHTML = DOMPurify.sanitize(html, PURIFY);
  stripForeignChrome(wrap, nonce);
  return wrap;
}

export function disposeTree(root) { dispose(root); }

// Copy buttons are delegated so rendered HTML carries no handlers.
export function handleCopyClick(ev) {
  const fn = ev.target.closest?.('[data-fn]');
  if (fn) {
    const def = [...(fn.closest('.md')?.querySelectorAll('[data-fn-def]') ?? [])].find((x) => x.dataset.fnDef === fn.dataset.fn);
    def?.scrollIntoView({ behavior: 'smooth', block: 'center' });
    return;
  }
  const btn = ev.target.closest?.('[data-copy]');
  if (!btn) return;
  const code = btn.closest('.codeblock')?.querySelector('code');
  if (!code) return;
  navigator.clipboard?.writeText(code.textContent).then(() => {
    btn.textContent = 'Copied';
    setTimeout(() => { btn.textContent = 'Copy'; }, 1200);
  }, () => { btn.textContent = 'Copy failed'; });
}
