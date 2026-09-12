// Zoomable media popup for Mermaid SVG diagrams, inline markdown images,
// and HTML artifact overlays.
//
// Design:
//   - openZoomableMedia: full-screen overlay with zoom controls (+/-/reset),
//     scroll-wheel zoom, drag-to-pan, double-click reset. Works for both
//     raster images (src) and SVG elements (svgEl clone).
//   - openArtifactPopup: 80% screen overlay with a sandboxed iframe (same
//     srcdoc policy as the inline artifact card). No zoom — interactive HTML
//     needs full pointer events, not transform scaling.
//   - attachZoomButtons: idempotent scan that adds a zoom icon button to
//     rendered Mermaid blocks (`.mermaid-block svg`) and inline markdown
//     images (`.agent-bubble-text img`), excluding generated-image cards
//     (which have their own lightbox) and broken images.
//
// All overlays register with registerOverlayDismiss so the router closes
// them on view change (same pattern as dialog()/openImageLightbox).

import { el, registerOverlayDismiss } from './ui.js';
import { renderMarkdown } from './markdown.js';
import { highlightCode } from './highlight-render.js';
import { codeLanguageInfo, ensureCodeMirror } from './code-language.js';

// ─── Zoomable media popup (SVG + images) ─────────────────────────────

const MIN_SCALE = 0.2;
const MAX_SCALE = 8;
const WHEEL_ZOOM_STEP = 0.15;
const BUTTON_ZOOM_STEP = 0.3;
const MIN_VECTOR_VIEWPORT_WIDTH = 420;

// captureOverlayFocus records the element that held keyboard focus when an
// overlay opens and returns a restore() closure that returns focus to it
// (guarded by isConnected) on close. Every modal/overlay in this module uses
// it so closing returns the user to the trigger rather than <body>, as
// required by frontend/AGENTS.md (safe focus return for overlays).
function captureOverlayFocus() {
  const invoker = document.activeElement?.focus ? document.activeElement : null;
  return function restoreOverlayFocus() {
    if (invoker?.isConnected) invoker.focus({ preventScroll: true });
  };
}

