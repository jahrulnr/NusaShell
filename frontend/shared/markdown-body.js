import { highlightCode } from '../js/highlight-render.js';
import { incrementalRender } from '../js/incremental-render.js';
import { renderMarkdown } from '../js/markdown.js';
import { renderMermaidDiagrams } from '../js/mermaid-render.js';
import { attachZoomButtons } from '../js/media-zoom.js';
import { attachCodeEditorButtons } from './code-surface.js?v=2';

const vendorScripts = {
  hljs: new URL('../vendor/highlightjs/highlight.min.js', import.meta.url).href,
  mermaid: new URL('../vendor/mermaid/mermaid.min.js', import.meta.url).href,
};
const pendingScripts = new Map();

function ensureScript(document, globalName) {
  if (document.defaultView?.[globalName]) return Promise.resolve();
  if (pendingScripts.has(globalName)) return pendingScripts.get(globalName);
  const pending = new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.src = vendorScripts[globalName];
    script.onload = resolve;
    script.onerror = () => {
      pendingScripts.delete(globalName);
      reject(new Error(`${globalName} failed to load`));
    };
    document.head.append(script);
  });
  pendingScripts.set(globalName, pending);
  return pending;
}

export function renderMarkdownBody(container, markdown) {
  container.innerHTML = renderMarkdown(markdown);
  attachZoomButtons(container);
  attachCodeEditorButtons(container);
  const tasks = [];
  if (container.querySelector('pre > code')) {
    tasks.push(ensureScript(container.ownerDocument, 'hljs').then(() => highlightCode(container)));
  }
  if (container.querySelector('.mermaid-block')) {
    tasks.push(ensureScript(container.ownerDocument, 'mermaid').then(() => renderMermaidDiagrams(container)));
  }
  return Promise.allSettled(tasks).then((results) => {
    attachZoomButtons(container);
    return results;
  });
}

// bindLocalFileLinks keeps document links inside a rendered Markdown body
// working through the app's own file-preview seam instead of letting them
// navigate the page. The listener delegates on the container, so it survives
// innerHTML/incremental re-renders and works inside open shadow roots.
//   - data-local-path anchors (absolute paths, line hints) emit the raw path.
//   - Relative file hrefs emit the raw href; the app-level local-file-open
//     handler resolves it against the conversation workspace.
// External links, other schemes, protocol-relative URLs, and bare #fragments
// keep their default behavior.
export function bindLocalFileLinks(container, host) {
  container.addEventListener('click', (event) => {
    const link = event.target.closest?.('a');
    if (!link || !container.contains(link)) return;
    const localPath = link.getAttribute('data-local-path');
    const href = link.getAttribute('href') || '';
    if (!localPath) {
      if (!href || href.startsWith('#')) return;
      if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(href) || href.startsWith('//')) return;
    }
    event.preventDefault();
    host.dispatchEvent(new CustomEvent('local-file-open', {
      bubbles: true,
      composed: true,
      detail: { path: localPath || href },
    }));
  });
}

export function updateMarkdownBody(container, markdown) {
  const changed = incrementalRender(container, markdown);
  attachZoomButtons(container);
  attachCodeEditorButtons(container);
  const tasks = [];
  if (changed.some((node) => node.matches?.('pre') || node.querySelector?.('pre > code'))) {
    tasks.push(ensureScript(container.ownerDocument, 'hljs').then(() => highlightCode(container)));
  }
  if (changed.some((node) => node.matches?.('.mermaid-block') || node.querySelector?.('.mermaid-block'))) {
    tasks.push(ensureScript(container.ownerDocument, 'mermaid')
      .then(() => renderMermaidDiagrams(container))
      .then(() => attachZoomButtons(container)));
  }
  return Promise.allSettled(tasks);
}
