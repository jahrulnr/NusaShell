// Markdown -> HTML renderer backed by micromark (CommonMark + GFM).
//
// parseBlocks returns top-level blocks annotated with data-start/data-end
// (byte offsets into the source). This enables incremental rendering: only
// blocks whose byte range changed are re-rendered; unchanged blocks —
// including rendered Mermaid SVGs — are preserved across streaming deltas.
//
// micromark provides full CommonMark + GFM compliance (nested lists,
// reference links, autolinks, strikethrough, task lists, tables). A two-pass
// adapter injects NusaShell-specific features: byte-range anchors for
// incremental rendering, data-complete flags for streaming fences, Mermaid
// placeholders, file:// → /local-file proxy, and video detection by extension.

import { micromark, parse, postprocess, preprocess, gfm, gfmHtml } from '../vendor/micromark/micromark.mjs';

// videoExtensions are file extensions that should render as <video>
// instead of <img> when used in markdown image syntax.
const videoExtensions = new Set(['mp4', 'webm', 'ogg', 'ogv', 'mov', 'avi', 'mkv', 'm4v']);

// GFM extensions enabled for all parse/compile calls.
const gfmExt = gfm();
const gfmHtmlExt = gfmHtml();
const opts = { extensions: [gfmExt], htmlExtensions: [gfmHtmlExt], allowDangerousProtocol: true };

// Block-level token types that correspond to top-level HTML elements.
const blockTypes = new Set([
  'atxHeading', 'setextHeading', 'paragraph', 'codeFenced', 'codeIndented',
  'listOrdered', 'listUnordered', 'blockQuote', 'thematicBreak',
  'table', 'htmlFlow',
]);

// HTML tag names that correspond to top-level blocks (for the HTML walker).
const blockTagNames = new Set(['h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'p', 'pre', 'ul', 'ol', 'blockquote', 'hr', 'table']);

function escapeHtml(s) {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// isVideoUrl checks if a URL points to a video file by extension.
function isVideoUrl(url) {
  try {
    const u = new URL(url, 'http://placeholder/');
    const ext = u.pathname.split('.').pop()?.toLowerCase();
    return videoExtensions.has(ext);
  } catch {
    return false;
  }
}

// resolveMediaUrl converts a file:// URL to a /local-file?path= proxy URL
// so the browser can load local files from an http:// origin. http(s)://
// and data: URLs pass through unchanged.
function resolveMediaUrl(url) {
  if (url.startsWith('file://')) {
    const path = decodeURIComponent(url.slice('file://'.length).replace(/^\/+/, '/'));
    return '/local-file?path=' + encodeURIComponent(path);
  }
  return url;
}

// ---- Pass 1: extract top-level blocks from parse events ----
//
// Walks micromark events with container-depth tracking. When a block-type
// token enters at depth 0, records its start offset. When it exits at depth
// 0, records its end offset. For codeFenced blocks, also tracks fence count
// to determine completeness (data-complete).
function extractBlocks(events, srcLen) {
  const blocks = [];
  let depth = 0;
  let currentBlock = null;
  let fenceCount = 0;

  for (const [kind, token] of events) {
    if (token.type === 'codeFencedFence' && kind === 'enter') fenceCount++;
    if (blockTypes.has(token.type)) {
      if (kind === 'enter') {
        if (depth === 0) {
          currentBlock = { type: token.type, start: token.start.offset, end: null };
        }
        depth++;
      } else {
        depth--;
        if (depth === 0 && currentBlock) {
          currentBlock.end = token.end.offset;
          if (currentBlock.type === 'codeFenced') {
            currentBlock.complete = fenceCount >= 2;
          } else if (currentBlock.type === 'codeIndented') {
            currentBlock.complete = true;
          }
          blocks.push(currentBlock);
          currentBlock = null;
        }
      }
    }
  }
  // Handle unclosed blocks (e.g., unclosed fence at EOF during streaming).
  if (currentBlock && depth > 0) {
    currentBlock.end = srcLen;
    if (currentBlock.type === 'codeFenced') currentBlock.complete = false;
    blocks.push(currentBlock);
  }
  return blocks;
}

// ---- Post-process individual block HTML ----
//
// Applies NusaShell-specific transforms to the HTML produced by micromark:
// 1. Mermaid code fences → placeholder div (SVG rendered separately by mermaid-render.js)
// 2. file:// URLs → /local-file?path= proxy
// 3. Video detection by extension (<img> → <video>)
// 4. Image attributes: loading="lazy", onerror for broken-image styling
// 5. External links: target="_blank" rel="noopener"
// 6. Task list checkboxes: <input> → styled span (AGENTS.md: no native controls)
function postProcessBlockHtml(block, html) {
  // 1. Mermaid placeholder: replace <pre><code class="language-mermaid">…</code></pre>
  //    with <div class="mermaid-block" …><pre class="mermaid-src">…</pre></div>.
  //    The raw source is preserved (HTML-escaped) for mermaid-render.js to turn
  //    into SVG when the fence is complete (data-complete="true").
  if (block.type === 'codeFenced') {
    const mermaidMatch = html.match(/<pre[^>]*><code class="language-mermaid">([\s\S]*?)<\/code><\/pre>/);
    if (mermaidMatch) {
      const rawSrc = mermaidMatch[1];
      const completeAttr = block.complete !== undefined ? ` data-complete="${block.complete}"` : '';
      return `<div class="mermaid-block" data-start="${block.start}" data-end="${block.end}"${completeAttr}><pre class="mermaid-src">${rawSrc}</pre></div>`;
    }
  }

  // 2–4. Media handling: file:// proxy, video detection, image attributes.
  // Combined into one pass so video detection sees the original URL (before
  // file:// → /local-file conversion would hide the extension in a query param).
  html = html.replace(/<img\s+([^>]*?)src="([^"]+)"([^>]*?)\s*\/>/g, (match, before, src, after) => {
    const altMatch = match.match(/alt="([^"]*)"/);
    const alt = altMatch ? altMatch[1] : '';
    const proxiedSrc = resolveMediaUrl(src);
    if (isVideoUrl(src)) {
      return `<video controls preload="metadata" src="${proxiedSrc}">${escapeHtml(alt)}</video>`;
    }
    return `<img src="${proxiedSrc}" alt="${escapeHtml(alt)}" loading="lazy" onerror="this.classList.add('img-load-error');this.nextElementSibling?.classList.remove('hidden')">`;
  });

  // 5. External links: add target="_blank" rel="noopener" to http(s) links.
  html = html.replace(/<a\s+href="(https?:\/\/[^"]+)"/g, '<a href="$1" target="_blank" rel="noopener"');

  // 6. Task list checkboxes: replace native <input type="checkbox"> with
  //    styled span (AGENTS.md: do not render visible native browser controls).
  html = html.replace(/<input\s+type="checkbox"\s+disabled=""\s+checked=""\s*\/>/g,
    '<span class="task-checkbox" data-checked="true" role="checkbox" aria-checked="true">✓</span>');
  html = html.replace(/<input\s+type="checkbox"\s+disabled=""\s*\/>/g,
    '<span class="task-checkbox" data-checked="false" role="checkbox" aria-checked="false">○</span>');

  return html;
}