// openZoomableMedia opens a full-screen zoomable overlay.
//   opts.src     — image URL (for raster images)
//   opts.svgEl   — SVG element to clone (for Mermaid diagrams)
//   opts.alt     — alt text / caption label
//   opts.caption — caption shown in the control bar
// At least one of src or svgEl must be provided.
export function openZoomableMedia({ src, svgEl, alt, caption } = {}) {
  if (!src && !svgEl) return;

  const restoreFocus = captureOverlayFocus();
  const overlay = el('div', {
    class: 'media-zoom-overlay',
    role: 'dialog',
    'aria-modal': 'true',
    'aria-label': alt || caption || 'Zoomable media',
  });

  // Close button (top-right, same placement as agent-image-lightbox).
  const closeBtn = el('button', {
    class: 'media-zoom-close',
    type: 'button',
    text: 'Close',
    'aria-label': 'Close',
  });

  // Control bar: zoom out, scale readout, zoom in, reset + caption.
  const zoomOut = el('button', { class: 'media-zoom-btn', type: 'button', 'aria-label': 'Zoom out', text: '−' });
  const scaleLabel = el('span', { class: 'media-zoom-scale', text: '100%' });
  const zoomIn = el('button', { class: 'media-zoom-btn', type: 'button', 'aria-label': 'Zoom in', text: '+' });
  const resetBtn = el('button', { class: 'media-zoom-btn media-zoom-reset', type: 'button', 'aria-label': 'Reset zoom', text: 'Reset' });

  const bar = el('div', { class: 'media-zoom-bar' },
    zoomOut, scaleLabel, zoomIn, resetBtn,
    caption ? el('span', { class: 'media-zoom-caption', text: caption }) : null,
  );

  // Viewport: the scrollable/pannable area that contains the media.
  const viewport = el('div', { class: 'media-zoom-viewport' });

  // Media stage: holds the image/SVG clone, transformed for zoom/pan.
  const stage = el('div', { class: 'media-zoom-stage' });
  let mediaEl;
  let vectorWidth = 0;
  let vectorHeight = 0;
  if (svgEl) {
    // The rendered inline SVG may be CSS-constrained to a few pixels. Its
    // viewBox is the actual vector canvas, so prefer it over layout geometry.
    // Electron rasterizes a transformed foreignObject/SVG layer while it is
    // animated; later zooming changes this element's real dimensions instead.
    const rect = svgEl.getBoundingClientRect();
    const vb = svgEl.getAttribute('viewBox');
    let viewBoxW = 0;
    let viewBoxH = 0;
    if (vb) {
      const parts = vb.split(/[\s,]+/).map(Number);
      if (parts.length === 4) { viewBoxW = parts[2]; viewBoxH = parts[3]; }
    }
    const naturalW = viewBoxW && viewBoxH
      ? Math.max(MIN_VECTOR_VIEWPORT_WIDTH, viewBoxW, rect.width || 0)
      : (rect.width || 0);
    const naturalH = viewBoxW && viewBoxH
      ? naturalW * (viewBoxH / viewBoxW)
      : (rect.height || 0);
    mediaEl = svgEl.cloneNode(true);
    vectorWidth = naturalW;
    vectorHeight = naturalH;
    // Explicit dimensions make the clone visible in the popup even after the
    // inline renderer stripped Mermaid's px width/height attributes.
    if (naturalW && naturalH) {
      mediaEl.setAttribute('width', String(naturalW));
      mediaEl.setAttribute('height', String(naturalH));
    }
    mediaEl.style.maxWidth = 'none';
    mediaEl.style.maxHeight = 'none';
    mediaEl.style.width = naturalW ? `${naturalW}px` : '';
    mediaEl.style.height = naturalH ? `${naturalH}px` : '';
    mediaEl.style.aspectRatio = naturalW && naturalH ? `${naturalW} / ${naturalH}` : '';
    stage.classList.add('is-vector');
  } else {
    mediaEl = el('img', { src, alt: alt || '' });
  }
  stage.append(mediaEl);
  viewport.append(stage);
  overlay.append(closeBtn, bar, viewport);
  document.body.append(overlay);

  // ── Zoom/pan state ──
  let scale = 1;
  let panX = 0;
  let panY = 0;
  let dragging = false;
  let dragStartX = 0;
  let dragStartY = 0;
  let panStartX = 0;
  let panStartY = 0;

  function applyTransform() {
    if (svgEl && vectorWidth && vectorHeight) {
      // Resize the vector's layout box rather than CSS-transforming the
      // composited stage. Chromium/Electron then paints SVG paths and labels
      // at the requested resolution instead of enlarging a raster snapshot.
      mediaEl.style.width = `${vectorWidth * scale}px`;
      mediaEl.style.height = `${vectorHeight * scale}px`;
      stage.style.transform = `translate(${panX}px, ${panY}px)`;
    } else {
      stage.style.transform = `translate(${panX}px, ${panY}px) scale(${scale})`;
    }
    scaleLabel.textContent = `${Math.round(scale * 100)}%`;
  }
  function setScale(next, pivotX, pivotY) {
    const clamped = Math.max(MIN_SCALE, Math.min(MAX_SCALE, next));
    if (pivotX != null && pivotY != null) {
      // Zoom toward the cursor: adjust pan so the point under the cursor
      // stays fixed. Math: newPan = pivot - (pivot - oldPan) * (newScale/oldScale).
      const rect = viewport.getBoundingClientRect();
      const cx = pivotX - rect.left - rect.width / 2;
      const cy = pivotY - rect.top - rect.height / 2;
      panX = cx - (cx - panX) * (clamped / scale);
      panY = cy - (cy - panY) * (clamped / scale);
    }
    scale = clamped;
    applyTransform();
  }
  function reset() {
    scale = 1;
    panX = 0;
    panY = 0;
    applyTransform();
  }

  // ── Event wiring ──
  zoomIn.addEventListener('click', () => setScale(scale + BUTTON_ZOOM_STEP));
  zoomOut.addEventListener('click', () => setScale(scale - BUTTON_ZOOM_STEP));
  resetBtn.addEventListener('click', reset);

  viewport.addEventListener('wheel', (e) => {
    e.preventDefault();
    const delta = e.deltaY > 0 ? -WHEEL_ZOOM_STEP : WHEEL_ZOOM_STEP;
    setScale(scale + delta * scale, e.clientX, e.clientY);
  }, { passive: false });

  viewport.addEventListener('dblclick', reset);

  // Drag-to-pan (only when zoomed in beyond 1x, or always — panning is
  // useful even at scale 1 for large diagrams that overflow).
  viewport.addEventListener('mousedown', (e) => {
    if (e.target === closeBtn || bar.contains(e.target)) return;
    dragging = true;
    dragStartX = e.clientX;
    dragStartY = e.clientY;
    panStartX = panX;
    panStartY = panY;
    viewport.classList.add('is-grabbing');
    e.preventDefault();
  });
  document.addEventListener('mousemove', onDragMove, true);
  document.addEventListener('mouseup', onDragEnd, true);
  function onDragMove(e) {
    if (!dragging) return;
    panX = panStartX + (e.clientX - dragStartX);
    panY = panStartY + (e.clientY - dragStartY);
    applyTransform();
  }
  function onDragEnd() {
    if (!dragging) return;
    dragging = false;
    viewport.classList.remove('is-grabbing');
  }

  // Touch pan (single finger) + pinch zoom (two fingers).
  let touchState = null;
  viewport.addEventListener('touchstart', (e) => {
    if (e.touches.length === 1) {
      touchState = { mode: 'pan', x: e.touches[0].clientX, y: e.touches[0].clientY, panX, panY };
    } else if (e.touches.length === 2) {
      const dx = e.touches[0].clientX - e.touches[1].clientX;
      const dy = e.touches[0].clientY - e.touches[1].clientY;
      touchState = { mode: 'pinch', dist: Math.hypot(dx, dy), scale };
    }
  }, { passive: true });
  viewport.addEventListener('touchmove', (e) => {
    if (!touchState) return;
    e.preventDefault();
    if (touchState.mode === 'pan' && e.touches.length === 1) {
      panX = touchState.panX + (e.touches[0].clientX - touchState.x);
      panY = touchState.panY + (e.touches[0].clientY - touchState.y);
      applyTransform();
    } else if (touchState.mode === 'pinch' && e.touches.length === 2) {
      const dx = e.touches[0].clientX - e.touches[1].clientX;
      const dy = e.touches[0].clientY - e.touches[1].clientY;
      const dist = Math.hypot(dx, dy);
      setScale(touchState.scale * (dist / touchState.dist));
    }
  }, { passive: false });
  viewport.addEventListener('touchend', () => { touchState = null; }, { passive: true });

  // Close handlers (Escape, click-outside, close button).
  const onKey = (e) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    }
    if (e.key === '+' || e.key === '=') setScale(scale + BUTTON_ZOOM_STEP);
    if (e.key === '-' || e.key === '_') setScale(scale - BUTTON_ZOOM_STEP);
    if (e.key === '0') reset();
  };
  const unregister = registerOverlayDismiss(close);
  let closed = false;
  function close() {
    if (closed) return;
    closed = true;
    unregister();
    document.removeEventListener('keydown', onKey, true);
    document.removeEventListener('mousemove', onDragMove, true);
    document.removeEventListener('mouseup', onDragEnd, true);
    overlay.remove();
    restoreFocus();
  }
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
  closeBtn.addEventListener('click', close);
  document.addEventListener('keydown', onKey, true);

  closeBtn.focus();
}

