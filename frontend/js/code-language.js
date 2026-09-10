// Single source of truth for code language classification used by the chat
// code renderer and the local-file text/code preview.
//
// Exposes:
//   - codeLanguageInfo(language) → { canonical, mode, hljs, label }
//   - codeModeFor(language)      → CodeMirror mode string (e.g. 'text/javascript')
//   - codeHljsClass(language)    → highlight.js class fragment (e.g. 'javascript')
//   - codeLanguageLabel(language)→ human display label (e.g. 'JavaScript')
//   - ensureCodeMirror()         → lazy, promise-cached CodeMirror loader
//                                   (script + CSS injection, same style as
//                                   highlight-render.js / mermaid-render.js)
//
// CodeMirror 5 is vendored under /vendor/codemirror/ (see
// frontend/vendor/codemirror/README.md). The vendored mode/javascript.min.js
// registers `text/typescript` (with a TypeScript flag) in addition to
// `text/javascript`, so TypeScript uses its own mode string while sharing
// the JavaScript highlighter.

// Canonical language → { mode: CodeMirror mode, hljs: highlight.js class, label }
const LANGUAGE_INFO = {
  javascript: { mode: 'text/javascript', hljs: 'javascript', label: 'JavaScript' },
  typescript: { mode: 'text/typescript', hljs: 'javascript', label: 'TypeScript' },
  css:        { mode: 'text/css',        hljs: 'css',        label: 'CSS' },
  html:       { mode: 'htmlmixed',       hljs: 'xml',        label: 'HTML' },
  markdown:   { mode: 'text/x-markdown', hljs: 'markdown',   label: 'Markdown' },
  shell:      { mode: 'text/x-sh',       hljs: 'bash',       label: 'Shell' },
  python:     { mode: 'text/x-python',   hljs: 'python',     label: 'Python' },
  go:         { mode: 'text/x-go',       hljs: 'go',         label: 'Go' },
  yaml:       { mode: 'text/x-yaml',      hljs: 'yaml',       label: 'YAML' },
  json:       { mode: 'application/json', hljs: 'json',       label: 'JSON' },
  sql:        { mode: 'text/x-sql',       hljs: 'sql',        label: 'SQL' },
  rust:       { mode: 'text/x-rust',      hljs: 'rust',       label: 'Rust' },
  xml:        { mode: 'xml',             hljs: 'xml',         label: 'XML' },
};

// Alias → canonical language. Extensions and common names map here.
const LANGUAGE_ALIASES = new Map([
  ['js', 'javascript'], ['jsx', 'javascript'], ['mjs', 'javascript'], ['cjs', 'javascript'],
  ['ts', 'typescript'], ['tsx', 'typescript'],
  ['sh', 'shell'], ['bash', 'shell'], ['zsh', 'shell'],
  ['yml', 'yaml'], ['yaml', 'yaml'],
  ['py', 'python'], ['golang', 'go'], ['go', 'go'],
  ['html', 'html'], ['htm', 'html'], ['svg', 'html'],
  ['json', 'json'], ['md', 'markdown'], ['markdown', 'markdown'], ['mdown', 'markdown'], ['mkdn', 'markdown'], ['mdx', 'markdown'],
  ['css', 'css'], ['sql', 'sql'], ['rs', 'rust'], ['xml', 'xml'],
]);

function normalizedLanguage(language) {
  const raw = String(language || '').trim().toLowerCase();
  return LANGUAGE_ALIASES.get(raw) || raw;
}

// codeLanguageInfo returns the full info record for a language hint.
// Returns { canonical, mode, hljs, label } or a plain-text fallback.
export function codeLanguageInfo(language) {
  const canonical = normalizedLanguage(language);
  const info = LANGUAGE_INFO[canonical];
  if (info) return { canonical, mode: info.mode, hljs: info.hljs, label: info.label };
  return { canonical: canonical || 'text', mode: 'text/plain', hljs: '', label: 'Plain text' };
}

export function codeModeFor(language) {
  return codeLanguageInfo(language).mode;
}

export function codeHljsClass(language) {
  return codeLanguageInfo(language).hljs;
}

export function codeLanguageLabel(language) {
  return codeLanguageInfo(language).label;
}

// ─── Lazy CodeMirror 5 loader ────────────────────────────────────────
//
// Mirrors the script-injection + promise-cache pattern in
// highlight-render.js (loadHljs) and mermaid-render.js (loadMermaid):
// a single cached promise injects the UMD script + base CSS + Dracula
// theme CSS and the mode scripts the preview needs. On failure the cache
// is cleared so a later call can retry; the caller falls back to a
// <pre><code> + highlightCode surface.

let cmPromise = null;

// Mode scripts required by the preview, in dependency order. htmlmixed
// depends on xml + css + javascript, so those load first.
const CODEMIRROR_MODE_SCRIPTS = [
  'mode/xml.min.js',
  'mode/javascript.min.js',
  'mode/css.min.js',
  'mode/htmlmixed.min.js',
  'mode/markdown.min.js',
  'mode/shell.min.js',
  'mode/python.min.js',
  'mode/go.min.js',
  'mode/yaml.min.js',
  'mode/sql.min.js',
];

function appendScript(src) {
  return new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.src = src;
    script.async = false; // preserve execution order for mode deps
    script.onload = () => resolve();
    script.onerror = () => reject(new Error(`CodeMirror asset failed to load: ${src}`));
    document.head.append(script);
  });
}

function appendStylesheet(href) {
  return new Promise((resolve, reject) => {
    const link = document.createElement('link');
    link.rel = 'stylesheet';
    link.href = href;
    link.onload = () => resolve();
    link.onerror = () => reject(new Error(`CodeMirror stylesheet failed to load: ${href}`));
    document.head.append(link);
  });
}

// ensureCodeMirror loads CodeMirror 5 (core + modes + Dracula theme) once
// and resolves with the global CodeMirror constructor. Rejects on any
// asset failure; the cache is cleared so a later call can retry.
export function ensureCodeMirror() {
  if (typeof window !== 'undefined' && typeof window.CodeMirror === 'function') {
    return Promise.resolve(window.CodeMirror);
  }
  if (cmPromise) return cmPromise;
  cmPromise = (async () => {
    // Core first — it defines window.CodeMirror.
    await appendScript('/vendor/codemirror/codemirror.min.js');
    if (typeof window !== 'undefined' && typeof window.CodeMirror !== 'function') {
      throw new Error('CodeMirror loaded but window.CodeMirror is undefined');
    }
    // CSS (base + Dracula theme) and mode scripts can load in parallel.
    await Promise.all([
      appendStylesheet('/vendor/codemirror/codemirror.min.css'),
      appendStylesheet('/vendor/codemirror/theme/dracula.min.css'),
      ...CODEMIRROR_MODE_SCRIPTS.map((m) => appendScript(`/vendor/codemirror/${m}`)),
    ]);
    return window.CodeMirror;
  })().catch((err) => {
    cmPromise = null; // allow a later retry
    throw err;
  });
  return cmPromise;
}