// ---- Pass 2+3: compile HTML, inject data-start/data-end, split into blocks ----
//
// Compiles the full markdown to HTML via micromark, then walks the HTML
// character by character with tag-depth tracking. When a top-level block
// opening tag is found (matching the next block in the list), injects
// data-start/data-end (and data-complete for fenced code) attributes and
// captures the full element until its matching close tag.
function compileAndSplit(src, blocks) {
  const html = micromark(src, opts);
  const result = [];
  let i = 0;
  let blockIdx = 0;
  let capture = '';
  let captureDepth = 0;
  let inCapture = false;

  while (i < html.length) {
    if (html[i] === '<') {
      const isClosing = html[i + 1] === '/';
      const tagEnd = html.indexOf('>', i);
      if (tagEnd === -1) { if (inCapture) capture += html[i]; i++; continue; }
      const tagContent = html.slice(i + (isClosing ? 2 : 1), tagEnd);
      const tagName = tagContent.split(/[\s\/]/)[0];
      const isSelfClosing = tagContent.endsWith('/');

      if (isClosing) {
        if (inCapture) {
          capture += html.slice(i, tagEnd + 1);
          captureDepth--;
          if (captureDepth === 0) {
            const b = blocks[blockIdx];
            result.push({ start: b.start, end: b.end, html: postProcessBlockHtml(b, capture) });
            blockIdx++;
            capture = '';
            inCapture = false;
          }
        }
        i = tagEnd + 1;
        continue;
      }

      // Opening tag — check if this is the next top-level block.
      if (!inCapture && blockIdx < blocks.length && blockTagNames.has(tagName)) {
        const b = blocks[blockIdx];
        let attrs = ` data-start="${b.start}" data-end="${b.end}"`;
        if (b.complete !== undefined) attrs += ` data-complete="${b.complete}"`;

        if (isSelfClosing) {
          const inner = tagContent.replace(/\s*\/$/, '');
          const blockHtml = `<${inner}${attrs} />`;
          result.push({ start: b.start, end: b.end, html: postProcessBlockHtml(b, blockHtml) });
          blockIdx++;
        } else {
          capture = `<${tagContent}${attrs}>`;
          captureDepth = 1;
          inCapture = true;
        }
        i = tagEnd + 1;
        continue;
      }

      if (inCapture) {
        capture += html.slice(i, tagEnd + 1);
        if (!isSelfClosing) captureDepth++;
      }
      i = tagEnd + 1;
      continue;
    }

    if (inCapture) capture += html[i];
    i++;
  }

  return result;
}

// parseBlocks parses markdown source into a list of top-level blocks, each
// annotated with its byte range in the source. The byte range is emitted as
// data-start/data-end attributes on the block's root element, enabling
// incremental rendering (see js/incremental-render.js).
//
// Block kinds: p, pre, mermaid, h1-h6, ul, ol, blockquote, hr, table.
// Each block: { start, end, html } where start/end are byte offsets.
export function parseBlocks(src) {
  if (!src) return [];
  const events = postprocess(parse(opts).document().write(preprocess()(src, undefined, true)));
  const blocks = extractBlocks(events, src.length);
  return compileAndSplit(src, blocks);
}

// renderMarkdown is the backward-compatible full-render entry point. It
// delegates to parseBlocks and joins the HTML. The data-start/data-end
// attributes are harmless in non-incremental context.
export function renderMarkdown(src) {
  return parseBlocks(src).map((b) => b.html).join('\n');
}