// ─── Artifact popup (80% screen, no zoom) ────────────────────────────

// openArtifactPopup opens an 80%-screen overlay with a sandboxed iframe
// rendering the artifact's srcdoc. Mirrors the inline iframe policy but
// at a larger size for better visibility of complex HTML/CSS/JS documents.
export function openArtifactPopup({ srcDoc, title, width, height } = {}) {
  if (!srcDoc) return;

  const restoreFocus = captureOverlayFocus();
  const overlay = el('div', {
    class: 'media-zoom-overlay artifact-popup-overlay',
    role: 'dialog',
    'aria-modal': 'true',
    'aria-label': title || 'Artifact',
  });

  const closeBtn = el('button', {
    class: 'media-zoom-close',
    type: 'button',
    text: 'Close',
    'aria-label': 'Close',
  });

  const bar = el('div', { class: 'media-zoom-bar artifact-popup-bar' },
    el('span', { class: 'artifact-popup-title', text: title || 'Artifact' }),
  );

  const frame = el('div', { class: 'artifact-popup-frame' });
  const iframe = el('iframe', { class: 'artifact-popup-iframe', title: title || 'Artifact' });
  iframe.sandbox = 'allow-scripts allow-same-origin allow-popups allow-forms allow-modals';
  if (width) iframe.width = width;
  if (height) iframe.height = height;
  iframe.srcdoc = srcDoc;
  frame.append(iframe);

  overlay.append(closeBtn, bar, frame);
  document.body.append(overlay);

  const onKey = (e) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      close();
    }
  };
  const unregister = registerOverlayDismiss(close);
  let closed = false;
  function close() {
    if (closed) return;
    closed = true;
    unregister();
    document.removeEventListener('keydown', onKey, true);
    overlay.remove();
    restoreFocus();
  }
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
  closeBtn.addEventListener('click', close);
  document.addEventListener('keydown', onKey, true);

  closeBtn.focus();
}

// ─── Zoom button attachment (idempotent) ─────────────────────────────

const ZOOM_BTN_FLAG = 'data-zoom-attached';

// querySelectorAll('.mermaid-block') does not match the root, and the live
// renderer often calls attachZoomButtons on the mermaid-block itself.
function mermaidBlocksIn(container) {
  if (!container || typeof container.querySelectorAll !== 'function') return [];
  const blocks = [];
  if (container.classList?.contains('mermaid-block')) blocks.push(container);
  blocks.push(...container.querySelectorAll('.mermaid-block'));
  return blocks;
}

// Zoom icon SVG (magnifier with plus).
const ZOOM_ICON_SVG = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" aria-hidden="true">'
  + '<circle cx="11" cy="11" r="7" stroke="currentColor" stroke-width="1.8"/>'
  + '<path d="M20 20l-3.5-3.5M11 8v6M8 11h6" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>';

function makeZoomButton(label, onClick) {
  const btn = el('button', {
    class: 'media-zoom-trigger',
    type: 'button',
    'aria-label': label,
    title: label,
  });
  btn.innerHTML = ZOOM_ICON_SVG;
  btn.addEventListener('click', (e) => {
    e.stopPropagation();
    onClick();
  });
  return btn;
}

