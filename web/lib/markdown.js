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

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const DIAGRAM_LANGS = new Set(['mermaid', 'mmd']);

const marked = new Marked({ gfm: true, breaks: false });
marked.use({
  renderer: {
    code({ text, lang, raw }) {
      const language = (lang || '').trim().split(/\s+/)[0].toLowerCase();
      const closed = /\n[ \t]*(`{3,}|~{3,})[ \t]*\n*$/.test(raw ?? '');
      if (DIAGRAM_LANGS.has(language) && closed) {
        return `<div class="diagram" data-diagram><div class="canvas"></div>`
          + `<details><summary>Diagram source</summary><pre>${esc(text)}</pre></details></div>`;
      }
      const label = DIAGRAM_LANGS.has(language) && !closed ? `${language} · streaming` : (language || 'text');
      return `<div class="codeblock"><header><span>${esc(label)}</span>`
        + `<button type="button" class="copybtn" data-copy>Copy</button></header>`
        + `<pre><code data-lang="${esc(language)}">${esc(text)}</code></pre></div>`;
    },
    // Images are never fetched. Raw HTML passes through and is reduced to
    // the allowlist below by DOMPurify.
    image({ text }) { return `<span class="md-image">[image: ${esc(text)}]</span>`; },
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
      return `<sup class="md-fnref"><button type="button" data-fn="${esc(t.id)}" title="Footnote ${n}">${n}</button></sup>`;
    },
  }],
});

// Footnotes of the message being rendered: id -> { n, text }.
let footnotes = new Map();

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
  'class', 'data-copy', 'data-lang', 'data-diagram', 'data-fn', 'data-fn-def', 'data-file'];
// Only the renderer's own class names survive, so pasted HTML cannot reach
// the app's utility classes (e.g. a fixed overlay over the UI).
const OWN_CLASSES = new Set(['codeblock', 'copybtn', 'diagram', 'canvas', 'md-image', 'md-fnref', 'md-footnotes']);

// Only http(s), mailto, and same-origin paths survive. Bare #fragments are
// the app router's, so they are dropped too.
const SAFE_URI = /^(?:https?:|mailto:|\/(?!\/)|\.\.?\/)/i;
const FILE_LINK = /^(?:file:|\/(?!\/|api\/)|\.\.?\/|~\/)/i;
// A link to a local file (file: URL, absolute, ~/ or ./relative path)
// opens the file preview; its href serves the file for a middle click.
// Read before sanitizing: the URI filter drops file: hrefs.
DOMPurify.addHook('beforeSanitizeAttributes', (node) => {
  if (node.tagName !== 'A') return;
  node.removeAttribute('data-file');
  const href = node.getAttribute('href') || '';
  if (!FILE_LINK.test(href)) return;
  let path = href;
  try { path = decodeURI(href); } catch { /* keep as written */ }
  node.setAttribute('data-file', path);
});
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.hasAttribute?.('class')) {
    const keep = node.getAttribute('class').split(/\s+/).filter((c) => OWN_CLASSES.has(c));
    if (keep.length) node.setAttribute('class', keep.join(' ')); else node.removeAttribute('class');
  }
  if (node.tagName === 'INPUT' && node.getAttribute('type') !== 'checkbox') node.remove();
  if (node.tagName === 'A') {
    const href = node.getAttribute('href') || '';
    const file = node.getAttribute('data-file');
    if (file) node.setAttribute('href', '/api/files?path=' + encodeURIComponent(file));
    else if (!SAFE_URI.test(href)) node.removeAttribute('href');
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
  return wrap;
}

function decorate(wrap, final) {
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
    const source = box.querySelector('details pre').textContent;
    const canvas = box.querySelector('.canvas');
    if (!validateDiagramSource(source)) {
      canvas.textContent = 'Diagram uses unsupported features — showing source.';
      canvas.className = 'canvas text-xs text-muted';
      box.querySelector('details').open = true;
      continue;
    }
    try {
      box._pele = mountDiagram(canvas, source, {
        onError: () => { canvas.dataset.error = '1'; },
      });
    } catch {
      canvas.textContent = 'Diagram could not be rendered.';
      canvas.className = 'canvas text-xs text-muted';
      box.querySelector('details').open = true;
    }
  }
  wrap._final = final;
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
    decorate(next);
    if (have) { dispose(have); root.replaceChild(next, have); } else root.appendChild(next);
  }
  for (let i = blocks.length; i < kids.length; i++) { dispose(kids[i]); kids[i].remove(); }
}

// renderInto reconciles `root` against `source`. Blocks keep their DOM when
// their raw text is unchanged; the trailing block re-renders while streaming.
export function renderInto(root, source) {
  let tokens;
  const { src, notes } = extractFootnotes(cleanFences(source));
  footnotes = notes;
  // Blocks re-render when the footnote set changes, since references in
  // them resolve against it.
  const sig = [...notes].map(([id, v]) => id + '\u0001' + v.n + '\u0001' + v.text).join('\u0002');
  try { tokens = marked.lexer(src); } catch { tokens = [{ type: 'paragraph', raw: src, text: src, tokens: [{ type: 'text', raw: src, text: src }] }]; }
  const blocks = tokens.filter((t) => t.type !== 'space');
  if (notes.size) blocks.push({ type: 'footnotes', raw: '\u0000footnotes' });
  reconcile(root, blocks, sig, (t) => (t.type === 'footnotes' ? renderFootnotes(notes, tokens.links) : renderBlock(t, tokens.links)));
}

// renderUserInto is the owner-typed profile from lib/usermark.js: single
// newlines break, a typed "[^id]:" or ```mermaid fence stays literal,
// and reference selections ride the parse as sentinels so the same
// sanitized block cache can reconcile them before they become spans.
export function renderUserInto(root, source, refs) {
  let tokens;
  const src = cleanFences(withRefSentinels(source, refs));
  try { tokens = markedUser.lexer(src); } catch { tokens = [{ type: 'paragraph', raw: src, text: src, tokens: [{ type: 'text', raw: src, text: src }] }]; }
  const blocks = tokens.filter((t) => t.type !== 'space');
  reconcile(root, blocks, '', (t) => renderBlock(t, tokens.links, markedUser));
  applyRefHighlights(root, refs);
}

function renderFootnotes(notes, links) {
  let html = '<section class="md-footnotes"><hr><ol>';
  for (const [id, v] of notes) {
    const body = marked.lexer(v.text);
    body.links = links;
    html += `<li data-fn-def="${esc(id)}">${marked.parser(body)}</li>`;
  }
  html += '</ol></section>';
  const wrap = document.createElement('div');
  wrap.style.display = 'contents';
  wrap.innerHTML = DOMPurify.sanitize(html, PURIFY);
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
