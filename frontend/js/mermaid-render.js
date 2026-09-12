import { attachMermaidZoomButton } from './media-zoom.js';

// querySelectorAll('.mermaid-block') does not match the root, and the live
// renderer often calls renderMermaidDiagrams on the mermaid-block itself.
function mermaidBlocksIn(container) {
  if (!container || typeof container.querySelectorAll !== 'function') return [];
  const blocks = [];
  if (container.classList?.contains('mermaid-block')) blocks.push(container);
  blocks.push(...container.querySelectorAll('.mermaid-block'));
  return blocks;
}

// Mermaid diagram renderer for chat messages.
//
// Design constraints (from the chat streaming model):
//   1. Live deltas keep Mermaid work cheap. renderMarkdown emits a placeholder
//      (raw source only); callers may render a block immediately when its fence
//      closes, while the content hash makes repeated enhancement passes free.
//   2. Invalid Mermaid must not break the message. We validate with
//      mermaid.parse({suppressErrors}) before rendering and fall back to the raw
//      source + a note on any failure, catching errors so a bad diagram from the
//      model never throws or injects Mermaid's error bomb into the thread.
//
// Mermaid is lazy-loaded (a ~3MB UMD bundle) the first time a diagram appears.

let mermaidPromise = null;
let configuredMermaid = null;
const MIN_INLINE_MERMAID_WIDTH = 420;

// The prototype and the production frontend can preload Mermaid themselves.
// Keep configuration idempotent so both paths get the same palette instead of
// relying on which script happened to win the race.
function configureMermaid(mermaid) {
  if (!mermaid || configuredMermaid === mermaid) return;
  if (typeof mermaid.initialize !== 'function') return;
  try {
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      theme: 'base',
      fontFamily: 'IBM Plex Sans, sans-serif',
      themeVariables: {
        background: '#0a0f1d',
        primaryColor: '#121b2e',
        primaryTextColor: '#edf2f4',
        primaryBorderColor: '#58d1c3',
        lineColor: '#79aee8',
        secondaryColor: '#0d1425',
        secondaryTextColor: '#edf2f4',
        secondaryBorderColor: '#344661',
        tertiaryColor: '#18243a',
        tertiaryTextColor: '#edf2f4',
        tertiaryBorderColor: '#344661',
        textColor: '#edf2f4',
        nodeTextColor: '#edf2f4',
        edgeLabelBackground: '#0a0f1d',
        clusterBkg: '#0d1425',
        clusterBorder: '#344661',
        titleColor: '#edf2f4',
        actorBkg: '#121b2e',
        actorBorder: '#58d1c3',
        actorTextColor: '#edf2f4',
        signalColor: '#79aee8',
        signalTextColor: '#b9c3cc',
        labelBoxBkgColor: '#121b2e',
        labelBoxBorderColor: '#344661',
        labelTextColor: '#edf2f4',
        noteBkgColor: '#18243a',
        noteBorderColor: '#d1b77f',
        noteTextColor: '#edf2f4',
        activationBkgColor: '#18243a',
        activationBorderColor: '#58d1c3',
        sequenceNumberColor: '#050610',
        fontFamily: 'IBM Plex Sans, sans-serif',
        fontSize: '14px',
      },
      // Mermaid's foreignObject labels otherwise clip long node text. Keep
      // the SVG layer transparent so the surrounding chat surface shows
      // through without a bright rectangle during rendering.
      themeCSS: '.label foreignObject { overflow: visible; } .nodeLabel, .edgeLabel, .label { overflow: visible; } svg { background: transparent; }',
    });
    configuredMermaid = mermaid;
  } catch { /* initialize is best-effort; rendering still has a raw fallback */ }
}

function loadMermaid() {
  if (typeof window !== 'undefined' && window.mermaid) {
    configureMermaid(window.mermaid);
    return Promise.resolve(window.mermaid);
  }
  if (mermaidPromise) return mermaidPromise;
  mermaidPromise = new Promise((resolve, reject) => {
    const script = document.createElement('script');
    // Resolve from the module instead of assuming the host mounts `vendor/`
    // at the document root. The native app serves this module from `/js/`,
    // while the widget prototype imports it from `/frontend/js/`.
    script.src = new URL('../vendor/mermaid/mermaid.min.js', import.meta.url).href;
    script.async = true;
    script.onload = () => {
      configureMermaid(window.mermaid);
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
  const blocks = mermaidBlocksIn(container).filter((b) => {
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
      // Mermaid 10.x emits explicit width/height in px on the <svg>. Replace
      // those dimensions with a viewBox-driven inline baseline: compact
      // diagrams no longer collapse to the browser's 300px SVG default. The
      // block itself owns the overflow viewport so the SVG can stay crisp.
      const svgEl = block.querySelector('svg');
      if (svgEl) {
        const vb = svgEl.getAttribute('viewBox');
        if (vb) {
          const parts = vb.split(/[\s,]+/).map(Number);
          const width = parts[2];
          const height = parts[3];
          svgEl.removeAttribute('width');
          svgEl.removeAttribute('height');
          if (Number.isFinite(width) && width > 0 && Number.isFinite(height) && height > 0) {
            svgEl.style.width = `${Math.max(MIN_INLINE_MERMAID_WIDTH, width)}px`;
            svgEl.style.aspectRatio = `${width} / ${height}`;
          }
          // Keep the vector's intrinsic width so narrow cards can scroll
          // horizontally instead of shrinking labels into unreadable pixels.
          // The surrounding `.mermaid-block` owns the overflow viewport.
          svgEl.style.maxWidth = 'none';
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