// attachMermaidZoomButton adds a zoom button to a single rendered mermaid
// block (called by mermaid-render.js after SVG render, and by
// attachZoomButtons for already-rendered blocks).
export function attachMermaidZoomButton(block) {
  if (!block) return;
  if (!block.querySelector('svg')) return; // not rendered yet (placeholder or error fallback)
  // Skip only when a trigger is actually in the DOM. Concurrent mermaid.render
  // replaces innerHTML (wiping the button) but leaves data-zoom-attached on
  // the block, so a flag-only check would skip forever until a full remount.
  if (block.querySelector(':scope > .media-zoom-trigger')) return;
  block.setAttribute(ZOOM_BTN_FLAG, '1');
  const btn = makeZoomButton('Zoom diagram', () => {
    const liveSvg = block.querySelector('svg');
    if (!liveSvg) return;
    openZoomableMedia({ svgEl: liveSvg, alt: 'Mermaid diagram', caption: 'Mermaid diagram' });
  });
  block.append(btn);
}

// attachZoomButtons scans a container for rendered Mermaid blocks and
// inline markdown images, adding a zoom button to each. Idempotent —
// blocks/images already carrying the zoom button flag are skipped.
// Generated-image cards (.agent-genimage-card) and broken images
// (.img-load-error) are excluded (they have their own lightbox / are
// not zoomable).
export function attachZoomButtons(container) {
  if (!container || typeof container.querySelectorAll !== 'function') return;

  // Mermaid blocks with rendered SVG (including when `container` is the block).
  for (const block of mermaidBlocksIn(container)) {
    attachMermaidZoomButton(block);
  }

  // Inline markdown images inside agent bubbles — exclude generated-image
  // cards (own lightbox), artifact iframes, and broken images.
  for (const img of container.querySelectorAll('.agent-bubble-text img')) {
    if (img.hasAttribute(ZOOM_BTN_FLAG)) continue;
    if (img.classList.contains('img-load-error')) continue;
    // Skip if inside a generated-image card or artifact frame.
    if (img.closest('.agent-genimage-card') || img.closest('.artifact-frame')) continue;
    img.setAttribute(ZOOM_BTN_FLAG, '1');
    // Wrap the image so the button can overlay it. The wrapper is inline
    // to preserve markdown flow.
    const wrapper = el('span', { class: 'media-zoom-img-wrap' });
    img.replaceWith(wrapper);
    wrapper.append(img);
    const btn = makeZoomButton('Zoom image', () => {
      openZoomableMedia({ src: img.src, alt: img.alt, caption: img.alt || '' });
    });
    wrapper.append(btn);
  }
}

// ─── Audio popup (fullscreen player, no zoom) ──────────────────────

// openAudioLightbox opens a fullscreen overlay with a centered <audio>
// element so the user can play the result without leaving the chat
// thread. Mirrors openImageLightbox in shape (close button + caption +
// Download affordance) but skips the zoom/pan controls — audio is a
// temporal medium, not a spatial one, and the native controls already
// cover the playback affordances.
export function openAudioLightbox({ src, name, caption } = {}) {
  if (!src) return;

  const restoreFocus = captureOverlayFocus();
  const overlay = el('div', {
    class: 'media-zoom-overlay agent-audio-lightbox',
    role: 'dialog',
    'aria-modal': 'true',
    'aria-label': name || caption || 'Audio player',
  });
  const closeBtn = el('button', {
    class: 'media-zoom-close',
    type: 'button',
    text: 'Close',
    'aria-label': 'Close',
  });
  const bar = el('div', { class: 'media-zoom-bar agent-audio-bar' },
    caption ? el('span', { class: 'media-zoom-caption', text: caption }) : null,
    el('a', { class: 'media-zoom-download', href: src, download: name || 'audio', text: 'Download' }),
  );
  const frame = el('div', { class: 'agent-audio-lightbox-frame' });
  const audio = el('audio', {
    class: 'agent-audio-lightbox-player',
    controls: true,
    preload: 'metadata',
    src,
  });
  audio.addEventListener('error', () => audio.classList.add('audio-load-error'));
  frame.append(audio);
  overlay.append(closeBtn, bar, frame);
  document.body.append(overlay);

  const onKey = (e) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      close();
    }
  };
  const unregister = registerOverlayDismiss(close);
  let closed = false;
  function close() {
    if (closed) return;
    closed = true;
    unregister();
    // Pause before removing so playback doesn't leak into the next mount.
    try { audio.pause(); } catch { /* ignore */ }
    document.removeEventListener('keydown', onKey, true);
    overlay.remove();
    restoreFocus();
  }
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
  closeBtn.addEventListener('click', close);
  document.addEventListener('keydown', onKey, true);

  // Autoplay inside the popup. Browser policies gate this on user gesture,
  // but opening a modal IS a user gesture in every browser, so playback
  // starts without prompting. If the policy blocks, the native controls
  // are right there.
  audio.play().catch(() => { /* blocked — controls remain usable */ });
  closeBtn.focus();
}

