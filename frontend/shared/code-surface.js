import { codeLanguageLabel, codeModeFor } from '../js/code-language.js';

// Production CodeMirror bundle (frontend/vendor/codemirror/ — minified core,
// modes, Dracula theme; see frontend/vendor/codemirror/README.md). Relative to
// the shell document base, same as index.html's other  refs.
// The min bundle ships no addons, so the styleActiveLine/matchBrackets options
// and the in-editor Ctrl-F search of the old unminified bundle are inert —
// same read-only preview semantics as production's media-zoom surface.
const vendorBase = 'vendor/codemirror';
const scripts = [
  'codemirror.min.js',
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

const pendingLoads = new WeakMap();
export { codeLanguageLabel, codeModeFor };

function loadScript(doc, source) {
  return new Promise((resolve, reject) => {
    const script = doc.createElement('script');
    script.src = `${vendorBase}/${source}`;
    script.async = false;
    script.onload = resolve;
    script.onerror = () => reject(new Error(`Could not load CodeMirror resource: ${source}`));
    doc.head.append(script);
  });
}

export function ensureCodeMirror(doc = document) {
  if (doc.defaultView?.CodeMirror) return Promise.resolve(doc.defaultView.CodeMirror);
  if (pendingLoads.has(doc)) return pendingLoads.get(doc);
  const task = scripts.reduce((chain, source) => chain.then(() => loadScript(doc, source)), Promise.resolve())
    .then(() => {
      const editor = doc.defaultView?.CodeMirror;
      if (!editor) throw new Error('CodeMirror did not register');
      return editor;
    })
    .finally(() => pendingLoads.delete(doc));
  pendingLoads.set(doc, task);
  return task;
}

function languageFromCode(code) {
  return [...code.classList].find((name) => name.startsWith('language-'))?.slice('language-'.length)
    || code.dataset.language || '';
}

export function attachCodeEditorButtons(container) {
  for (const code of container.querySelectorAll('pre > code')) {
    const pre = code.parentElement;
    if (pre.querySelector('.code-editor-trigger')) continue;
    const language = languageFromCode(code);
    const languageLabel = language ? codeLanguageLabel(language) : 'Code';
    const label = container.ownerDocument.createElement('span');
    label.className = 'code-language-label';
    label.textContent = languageLabel;
    label.setAttribute('aria-hidden', 'true');
    const button = container.ownerDocument.createElement('button');
    button.type = 'button';
    button.className = 'code-editor-trigger';
    button.textContent = 'Open';
    button.setAttribute('aria-label', `Open ${languageLabel} code in editor`);
    button.addEventListener('click', () => openCodeSurface({
      document: container.ownerDocument,
      source: code.textContent || '',
      language,
    }));
    pre.append(label, button);
  }
}

export function openCodeSurface({ document: doc = document, source = '', language = '', title = '' } = {}) {
  const focusReturn = doc.activeElement;
  const overlay = doc.createElement('div');
  overlay.className = 'code-surface-backdrop';
  overlay.setAttribute('role', 'presentation');
  const panel = doc.createElement('section');
  panel.className = 'code-surface';
  panel.setAttribute('role', 'dialog');
  panel.setAttribute('aria-modal', 'true');
  panel.setAttribute('aria-label', title || `${codeLanguageLabel(language)} code editor`);
  panel.innerHTML = `
    <header class="code-surface-head">
      <div><strong></strong><span></span></div>
      <div class="code-surface-actions"><button type="button" data-copy>Copy</button><button type="button" data-close aria-label="Close code editor">Close</button></div>
    </header>
    <div class="code-surface-editor" aria-busy="true"></div>`;
  panel.querySelector('strong').textContent = title || 'Code viewer';
  panel.querySelector('span').textContent = codeLanguageLabel(language);
  overlay.append(panel);
  doc.body.append(overlay);

  const close = () => {
    doc.removeEventListener('keydown', onKeydown);
    overlay.remove();
    focusReturn?.focus?.();
  };
  const onKeydown = (event) => { if (event.key === 'Escape') close(); };
  doc.addEventListener('keydown', onKeydown);
  panel.querySelector('[data-close]').addEventListener('click', close);
  overlay.addEventListener('click', (event) => { if (event.target === overlay) close(); });
  panel.querySelector('[data-copy]').addEventListener('click', async (event) => {
    await doc.defaultView?.navigator?.clipboard?.writeText(String(source));
    event.currentTarget.textContent = 'Copied';
  });

  const host = panel.querySelector('.code-surface-editor');
  void ensureCodeMirror(doc).then((CodeMirror) => {
    if (!overlay.isConnected) return;
    host.removeAttribute('aria-busy');
    const editor = CodeMirror(host, {
      value: String(source),
      mode: codeModeFor(language),
      theme: 'dracula',
      readOnly: true,
      lineNumbers: true,
      styleActiveLine: true,
      matchBrackets: true,
      lineWrapping: false,
      viewportMargin: 12,
    });
    editor.getWrapperElement().setAttribute('aria-label', `${codeLanguageLabel(language)} source`);
    editor.focus();
  }).catch((error) => {
    host.removeAttribute('aria-busy');
    host.textContent = `Code editor unavailable: ${error.message}`;
  });
  return { close, overlay };
}
