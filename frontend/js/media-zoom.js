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

// ─── Zoomable media popup (SVG + images) ─────────────────────────────

const MIN_SCALE = 0.2;
const MAX_SCALE = 8;
const WHEEL_ZOOM_STEP = 0.15;
const BUTTON_ZOOM_STEP = 0.3;

// openZoomableMedia opens a full-screen zoomable overlay.
//   opts.src     — image URL (for raster images)
//   opts.svgEl   — SVG element to clone (for Mermaid diagrams)
//   opts.alt     — alt text / caption label
//   opts.caption — caption shown in the control bar
// At least one of src or svgEl must be provided.
export function openZoomableMedia({ src, svgEl, alt, caption } = {}) {
  if (!src && !svgEl) return;

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
  if (svgEl) {
    // Read natural dimensions before cloning — the original SVG may already
    // have width/height stripped by mermaid-render.js, so getBoundingClientRect
    // or viewBox gives us the intrinsic size to set on the clone. Without
    // explicit dimensions, an SVG in a flex container collapses to 0.
    const rect = svgEl.getBoundingClientRect();
    const vb = svgEl.getAttribute('viewBox');
    let naturalW = rect.width || 0;
    let naturalH = rect.height || 0;
    if ((!naturalW || !naturalH) && vb) {
      const parts = vb.split(/[\s,]+/).map(Number);
      if (parts.length === 4) { naturalW = parts[2]; naturalH = parts[3]; }
    }
    mediaEl = svgEl.cloneNode(true);
    // Set explicit pixel dimensions so the clone is visible in the popup.
    if (naturalW && naturalH) {
      mediaEl.setAttribute('width', String(naturalW));
      mediaEl.setAttribute('height', String(naturalH));
    }
    mediaEl.style.maxWidth = 'none';
    mediaEl.style.maxHeight = 'none';
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
    stage.style.transform = `translate(${panX}px, ${panY}px) scale(${scale})`;
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
  function close() {
    unregister();
    document.removeEventListener('keydown', onKey, true);
    document.removeEventListener('mousemove', onDragMove, true);
    document.removeEventListener('mouseup', onDragEnd, true);
    overlay.remove();
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
  function close() {
    unregister();
    document.removeEventListener('keydown', onKey, true);
    overlay.remove();
  }
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
  closeBtn.addEventListener('click', close);
  document.addEventListener('keydown', onKey, true);

  closeBtn.focus();
}

// ─── Zoom button attachment (idempotent) ─────────────────────────────

const ZOOM_BTN_FLAG = 'data-zoom-attached';

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
  if (!block || block.hasAttribute(ZOOM_BTN_FLAG)) return;
  const svg = block.querySelector('svg');
  if (!svg) return; // not rendered yet (placeholder or error fallback)
  block.setAttribute(ZOOM_BTN_FLAG, '1');
  const btn = makeZoomButton('Zoom diagram', () => {
    openZoomableMedia({ svgEl: svg, alt: 'Mermaid diagram', caption: 'Mermaid diagram' });
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

  // Mermaid blocks with rendered SVG.
  for (const block of container.querySelectorAll('.mermaid-block')) {
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
  function close() {
    unregister();
    // Pause before removing so playback doesn't leak into the next mount.
    try { audio.pause(); } catch { /* ignore */ }
    document.removeEventListener('keydown', onKey, true);
    overlay.remove();
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
  function close() {
    unregister();
    // Pause before removing so playback doesn't leak into the next mount.
    try { video.pause(); } catch { /* ignore */ }
    document.removeEventListener('keydown', onKey, true);
    overlay.remove();
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

// openTextPreviewPopup loads a file via /local-file?path= and opens an overlay.
// If the path ends in .md, it renders via renderMarkdown; otherwise as <pre><code>.
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

  const bar = el('div', { class: 'media-zoom-bar agent-text-preview-bar' },
    el('span', { class: 'media-zoom-caption', text: filePath }),
    el('a', { class: 'media-zoom-download', href: '/local-file?path=' + encodeURIComponent(cleanPath), download: fileName, text: 'Download' }),
  );

  const frame = el('div', { class: 'agent-text-preview-frame' });
  const contentBox = el('div', { class: 'agent-text-preview-content' });
  contentBox.append(el('div', { class: 'agent-text-preview-loading', text: 'Loading…' }));
  frame.append(contentBox);
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
  function close() {
    unregister();
    document.removeEventListener('keydown', onKey, true);
    overlay.remove();
  }
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
  closeBtn.addEventListener('click', close);
  document.addEventListener('keydown', onKey, true);
  closeBtn.focus();

  try {
    const res = await fetch('/local-file?path=' + encodeURIComponent(cleanPath));
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`);
    const text = await res.text();
    contentBox.replaceChildren();

    const isMd = /\.md$/i.test(cleanPath);
    if (isMd) {
      const mdWrapper = el('div', { class: 'agent-bubble-text agent-text-preview-md' });
      mdWrapper.innerHTML = renderMarkdown(text);
      contentBox.append(mdWrapper);
      await renderPreviewMermaid(mdWrapper);
      void highlightCode(contentBox);
    } else {
      const pre = el('pre', {}, el('code', { text }));
      contentBox.append(pre);
      void highlightCode(contentBox);
    }
  } catch (err) {
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