// openVideoLightbox opens a fullscreen overlay with a centered <video>
// element so the user can play the result without leaving the chat
// thread. Mirrors openImageLightbox / openAudioLightbox in shape (close
// button + caption + Download affordance). Native controls already cover
// the playback affordances, so no zoom/pan controls are needed.
export function openVideoLightbox({ src, name, caption } = {}) {
  if (!src) return;

  const restoreFocus = captureOverlayFocus();
  const overlay = el('div', {
    class: 'media-zoom-overlay agent-video-lightbox',
    role: 'dialog',
    'aria-modal': 'true',
    'aria-label': name || caption || 'Video player',
  });
  const closeBtn = el('button', {
    class: 'media-zoom-close',
    type: 'button',
    text: 'Close',
    'aria-label': 'Close',
  });
  const bar = el('div', { class: 'media-zoom-bar agent-video-bar' },
    caption ? el('span', { class: 'media-zoom-caption', text: caption }) : null,
    el('a', { class: 'media-zoom-download', href: src, download: name || 'video', text: 'Download' }),
  );
  const frame = el('div', { class: 'agent-video-lightbox-frame' });
  const video = el('video', {
    class: 'agent-video-lightbox-player',
    controls: true,
    autoplay: true,
    preload: 'metadata',
    src,
  });
  video.addEventListener('error', () => video.classList.add('video-load-error'));
  frame.append(video);
  overlay.append(closeBtn, bar, frame);
  document.body.append(overlay);

  const onKey = (e) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      close();
    }
  };
  const unregister = registerOverlayDismiss(close);
  let closed = false;
  function close() {
    if (closed) return;
    closed = true;
    unregister();
    // Pause before removing so playback doesn't leak into the next mount.
    try { video.pause(); } catch { /* ignore */ }
    document.removeEventListener('keydown', onKey, true);
    overlay.remove();
    restoreFocus();
  }
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
  closeBtn.addEventListener('click', close);
  document.addEventListener('keydown', onKey, true);

  // Autoplay inside the popup. Browser policies gate this on user gesture,
  // but opening a modal IS a user gesture in every browser, so playback
  // starts without prompting. If the policy blocks, the native controls
  // are right there.
  video.play().catch(() => { /* blocked — controls remain usable */ });
  closeBtn.focus();
}

// ─── Local file text / markdown preview popup ──────────────────────

const LOCAL_PREVIEW_EXTENSIONS = new Map([
  ['md', 'markdown'], ['markdown', 'markdown'], ['mdown', 'markdown'], ['mkdn', 'markdown'], ['mdx', 'markdown'],
  ['pdf', 'pdf'],
  ['png', 'image'], ['jpg', 'image'], ['jpeg', 'image'], ['gif', 'image'], ['webp', 'image'], ['bmp', 'image'], ['svg', 'image'], ['avif', 'image'], ['tif', 'image'], ['tiff', 'image'],
  ['mp3', 'audio'], ['wav', 'audio'], ['ogg', 'audio'], ['oga', 'audio'], ['flac', 'audio'], ['m4a', 'audio'], ['aac', 'audio'],
  ['mp4', 'video'], ['webm', 'video'], ['mov', 'video'], ['mkv', 'video'], ['avi', 'video'], ['m4v', 'video'],
]);

const PREVIEW_KIND_LABEL = {
  markdown: 'Markdown', text: 'Text', image: 'Image', audio: 'Audio', video: 'Video', pdf: 'PDF', binary: 'Binary',
};

function cleanContentType(value = '') {
  return String(value).split(';', 1)[0].trim().toLowerCase();
}

function extensionForPath(path = '') {
  const match = /\.([a-z0-9]+)$/i.exec(path);
  return match?.[1]?.toLowerCase() || '';
}

function kindForContentType(contentType) {
  if (contentType === 'text/markdown' || contentType === 'text/x-markdown') return 'markdown';
  if (contentType === 'application/pdf') return 'pdf';
  if (contentType.startsWith('image/')) return 'image';
  if (contentType.startsWith('audio/') || contentType === 'application/ogg') return 'audio';
  if (contentType.startsWith('video/')) return 'video';
  return '';
}

// ISO-BMFF (MP4/MOV/M4A/AVIF/HEIC) major brands at ftyp bytes 8..12 that are
// audio or image rather than the default video container kind.
const ISO_BMFF_AUDIO_BRANDS = new Set(['M4A ', 'M4V ']);
const ISO_BMFF_IMAGE_BRANDS = new Set(['avif', 'avis', 'heic', 'heix', 'hevc', 'hevx', 'mif1', 'msf1']);

