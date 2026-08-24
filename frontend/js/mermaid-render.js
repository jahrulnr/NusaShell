import { attachMermaidZoomButton } from './media-zoom.js';

// Mermaid diagram renderer for chat messages.
//
// Design constraints (from the chat streaming model):
//   1. Live deltas must not re-render diagrams. renderMarkdown emits a cheap
//      `.mermaid-block` placeholder (raw source only); this module renders it to
//      SVG only when called at a settle point (renderThread / turn.done), never
//      per delta. Each block is keyed by a content hash so repeated calls skip
//      already-rendered diagrams.
//   2. Invalid Mermaid must not break the message. We validate with
//      mermaid.parse({suppressErrors}) before rendering and fall back to the raw
//      source + a note on any failure, catching errors so a bad diagram from the
//      model never throws or injects Mermaid's error bomb into the thread.
//
// Mermaid is lazy-loaded (a ~3MB UMD bundle) the first time a diagram appears.

let mermaidPromise = null;

function loadMermaid() {
  if (typeof window !== 'undefined' && window.mermaid) return Promise.resolve(window.mermaid);
  if (mermaidPromise) return mermaidPromise;
  mermaidPromise = new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.src = '/vendor/mermaid/mermaid.min.js';
    script.async = true;
    script.onload = () => {
      try {
        window.mermaid.initialize({
          startOnLoad: false,
          securityLevel: 'strict',
          theme: 'dark',
          fontFamily: 'inherit',
          // Workaround for mermaid-js/mermaid#790: foreignObject (HTML
          // labels) has overflow:hidden by default, which clips long node
          // text. Force overflow:visible so labels never get cut off.
          themeCSS: '.label foreignObject { overflow: visible; } .nodeLabel, .edgeLabel, .label { overflow: visible; }',
        });
      } catch { /* initialize is best-effort */ }
      resolve(window.mermaid);
    };
    script.onerror = () => {
      mermaidPromise = null; // allow a later retry
      reject(new Error('mermaid failed to load'));
    };
    document.head.append(script);
  });
  return mermaidPromise;
}

// djb2 hash of the diagram source, used to skip re-rendering unchanged blocks.
function hashCode(s) {
  let h = 5381;
  for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0;
  return String(h >>> 0);
}

function renderFallback(block, code, hash) {
  block.classList.add('mermaid-error');
  block.dataset.rendered = hash;
  block.replaceChildren();
  const note = document.createElement('div');
  note.className = 'mermaid-error-note';
  note.textContent = '⚠ Diagram could not be rendered (invalid Mermaid syntax).';
  const pre = document.createElement('pre');
  pre.className = 'mermaid-src';
  pre.textContent = code;
  block.append(note, pre);
}

// renderMermaidDiagrams renders every not-yet-rendered `.mermaid-block` inside
// container. Safe to call repeatedly (idempotent per block via a content hash)
// and safe to call before Mermaid has loaded (it lazy-loads on first use).
export async function renderMermaidDiagrams(container) {
  if (!container || typeof container.querySelectorAll !== 'function') return;
  // A pending block still has its `.mermaid-src` placeholder. Once rendered to
  // SVG the placeholder is gone, so it is skipped — this is what keeps repeated
  // calls (and live re-renders) from re-rendering an already-drawn diagram.
  const blocks = [...container.querySelectorAll('.mermaid-block')].filter((b) => {
    // Skip incomplete blocks — the fence is still open (streaming delta
    // hasn't received the closing ``` yet). Rendering now would show a
    // misleading "invalid syntax" warning for source that is still growing.
    if (b.dataset.complete === 'false') return false;
    const src = b.querySelector('.mermaid-src');
    if (!src) return false;
    const code = (src.textContent || '').trim();
    return code && b.dataset.rendered !== hashCode(code);
  });
  if (!blocks.length) return;

  let mermaid;
  try {
    mermaid = await loadMermaid();
  } catch {
    return; // leave placeholders as raw source if the library can't load
  }

  for (const block of blocks) {
    const src = block.querySelector('.mermaid-src');
    if (!src) continue;
    const code = (src.textContent || '').trim();
    if (!code) continue;
    const hash = hashCode(code);
    if (block.dataset.rendered === hash) continue;

    let valid = true;
    try {
      valid = await mermaid.parse(code, { suppressErrors: true });
    } catch {
      valid = false;
    }
    if (!valid) {
      renderFallback(block, code, hash);
      continue;
    }

    const id = `mmd-${hash}-${Math.random().toString(36).slice(2, 7)}`;
    try {
      const { svg } = await mermaid.render(id, code);
      block.classList.remove('mermaid-error');
      block.innerHTML = svg;
      block.dataset.rendered = hash;
      // Mermaid 10.x emits explicit width/height in px on the <svg>. When
      // the container is narrower than the diagram, max-width:100% scales
      // the width but the fixed px height clips text. Replace the px
      // dimensions with viewBox-driven sizing so the SVG scales
      // proportionally and never clips.
      const svgEl = block.querySelector('svg');
      if (svgEl) {
        const vb = svgEl.getAttribute('viewBox');
        if (vb) {
          svgEl.removeAttribute('width');
          svgEl.removeAttribute('height');
          svgEl.style.maxWidth = '100%';
          svgEl.style.height = 'auto';
        }
      }
      // Attach the zoom button now that the SVG is in the DOM. This catches
      // the lazy-load case where mermaid.min.js loaded after the settle-point
      // attachZoomButtons pass already ran.
      attachMermaidZoomButton(block);
    } catch {
      renderFallback(block, code, hash);
    }
    // Remove any stray node Mermaid may have appended on a failed render.
    document.getElementById(`d${id}`)?.remove();
  }
}
