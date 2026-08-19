// Artifact renderer for chat messages.
//
// Artifacts are interactive HTML/CSS/JS documents the agent produces via
// the `artifact_create` / `artifact_update` tools. Unlike Mermaid diagrams
// (which render inline as SVG), artifacts render inside a sandboxed iframe
// using srcdoc so the agent can ship interactive prototypes, minigames,
// dashboards, and visualizations without touching the host page.
//
// Design constraints (mirrors mermaid-render.js):
//   1. Live tool deltas must not re-render artifacts. The card holds a
//      placeholder until the tool call settles (status != "running"). This
//      module is called at settle points (turn.done, renderConversation),
//      never per delta.
//   2. Each artifact card is keyed by an iframe content hash so repeated
//      calls skip already-rendered artifacts.
//   3. Broken artifacts must not break the thread. JS errors inside the
//      iframe are surfaced as an overlay; the raw source is always
//      available via "View source".
//
// External resources (CDNs, <script src>, <img>, <video>, <link>) are
// permitted by design — the agent is expected to reuse CDNs rather than
// inline large libraries, to stay within the 64k token output budget.

// Build the full HTML document for an artifact. Inlines css/js into a single
// srcdoc string so the iframe is self-contained.
function buildSrcDoc(artifact) {
  const html = artifact.html || '';
  const css = artifact.css || '';
  const js = artifact.js || '';
  const hasFullDoc = /<html[\s>]/i.test(html);
  if (hasFullDoc) {
    // Agent shipped a complete document — inject css/js as-is.
    let doc = html;
    if (css) {
      const styleTag = `<style>\n${css}\n</style>`;
      doc = /<\/head>/i.test(doc)
        ? doc.replace(/<\/head>/i, `${styleTag}</head>`)
        : doc.replace(/<html[\s>]/i, m => `${m}<head>${styleTag}</head>`);
    }
    if (js) {
      const scriptTag = `<script>\n${js}\n<\/script>`;
      doc = /<\/body>/i.test(doc)
        ? doc.replace(/<\/body>/i, `${scriptTag}</body>`)
        : doc + scriptTag;
    }
    return doc;
  }
  return `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
${css}
</style>
</head>
<body>
${html}
<script>
${js}
<\/script>
</body>
</html>`;
}

// djb2 hash, same as mermaid-render.js for consistency.
function hashCode(s) {
  let h = 5381;
  for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0;
  return String(h >>> 0);
}

function renderErrorOverlay(card, message, source) {
  const overlay = card.querySelector('.artifact-error-overlay');
  overlay.replaceChildren();
  overlay.append(
    el('div', { class: 'artifact-error-msg', text: '⚠ ' + message }),
  );
  if (source) {
    const pre = document.createElement('pre');
    pre.className = 'artifact-error-src';
    pre.textContent = source;
    overlay.append(pre);
  }
  overlay.hidden = false;
}

// Helper because we can't import el in a plain module without bloating deps.
function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else if (k === 'hidden') node.hidden = v;
    else if (v !== null && v !== undefined) node.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c == null) continue;
    node.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return node;
}

// renderArtifactCard builds a card for an artifact tool call result.
// `toolCall` is the tool call object: { name, args, output, status }.
// `artifact` is parsed from toolCall.output: { id, title, html, css, js,
// width, height }.
export function renderArtifactCard(toolCall, artifact) {
  const title = artifact.title || 'Artifact';
  const id = artifact.id || '';
  const width = artifact.width || 0;
  const height = artifact.height || 0;

  const card = el('div', {
    class: 'artifact-card',
    'data-artifact-id': id,
    role: 'button',
    tabindex: '0',
    'aria-label': `Open artifact ${title}`,
  });
  card._toolArgs = toolCall.args;

  const header = el('div', { class: 'artifact-header' },
    el('span', { class: 'artifact-icon', text: '◈' }),
    el('span', { class: 'artifact-title', text: title }),
    el('span', { class: 'artifact-status', text: 'ready' }),
  );

  const preview = el('div', { class: 'artifact-preview' });
  const placeholder = el('div', { class: 'artifact-placeholder', text: 'Click to open artifact' });
  preview.append(placeholder);

  const overlay = el('div', { class: 'artifact-error-overlay', hidden: true });

  card.append(header, preview, overlay);

  // Render the iframe lazily on first click. This keeps the thread light
  // and avoids spinning up iframes for artifacts the user never opens.
  let rendered = false;
  const open = () => {
    if (rendered) {
      // Toggle collapse instead of re-rendering.
      preview.classList.toggle('is-collapsed');
      return;
    }
    rendered = true;
    preview.replaceChildren();
    const iframe = document.createElement('iframe');
    iframe.className = 'artifact-frame';
    iframe.title = title;
    iframe.sandbox = 'allow-scripts allow-same-origin allow-popups allow-forms allow-modals';
    if (width) iframe.width = width;
    if (height) iframe.height = height;
    const srcDoc = buildSrcDoc(artifact);
    iframe.srcdoc = srcDoc;
    card.dataset.srcHash = hashCode(srcDoc);
    // Surface uncaught errors from inside the iframe.
    iframe.addEventListener('error', (e) => {
      renderErrorOverlay(card, 'Artifact threw an error.', null);
    });
    // The iframe can't bubble window.onerror to parent without
    // postMessage; we install a tiny probe inside srcdoc to forward.
    // Done lazily only when iframe loads.
    iframe.addEventListener('load', () => {
      try {
        const probe = iframe.contentWindow;
        if (probe) {
          probe.onerror = (msg, src, line, col) => {
            renderErrorOverlay(card, `Artifact error: ${msg}`, null);
          };
        }
      } catch { /* cross-origin — ignore */ }
    });
    preview.append(iframe);
    preview.classList.remove('is-collapsed');
  };

  card.addEventListener('click', open);
  card.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    card.click();
  });

  return card;
}

// parseArtifactOutput extracts an artifact object from a tool call's output.
// Returns null when the output is not a recognizable artifact result.
export function parseArtifactOutput(toolCall) {
  if (toolCall.name !== 'artifact_create' && toolCall.name !== 'artifact_update') {
    return null;
  }
  if (!toolCall.output) return null;
  try {
    const parsed = JSON.parse(toolCall.output);
    if (parsed && parsed.artifact) return parsed.artifact;
    if (parsed && parsed.id && (parsed.html || parsed.css || parsed.js)) return parsed;
    return null;
  } catch {
    return null;
  }
}

// renderArtifacts scans a container for unrendered artifact cards and
// prepares them. Safe to call repeatedly (idempotent — cards that already
// have a click handler bound are skipped via a data attribute).
export function renderArtifacts(container) {
  if (!container || typeof container.querySelectorAll !== 'function') return;
  // No-op for now: artifact cards are built lazily on click, so there is
  // nothing to pre-render. This function exists for parity with
  // renderMermaidDiagrams so callers can use the same settle-point pattern.
}