function kindForKnownMagic(bytes) {
  const has = (...signature) => signature.every((value, index) => bytes[index] === value);
  if (has(0x25, 0x50, 0x44, 0x46, 0x2d)) return 'pdf'; // %PDF-
  if (has(0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a) || has(0xff, 0xd8, 0xff) || has(0x47, 0x49, 0x46, 0x38)) return 'image';
  if (has(0x49, 0x44, 0x33) || has(0x4f, 0x67, 0x67, 0x53) || has(0x66, 0x4c, 0x61, 0x43)) return 'audio';
  // ISO-BMFF: a `ftyp` box is not always video. Inspect the major brand at
  // bytes 8..12 so AVIF/HEIC images and M4A/M4V audio are not misclassified
  // as video; `qt`, `isom`, `mp41`, `mp42`, and anything else stay video.
  if (bytes.length >= 12 && String.fromCharCode(...bytes.slice(4, 8)) === 'ftyp') {
    const brand = String.fromCharCode(...bytes.slice(8, 12));
    if (ISO_BMFF_AUDIO_BRANDS.has(brand)) return 'audio';
    if (ISO_BMFF_IMAGE_BRANDS.has(brand)) return 'image';
    return 'video';
  }
  return '';
}

function looksBinary(bytes) {
  const sample = bytes.slice(0, 4096);
  if (!sample.length) return false;
  let control = 0;
  for (const byte of sample) {
    if (byte === 0) return true;
    if (byte < 7 || (byte > 14 && byte < 32)) control++;
  }
  return control / sample.length > 0.08;
}

function isTextContentType(contentType) {
  return contentType.startsWith('text/')
    || /^(application\/(json|ld\+json|xml|javascript|x-javascript|sql|wasm-text)|image\/svg\+xml)$/.test(contentType);
}

// The backend's local-file endpoint sets Content-Type from domain.SniffMagic,
// so it wins over a deceptive suffix. The byte checks below are a safety net
// for older/custom servers which omit that header; extensions are the final
// fallback for readable text and known media files.
export function classifyLocalPreview({ filePath = '', contentType = '', bytes = new Uint8Array() } = {}) {
  const type = cleanContentType(contentType);
  const fromContentType = kindForContentType(type);
  if (fromContentType) return { kind: fromContentType, source: 'content-type', contentType: type };

  const fromMagic = kindForKnownMagic(bytes);
  if (fromMagic) return { kind: fromMagic, source: 'bytes', contentType: type };

  const fromExtension = LOCAL_PREVIEW_EXTENSIONS.get(extensionForPath(filePath));
  if (fromExtension) return { kind: fromExtension, source: 'extension', contentType: type };

  if (type === 'application/octet-stream') return { kind: 'binary', source: 'content-type', contentType: type };
  if (!isTextContentType(type) && looksBinary(bytes)) return { kind: 'binary', source: 'bytes', contentType: type };
  return { kind: 'text', source: type ? 'content-type' : 'fallback', contentType: type };
}

function textFromBytes(bytes) {
  return new TextDecoder('utf-8', { fatal: false }).decode(bytes);
}

// readBoundedSample reads at most `limit` bytes from the response body for
// the magic/binary heuristic without downloading the whole file. When the
// response exposes a streaming body (real browsers), it reads chunks via
// getReader() and cancels the reader once the bound is reached or the
// signal aborts. Test doubles that only expose arrayBuffer()/text() fall
// back to buffering and slicing.
async function readBoundedSample(response, limit, signal) {
  if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
  if (response.body && typeof response.body.getReader === 'function') {
    const reader = response.body.getReader();
    const chunks = [];
    let total = 0;
    const onAbort = () => { try { reader.cancel(); } catch { /* ignore */ } };
    signal?.addEventListener?.('abort', onAbort, { once: true });
    try {
      while (total < limit) {
        if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
        const { done, value } = await reader.read();
        if (done) break;
        const remaining = limit - total;
        if (value.byteLength > remaining) {
          chunks.push(value.slice(0, remaining));
          total = limit;
          break;
        }
        chunks.push(value);
        total += value.byteLength;
      }
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
    } finally {
      signal?.removeEventListener?.('abort', onAbort);
      try { await reader.cancel(); } catch { /* already cancelled */ }
    }
    const out = new Uint8Array(total);
    let offset = 0;
    for (const c of chunks) { out.set(c, offset); offset += c.byteLength; }
    return out;
  }
  if (typeof response.arrayBuffer === 'function') {
    return new Uint8Array(await response.arrayBuffer()).slice(0, limit);
  }
  if (typeof response.text === 'function') {
    return new TextEncoder().encode(await response.text()).slice(0, limit);
  }
  return new Uint8Array();
}

// readFullText reads the entire response body as UTF-8 text. Used for
// markdown/text previews that need the full content.
async function readFullText(response, signal) {
  if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
  if (typeof response.text === 'function') return response.text();
  if (typeof response.arrayBuffer === 'function') {
    return textFromBytes(new Uint8Array(await response.arrayBuffer()));
  }
  throw new Error('The file response did not include readable content');
}

function formatByteSize(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10 * 1024 ? 1 : 0)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

