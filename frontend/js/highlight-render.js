// Syntax highlighting for fenced/indented code blocks in chat messages.
//
// Design constraints (mirror js/mermaid-render.js):
//   1. Live deltas must not re-highlight. parseBlocks emits <pre
//      data-complete="false"> while a fence is still open; this module
//      skips those and only highlights blocks whose fence has closed
//      (data-complete="true"). Each block is keyed by a content hash so
//      repeated calls skip already-highlighted blocks — the same "lock"
//      pattern Mermaid uses, which keeps settled code blocks untouched
//      while subsequent text deltas arrive.
//   2. A bad/unknown language must never break the message. highlight.js
//      falls back to auto-detection when a `language-xxx` class is not
//      registered; we also guard with try/catch so a highlighting error
//      leaves the raw escaped source visible.
//
// highlight.js is lazy-loaded (a ~130KB UMD bundle) the first time a
// complete code block appears. When no language class is present on the
// <code> element, highlight.js auto-detects the language from the content.

let hljsPromise = null;

function loadHljs() {
  if (typeof window !== 'undefined' && window.hljs) return Promise.resolve(window.hljs);
  if (hljsPromise) return hljsPromise;
  hljsPromise = new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.src = '/vendor/highlightjs/highlight.min.js';
    script.async = true;
    script.onload = () => {
      const hljs = window.hljs;
      if (!hljs) {
        hljsPromise = null;
        reject(new Error('highlight.js loaded but window.hljs is undefined'));
        return;
      }
      resolve(hljs);
    };
    script.onerror = () => {
      hljsPromise = null; // allow a later retry
      reject(new Error('highlight.js failed to load'));
    };
    document.head.append(script);
  });
  return hljsPromise;
}

// djb2 hash of the code source, used to skip re-highlighting unchanged blocks.
function hashCode(s) {
  let h = 5381;
  for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0;
  return String(h >>> 0);
}

// highlightCode highlights every not-yet-highlighted `pre > code` block inside
// container. Safe to call repeatedly (idempotent per block via a content hash)
// and safe to call before highlight.js has loaded (it lazy-loads on first use).
// Incomplete fences (data-complete="false") are skipped so streaming deltas
// don't waste work on source that is still growing.
export async function highlightCode(container) {
  if (!container || typeof container.querySelectorAll !== 'function') return;
  // Only target <pre><code> blocks that are complete and not yet highlighted.
  // Mermaid blocks use <div class="mermaid-block"> (no <pre><code>), so they
  // are naturally excluded.
  const blocks = [...container.querySelectorAll('pre > code')].filter((code) => {
    const pre = code.parentElement;
    if (!pre) return false;
    // Skip incomplete fences — the fence is still open (streaming delta
    // hasn't received the closing ``` yet).
    if (pre.dataset.complete === 'false') return false;
    const text = (code.textContent || '').trim();
    if (!text) return false;
    const hash = hashCode(text);
    // Skip already-highlighted blocks whose content hasn't changed.
    if (code.dataset.highlighted === hash) return false;
    return true;
  });
  if (!blocks.length) return;

  let hljs;
  try {
    hljs = await loadHljs();
  } catch {
    return; // leave raw escaped source if the library can't load
  }

  for (const code of blocks) {
    const text = (code.textContent || '').trim();
    if (!text) continue;
    const hash = hashCode(text);
    if (code.dataset.highlighted === hash) continue;
    try {
      // highlightElement uses the `language-xxx` class if present and
      // registered; otherwise it auto-detects from the content.
      hljs.highlightElement(code);
      code.dataset.highlighted = hash;
    } catch {
      // Leave the raw escaped source visible on any highlighting error.
    }
  }
}