// renderTextCodePreview mounts a read-only Dracula CodeMirror editor when the
// vendored CodeMirror 5 assets load; otherwise it falls back to a
// <pre><code class="language-xxx"> surface highlighted by highlight.js. The
// language map (canonical, CodeMirror mode, hljs class, label) comes from
// code-language.js — the single source of truth.
async function renderTextCodePreview({ contentBox, filePath, text }) {
  const info = codeLanguageInfo(extensionForPath(filePath));
  const hljsClass = info.hljs ? `language-${info.hljs}` : '';
  const fallback = () => {
    const pre = el('pre', { class: 'agent-text-preview-code' }, el('code', { class: hljsClass, text }));
    contentBox.append(pre);
    void highlightCode(contentBox);
  };

  let CodeMirror;
  try {
    CodeMirror = await ensureCodeMirror();
  } catch {
    fallback();
    return;
  }
  if (typeof CodeMirror !== 'function') {
    fallback();
    return;
  }

  const host = el('div', { class: 'agent-text-preview-code agent-text-preview-codemirror' });
  contentBox.classList.add('is-code-editor');
  contentBox.append(host);
  try {
    const editor = CodeMirror(host, {
      value: text,
      mode: info.mode,
      theme: 'dracula',
      readOnly: true,
      lineNumbers: true,
      styleActiveLine: true,
      matchBrackets: true,
      lineWrapping: false,
      viewportMargin: 12,
    });
    editor.getWrapperElement?.().setAttribute('aria-label', `${info.label} source`);
  } catch {
    host.remove();
    contentBox.classList.remove('is-code-editor');
    fallback();
  }
}

function mediaPreviewURL(path) {
  return '/local-file?path=' + encodeURIComponent(path);
}

function renderBinaryPreview({ fileName, preview }) {
  return el('div', { class: 'agent-text-preview-binary', role: 'status' },
    el('span', { class: 'agent-text-preview-binary-mark', text: '↧', 'aria-hidden': 'true' }),
    el('div', { class: 'agent-text-preview-binary-copy' },
      el('strong', { text: fileName }),
      el('span', { text: 'This binary format opens outside the preview.' }),
    ),
    el('span', { class: 'agent-text-preview-binary-kind', text: PREVIEW_KIND_LABEL[preview.kind] || 'File' }),
  );
}

function renderMediaPreview({ kind, url, fileName, preview }) {
  if (kind === 'pdf') {
    return el('iframe', { class: 'agent-text-preview-pdf', src: url, title: `Preview ${fileName}`, loading: 'lazy' });
  }
  if (kind === 'image') {
    const image = el('img', { class: 'agent-text-preview-image', src: url, alt: fileName });
    const zoom = el('button', { class: 'agent-text-preview-media-action', type: 'button', text: 'Zoom', 'aria-label': `Zoom ${fileName}` });
    zoom.addEventListener('click', () => openZoomableMedia({ src: url, alt: fileName, caption: fileName }));
    return el('div', { class: 'agent-text-preview-media agent-text-preview-image-stage' }, image, zoom);
  }
  if (kind === 'audio') return el('div', { class: 'agent-text-preview-media agent-text-preview-audio-stage' }, el('audio', { controls: true, src: url }));
  if (kind === 'video') return el('div', { class: 'agent-text-preview-media agent-text-preview-video-stage' }, el('video', { controls: true, src: url }));
  return renderBinaryPreview({ fileName, preview });
}

// openTextPreviewPopup loads a local file into a type-aware viewer.
// Classification is header-first: the backend's magic-derived Content-Type
// (set from magic bytes in transport/local_file.go) and Content-Length drive
// the preview kind and size badge without downloading the body for media.
// Only markdown/text need the full body; an uninformative header falls back
// to a bounded 4 KiB sample for the magic/binary heuristic. Closing the
// popup aborts an in-flight fetch via AbortController.
export async function openTextPreviewPopup(filePath) {
  if (!filePath) return;
  // Strip trailing line numbers if any (e.g., /path/to/file.go:12)
  const cleanPath = filePath.replace(/:\d+(?::\d+)?$/, '');
  const fileName = cleanPath.split(/[\\/]/).pop() || cleanPath;

  const overlay = el('div', {
    class: 'media-zoom-overlay agent-text-preview-overlay',
    role: 'dialog',
    'aria-modal': 'true',
    'aria-label': fileName,
  });

  const closeBtn = el('button', {
    class: 'media-zoom-close',
    type: 'button',
    text: 'Close',
    'aria-label': 'Close',
  });

  const frame = el('div', { class: 'agent-text-preview-frame' });
  const bar = el('div', { class: 'agent-text-preview-bar' },
    el('span', { class: 'agent-text-preview-file-mark', text: '⌁', 'aria-hidden': 'true' }),
    el('div', { class: 'agent-text-preview-file' },
      el('strong', { class: 'agent-text-preview-name', text: fileName }),
      el('span', { class: 'agent-text-preview-path', text: cleanPath, title: cleanPath }),
    ),
    el('span', { class: 'agent-text-preview-kind', dataset: { previewKind: 'loading' }, text: 'Loading' }),
    el('a', { class: 'agent-text-preview-download', href: mediaPreviewURL(cleanPath), download: fileName, text: 'Download' }),
  );
  const contentBox = el('div', { class: 'agent-text-preview-content' });
  contentBox.append(el('div', { class: 'agent-text-preview-loading', text: 'Loading…' }));
  frame.append(bar, contentBox);
  overlay.append(closeBtn, frame);
  document.body.append(overlay);

  const restoreFocus = captureOverlayFocus();
  let closed = false;
  const controller = new AbortController();
  const onKey = (e) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      close();
    }
  };
  const unregister = registerOverlayDismiss(close);
  function close() {
    if (closed) return;
    closed = true;
    // Cancel any in-flight fetch so closing the popup stops the download.
    try { controller.abort(); } catch { /* already aborted */ }
    unregister();
    document.removeEventListener('keydown', onKey, true);
    overlay.remove();
    restoreFocus();
  }
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
  closeBtn.addEventListener('click', close);
  document.addEventListener('keydown', onKey, true);
  closeBtn.focus();

  try {
    const res = await fetch(mediaPreviewURL(cleanPath), { signal: controller.signal });
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`);
    const contentType = res.headers?.get?.('content-type') || '';
    const contentLength = res.headers?.get?.('content-length') || '';
    // Header-first classification: the backend sets Content-Type from magic
    // bytes, so it wins over a deceptive suffix. Media kinds (image/audio/
    // video/pdf) must NOT be fully downloaded for classification.
    const headerKind = kindForContentType(cleanContentType(contentType));
    const sizeFromHeader = contentLength ? Number.parseInt(contentLength, 10) : 0;

    let preview;
    let bytes = new Uint8Array();
    if (headerKind) {
      // A known media/text kind from the header — no body read needed for
      // classification. Markdown/text still need the full body below.
      preview = { kind: headerKind, source: 'content-type', contentType: cleanContentType(contentType) };
    } else {
      // Uninformative header: read a bounded 4 KiB sample for the magic
      // and binary heuristic, then let classifyLocalPreview decide. Keep the
      // original response unread because text/Markdown may need its full body
      // below; a Response body cannot be consumed twice.
      const sampleResponse = typeof res.clone === 'function' ? res.clone() : res;
      bytes = await readBoundedSample(sampleResponse, 4096, controller.signal);
      if (closed) return;
      preview = classifyLocalPreview({ filePath: cleanPath, contentType, bytes });
    }

    const kindLabel = PREVIEW_KIND_LABEL[preview.kind] || 'File';
    const kindBadge = bar.querySelector('.agent-text-preview-kind');
    kindBadge.dataset.previewKind = preview.kind;
    kindBadge.dataset.previewSource = preview.source;
    // Prefer Content-Length for the size badge (accurate for media without
    // downloading the body); fall back to the bytes actually read.
    const sizeForBadge = sizeFromHeader || bytes.byteLength;
    kindBadge.textContent = `${kindLabel}${sizeForBadge ? ` · ${formatByteSize(sizeForBadge)}` : ''}`;
    kindBadge.title = preview.source === 'content-type' ? 'Detected from the file signature by NusaShell' : `Detected from ${preview.source}`;
    contentBox.replaceChildren();

    if (preview.kind === 'markdown') {
      const text = await readFullText(res, controller.signal);
      if (closed) return;
      const mdWrapper = el('div', { class: 'agent-bubble-text agent-text-preview-md' });
      mdWrapper.innerHTML = renderMarkdown(text);
      contentBox.append(mdWrapper);
      await renderPreviewMermaid(mdWrapper);
      void highlightCode(contentBox);
    } else if (preview.kind === 'text') {
      const text = await readFullText(res, controller.signal);
      if (closed) return;
      await renderTextCodePreview({ contentBox, filePath: cleanPath, text });
    } else if (preview.kind === 'binary') {
      contentBox.append(renderBinaryPreview({ fileName, preview }));
    } else {
      contentBox.append(renderMediaPreview({ kind: preview.kind, url: mediaPreviewURL(cleanPath), fileName, preview }));
    }
  } catch (err) {
    if (closed) return;
    if (err?.name === 'AbortError') return; // close aborted the fetch
    contentBox.replaceChildren(el('div', { class: 'agent-text-preview-error', text: `Failed to load ${cleanPath}: ${err.message}` }));
  }
}

// Markdown previews share the chat Markdown parser, including Mermaid
// placeholders. Load the heavier renderer only when a preview actually has a
// diagram; failures leave the escaped source visible instead of replacing the
// whole document with an error state.
async function renderPreviewMermaid(container) {
  if (!container?.querySelector('.mermaid-block')) return;
  try {
    const { renderMermaidDiagrams } = await import('./mermaid-render.js');
    await renderMermaidDiagrams(container);
  } catch {
    // Keep the Mermaid source placeholder as a useful fallback.
  }
}
